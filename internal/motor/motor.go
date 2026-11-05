// Package motor runs containers on one machine, through the command line of
// containerd (nerdctl) or Docker: both take almost the same commands, so
// one implementation serves either without their libraries. containerd is
// recommended; Docker is used when it is what the machine has.
package motor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// Motor runs containers with one command-line tool.
type Motor struct {
	// Bin is the tool: "nerdctl" (containerd) or "docker".
	Bin string
	// Exec runs the tool with args, writing its output to out (nil: os/exec);
	// tests replace it.
	Exec func(ctx context.Context, bin string, args []string, out io.Writer) error
}

// ErrSinMotor: neither nerdctl nor docker is installed.
var ErrSinMotor = errors.New("no container engine: install containerd with nerdctl (recommended) or Docker")

// Buscar finds the machine's engine: nerdctl first, then docker.
func Buscar() (*Motor, error) {
	for _, bin := range []string{"nerdctl", "docker"} {
		if _, err := exec.LookPath(bin); err == nil {
			return &Motor{Bin: bin}, nil
		}
	}
	return nil, ErrSinMotor
}

// Recomendado reports whether this is the recommended engine (containerd).
func (m *Motor) Recomendado() bool { return m.Bin == "nerdctl" }

// Montaje is a named volume, or a folder of this machine (an absolute
// path), mounted in a container.
type Montaje struct {
	Volumen, Ruta string
}

// Contenedor is one container to run.
type Contenedor struct {
	Nombre   string
	Imagen   string
	Comando  []string
	Env      map[string]string
	Dir      string
	Montajes []Montaje
	// Red: "" (the default), "host", or "container:<name>" to share another
	// container's network (its 127.0.0.1).
	Red string
	// CPUs ("1.5") and Memoria ("512m") limit it ("": no limit).
	CPUs, Memoria string
	Etiquetas     map[string]string
	// Puertos publishes ports, as "127.0.0.1:5000:5000".
	Puertos []string
	// Siempre restarts it when it stops and when the machine starts (a
	// service of the platform's own, such as its database).
	Siempre bool
	// Privilegiado gives it the machine's privileges (a BuildKit daemon
	// under Docker).
	Privilegiado bool
}

func (c Contenedor) args(detached bool) []string {
	// --quiet: the image's download is not part of the step's log.
	args := []string{"run", "--quiet", "--name", c.Nombre}
	if detached {
		args = append(args, "-d")
	} else {
		args = append(args, "--rm")
	}
	for _, k := range sortedKeys(c.Env) {
		args = append(args, "-e", k+"="+c.Env[k])
	}
	for _, k := range sortedKeys(c.Etiquetas) {
		args = append(args, "--label", k+"="+c.Etiquetas[k])
	}
	for _, m := range c.Montajes {
		args = append(args, "-v", m.Volumen+":"+m.Ruta)
	}
	if c.Dir != "" {
		args = append(args, "-w", c.Dir)
	}
	if c.Red != "" {
		args = append(args, "--network", c.Red)
	}
	if c.CPUs != "" {
		args = append(args, "--cpus", c.CPUs)
	}
	if c.Memoria != "" {
		args = append(args, "--memory", c.Memoria)
	}
	for _, p := range c.Puertos {
		args = append(args, "-p", p)
	}
	if c.Siempre {
		args = append(args, "--restart", "always")
	}
	if c.Privilegiado {
		args = append(args, "--privileged")
	}
	// Comando replaces the image's entrypoint, as a pod's command does.
	if len(c.Comando) > 0 {
		args = append(args, "--entrypoint", c.Comando[0])
	}
	args = append(args, c.Imagen)
	if len(c.Comando) > 1 {
		args = append(args, c.Comando[1:]...)
	}
	return args
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m *Motor) run(ctx context.Context, args []string, out io.Writer) error {
	if m.Exec != nil {
		return m.Exec(ctx, m.Bin, args, out)
	}
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// Correr runs the container to the end, its output to w, and removes it.
// A cancelled ctx removes it too.
func (m *Motor) Correr(ctx context.Context, c Contenedor, w io.Writer) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = m.Quitar(context.Background(), c.Nombre)
		case <-done:
		}
	}()
	out := &sinAviso{w: w}
	err := m.run(ctx, c.args(false), out)
	if len(out.part) > 0 { // a last line without its newline
		_, _ = w.Write(out.part)
	}
	return err
}

// sinAviso drops containerd's notice that a named volume already exists,
// which it prints each time one is mounted.
type sinAviso struct {
	w    io.Writer
	part []byte
}

func (s *sinAviso) Write(p []byte) (int, error) {
	s.part = append(s.part, p...)
	for {
		i := bytes.IndexByte(s.part, '\n')
		if i < 0 {
			break
		}
		line := s.part[:i+1]
		if !(bytes.Contains(line, []byte("level=warning")) && bytes.Contains(line, []byte("already exists and will be returned as-is"))) {
			if _, err := s.w.Write(line); err != nil {
				return 0, err
			}
		}
		s.part = s.part[i+1:]
	}
	return len(p), nil
}

// Iniciar starts the container in the background (Quitar removes it).
func (m *Motor) Iniciar(ctx context.Context, c Contenedor) error {
	var out bytes.Buffer
	if err := m.run(ctx, c.args(true), &out); err != nil {
		return fmt.Errorf("%s run %s: %v: %s", m.Bin, c.Nombre, err, strings.TrimSpace(out.String()))
	}
	return nil
}

// Asegurar starts the container unless one by that name is already there
// (running, or stopped and started again).
func (m *Motor) Asegurar(ctx context.Context, c Contenedor) error {
	if m.run(ctx, []string{"inspect", "--type", "container", c.Nombre}, io.Discard) == nil {
		return m.run(ctx, []string{"start", c.Nombre}, io.Discard)
	}
	return m.Iniciar(ctx, c)
}

// Ejecutar runs a command in a running container, and fails when it does.
func (m *Motor) Ejecutar(ctx context.Context, nombre string, comando ...string) error {
	return m.run(ctx, append([]string{"exec", nombre}, comando...), io.Discard)
}

// Quitar stops and removes a container, with the anonymous volumes its
// image made (Postgres's data); one that is not there is gone already.
func (m *Motor) Quitar(ctx context.Context, nombre string) error {
	_ = m.run(ctx, []string{"rm", "-f", "-v", nombre}, io.Discard)
	return nil
}

// CrearVolumen makes a named volume (an existing one is kept).
func (m *Motor) CrearVolumen(ctx context.Context, nombre string, etiquetas map[string]string) error {
	args := []string{"volume", "create"}
	for _, k := range sortedKeys(etiquetas) {
		args = append(args, "--label", k+"="+etiquetas[k])
	}
	var out bytes.Buffer
	if err := m.run(ctx, append(args, nombre), &out); err != nil && !strings.Contains(out.String(), "exist") {
		return fmt.Errorf("%s volume create %s: %v: %s", m.Bin, nombre, err, strings.TrimSpace(out.String()))
	}
	return nil
}

// Volumenes lists the named volumes with this label (key=value).
func (m *Motor) Volumenes(ctx context.Context, etiqueta string) ([]string, error) {
	var out bytes.Buffer
	if err := m.run(ctx, []string{"volume", "ls", "-q", "--filter", "label=" + etiqueta}, &out); err != nil {
		return nil, fmt.Errorf("%s volume ls: %v: %s", m.Bin, err, strings.TrimSpace(out.String()))
	}
	return strings.Fields(out.String()), nil
}

// QuitarVolumen removes a named volume; one that is not there is gone already.
func (m *Motor) QuitarVolumen(ctx context.Context, nombre string) error {
	_ = m.run(ctx, []string{"volume", "rm", "-f", nombre}, io.Discard)
	return nil
}

// Salida is the exit code of a failed command, when the engine said it.
func Salida(err error) (int, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}
