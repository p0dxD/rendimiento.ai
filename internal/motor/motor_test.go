package motor

import (
	"context"
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
