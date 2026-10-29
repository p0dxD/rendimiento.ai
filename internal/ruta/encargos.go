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

// Encargos is where encargos live (the store): work asked of agents, with
// an owner and a life cycle.
type Encargos interface {
	CreateEncargo(ctx context.Context, e store.Encargo) (store.Encargo, error)
	Encargo(ctx context.Context, id int64) (store.Encargo, error)
	ListEncargos(ctx context.Context, orgID int64, openOnly bool, limit int) ([]store.Encargo, error)
	TakeEncargo(ctx context.Context, id, keyID, cargoID int64, who string) (store.Encargo, error)
	WaitEncargo(ctx context.Context, id, keyID, runID int64, who string) (store.Encargo, error)
	ResumeEncargo(ctx context.Context, id, keyID int64, who, note string) (store.Encargo, error)
	DeliverEncargo(ctx context.Context, id, keyID int64, who, result string, links []string) (store.Encargo, error)
	ReleaseEncargo(ctx context.Context, id, keyID int64, who, note string) (store.Encargo, error)
}

// Waits on a run: the default, and the longest one call may wait (the agent
// calls again to keep waiting). Under a minute, because MCP clients give up
// on a tool call after about that long (Claude Code does at 60 seconds):
// a longer wait came back to the agent as a timeout, not as an answer.
const (
	defaultWait = 45 * time.Second
	maxWait     = 50 * time.Second
)

// estados are the encargo states as agents read them.
var estados = map[string]string{
	store.EncargoPending: "pendiente", store.EncargoTaken: "tomado", store.EncargoWaiting: "esperando",
	store.EncargoDelivered: "entregado", store.EncargoReviewed: "revisado", store.EncargoCancelled: "cancelado",
}

var sucesos = map[string]string{
	"created": "creado", "taken": "tomado", "waiting": "esperando", "resumed": "terminó la espera", "delivered": "entregado",
	"reviewed": "revisado", "returned": "devuelto", "released": "soltado", "cancelled": "cancelado",
}

// EncargoOut is an encargo as agents see it.
type EncargoOut struct {
	ID          int64       `json:"id"`
	Titulo      string      `json:"titulo"`
	Cuerpo      string      `json:"cuerpo,omitempty"`
	App         string      `json:"app,omitempty"`
	Ejecucion   *int64      `json:"ejecucion,omitempty"`
	Estado      string      `json:"estado"`
	CreadoPor   string      `json:"creado_por"`
	Agente      string      `json:"agente,omitempty"`
	EsperaA     *int64      `json:"espera_a,omitempty"`
	Resultado   string      `json:"resultado,omitempty"`
	Enlaces     []string    `json:"enlaces,omitempty"`
	Comentario  string      `json:"comentario,omitempty"`
	Actualizado time.Time   `json:"actualizado"`
	Historia    []SucesoOut `json:"historia,omitempty"`
}

// SucesoOut is one step of an encargo's history.
type SucesoOut struct {
	Cuando time.Time `json:"cuando"`
	Quien  string    `json:"quien"`
	Que    string    `json:"que"`
	Nota   string    `json:"nota,omitempty"`
}

func encargoOut(e store.Encargo, full bool) EncargoOut {
	o := EncargoOut{ID: e.ID, Titulo: e.Title, App: e.App, Ejecucion: e.RunID, Estado: estados[e.Status], CreadoPor: e.CreatedBy,
		Agente: e.KeyName, EsperaA: e.WaitingRun, Resultado: e.Result, Enlaces: e.Links, Comentario: e.Comment, Actualizado: e.UpdatedAt}
	if full {
		o.Cuerpo = e.Body
		for _, l := range e.Log {
			o.Historia = append(o.Historia, SucesoOut{Cuando: l.At, Quien: l.Who, Que: sucesos[l.What], Nota: l.Note})
		}
	}
	return o
}

type encargosIn struct {
	Todos bool `json:"todos,omitempty" jsonschema:"también los revisados y cancelados"`
}

type encargosOut struct {
	Encargos []EncargoOut `json:"encargos"`
}

type encargoIn struct {
	ID int64 `json:"id" jsonschema:"el número del encargo"`
}

type crearEncargoIn struct {
	Titulo string `json:"titulo" jsonschema:"qué hay que hacer, corto"`
	Cuerpo string `json:"cuerpo,omitempty" jsonschema:"el detalle: qué se sabe, dónde mirar, cómo saber que quedó"`
	App    string `json:"app,omitempty" jsonschema:"la aplicación, si es de una"`
}

type esperarIn struct {
	ID        int64 `json:"id" jsonschema:"el número del encargo"`
	Ejecucion int64 `json:"ejecucion" jsonschema:"la ejecución de CI a esperar"`
	Segundos  int   `json:"segundos,omitempty" jsonschema:"cuánto esperar como máximo, hasta 900 (300 si se omite)"`
}

type esperarOut struct {
	Terminada bool       `json:"terminada"`
	Ejecucion Corrida    `json:"ejecucion"`
	Encargo   EncargoOut `json:"encargo"`
	Nota      string     `json:"nota"`
}

type entregarEncargoIn struct {
	ID        int64    `json:"id" jsonschema:"el número del encargo"`
	Resultado string   `json:"resultado" jsonschema:"qué hizo y cómo lo comprobó; qué debe revisar la persona"`
	Enlaces   []string `json:"enlaces,omitempty" jsonschema:"la rama, la ejecución, la solicitud de cambios"`
}

type soltarIn struct {
	ID     int64  `json:"id" jsonschema:"el número del encargo"`
	Motivo string `json:"motivo" jsonschema:"por qué lo suelta, para quien lo tome después"`
}

// addEncargoTools registers the encargo tools: reading them for any key;
// creating, taking, waiting, handing over and letting go for keys that
// write, inside a cargo.
func (s *Server) addEncargoTools(srv *mcp.Server, write bool) {
	addTool(s, srv, &mcp.Tool{Name: "encargos_listar", Description: "Los encargos: trabajo que una persona (u otro agente) pidió. Los abiertos, o todos. Un encargo pendiente lo puede tomar usted con encargo_tomar."}, s.encargosListar)
	addTool(s, srv, &mcp.Tool{Name: "encargo_leer", Description: "Un encargo completo: lo que se pide, quién lo tiene, lo que se entregó, el comentario de la persona si lo devolvió, y su historia."}, s.encargoLeer)
	if !write {
		return
	}
	addTool(s, srv, &mcp.Tool{Name: "encargo_crear", Description: "Crea un encargo pendiente: algo que hay que hacer y que usted no va a hacer ahora (p. ej. una falla que encontró). Necesita un cargo abierto."}, s.encargoCrear)
	addTool(s, srv, &mcp.Tool{Name: "encargo_tomar", Description: "Toma un encargo pendiente: queda a su nombre y nadie más lo puede tomar. Necesita un cargo abierto."}, s.encargoTomar)
	addTool(s, srv, &mcp.Tool{Name: "encargo_esperar", Description: "Espera a que termine una ejecución de CI de su encargo (hasta 50 segundos por llamada) y le dice cómo terminó. Si no terminó, vuelva a llamar. Úsela en vez de consultar una y otra vez."}, s.encargoEsperar)
	addTool(s, srv, &mcp.Tool{Name: "encargo_entregar", Description: "Entrega su encargo: qué hizo, cómo lo comprobó, y los enlaces (rama, ejecución, solicitud de cambios). La persona lo revisa o lo devuelve con un comentario."}, s.encargoEntregar)
	addTool(s, srv, &mcp.Tool{Name: "encargo_soltar", Description: "Suelta un encargo que no puede terminar, con el motivo; vuelve a quedar pendiente para otro."}, s.encargoSoltar)
}

// encargoFor reads an encargo the key may see (its app, if any).
func (s *Server) encargoFor(ctx context.Context, k store.AgentKey, id int64) (store.Encargo, error) {
	e, err := s.Encargos.Encargo(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return e, fmt.Errorf("no existe el encargo %d", id)
	}
	if err == nil && e.OrgID != k.OrgID {
		return e, fmt.Errorf("no existe el encargo %d", id)
	}
	if err != nil {
		return e, err
	}
	if e.App != "" && !k.AllowsApp(e.App) {
		return e, fmt.Errorf("esta llave no tiene acceso a la aplicación %s del encargo %d", e.App, id)
	}
	return e, nil
}

// stateErr explains a change the encargo's state does not allow.
func stateErr(e store.Encargo, k store.AgentKey, want string) error {
	switch {
	case e.KeyID != nil && *e.KeyID != k.ID && (e.Status == store.EncargoTaken || e.Status == store.EncargoWaiting):
		return fmt.Errorf("el encargo %d lo tiene %s", e.ID, e.KeyName)
	default:
		return fmt.Errorf("el encargo %d está %s; %s", e.ID, estados[e.Status], want)
	}
}

// encargosListar (encargos_listar) lists encargos the key may see.
func (s *Server) encargosListar(ctx context.Context, k store.AgentKey, in encargosIn) (encargosOut, error) {
	list, err := s.Encargos.ListEncargos(ctx, k.OrgID, !in.Todos, 50)
	if err != nil {
		return encargosOut{}, err
	}
	out := encargosOut{Encargos: []EncargoOut{}}
	for _, e := range list {
		if e.App == "" || k.AllowsApp(e.App) {
			out.Encargos = append(out.Encargos, encargoOut(e, false))
		}
	}
	return out, nil
}

// encargoLeer (encargo_leer) is one encargo with its history.
func (s *Server) encargoLeer(ctx context.Context, k store.AgentKey, in encargoIn) (EncargoOut, error) {
	e, err := s.encargoFor(ctx, k, in.ID)
	if err != nil {
		return EncargoOut{}, err
	}
	return encargoOut(e, true), nil
}

// encargoCrear (encargo_crear) leaves work for later, in the agent's cargo.
func (s *Server) encargoCrear(ctx context.Context, k store.AgentKey, in crearEncargoIn) (EncargoOut, error) {
	if _, err := s.cargoFor(ctx, k); err != nil {
		return EncargoOut{}, err
	}
	app := strings.TrimSpace(in.App)
	if app != "" {
		if _, err := s.appFor(ctx, k, app); err != nil {
			return EncargoOut{}, err
		}
	}
	if err := checkText(in.Titulo, in.Cuerpo, nil); err != nil {
		return EncargoOut{}, err
	}
	e, err := s.Encargos.CreateEncargo(ctx, store.Encargo{Title: strings.TrimSpace(in.Titulo), Body: in.Cuerpo, App: app, CreatedBy: k.Name, ByAgent: true, OrgID: k.OrgID})
	if err != nil {
		return EncargoOut{}, err
	}
	return encargoOut(e, true), nil
}

// encargoTomar (encargo_tomar) takes a pending encargo in the agent's cargo.
func (s *Server) encargoTomar(ctx context.Context, k store.AgentKey, in encargoIn) (EncargoOut, error) {
	c, err := s.cargoFor(ctx, k)
	if err != nil {
		return EncargoOut{}, err
	}
	e, err := s.encargoFor(ctx, k, in.ID)
	if err != nil {
		return EncargoOut{}, err
	}
	taken, err := s.Encargos.TakeEncargo(ctx, e.ID, k.ID, c.ID, k.Name)
	if errors.Is(err, store.ErrEncargoState) {
		return EncargoOut{}, stateErr(e, k, "solo se toma uno pendiente")
	}
	if err != nil {
		return EncargoOut{}, err
	}
	return encargoOut(taken, true), nil
}

// mine checks the encargo is the agent's, taken or waiting.
func (s *Server) mine(ctx context.Context, k store.AgentKey, id int64) (store.Encargo, error) {
	if _, err := s.cargoFor(ctx, k); err != nil {
		return store.Encargo{}, err
	}
	e, err := s.encargoFor(ctx, k, id)
	if err != nil {
		return e, err
	}
	if e.KeyID == nil || *e.KeyID != k.ID || (e.Status != store.EncargoTaken && e.Status != store.EncargoWaiting) {
		return e, stateErr(e, k, "primero tómelo con encargo_tomar")
	}
	return e, nil
}

func finished(status string) bool {
	return status == store.RunSucceeded || status == store.RunFailed || status == store.RunCancelled
}

// encargoEsperar (encargo_esperar) waits for a run of the agent's encargo to
// finish, up to 50 seconds a call: the encargo shows it is waiting, and the
// run's end (from the platform's events) wakes the call.
func (s *Server) encargoEsperar(ctx context.Context, k store.AgentKey, in esperarIn) (esperarOut, error) {
	if s.Plataforma == nil {
		return esperarOut{}, errors.New("esperar necesita las herramientas de la plataforma")
	}
	e, err := s.mine(ctx, k, in.ID)
	if err != nil {
		return esperarOut{}, err
	}
	run, app, err := s.runFor(ctx, k, in.Ejecucion)
	if err != nil {
		return esperarOut{}, err
	}
	wait := defaultWait
	if in.Segundos > 0 {
		wait = min(time.Duration(in.Segundos)*time.Second, maxWait)
	}
	if !finished(run.Status) {
		if e, err = s.Encargos.WaitEncargo(ctx, e.ID, k.ID, run.ID, k.Name); err != nil {
			return esperarOut{}, err
		}
		wctx, cancel := context.WithTimeout(ctx, wait)
		got, werr := s.Plataforma.WaitRun(wctx, run.ID)
		cancel()
		if got != nil {
			run = got
		}
		if werr != nil && !errors.Is(werr, context.DeadlineExceeded) {
			return esperarOut{}, werr
		}
	}
	out := esperarOut{Terminada: finished(run.Status), Ejecucion: corrida(run, app.Name)}
	if !out.Terminada {
		out.Encargo = encargoOut(e, false)
		out.Nota = fmt.Sprintf("la ejecución %d sigue %s; vuelva a llamar encargo_esperar para seguir esperando", run.ID, run.Status)
		return out, nil
	}
	note := fmt.Sprintf("la ejecución %d terminó: %s", run.ID, run.Status)
	if e.Status == store.EncargoWaiting {
		resumed, err := s.Encargos.ResumeEncargo(context.WithoutCancel(ctx), e.ID, k.ID, k.Name, note)
		switch {
		case errors.Is(err, store.ErrEncargoState):
			// Another call of the same agent (one its client gave up on)
			// saw the run end first and already resumed the encargo.
			if resumed, err = s.Encargos.Encargo(ctx, e.ID); err != nil {
				return esperarOut{}, err
			}
		case err != nil:
			return esperarOut{}, err
		}
		e = resumed
	}
	out.Encargo, out.Nota = encargoOut(e, false), note
	return out, nil
}

// encargoEntregar (encargo_entregar) hands the agent's encargo over.
func (s *Server) encargoEntregar(ctx context.Context, k store.AgentKey, in entregarEncargoIn) (EncargoOut, error) {
	e, err := s.mine(ctx, k, in.ID)
	if err != nil {
		return EncargoOut{}, err
	}
	result := strings.TrimSpace(in.Resultado)
	if result == "" {
		return EncargoOut{}, errors.New("diga qué hizo y cómo lo comprobó")
	}
	links := []string{}
	for _, l := range in.Enlaces {
		if l = strings.TrimSpace(l); l != "" {
			links = append(links, l)
		}
	}
	if len(links) > 10 {
		return EncargoOut{}, errors.New("hasta 10 enlaces")
	}
	if err := checkText(e.Title, result, nil); err != nil {
		return EncargoOut{}, err
	}
	if LooksSecret(strings.Join(links, " ")) {
		return EncargoOut{}, errSecret
	}
	d, err := s.Encargos.DeliverEncargo(ctx, e.ID, k.ID, k.Name, result, links)
	if err != nil {
		return EncargoOut{}, err
	}
	return encargoOut(d, true), nil
}

// encargoSoltar (encargo_soltar) lets go of the agent's encargo, with why.
func (s *Server) encargoSoltar(ctx context.Context, k store.AgentKey, in soltarIn) (EncargoOut, error) {
	e, err := s.mine(ctx, k, in.ID)
	if err != nil {
		return EncargoOut{}, err
	}
	motivo := strings.TrimSpace(in.Motivo)
	if motivo == "" || LooksSecret(motivo) {
		return EncargoOut{}, errors.New("diga por qué lo suelta (sin secretos)")
	}
	r, err := s.Encargos.ReleaseEncargo(ctx, e.ID, k.ID, k.Name, motivo)
	if err != nil {
		return EncargoOut{}, err
	}
	return encargoOut(r, true), nil
}

// encargosDe summarizes encargos for ruta_inicio: pending ones the key may
// take, and the ones it has.
func (s *Server) encargosDe(ctx context.Context, k store.AgentKey) (pendientes, suyos []EncargoOut, err error) {
	list, err := s.Encargos.ListEncargos(ctx, k.OrgID, true, 50)
	if err != nil {
		return nil, nil, err
	}
	pendientes, suyos = []EncargoOut{}, []EncargoOut{}
	for _, e := range list {
		if e.App != "" && !k.AllowsApp(e.App) {
			continue
		}
		switch {
		case e.Status == store.EncargoPending:
			pendientes = append(pendientes, encargoOut(e, false))
		case e.KeyID != nil && *e.KeyID == k.ID && (e.Status == store.EncargoTaken || e.Status == store.EncargoWaiting):
			suyos = append(suyos, encargoOut(e, false))
		}
	}
	return pendientes, suyos, nil
}
