package pipeline

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/motor"
	"github.com/rendimiento-ai/rendimiento/internal/spec"
)

// TestLocalReal runs real steps on this machine's engine, against public
// repositories. It runs only when asked, on a machine with nerdctl or
// docker, a BuildKit and a registry at localhost:5000:
//
//	RENDIMIENTO_MOTOR_REAL=unix:///run/user/1000/buildkit/buildkitd.sock go test -run TestLocalReal ./internal/pipeline
func TestLocalReal(t *testing.T) {
	addr := os.Getenv("RENDIMIENTO_MOTOR_REAL")
	if addr == "" {
		t.Skip("RENDIMIENTO_MOTOR_REAL not set (the BuildKit address)")
	}
	m, err := motor.Buscar()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("engine: %s", m.Bin)
	ex := &LocalExecutor{Motor: m, BuildkitAddr: addr, BuildImage: "moby/buildkit:v0.18.2", GitImage: "alpine/git:2.47.2",
		PostgresImage: "postgres:17-alpine", InsecureRegistry: true, Timeout: 30 * time.Minute}
	ctx := context.Background()
	run := func(name string, src Source, step Step) StepResult {
		t.Helper()
		var log strings.Builder
		res := ex.Execute(ctx, "real-"+name, src, step, &log)
		t.Logf("%s: %s %s\n%s", name, res.Status, res.Message, tailLines(log.String(), 25))
		return res
	}

	demo := Source{Repo: "p0dxD/rendimiento-demo", Branch: "main"}
	if res := run("go", demo, Step{ID: "demo:test", Kind: KindTest, App: "demo", Path: ".", Image: "golang:1.25",
		Command: "go test ./...", Cache: true, Resources: spec.Resources{CPULimit: "2", MemLimit: "2Gi"}}); res.Status != StatusSucceeded {
		t.Errorf("go test: %s", res.Message)
	}
	if res := run("postgres", demo, Step{ID: "db:test", Kind: KindTest, App: "demo", Path: ".", Image: "postgres:17-alpine",
		Command: `psql "$DATABASE_URL" -c "select 1"`, Postgres: true}); res.Status != StatusSucceeded {
		t.Errorf("postgres: %s", res.Message)
	}
	if res := run("failing", demo, Step{ID: "fails:test", Kind: KindTest, App: "demo", Path: ".", Image: "alpine",
		Command: "exit 3"}); res.Status != StatusFailed || !strings.Contains(res.Message, "code 3") {
		t.Errorf("a failing step: %s %s", res.Status, res.Message)
	}
	self := Source{Repo: "rendimiento-ai/rendimiento", Branch: "main"}
	res := run("build", self, Step{ID: "platform:build", Kind: KindBuild, Path: ".", Target: "localhost:5000/rendimiento-prueba", Dockerfile: "Dockerfile"})
	if res.Status != StatusSucceeded || !strings.HasPrefix(res.Digest, "sha256:") {
		t.Errorf("build: %s %s", res.Message, res.Digest)
	}
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
