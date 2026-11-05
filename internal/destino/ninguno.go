package destino

import (
	"context"
	"errors"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/spec"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// ErrNoDespliega: this installation has nowhere to run releases yet (a
// computer without a cluster, until its own destination exists).
var ErrNoDespliega = errors.New("this computer cannot deploy yet: its apps are built and tested, but not run")

// Ninguno is the destination of a rendimiento that only builds and tests:
// apps are kept and their CI runs, and a release says it cannot deploy.
type Ninguno struct{}

// Nombre is "ninguno".
func (Ninguno) Nombre() string { return "ninguno" }

// Crear keeps nothing: there is nowhere to make room.
func (Ninguno) Crear(context.Context, *store.App, bool) error { return nil }

// Aplicar cannot run the release.
func (Ninguno) Aplicar(context.Context, string, *store.Release) error { return ErrNoDespliega }

// Estado: a release never rolls out here.
func (Ninguno) Estado(context.Context, string, int64) (Estado, string, error) {
	return Fallida, ErrNoDespliega.Error(), nil
}

// Tarea cannot run a deploy task.
func (Ninguno) Tarea(_ context.Context, _ *store.App, _ *store.Release, _ spec.Task) store.ReleaseTask {
	end := time.Now()
	return store.ReleaseTask{Status: store.TaskFailed, Message: ErrNoDespliega.Error(), FinishedAt: &end}
}

// Quitar has nothing to remove.
func (Ninguno) Quitar(context.Context, string) error { return nil }

// Situacion: nothing runs.
func (Ninguno) Situacion(context.Context, string) (Situacion, error) { return Situacion{}, nil }

// Recursos is the app with nothing running.
func (Ninguno) Recursos(_ context.Context, a *store.App) (*Recurso, error) {
	return &Recurso{Kind: "App", Name: a.Name, Health: "Missing", Info: []string{ErrNoDespliega.Error()}}, nil
}

// Reiniciar has nothing to restart.
func (Ninguno) Reiniciar(context.Context, string) error { return nil }

// Impacto: removing the app destroys nothing here.
func (Ninguno) Impacto(_ context.Context, app string) (*Impacto, error) {
	return &Impacto{Namespace: app, Volumes: []string{}, Secrets: []string{}}, nil
}

// Desconectar has nothing to leave running.
func (Ninguno) Desconectar(context.Context, string) error { return nil }

// LlavesSecreto: an app's secrets need a destination.
func (Ninguno) LlavesSecreto(context.Context, string, string) ([]string, error) {
	return nil, ErrNoDespliega
}

// GuardarSecreto: an app's secrets need a destination.
func (Ninguno) GuardarSecreto(context.Context, string, string, map[string]*string) error {
	return ErrNoDespliega
}
