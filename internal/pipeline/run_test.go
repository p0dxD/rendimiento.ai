package pipeline

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

type fakeExec struct {
	fail             map[string]bool
	running, maxSeen atomic.Int32
	mu               sync.Mutex
	order            []string
}

func (f *fakeExec) Execute(_ context.Context, _ string, _ Source, s Step, w io.Writer) StepResult {
	n := f.running.Add(1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	f.running.Add(-1)
	f.mu.Lock()
	f.order = append(f.order, s.ID)
	f.mu.Unlock()
	io.WriteString(w, "log for "+s.ID)
	if f.fail[s.ID] {
		return StepResult{Status: StatusFailed}
	}
	return StepResult{Status: StatusSucceeded, Digest: "sha256:" + s.Service}
}

type nopRecorder struct{}

func (nopRecorder) StepUpdate(string, string, StepResult) {}
func (nopRecorder) StepLog(string, string) io.WriteCloser { return nopCloser{io.Discard} }

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func testSpec() spec.Spec {
	s := spec.Spec{Services: []spec.Service{
		{Name: "api", Path: "api", Test: &spec.Test{Image: "python:3.12-slim", Command: "pytest"}},
		{Name: "web", Path: "web", Test: &spec.Test{Image: "node:20", Command: "npm test"}},
		{Name: "worker", Path: "worker"},
	}}
	s.Default()
	return s
}

func TestPlan(t *testing.T) {
	steps := Plan("shop", "registry.cube.local:5000", testSpec())
	ids := []string{}
	for _, s := range steps {
		ids = append(ids, s.ID+"<"+strings.Join(s.DependsOn, ","))
	}
	want := "api:test<|api:build<api:test|web:test<|web:build<web:test|worker:build<"
	if got := strings.Join(ids, "|"); got != want {
		t.Fatalf("plan = %s\nwant   %s", got, want)
	}
	if steps[1].Target != "registry.cube.local:5000/shop-api" {
		t.Fatalf("target = %s", steps[1].Target)
	}
}

func TestPlanSkipsReadyMadeImages(t *testing.T) {
	s := spec.Spec{Services: []spec.Service{{Name: "web"}, {Name: "db", Image: "postgres:15-alpine"}}}
	s.Default()
	steps := Plan("app", "reg", s)
	if len(steps) != 1 || steps[0].ID != "web:build" {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestPlanBuildsJobImages(t *testing.T) {
	s := spec.Spec{Services: []spec.Service{{Name: "api"}}, Jobs: []spec.Job{
		{Name: "scraper", Schedule: "@daily", Path: "congress"}, {Name: "recap", Schedule: "@daily", Service: "api"}}}
	s.Default()
	steps := Plan("sp", "reg", s)
	if len(steps) != 2 || steps[1].ID != "job-scraper:build" || steps[1].Service != "job:scraper" || steps[1].Target != "reg/sp-scraper" || steps[1].Path != "congress" {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestBuildPodsAvoidExcludedNodes(t *testing.T) {
	k := &KubeExecutor{Namespace: "b", ExcludeNodes: []string{"podoi-ai"}}
	k.defaults()
	pod, err := k.pod("run-1-web-build-abc", nil, Source{Repo: "o/r", SHA: "s"}, Step{ID: "web:build", Kind: KindBuild, Path: "."}, "tcp://b:1234")
	if err != nil {
		t.Fatal(err)
	}
	req := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0]
	if req.Key != "kubernetes.io/hostname" || req.Operator != "NotIn" || req.Values[0] != "podoi-ai" {
		t.Fatalf("affinity = %+v", req)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("build pods must not get API credentials")
	}
	k.ExcludeNodes = nil
	if pod, _ := k.pod("x", nil, Source{}, Step{Kind: KindTest, Path: ".", Image: "i", Command: "c"}, ""); pod.Spec.Affinity != nil {
		t.Fatal("no exclusion configured → no affinity")
	}
}

func TestTransientRetry(t *testing.T) {
	calls := 0
	locked := fmt.Errorf(`rpc error: code = Unknown desc = database is locked`)
	err := transientRetry(context.Background(), func() error {
		calls++
		if calls < 3 {
			return locked
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	calls = 0
	permanent := fmt.Errorf("forbidden")
	if err := transientRetry(context.Background(), func() error { calls++; return permanent }); err != permanent || calls != 1 {
		t.Fatalf("permanent errors must not be retried: err=%v calls=%d", err, calls)
	}
}

func TestRunnerFailureSkipsDependents(t *testing.T) {
	exec := &fakeExec{fail: map[string]bool{"web:test": true}}
	r := NewRunner(exec, nopRecorder{}, 2)
	res, err := r.Run(context.Background(), "run1", Source{}, Plan("shop", "reg", testSpec()))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{"api:test": StatusSucceeded, "api:build": StatusSucceeded, "web:test": StatusFailed, "web:build": StatusSkipped, "worker:build": StatusSucceeded}
	for id, st := range want {
		if res[id].Status != st {
			t.Errorf("%s = %s, want %s", id, res[id].Status, st)
		}
	}
	if m := exec.maxSeen.Load(); m > 2 {
		t.Errorf("concurrency limit exceeded: %d", m)
	}
	pos := map[string]int{}
	for i, id := range exec.order {
		pos[id] = i
	}
	if pos["api:build"] < pos["api:test"] {
		t.Error("build ran before its test")
	}
}

func TestValidateCycle(t *testing.T) {
	_, err := NewRunner(&fakeExec{}, nopRecorder{}, 1).Run(context.Background(), "r", Source{}, []Step{
		{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestBuildPodPicksBuilder(t *testing.T) {
	k := &KubeExecutor{Namespace: "b", RailpackImage: "rp:1"}
	k.defaults()
	step := Step{ID: "web:build", Kind: KindBuild, Path: "web", Dockerfile: "Dockerfile", Start: "node server.js",
		BuildArgs: map[string]string{"B": "2", "A": "1"}}
	pod, err := k.pod("p", nil, Source{Repo: "o/r", SHA: "abc"}, step, "tcp://b:1234")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pod.Spec.InitContainers); n != 2 || pod.Spec.InitContainers[1].Name != "plan" {
		t.Fatalf("init containers = %d", n)
	}
	plan := pod.Spec.InitContainers[1]
	if got := strings.Join(plan.Command[4:], " "); got != "--env A=1 --env B=2" {
		t.Errorf("railpack args = %q", got)
	}
	if got := strings.Join(pod.Spec.Containers[0].Command[4:], " "); got != "--opt build-arg:A=1 --opt build-arg:B=2" {
		t.Errorf("docker args = %q", got)
	}

	// Without a Railpack image, builds use the Dockerfile and forcing Railpack fails.
	k.RailpackImage = ""
	if pod, _ := k.pod("p", nil, Source{}, step, ""); len(pod.Spec.InitContainers) != 1 {
		t.Error("plan container without a Railpack image")
	}
	step.Builder = "railpack"
	if _, err := k.pod("p", nil, Source{}, step, ""); err == nil {
		t.Error("railpack builder without a Railpack image must fail")
	}
}

// TestPlanScript runs the real plan script with a stub railpack CLI.
func TestPlanScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	for _, tc := range []struct {
		name, builder string
		dockerfile    bool
		want          string
		railpackRan   bool
	}{
		{"auto with Dockerfile", "", true, "dockerfile", false},
		{"auto without Dockerfile", "", false, "railpack", true},
		{"forced railpack", "railpack", true, "railpack", true},
		{"forced dockerfile", "dockerfile", false, "dockerfile", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ctxDir, bin, ws := filepath.Join(dir, "src"), filepath.Join(dir, "bin"), filepath.Join(dir, "ws")
			for _, d := range []string{ctxDir, bin, ws} {
				os.MkdirAll(d, 0o755)
			}
			if tc.dockerfile {
				os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
			}
			os.WriteFile(filepath.Join(bin, "railpack"), []byte("#!/bin/sh\necho \"$@\" > \"$WS/railpack-args\"\n"), 0o755)
			script := strings.ReplaceAll(planScript, "/workspace", ws)
			cmd := exec.Command("sh", "-c", script, "plan", "--env", "A=1")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "WS="+ws,
				"CONTEXT="+ctxDir, "DOCKERFILE=Dockerfile", "BUILDER="+tc.builder, "START=npm start")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, _ := os.ReadFile(filepath.Join(ws, "plan", "builder"))
			if strings.TrimSpace(string(got)) != tc.want {
				t.Errorf("builder = %q, want %s (output: %s)", got, tc.want, out)
			}
			args, err := os.ReadFile(filepath.Join(ws, "railpack-args"))
			if (err == nil) != tc.railpackRan {
				t.Fatalf("railpack ran = %v, want %v", err == nil, tc.railpackRan)
			}
			if tc.railpackRan {
				want := "prepare " + ctxDir + " --plan-out " + ws + "/plan/railpack-plan.json --error-missing-start --env A=1 --start-cmd npm start"
				if strings.TrimSpace(string(args)) != want {
					t.Errorf("railpack args = %q\nwant %q", args, want)
				}
			}
		})
	}
}

type fakeSRV struct {
	targets []string
	err     error
}

func (f fakeSRV) LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error) {
	var out []*net.SRV
	for _, t := range f.targets {
		out = append(out, &net.SRV{Target: t + ".buildkitd-pool.devops-tools.svc.cluster.local.", Port: 1234})
	}
	return "", out, f.err
}

func TestBuildkitPool(t *testing.T) {
	k := &KubeExecutor{BuildkitAddr: "tcp://single:1234"}
	if addr, d := k.buildkitFor(context.Background(), "reg/shop-api"); addr != "tcp://single:1234" || d != "" {
		t.Fatalf("no pool: %s %s", addr, d)
	}
	all := []string{"buildkitd-0", "buildkitd-1", "buildkitd-2", "buildkitd-3"}
	k.BuildkitPool, k.Resolver = "buildkitd-pool.devops-tools.svc.cluster.local", fakeSRV{targets: all}
	keys := []string{"reg/a-web", "reg/a-api", "reg/b-web", "reg/c-ui", "reg/d-worker", "reg/e-api", "reg/f-fn", "reg/g-db"}
	first := map[string]string{}
	used := map[string]bool{}
	for _, key := range keys {
		addr, d := k.buildkitFor(context.Background(), key)
		if again, _ := k.buildkitFor(context.Background(), key); again != addr {
			t.Fatalf("%s is not stable: %s then %s", key, addr, again)
		}
		if !strings.HasPrefix(addr, "tcp://"+d+".buildkitd-pool.devops-tools.svc.cluster.local:1234") {
			t.Fatalf("addr %s daemon %s", addr, d)
		}
		first[key], used[d] = d, true
	}
	if len(used) < 3 {
		t.Errorf("8 images should spread over most of 4 daemons, used %v", used)
	}
	// A daemon going away only moves its own images.
	k.Resolver = fakeSRV{targets: []string{"buildkitd-0", "buildkitd-1", "buildkitd-3"}}
	for _, key := range keys {
		_, d := k.buildkitFor(context.Background(), key)
		if first[key] != "buildkitd-2" && d != first[key] {
			t.Errorf("%s moved from %s to %s although its daemon is still there", key, first[key], d)
		}
	}
	// No ready daemon: the single address.
	k.Resolver = fakeSRV{err: fmt.Errorf("no such host")}
	if addr, _ := k.buildkitFor(context.Background(), "reg/a-web"); addr != "tcp://single:1234" {
		t.Errorf("fallback = %s", addr)
	}
}
