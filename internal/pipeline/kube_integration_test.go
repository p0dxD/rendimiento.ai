//go:build integration

package pipeline

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// TestBuildOnCluster clones a public Go repo, builds it with the cluster's
// buildkitd and pushes it to the registry. Run with:
//
//	go test -tags integration ./internal/pipeline -run TestBuildOnCluster -v
func TestBuildOnCluster(t *testing.T) {
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("HOME")+"/.kube/config")
	if err != nil {
		t.Skip(err)
	}
	exec := &KubeExecutor{
		Client:           kubernetes.NewForConfigOrDie(cfg),
		Namespace:        "rendimiento-builds",
		BuildkitAddr:     "tcp://buildkitd.devops-tools.svc.cluster.local:1234",
		InsecureRegistry: true,
		Timeout:          20 * time.Minute,
		ExcludeNodes:     []string{"podoi-ai"},
	}
	src := Source{Repo: "https://github.com/mccutchen/go-httpbin.git", Branch: "main"}
	steps := []Step{
		{ID: "httpbin:test", Service: "httpbin", Kind: KindTest, Path: ".", Image: "golang:1.26", Command: "go vet ./cmd/..."},
		{ID: "httpbin:build", Service: "httpbin", Kind: KindBuild, Path: ".", Dockerfile: "Dockerfile",
			Target: "registry.cube.local:5000/rendimiento-itest-httpbin", DependsOn: []string{"httpbin:test"}},
	}
	rec := &bufRecorder{logs: map[string]*bytes.Buffer{}}
	res, err := NewRunner(exec, rec, 1).Run(context.Background(), "itest", src, steps)
	if err != nil {
		t.Fatal(err)
	}
	for id, r := range res {
		t.Logf("%s: %s %s %s", id, r.Status, r.Digest, r.Message)
		if r.Status != StatusSucceeded {
			t.Errorf("%s failed; log tail:\n%s", id, tail(rec.logs[id].String(), 40))
		}
	}
	if !strings.HasPrefix(res["httpbin:build"].Digest, "sha256:") {
		t.Fatal("no digest")
	}
}

// TestRailpackBuildOnCluster builds a repo without using its Dockerfile:
// the railpack CLI writes a plan and BuildKit's railpack frontend builds it.
//
//	go test -tags integration ./internal/pipeline -run TestRailpackBuildOnCluster -v
func TestRailpackBuildOnCluster(t *testing.T) {
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("HOME")+"/.kube/config")
	if err != nil {
		t.Skip(err)
	}
	exec := &KubeExecutor{
		Client:           kubernetes.NewForConfigOrDie(cfg),
		Namespace:        "rendimiento-builds",
		BuildkitAddr:     "tcp://buildkitd.devops-tools.svc.cluster.local:1234",
		InsecureRegistry: true,
		Timeout:          20 * time.Minute,
		ExcludeNodes:     []string{"podoi-ai"},
		RailpackImage:    "registry.cube.local:5000/rendimiento-railpack:0.40.0",
	}
	src := Source{Repo: "https://github.com/p0dxD/hello-rendimiento.git", Branch: "main"}
	steps := []Step{{ID: "hello:build", Service: "hello", Kind: KindBuild, Path: ".", Dockerfile: "Dockerfile",
		Builder: "railpack", Target: "registry.cube.local:5000/rendimiento-itest-railpack"}}
	rec := &bufRecorder{logs: map[string]*bytes.Buffer{}}
	res, err := NewRunner(exec, rec, 1).Run(context.Background(), "itest-rp", src, steps)
	if err != nil {
		t.Fatal(err)
	}
	r := res["hello:build"]
	log := rec.logs["hello:build"].String()
	t.Logf("%s %s %s", r.Status, r.Digest, r.Message)
	if r.Status != StatusSucceeded || !strings.HasPrefix(r.Digest, "sha256:") {
		t.Fatalf("railpack build failed; log tail:\n%s", tail(log, 60))
	}
	if !strings.Contains(log, "builder: Railpack") || !strings.Contains(log, "railpack-frontend") {
		t.Errorf("log does not show a Railpack build:\n%s", tail(log, 60))
	}
}

type bufRecorder struct{ logs map[string]*bytes.Buffer }

func (b *bufRecorder) StepUpdate(string, string, StepResult) {}
func (b *bufRecorder) StepLog(_, id string) io.WriteCloser {
	buf := &bytes.Buffer{}
	b.logs[id] = buf
	return nopCloser{buf}
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
