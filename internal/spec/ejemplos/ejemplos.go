// Package ejemplos holds rendimiento.yaml examples that every layer must
// accept: the spec's validation, the App object's schema in Kubernetes,
// render, and the local destination. Each layer's tests run them all, so a
// rule changed in one layer cannot quietly stay different in another. Add
// one here for every kind of app rendimiento supports.
package ejemplos

import (
	"embed"
	"io/fs"
	"sort"
	"strings"

	"github.com/rendimiento-ai/rendimiento/internal/spec"
)

//go:embed *.yaml
var files embed.FS

// Ejemplo is one example, by its file name (without .yaml).
type Ejemplo struct {
	Nombre string
	Spec   *spec.Spec
	// Crudo is the file as written, for a repository that carries it.
	Crudo []byte
}

// Todos parses every example, defaulted, in name order.
func Todos() ([]Ejemplo, error) {
	names, err := fs.Glob(files, "*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Ejemplo
	for _, n := range names {
		raw, err := files.ReadFile(n)
		if err != nil {
			return nil, err
		}
		s, err := spec.Parse(raw)
		if err != nil {
			return nil, err
		}
		s.Default()
		out = append(out, Ejemplo{Nombre: strings.TrimSuffix(n, ".yaml"), Spec: s, Crudo: raw})
	}
	return out, nil
}
