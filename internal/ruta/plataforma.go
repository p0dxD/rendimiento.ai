package ruta

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Plataforma is what the platform's tools read and do: the apps, their runs,
// releases, logs, kept requests and problems, and running or cancelling CI.
// main wires it from the store and the platform.
type Plataforma interface {
	ListApps(ctx context.Context) ([]*store.App, error)
	GetApp(ctx context.Context, name string) (*store.App, error)
	GetAppByID(ctx context.Context, id int64) (*store.App, error)
	ListRuns(ctx context.Context, appID int64, limit int) ([]*store.Run, error)
	GetRun(ctx context.Context, id int64) (*store.Run, error)
	ListReleases(ctx context.Context, appID int64, limit int) ([]*store.Release, error)
	// StepLog is a step's whole log, from the database or the archive.
	StepLog(ctx context.Context, runID int64, step string) (string, error)
	ListTraces(ctx context.Context, q store.TraceQuery) ([]store.Trace, error)
	GetTrace(ctx context.Context, id string) (*store.Trace, []store.Span, error)
	ListProblems(ctx context.Context, app string, since time.Time, withDismissed bool, limit int) ([]store.Problem, error)
	// QueueRun puts a run of a branch in the CI queue.
	QueueRun(ctx context.Context, app *store.App, branch string) (*store.Run, error)
	CancelRun(ctx context.Context, id int64) error
}

// ---- inputs and outputs ----

type appIn struct {
	App string `json:"app" jsonschema:"el nombre de la aplicación"`
}

type ejecucionIn struct {
	ID int64 `json:"id" jsonschema:"el número de la ejecución"`
}

type registroIn struct {
	Ejecucion int64  `json:"ejecucion" jsonschema:"el número de la ejecución"`
	Paso      string `json:"paso" jsonschema:"el paso, p. ej. web:build o api:test"`
	Lineas    int    `json:"lineas,omitempty" jsonschema:"cuántas líneas del final, hasta 2000 (200 si se omite)"`
}

type solicitudesIn struct {
	Servicio string `json:"servicio,omitempty" jsonschema:"aplicación/servicio, p. ej. shop/web; vacío para todas"`
	Fallidas bool   `json:"fallidas,omitempty" jsonschema:"solo las que fallaron"`
}

type solicitudIn struct {
	ID string `json:"id" jsonschema:"el identificador de la traza"`
}

type lanzarIn struct {
	App  string `json:"app" jsonschema:"el nombre de la aplicación"`
	Rama string `json:"rama" jsonschema:"la rama a probar; no la principal, que despliega"`
}

// Corrida is a run as agents see it.
type Corrida struct {
	ID        int64      `json:"id"`
	App       string     `json:"app"`
	Rama      string     `json:"rama"`
	Commit    string     `json:"commit"`
	Evento    string     `json:"evento"`
	Despliega bool       `json:"despliega"`
	Estado    string     `json:"estado"`
	Mensaje   string     `json:"mensaje,omitempty"`
	Creada    time.Time  `json:"creada"`
	Termino   *time.Time `json:"termino,omitempty"`
	Pasos     []Paso     `json:"pasos,omitempty"`
}

// Paso is one step of a run.
type Paso struct {
	ID      string `json:"id"`
	Tipo    string `json:"tipo"`
	Estado  string `json:"estado"`
	Mensaje string `json:"mensaje,omitempty"`
}

// Version is a release as agents see it.
type Version struct {
	Numero       int64     `json:"numero"`
	Commit       string    `json:"commit"`
	Reversion    bool      `json:"reversion,omitempty"`
	Verificacion string    `json:"verificacion,omitempty"`
	MensajeVerif string    `json:"mensaje_verificacion,omitempty"`
	Creada       time.Time `json:"creada"`
}

// AppResumen is one app in apps_listar.
type AppResumen struct {
	Nombre          string   `json:"nombre"`
	Repo            string   `json:"repo"`
	RamaPrincipal   string   `json:"rama_principal"`
	UltimaEjecucion *Corrida `json:"ultima_ejecucion,omitempty"`
	Version         *Version `json:"version,omitempty"`
}

type appsOut struct {
	Apps []AppResumen `json:"apps"`
}

// Problema is a problem the platform recorded.
type Problema struct {
	Nivel      string    `json:"nivel"`
	Componente string    `json:"componente"`
	Mensaje    string    `json:"mensaje"`
	Detalle    string    `json:"detalle,omitempty"`
	Veces      int       `json:"veces"`
	Ultima     time.Time `json:"ultima"`
}

type estadoOut struct {
	App         AppResumen `json:"app"`
	Servicios   []string   `json:"servicios"`
	Ejecuciones []Corrida  `json:"ejecuciones"`
	Versiones   []Version  `json:"versiones"`
	Problemas   []Problema `json:"problemas"`
}

type registroOut struct {
	Ejecucion int64  `json:"ejecucion"`
	Paso      string `json:"paso"`
	Lineas    int    `json:"lineas"`
	Cortado   bool   `json:"cortado"` // only the end is shown
	Registro  string `json:"registro"`
}

type solicitudesOut struct {
	Solicitudes []store.Trace `json:"solicitudes"`
}

type solicitudOut struct {
	Solicitud store.Trace  `json:"solicitud"`
	Tramos    []store.Span `json:"tramos"`
}

// ---- conversions ----

func corrida(r *store.Run, app string) Corrida {
	c := Corrida{ID: r.ID, App: app, Rama: r.Branch, Commit: short(r.SHA), Evento: r.Event, Despliega: r.Deploy, Estado: r.Status,
		Mensaje: r.Message, Creada: r.CreatedAt, Termino: r.FinishedAt}
	for _, s := range r.Steps {
		c.Pasos = append(c.Pasos, Paso{ID: s.ID, Tipo: s.Kind, Estado: s.Status, Mensaje: s.Message})
	}
	return c
}

func version(r *store.Release) Version {
	return Version{Numero: r.Number, Commit: short(r.SHA), Reversion: r.RollbackOf != nil, Verificacion: r.VerifyStatus,
		MensajeVerif: r.VerifyMessage, Creada: r.CreatedAt}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ---- the tools ----

// addPlataformaTools registers the platform's tools: reading for any key,
// running and cancelling CI for keys that write (with a cargo). A key
// limited to some apps sees and acts on those only.
func (s *Server) addPlataformaTools(srv *mcp.Server, write bool) {
	addTool(s, srv, &mcp.Tool{Name: "apps_listar", Description: "Las aplicaciones que despliega la plataforma, con su última ejecución y su versión en producción."}, s.appsListar)
	addTool(s, srv, &mcp.Tool{Name: "app_estado", Description: "El estado de una aplicación: sus servicios, sus últimas ejecuciones y versiones (con su verificación) y los problemas abiertos."}, s.appEstado)
	addTool(s, srv, &mcp.Tool{Name: "ejecucion_leer", Description: "Una ejecución de CI con sus pasos y el estado de cada uno. Para ver por qué falló un paso, use paso_registro."}, s.ejecucionLeer)
	addTool(s, srv, &mcp.Tool{Name: "paso_registro", Description: "El final del registro de un paso de una ejecución (200 líneas si no se piden más)."}, s.pasoRegistro)
	addTool(s, srv, &mcp.Tool{Name: "solicitudes_listar", Description: "Las solicitudes que guardó el servicio de trazas (las que fallaron, las lentas y una muestra), de la más nueva a la más vieja; por servicio o solo las que fallaron."}, s.solicitudesListar)
	addTool(s, srv, &mcp.Tool{Name: "solicitud_leer", Description: "Una solicitud guardada con todos sus tramos (la cascada): servicio, nombre, duración, error, atributos y eventos de cada paso."}, s.solicitudLeer)
	if write {
		addTool(s, srv, &mcp.Tool{Name: "ejecucion_lanzar", Description: "Pone en la cola de CI una ejecución de una rama, para probarla (pruebas y construcción). No se permite la rama principal, porque despliega a producción: eso lo hace una persona. Necesita un cargo abierto."}, s.ejecucionLanzar)
		addTool(s, srv, &mcp.Tool{Name: "ejecucion_cancelar", Description: "Cancela una ejecución en cola o en curso. Necesita un cargo abierto."}, s.ejecucionCancelar)
	}
}

// appFor finds an app the key may use.
func (s *Server) appFor(ctx context.Context, k store.AgentKey, name string) (*store.App, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("falta el nombre de la aplicación")
	}
	if !k.AllowsApp(name) {
		return nil, fmt.Errorf("esta llave no tiene acceso a la aplicación %s", name)
	}
	a, err := s.Plataforma.GetApp(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no existe la aplicación %s", name)
	}
	return a, err
}

// runFor finds a run of an app the key may use.
func (s *Server) runFor(ctx context.Context, k store.AgentKey, id int64) (*store.Run, *store.App, error) {
	r, err := s.Plataforma.GetRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, fmt.Errorf("no existe la ejecución %d", id)
	}
	if err != nil {
		return nil, nil, err
	}
	a, err := s.Plataforma.GetAppByID(ctx, r.AppID)
	if err != nil {
		return nil, nil, err
	}
	if !k.AllowsApp(a.Name) {
		return nil, nil, fmt.Errorf("esta llave no tiene acceso a la aplicación de la ejecución %d", id)
	}
	return r, a, nil
}

func (s *Server) resumen(ctx context.Context, a *store.App) (AppResumen, error) {
	out := AppResumen{Nombre: a.Name, Repo: a.Repo, RamaPrincipal: a.DefaultBranch}
	runs, err := s.Plataforma.ListRuns(ctx, a.ID, 1)
	if err != nil {
		return out, err
	}
	if len(runs) > 0 {
		c := corrida(runs[0], a.Name)
		out.UltimaEjecucion = &c
	}
	rels, err := s.Plataforma.ListReleases(ctx, a.ID, 1)
	if err != nil {
		return out, err
	}
	if len(rels) > 0 {
		v := version(rels[0])
		out.Version = &v
	}
	return out, nil
}

// appsListar (apps_listar) lists the apps the key may see.
func (s *Server) appsListar(ctx context.Context, k store.AgentKey, _ empty) (appsOut, error) {
	apps, err := s.Plataforma.ListApps(ctx)
	if err != nil {
		return appsOut{}, err
	}
	out := appsOut{Apps: []AppResumen{}}
	for _, a := range apps {
		if !k.AllowsApp(a.Name) {
			continue
		}
		r, err := s.resumen(ctx, a)
		if err != nil {
			return out, err
		}
		out.Apps = append(out.Apps, r)
	}
	return out, nil
}

// appEstado (app_estado) is one app: services, recent runs and releases,
// and its open problems.
func (s *Server) appEstado(ctx context.Context, k store.AgentKey, in appIn) (estadoOut, error) {
	a, err := s.appFor(ctx, k, in.App)
	if err != nil {
		return estadoOut{}, err
	}
	r, err := s.resumen(ctx, a)
	if err != nil {
		return estadoOut{}, err
	}
	out := estadoOut{App: r, Servicios: []string{}, Ejecuciones: []Corrida{}, Versiones: []Version{}, Problemas: []Problema{}}
	for _, svc := range a.Spec.Services {
		out.Servicios = append(out.Servicios, svc.Name)
	}
	runs, err := s.Plataforma.ListRuns(ctx, a.ID, 10)
	if err != nil {
		return out, err
	}
	for _, run := range runs {
		out.Ejecuciones = append(out.Ejecuciones, corrida(run, a.Name))
	}
	rels, err := s.Plataforma.ListReleases(ctx, a.ID, 5)
	if err != nil {
		return out, err
	}
	for _, rel := range rels {
		out.Versiones = append(out.Versiones, version(rel))
	}
	probs, err := s.Plataforma.ListProblems(ctx, a.Name, time.Now().AddDate(0, 0, -7), false, 10)
	if err != nil {
		return out, err
	}
	for _, p := range probs {
		if p.ResolvedAt == nil {
			out.Problemas = append(out.Problemas, Problema{Nivel: p.Level, Componente: p.Component, Mensaje: p.Message, Detalle: p.Detail, Veces: p.Count, Ultima: p.LastAt})
		}
	}
	return out, nil
}

// ejecucionLeer (ejecucion_leer) is one run with its steps.
func (s *Server) ejecucionLeer(ctx context.Context, k store.AgentKey, in ejecucionIn) (Corrida, error) {
	r, a, err := s.runFor(ctx, k, in.ID)
	if err != nil {
		return Corrida{}, err
	}
	return corrida(r, a.Name), nil
}

// pasoRegistro (paso_registro) is the end of a step's log.
func (s *Server) pasoRegistro(ctx context.Context, k store.AgentKey, in registroIn) (registroOut, error) {
	if _, _, err := s.runFor(ctx, k, in.Ejecucion); err != nil {
		return registroOut{}, err
	}
	n := in.Lineas
	if n <= 0 {
		n = 200
	}
	n = min(n, 2000)
	log, err := s.Plataforma.StepLog(ctx, in.Ejecucion, in.Paso)
	if errors.Is(err, store.ErrNotFound) {
		return registroOut{}, fmt.Errorf("la ejecución %d no tiene el paso %q", in.Ejecucion, in.Paso)
	}
	if err != nil {
		return registroOut{}, err
	}
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	out := registroOut{Ejecucion: in.Ejecucion, Paso: in.Paso}
	if len(lines) > n {
		lines, out.Cortado = lines[len(lines)-n:], true
	}
	out.Lineas, out.Registro = len(lines), strings.Join(lines, "\n")
	if LooksSecret(out.Registro) {
		out.Registro = "(el registro parece contener un secreto; ábralo en la interfaz)"
	}
	return out, nil
}

// traceAllowed says whether a key may see a request: it went through one of
// the key's apps (services are app/service).
func traceAllowed(k store.AgentKey, services []string) bool {
	if len(k.Apps) == 0 {
		return true
	}
	for _, svc := range services {
		app, _, _ := strings.Cut(svc, "/")
		if k.AllowsApp(app) {
			return true
		}
	}
	return false
}

// solicitudesListar (solicitudes_listar) lists kept requests.
func (s *Server) solicitudesListar(ctx context.Context, k store.AgentKey, in solicitudesIn) (solicitudesOut, error) {
	if in.Servicio != "" {
		app, _, _ := strings.Cut(in.Servicio, "/")
		if !k.AllowsApp(app) {
			return solicitudesOut{}, fmt.Errorf("esta llave no tiene acceso a la aplicación %s", app)
		}
	}
	list, err := s.Plataforma.ListTraces(ctx, store.TraceQuery{Service: in.Servicio, ErrorsOnly: in.Fallidas, Limit: 30})
	if err != nil {
		return solicitudesOut{}, err
	}
	out := solicitudesOut{Solicitudes: []store.Trace{}}
	for _, t := range list {
		if traceAllowed(k, t.Services) {
			out.Solicitudes = append(out.Solicitudes, t)
		}
	}
	return out, nil
}

// solicitudLeer (solicitud_leer) is one kept request with its spans.
func (s *Server) solicitudLeer(ctx context.Context, k store.AgentKey, in solicitudIn) (solicitudOut, error) {
	t, spans, err := s.Plataforma.GetTrace(ctx, strings.TrimSpace(in.ID))
	if errors.Is(err, store.ErrNotFound) {
		return solicitudOut{}, fmt.Errorf("no se guardó la solicitud %s, o ya venció su día", in.ID)
	}
	if err != nil {
		return solicitudOut{}, err
	}
	if !traceAllowed(k, t.Services) {
		return solicitudOut{}, errors.New("esta llave no tiene acceso a las aplicaciones de esa solicitud")
	}
	return solicitudOut{Solicitud: *t, Tramos: spans}, nil
}

// ejecucionLanzar (ejecucion_lanzar) queues a run of a branch other than
// the default one: agents test; deploying stays with people.
func (s *Server) ejecucionLanzar(ctx context.Context, k store.AgentKey, in lanzarIn) (Corrida, error) {
	if _, err := s.cargoFor(ctx, k); err != nil {
		return Corrida{}, err
	}
	a, err := s.appFor(ctx, k, in.App)
	if err != nil {
		return Corrida{}, err
	}
	branch := strings.TrimSpace(in.Rama)
	switch {
	case branch == "":
		return Corrida{}, errors.New("falta la rama")
	case strings.EqualFold(branch, a.DefaultBranch):
		return Corrida{}, fmt.Errorf("%s es la rama principal de %s: ejecutarla despliega a producción, y eso lo hace una persona", branch, a.Name)
	}
	r, err := s.Plataforma.QueueRun(ctx, a, branch)
	if err != nil {
		return Corrida{}, err
	}
	if r == nil {
		return Corrida{}, fmt.Errorf("no se puso en cola: la rama %s no tiene un rendimiento.yaml válido", branch)
	}
	return corrida(r, a.Name), nil
}

// ejecucionCancelar (ejecucion_cancelar) cancels a queued or running run.
func (s *Server) ejecucionCancelar(ctx context.Context, k store.AgentKey, in ejecucionIn) (Corrida, error) {
	if _, err := s.cargoFor(ctx, k); err != nil {
		return Corrida{}, err
	}
	_, a, err := s.runFor(ctx, k, in.ID)
	if err != nil {
		return Corrida{}, err
	}
	if err := s.Plataforma.CancelRun(ctx, in.ID); err != nil {
		return Corrida{}, err
	}
	r, err := s.Plataforma.GetRun(ctx, in.ID)
	if err != nil {
		return Corrida{}, err
	}
	return corrida(r, a.Name), nil
}
