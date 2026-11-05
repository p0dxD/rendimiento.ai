package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/rendimiento-ai/rendimiento/internal/motor"
	"github.com/rendimiento-ai/rendimiento/internal/spec"
)

// fakeEngine records each command and answers like nerdctl would.
type fakeEngine struct {
	mu    sync.Mutex
	calls []string
	// fail makes the command whose args contain it exit with code 1.
	fail string
	// output is what the step's container prints.
	output string
}

func (f *fakeEngine) exec(_ context.Context, bin string, args []string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := bin + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.fail != "" && strings.Contains(line, f.fail) {
		return errors.New("exit status 1")
	}
	if args[0] == "run" && !strings.Contains(line, "-clone") && !strings.Contains(line, " -d ") {
		fmt.Fprint(out, f.output)
	}
	return nil
}

func (f *fakeEngine) find(t *testing.T, part string) string {
	t.Helper()
	for _, c := range f.calls {
		if strings.Contains(c, part) {
			return c
		}
	}
	t.Fatalf("no command with %q in:\n%s", part, strings.Join(f.calls, "\n"))
	return ""
}

func localExec(f *fakeEngine) *LocalExecutor {
	return &LocalExecutor{Motor: &motor.Motor{Bin: "nerdctl", Exec: f.exec}, BuildkitAddr: "tcp://127.0.0.1:1234",
		BuildImage: "moby/buildkit:v0.18.2", GitImage: "alpine/git", PostgresImage: "postgres:17-alpine", InsecureRegistry: true,
		Token: func(context.Context, string) (string, error) { return "tok", nil }}
}

// A test runs in its image after the clone, with its Postgres sharing its
// network and its cache volume, and everything is removed after.
func TestLocalTest(t *testing.T) {
	f := &fakeEngine{output: "ok\n"}
	step := Step{ID: "web:test", Kind: KindTest, App: "tienda", Path: "web", Image: "node:20", Command: "npm test",
		Postgres: true, Cache: true, Env: map[string]string{"CI": "yes"}, Resources: spec.Resources{CPULimit: "500m", MemLimit: "1Gi"}}
	var log strings.Builder
	res := localExec(f).Execute(context.Background(), "42", Source{Repo: "p0dxD/tienda", SHA: "abc123"}, step, &log)
	if res.Status != StatusSucceeded {
		t.Fatalf("%+v\n%s", res, log.String())
	}
	clone := f.find(t, "-clone")
	if !strings.Contains(clone, "GIT_TOKEN=tok") || !strings.Contains(clone, "REF=abc123") || !strings.Contains(clone, "https://github.com/p0dxD/tienda.git") {
		t.Fatalf("clone: %s", clone)
	}
	pg := f.find(t, "-postgres -d")
	if !strings.Contains(pg, "postgres:17-alpine") {
		t.Fatalf("postgres: %s", pg)
	}
	f.find(t, "pg_isready")
	run := f.find(t, " node:20 ")
	for _, want := range []string{"--network container:", "-w /workspace/src/web", "-v cache-tienda-web:/cache", "--cpus 0.5", "--memory 1024m",
		"DATABASE_URL=postgres://postgres:test@127.0.0.1:5432/test", "-e CI=yes", "GOCACHE=/cache/go-build", "--entrypoint sh node:20 -c npm test"} {
		if !strings.Contains(run, want) {
			t.Errorf("test container lacks %q:\n%s", want, run)
		}
	}
	f.find(t, "rm -f -v "+strings.Fields(pg)[4])
	f.find(t, "volume rm -f")
}

// A build pushes with buildctl on the host's network and reads its digest
// from the last line; without it, the step fails.
func TestLocalBuild(t *testing.T) {
	f := &fakeEngine{output: "pushing\nimage digest: sha256:0123abcd\n"}
	step := Step{ID: "web:build", Kind: KindBuild, Path: ".", Target: "localhost:5000/tienda-web", Dockerfile: "Dockerfile",
		BuildArgs: map[string]string{"B": "2", "A": "1"}}
	ex := localExec(f)
	res := ex.Execute(context.Background(), "7", Source{Repo: "p0dxD/tienda", SHA: "0123456789abcdef"}, step, io.Discard)
	if res.Status != StatusSucceeded || res.Digest != "sha256:0123abcd" {
		t.Fatalf("%+v", res)
	}
	run := f.find(t, " moby/buildkit:v0.18.2 ")
	for _, want := range []string{"--network host", "BUILDKIT_ADDR=tcp://127.0.0.1:1234", "TAG=0123456789ab", "INSECURE=,registry.insecure=true",
		"--opt build-arg:A=1 --opt build-arg:B=2"} {
		if !strings.Contains(run, want) {
			t.Errorf("build container lacks %q:\n%s", want, run)
		}
	}
	f2 := &fakeEngine{output: "pushing\n"}
	if res := localExec(f2).Execute(context.Background(), "8", Source{Repo: "p0dxD/tienda"}, step, io.Discard); res.Status != StatusFailed || !strings.Contains(res.Message, "no image digest") {
		t.Fatalf("no digest: %+v", res)
	}
}

// A failing step, a failing clone and a task with secrets fail with a reason.
func TestLocalFailures(t *testing.T) {
	step := Step{ID: "web:test", Kind: KindTest, Path: ".", Image: "node:20", Command: "npm test"}
	f := &fakeEngine{fail: " node:20 "}
	if res := localExec(f).Execute(context.Background(), "1", Source{Repo: "o/r"}, step, io.Discard); res.Status != StatusFailed || res.Message == "" {
		t.Fatalf("step: %+v", res)
	}
	f = &fakeEngine{fail: "-clone"}
	if res := localExec(f).Execute(context.Background(), "1", Source{Repo: "o/r"}, step, io.Discard); res.Status != StatusFailed || !strings.Contains(res.Message, "clone") {
		t.Fatalf("clone: %+v", res)
	}
	task := Step{ID: "seed:task", Kind: KindTask, Path: ".", Image: "alpine", Command: "true", Secrets: []string{"db"}}
	if res := localExec(&fakeEngine{}).Execute(context.Background(), "1", Source{Repo: "o/r"}, task, io.Discard); res.Status != StatusFailed || !strings.Contains(res.Message, "secrets") {
		t.Fatalf("task with secrets: %+v", res)
	}
}
