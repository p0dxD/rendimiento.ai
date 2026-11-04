// Package destino is where an app's releases run. The platform builds,
// tests and decides what to release; a Destino makes it run and says how
// it went. Kubernetes is the first one (the App object and its controller);
// a computer of one's own, a server over SSH and the clouds come later.
package destino

import (
	"context"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Estado is where a release's rollout stands.
type Estado int

// Rollout states.
const (
	Pendiente   Estado = iota
	Sana               // running and healthy
	Fallida            // it failed to roll out
	Reemplazada        // a newer release replaced it
)

// Destino runs an app's releases somewhere.
type Destino interface {
	// Nombre is the destination's kind, as kept with each app ("kubernetes").
	Nombre() string
	// Crear makes room for a new app; adopt takes over what already runs
	// under its name.
	Crear(ctx context.Context, app *store.App, adopt bool) error
	// Aplicar makes the release the one that runs.
	Aplicar(ctx context.Context, app string, rel *store.Release) error
	// Estado reports a release's rollout, with a reason for people.
	Estado(ctx context.Context, app string, release int64) (Estado, string, error)
	// Tarea runs one of the release's deploy tasks (before or after it goes
	// live) to the end, with its log.
	Tarea(ctx context.Context, app *store.App, rel *store.Release, t spec.Task) store.ReleaseTask
	// Quitar removes the app and what runs for it; an app that is not
	// there is already removed.
	Quitar(ctx context.Context, app string) error

	// Situacion is how the app runs now, for the Apps page.
	Situacion(ctx context.Context, app string) (Situacion, error)
	// Recursos is what runs for the app, as a tree with each part's health.
	Recursos(ctx context.Context, app *store.App) (*Recurso, error)
	// Reiniciar restarts the app's services (after its secrets change).
	Reiniciar(ctx context.Context, app string) error
	// Impacto lists what removing the app would destroy, asked before.
	Impacto(ctx context.Context, app string) (*Impacto, error)
	// Desconectar stops managing the app and leaves it running as it is; an
	// app that is not there is already disconnected.
	Desconectar(ctx context.Context, app string) error
}

// Situacion is an app as it runs now: the status its page shows, and
// whether it is suspended.
type Situacion struct {
	Status     v1alpha1.AppStatus
	Suspendida bool
}

// Recurso is one part of what runs for an app, in the tree on its page.
type Recurso struct {
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Health   string    `json:"health"` // Healthy | Progressing | Degraded | Missing
	Info     []string  `json:"info,omitempty"`
	Children []Recurso `json:"children,omitempty"`
}

// Impacto describes what removing an app would destroy.
type Impacto struct {
	Namespace string   `json:"namespace"`
	Volumes   []string `json:"volumes"` // "name (size)"
	Secrets   []string `json:"secrets"`
	Adopted   bool     `json:"adopted"` // it ran before rendimiento took it over
	// SharedNamespace: the namespace and whatever else lives in it are kept.
	SharedNamespace bool `json:"sharedNamespace"`
}
