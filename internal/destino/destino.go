// Package destino is where an app's releases run. The platform builds,
// tests and decides what to release; a Destino makes it run and says how
// it went. Kubernetes is the first one (the App object and its controller);
// a computer of one's own, a server over SSH and the clouds come later.
package destino

import (
	"context"

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
	// Quitar removes the app and what runs for it; an app that is not
	// there is already removed.
	Quitar(ctx context.Context, app string) error
}
