package render

import (
	"testing"

	"github.com/rendimiento-ai/rendimiento/internal/spec/ejemplos"
)

// Every example that runs something renders.
func TestEjemplos(t *testing.T) {
	todos, err := ejemplos.Todos()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range todos {
		if !e.Spec.Runs() {
			continue
		}
		images, urls := map[string]string{}, map[string]string{}
		for _, s := range e.Spec.Services {
			images[s.Name] = "registry.example.lan:5000/" + e.Nombre + "-" + s.Name + "@sha256:1"
		}
		// The controller resolves the services an app needs (serviceURLs).
		for _, ref := range NeedRefs(*e.Spec) {
			urls[ref] = "http://" + ref + ":8000"
		}
		if _, err := Render(Input{App: "ej-" + e.Nombre, Spec: *e.Spec, Images: images, ServiceURLs: urls}, DefaultOptions()); err != nil {
			t.Errorf("%s: %v", e.Nombre, err)
		}
	}
}
