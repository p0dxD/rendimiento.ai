package motor

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// A container's command replaces its image's entrypoint, as in a pod; the
// download is quiet; containerd's notice about existing volumes is dropped
// from its output, and a last line without a newline is kept.
func TestCorrer(t *testing.T) {
	var got []string
	m := &Motor{Bin: "nerdctl", Exec: func(_ context.Context, bin string, args []string, out io.Writer) error {
		got = args
		_, _ = io.WriteString(out, "uno\ntime=\"x\" level=warning msg=\"volume \\\"w\\\" already exists and will be returned as-is\"\ndos")
		return nil
	}}
	var log strings.Builder
	err := m.Correr(context.Background(), Contenedor{Nombre: "paso", Imagen: "alpine/git", Comando: []string{"sh", "-c", "git version"},
		Env: map[string]string{"B": "2", "A": "1"}, Montajes: []Montaje{{Volumen: "ws", Ruta: "/workspace"}}}, &log)
	if err != nil {
		t.Fatal(err)
	}
	want := "run --quiet --name paso --rm -e A=1 -e B=2 -v ws:/workspace --entrypoint sh alpine/git -c git version"
	if strings.Join(got, " ") != want {
		t.Fatalf("args:\n%s\nwant:\n%s", strings.Join(got, " "), want)
	}
	if log.String() != "uno\ndos" {
		t.Fatalf("output: %q", log.String())
	}
}

// A platform's own container is started once, published and restarted
// with the machine; one already there is only started.
func TestAsegurar(t *testing.T) {
	var calls []string
	there := false
	m := &Motor{Bin: "docker", Exec: func(_ context.Context, _ string, args []string, _ io.Writer) error {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "inspect" && !there {
			return errors.New("no such container")
		}
		return nil
	}}
	c := Contenedor{Nombre: "rendimiento-registro", Imagen: "registry:2", Puertos: []string{"127.0.0.1:5000:5000"}, Siempre: true}
	if err := m.Asegurar(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := calls[len(calls)-1]; got != "run --quiet --name rendimiento-registro -d -p 127.0.0.1:5000:5000 --restart always registry:2" {
		t.Fatalf("first time: %s", got)
	}
	there, calls = true, nil
	_ = m.Asegurar(context.Background(), c)
	if strings.Join(calls, "; ") != "inspect --type container rendimiento-registro; start rendimiento-registro" {
		t.Fatalf("already there: %v", calls)
	}
}
