package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/rendimiento-ai/rendimiento/internal/motor"
	"github.com/rendimiento-ai/rendimiento/internal/spec"
)

// LocalExecutor runs each step on this machine, as containers of its
// engine (containerd or Docker): the parts of a step's pod, one by one,
// with the same images and scripts. The clone and the step share a volume;
// a test's Postgres shares its network with the test, so it is at
// 127.0.0.1 as in the cluster; builds go to a local BuildKit and registry
// on the host's network.
type LocalExecutor struct {
	Motor *motor.Motor
	// BuildkitAddr is the local BuildKit daemon: its socket
	// (unix:///run/user/1000/buildkit/buildkitd.sock, mounted into the
	// build container) or tcp://127.0.0.1:1234 (on the host's network).
	BuildkitAddr string
	BuildImage   string // buildctl client image; match the daemon version
	GitImage     string
	// InsecureRegistry pushes over plain HTTP (a local registry).
	InsecureRegistry bool
	Token            func(ctx context.Context, repo string) (string, error)
	Timeout          time.Duration
	RailpackImage    string
	RailpackFrontend string
	PostgresImage    string
}

// digestLine starts the line a build prints with its image's digest.
const digestLine = "image digest: "

// defaults fills in the images and frontend the cluster's executor uses.
func (l *LocalExecutor) defaults() {
	if l.BuildImage == "" {
		l.BuildImage = "moby/buildkit:v0.18.2"
	}
	if l.GitImage == "" {
		l.GitImage = "alpine/git:2.47.2"
	}
	if l.RailpackFrontend == "" {
		l.RailpackFrontend = "ghcr.io/railwayapp/railpack-frontend:v0.40.0"
	}
	if l.PostgresImage == "" {
		l.PostgresImage = "postgres:17-alpine"
	}
}

// Execute runs one step and removes its containers and workspace.
func (l *LocalExecutor) Execute(ctx context.Context, runID string, src Source, step Step, w io.Writer) StepResult {
	l.defaults()
	res := StepResult{Started: time.Now()}
	fail := func(format string, a ...any) StepResult {
		res.Status, res.Message, res.Finished = StatusFailed, fmt.Sprintf(format, a...), time.Now()
		fmt.Fprintln(w, "rendimiento: "+res.Message)
		return res
	}
	timeout := l.Timeout
	if step.Timeout > 0 && (timeout == 0 || step.Timeout < timeout) {
		timeout = step.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	m := l.Motor
	// A random suffix keeps a retried run from colliding with containers
	// its interrupted predecessor left behind.
	name := podName(runID, step.ID) + "-" + randSuffix()
	ws := name + "-workspace"
	if err := m.CrearVolumen(ctx, ws, map[string]string{"rendimiento.ai/run": runID}); err != nil {
		return fail("%v", err)
	}
	defer func() { _ = m.QuitarVolumen(context.Background(), ws) }()
	mounts := []motor.Montaje{{Volumen: ws, Ruta: "/workspace"}}

	cloneURL := src.Repo
	if !strings.Contains(cloneURL, "://") {
		cloneURL = "https://github.com/" + src.Repo + ".git"
	}
	ref := src.SHA
	if ref == "" {
		ref = src.Branch
	}
	token := ""
	if l.Token != nil {
		t, err := l.Token(ctx, src.Repo)
		if err != nil {
			return fail("could not get a clone token: %v", err)
		}
		token = t
	}
	fmt.Fprintln(w, "::group::clone")
	err := m.Correr(ctx, motor.Contenedor{Nombre: name + "-clone", Imagen: l.GitImage, Comando: []string{"sh", "-c", cloneScript},
		Env: map[string]string{"CLONE_URL": cloneURL, "REF": ref, "GIT_TOKEN": token}, Montajes: mounts}, w)
	fmt.Fprintln(w, "::endgroup::")
	if err != nil {
		return l.ended(ctx, fail, "clone", err)
	}

	workdir := path.Join("/workspace/src", path.Clean(step.Path))
	if !strings.HasPrefix(workdir, "/workspace/src") {
		return fail("step path %q escapes the workspace", step.Path)
	}
	main := motor.Contenedor{Nombre: name, Montajes: mounts}
	switch step.Kind {
	case KindTest, KindTask:
		if len(step.Secrets) > 0 || len(step.SecretEnv) > 0 {
			return fail("tasks with secrets do not run on this machine yet")
		}
		main.Imagen, main.Comando, main.Dir = step.Image, []string{"sh", "-c", step.Command}, workdir
		main.Env = map[string]string{"CI": "true", "GIT_SHA": src.SHA}
		if step.Kind == KindTask {
			main.Env["GIT_BRANCH"], main.Env["RENDIMIENTO_APP"] = src.Branch, step.App
		}
		main.CPUs, main.Memoria = limits(step.Resources)
		if step.Postgres {
			pg := name + "-postgres"
			if err := l.startPostgres(ctx, pg); err != nil {
				return fail("%v", err)
			}
			defer func() { _ = m.Quitar(context.Background(), pg) }()
			main.Red = "container:" + pg
			main.Env["DATABASE_URL"] = testDatabaseURL
		}
		if step.Cache {
			vol := cacheName(step)
			if err := m.CrearVolumen(ctx, vol, map[string]string{CacheAppLabel: step.App}); err != nil {
				return fail("%v", err)
			}
			main.Montajes = append(main.Montajes, motor.Montaje{Volumen: vol, Ruta: "/cache"})
			for _, e := range cacheEnv {
				main.Env[e.Name] = e.Value
			}
		}
		for k, v := range step.Env { // the step's own env wins
			main.Env[k] = v
		}
	case KindBuild:
		if l.RailpackImage != "" {
			fmt.Fprintln(w, "::group::plan")
			err := m.Correr(ctx, motor.Contenedor{Nombre: name + "-plan", Imagen: l.RailpackImage, Montajes: mounts,
				Comando: append([]string{"sh", "-c", planScript, "plan"}, railpackArgs(step)...),
				Env:     map[string]string{"CONTEXT": workdir, "DOCKERFILE": step.Dockerfile, "BUILDER": step.Builder, "START": step.Start}}, w)
			fmt.Fprintln(w, "::endgroup::")
			if err != nil {
				return l.ended(ctx, fail, "plan", err)
			}
		} else if step.Builder == "railpack" {
			return fail("build.builder is railpack but Railpack is not configured on this platform (RAILPACK_IMAGE)")
		}
		insecure := ""
		if l.InsecureRegistry {
			insecure = ",registry.insecure=true"
		}
		tag := src.SHA
		if len(tag) > 12 {
			tag = tag[:12]
		}
		if tag == "" {
			tag = "latest"
		}
		main.Imagen, main.Red = l.BuildImage, "host"
		main.Comando = append([]string{"sh", "-c", buildScript, "build"}, dockerArgs(step)...)
		addr := l.BuildkitAddr
		if sock, ok := strings.CutPrefix(addr, "unix://"); ok {
			// The daemon's socket goes into the container: with rootless
			// containerd, the host's network is not this machine's.
			main.Montajes = append(main.Montajes, motor.Montaje{Volumen: path.Dir(sock), Ruta: "/run/buildkit"})
			addr, main.Red = "unix:///run/buildkit/"+path.Base(sock), ""
		}
		main.Env = map[string]string{"RAILPACK_FRONTEND": l.RailpackFrontend, "BUILDKIT_ADDR": addr, "CONTEXT": workdir,
			"DOCKERFILE": step.Dockerfile, "IMAGE": step.Target, "TAG": tag, "GIT_SHA": src.SHA, "INSECURE": insecure}
	default:
		return fail("unknown step kind %q", step.Kind)
	}

	fmt.Fprintln(w, "::group::step")
	var tail lastLine
	err = m.Correr(ctx, main, io.MultiWriter(w, &tail))
	fmt.Fprintln(w, "::endgroup::")
	if err != nil {
		return l.ended(ctx, fail, "step", err)
	}
	if step.Kind == KindBuild {
		res.Digest = strings.TrimPrefix(tail.find(digestLine), digestLine)
		if !strings.HasPrefix(res.Digest, "sha256:") {
			return fail("build finished but reported no image digest")
		}
		fmt.Fprintf(w, "rendimiento: pushed %s@%s\n", step.Target, res.Digest)
	}
	res.Status, res.Finished = StatusSucceeded, time.Now()
	return res
}

// DeleteCaches removes the cache volumes of an app's tests.
func (l *LocalExecutor) DeleteCaches(ctx context.Context, app string) error {
	vols, err := l.Motor.Volumenes(ctx, CacheAppLabel+"="+app)
	if err != nil {
		return err
	}
	for _, v := range vols {
		_ = l.Motor.QuitarVolumen(ctx, v)
	}
	return nil
}

// ended turns a container's failure into the step's: cancelled, timed out,
// or its exit code.
func (l *LocalExecutor) ended(ctx context.Context, fail func(string, ...any) StepResult, part string, err error) StepResult {
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return fail("step timed out")
	case ctx.Err() != nil:
		return fail("cancelled")
	}
	if code, ok := motor.Salida(err); ok {
		if part == "step" {
			return fail("step failed: step exited with code %d", code)
		}
		return fail("%s failed: exited with code %d", part, code)
	}
	return fail("%s failed: %v", part, err)
}

// startPostgres starts a test's throwaway database and waits until it
// accepts connections over TCP, as the pod's startup probe does.
func (l *LocalExecutor) startPostgres(ctx context.Context, name string) error {
	m := l.Motor
	if err := m.Iniciar(ctx, motor.Contenedor{Nombre: name, Imagen: l.PostgresImage, Memoria: "1g",
		Env: map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "test"}}); err != nil {
		return err
	}
	for i := 0; i < 90; i++ {
		if m.Ejecutar(ctx, name, "pg_isready", "-U", "postgres", "-h", "127.0.0.1") == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	_ = m.Quitar(context.Background(), name)
	return fmt.Errorf("the test database did not start")
}

// limits is a step's CPU and memory limits in the engine's terms ("1.5",
// "512m"); "" where it has none.
func limits(r spec.Resources) (cpus, mem string) {
	if q, err := resource.ParseQuantity(r.CPULimit); err == nil && r.CPULimit != "" {
		cpus = strconv.FormatFloat(float64(q.MilliValue())/1000, 'f', -1, 64)
	}
	if q, err := resource.ParseQuantity(r.MemLimit); err == nil && r.MemLimit != "" {
		mem = strconv.FormatInt(q.Value()/(1<<20), 10) + "m"
	}
	return cpus, mem
}

// lastLine keeps the last lines of a step's output, which its executor
// reads back (a build's digest).
type lastLine struct {
	part  []byte
	lines []string
}

func (t *lastLine) Write(p []byte) (int, error) {
	t.part = append(t.part, p...)
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		t.lines = append(t.lines, string(t.part[:i]))
		t.part = t.part[i+1:]
	}
	if n := len(t.lines); n > 50 {
		t.lines = t.lines[n-50:]
	}
	return len(p), nil
}

// find is the last line starting with prefix ("" when none).
func (t *lastLine) find(prefix string) string {
	lines := t.lines
	if len(t.part) > 0 {
		lines = append(lines, string(t.part))
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}
