package destino

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/rendimiento-ai/rendimiento/api/v1alpha1"
	"github.com/rendimiento-ai/rendimiento/internal/motor"
	"github.com/rendimiento-ai/rendimiento/internal/spec"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// Caja keeps sealed values by name (*secretos.Caja).
type Caja interface {
	Leer(ctx context.Context, nombre string) ([]byte, bool, error)
	Guardar(ctx context.Context, nombre string, valor []byte) error
	Borrar(ctx context.Context, nombre string) error
}

// Labels on the containers and volumes of the local destination.
const (
	etApp      = "rendimiento.ai/app"
	etServicio = "rendimiento.ai/service"
	etVersion  = "rendimiento.ai/release"
	etPuerto   = "rendimiento.ai/host-port"
	etParte    = "rendimiento.ai/part" // service, postgres, redis
)

// Local runs releases on this computer, as containers of its engine: each
// service of a release is one container (its first replica), on the app's
// own network, published on 127.0.0.1 and reached through the Proxy at
// <app>.localhost (its first service) or <service>.<app>.localhost. A new
// release starts beside the running one, and the old one goes once the new
// one answers. An app's Postgres and Redis are containers of its own, with
// credentials made once; its secrets are sealed in the Caja.
type Local struct {
	Motor *motor.Motor
	Caja  Caja
	// Esperar is how long a new release has to answer (default 3m).
	Esperar time.Duration
	// Puerto is the proxy's port, for the addresses the app's page shows
	// ("8000"; "" for 80).
	Puerto string
	// Responde checks a service once (nil: an HTTP or TCP check of its
	// port); tests replace it.
	Responde func(ctx context.Context, addr string, h *spec.Health) bool

	mu      sync.Mutex
	estados map[string]estadoLocal // app → its latest release's rollout
	rutas   map[string]string      // host → 127.0.0.1:port
}

type estadoLocal struct {
	version int64
	estado  Estado
	mensaje string
}

// Nombre is "local".
func (l *Local) Nombre() string { return "local" }

func red(app string) string { return "rendimiento-" + app }

// Crear makes the app's network.
func (l *Local) Crear(ctx context.Context, app *store.App, _ bool) error {
	return l.Motor.CrearRed(ctx, red(app.Name))
}

// Aplicar starts the release in the background; Estado reports how it goes.
func (l *Local) Aplicar(ctx context.Context, app string, rel *store.Release) error {
	for _, svc := range rel.Spec.Services {
		if svc.Image == "" && rel.Images[svc.Name] == "" {
			return fmt.Errorf("service %s has no image in release %d", svc.Name, rel.Number)
		}
		for _, n := range svc.Needs {
			if n.Kind == spec.NeedService && !propio(rel.Spec, n.Service) {
				return fmt.Errorf("service %s needs %s: on this computer a service can call only the services of its own app", svc.Name, n.Service)
			}
		}
	}
	l.poner(app, estadoLocal{version: rel.Number, estado: Pendiente, mensaje: "starting"})
	go l.desplegar(context.WithoutCancel(ctx), app, rel)
	return nil
}

func (l *Local) poner(app string, e estadoLocal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.estados == nil {
		l.estados = map[string]estadoLocal{}
	}
	if cur, ok := l.estados[app]; ok && cur.version > e.version {
		return // a newer release replaced this one
	}
	l.estados[app] = e
}

// desplegar starts the release's services next to the running ones, waits
// for them to answer, points the proxy at them and removes the old ones.
func (l *Local) desplegar(ctx context.Context, app string, rel *store.Release) {
	fail := func(format string, a ...any) {
		l.poner(app, estadoLocal{version: rel.Number, estado: Fallida, mensaje: fmt.Sprintf(format, a...)})
	}
	if err := l.Motor.CrearRed(ctx, red(app)); err != nil {
		fail("%v", err)
		return
	}
	needs, err := l.necesidades(ctx, app, rel.Spec)
	if err != nil {
		fail("%v", err)
		return
	}
	viejos, _ := l.Motor.Nombres(ctx, etApp+"="+app)
	nuevos := map[string]string{} // service → container
	rutas := map[string]string{}
	esperar := l.Esperar
	if esperar <= 0 {
		esperar = 3 * time.Minute
	}
	// The services of one release call each other by their containers'
	// names, which are known before any starts.
	for _, svc := range rel.Spec.Services {
		if svc.Port > 0 {
			needs["service:"+svc.Name] = direccion(contenedor(app, svc.Name, rel.Number), svc.Port)
		}
	}
	for i, svc := range rel.Spec.Services {
		name := contenedor(app, svc.Name, rel.Number)
		_ = l.Motor.Quitar(ctx, name) // a retried release starts clean
		image := rel.Images[svc.Name]
		if image == "" {
			image = svc.Image
		}
		env, err := l.entorno(ctx, app, svc, needs)
		if err != nil {
			fail("%s: %v", svc.Name, err)
			l.quitar(ctx, nuevos)
			return
		}
		c := motor.Contenedor{Nombre: name, Imagen: image, Env: env, Red: red(app), Siempre: true,
			Etiquetas: map[string]string{etApp: app, etServicio: svc.Name, etVersion: strconv.FormatInt(rel.Number, 10), etParte: "service"}}
		if len(svc.Command) > 0 {
			c.Comando = append(append([]string{}, svc.Command...), svc.Args...)
		}
		if svc.Volume != nil && svc.Volume.Mount != "" {
			vol := fmt.Sprintf("%s-%s-data", app, svc.Name)
			if err := l.Motor.CrearVolumen(ctx, vol, map[string]string{etApp: app}); err != nil {
				fail("%v", err)
				l.quitar(ctx, nuevos)
				return
			}
			c.Montajes = append(c.Montajes, motor.Montaje{Volumen: vol, Ruta: svc.Volume.Mount})
		}
		port := 0
		if svc.Port > 0 {
			if port, err = puertoLibre(); err != nil {
				fail("%v", err)
				l.quitar(ctx, nuevos)
				return
			}
			c.Puertos = []string{fmt.Sprintf("127.0.0.1:%d:%d", port, svc.Port)}
			c.Etiquetas[etPuerto] = strconv.Itoa(port)
		}
		res := spec.ResourcesFor(svc.Size, svc.Resources)
		c.CPUs, c.Memoria = limites(res)
		if err := l.Motor.Iniciar(ctx, c); err != nil {
			fail("%s: %v", svc.Name, err)
			l.quitar(ctx, nuevos)
			return
		}
		nuevos[svc.Name] = name
		if port > 0 {
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			for _, h := range hosts(app, svc, i == 0) {
				rutas[h] = addr
			}
		}
	}
	// Each new service must answer before anyone is sent to it.
	for _, svc := range rel.Spec.Services {
		if err := l.esperar(ctx, nuevos[svc.Name], svc, rutas, esperar); err != nil {
			fail("%s: %v", svc.Name, err)
			l.quitar(ctx, nuevos)
			return
		}
	}
	l.mu.Lock()
	if l.rutas == nil {
		l.rutas = map[string]string{}
	}
	for h := range l.rutas {
		if strings.HasSuffix(h, "."+app+".localhost") || h == app+".localhost" {
			delete(l.rutas, h)
		}
	}
	for h, addr := range rutas {
		l.rutas[h] = addr
	}
	l.mu.Unlock()
	for _, v := range viejos {
		if info, err := l.Motor.Inspeccionar(ctx, v); err == nil && info.Etiquetas[etParte] == "service" && info.Etiquetas[etVersion] != strconv.FormatInt(rel.Number, 10) {
			_ = l.Motor.Quitar(ctx, v)
		}
	}
	l.poner(app, estadoLocal{version: rel.Number, estado: Sana})
}

func (l *Local) quitar(ctx context.Context, containers map[string]string) {
	for _, c := range containers {
		_ = l.Motor.Quitar(ctx, c)
	}
}

// esperar waits for a service to answer: its health path over HTTP, a TCP
// connection to its port, or, without a port, for it to keep running.
func (l *Local) esperar(ctx context.Context, name string, svc spec.Service, rutas map[string]string, d time.Duration) error {
	deadline := time.Now().Add(d)
	info, _ := l.Motor.Inspeccionar(ctx, name)
	addr := ""
	if p := info.Etiquetas[etPuerto]; p != "" {
		addr = "127.0.0.1:" + p
	}
	parado := 0 // checks in a row it was not running
	for {
		info, err := l.Motor.Inspeccionar(ctx, name)
		if err == nil && !info.Corriendo && info.Estado != "created" {
			parado++
		} else {
			parado = 0
		}
		switch {
		case parado >= 3:
			// It keeps stopping (its restart policy brings it back): say
			// why, from what it wrote, before it is removed.
			why := info.Estado
			if why == "" {
				why = "exited"
			}
			if tail := l.Motor.Registros(ctx, name, 15); tail != "" {
				return fmt.Errorf("it stopped (%s); its last lines:\n%s", why, tail)
			}
			return fmt.Errorf("it stopped (%s) and wrote nothing", why)
		case err == nil && addr == "" && info.Corriendo && time.Until(deadline) < d-10*time.Second:
			return nil // no port: running for 10 seconds is enough
		case err == nil && addr != "" && l.responde(ctx, addr, svc.Health):
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("it did not answer within %s", d)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(l.pausa()):
		}
	}
}

// pausa is the wait between checks of a new release (short in tests).
func (l *Local) pausa() time.Duration {
	if l.Responde != nil {
		return 10 * time.Millisecond
	}
	return 2 * time.Second
}

func (l *Local) responde(ctx context.Context, addr string, h *spec.Health) bool {
	if l.Responde != nil {
		return l.Responde(ctx, addr, h)
	}
	return responde(ctx, addr, h)
}

// responde checks a service once: an HTTP answer below 500 on its health
// path, or else a TCP connection.
func responde(ctx context.Context, addr string, h *spec.Health) bool {
	if h != nil && h.Path != "" && !h.TCP {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+h.Path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode < 500
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// hosts are the names the proxy answers a service at.
func hosts(app string, svc spec.Service, first bool) []string {
	out := []string{svc.Name + "." + app + ".localhost"}
	if first {
		out = append(out, app+".localhost")
	}
	return append(out, svc.Hosts()...)
}

// necesidades starts the app's Postgres and Redis, when it needs them, and
// returns each one's address.
func (l *Local) necesidades(ctx context.Context, app string, sp spec.Spec) (map[string]string, error) {
	out := map[string]string{}
	if sp.Needs(spec.NeedPostgres) {
		pw, err := l.clave(ctx, app, "postgres")
		if err != nil {
			return nil, err
		}
		db := strings.ReplaceAll(app, "-", "_")
		name := app + "-postgres"
		if err := l.Motor.Asegurar(ctx, motor.Contenedor{Nombre: name, Imagen: "postgres:17-alpine", Red: red(app), Siempre: true,
			Env:       map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": pw, "POSTGRES_DB": db},
			Montajes:  []motor.Montaje{{Volumen: app + "-postgres-data", Ruta: "/var/lib/postgresql/data"}},
			Etiquetas: map[string]string{etApp: app, etParte: "postgres"}}); err != nil {
			return nil, fmt.Errorf("its Postgres: %w", err)
		}
		out[spec.NeedPostgres] = fmt.Sprintf("postgresql://app:%s@%s:5432/%s?sslmode=disable", pw, name, db)
	}
	if sp.Needs(spec.NeedRedis) {
		pw, err := l.clave(ctx, app, "redis")
		if err != nil {
			return nil, err
		}
		name := app + "-redis"
		if err := l.Motor.Asegurar(ctx, motor.Contenedor{Nombre: name, Imagen: "redis:7-alpine", Red: red(app), Siempre: true,
			Comando:   []string{"redis-server", "--requirepass", pw, "--appendonly", "yes"},
			Montajes:  []motor.Montaje{{Volumen: app + "-redis-data", Ruta: "/data"}},
			Etiquetas: map[string]string{etApp: app, etParte: "redis"}}); err != nil {
			return nil, fmt.Errorf("its Redis: %w", err)
		}
		out[spec.NeedRedis] = fmt.Sprintf("redis://:%s@%s:6379/0", pw, name)
	}
	return out, nil
}

// clave is the password of one of the app's needs, made once and sealed:
// the database is initialised with it, so it never changes.
func (l *Local) clave(ctx context.Context, app, need string) (string, error) {
	nombre := "app/" + app + "/need/" + need
	if v, ok, err := l.Caja.Leer(ctx, nombre); err != nil || ok {
		return string(v), err
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	pw := hex.EncodeToString(b)
	return pw, l.Caja.Guardar(ctx, nombre, []byte(pw))
}

// entorno is a service's environment: PORT, its env, its needs' addresses
// and the values of the secrets it names.
func (l *Local) entorno(ctx context.Context, app string, svc spec.Service, needs map[string]string) (map[string]string, error) {
	env := map[string]string{}
	for _, name := range svc.Secrets {
		vals, err := l.secreto(ctx, app, name)
		if err != nil {
			return nil, err
		}
		for k, v := range vals {
			env[k] = v
		}
	}
	for k, v := range svc.Env {
		env[k] = v
	}
	if svc.Port > 0 {
		env["PORT"] = strconv.Itoa(svc.Port)
	}
	for k, ref := range svc.SecretEnv {
		name, key, _ := strings.Cut(ref, "/")
		vals, err := l.secreto(ctx, app, name)
		if err != nil {
			return nil, err
		}
		if v, ok := vals[key]; ok {
			env[k] = v
		}
	}
	for _, n := range svc.Needs {
		key := n.Kind
		if n.Kind == spec.NeedService {
			key = "service:" + n.Service
		}
		if addr, ok := needs[key]; ok {
			env[n.EnvName()] = addr
		}
	}
	return env, nil
}

// contenedor is the name of a service's container in a release.
func contenedor(app, svc string, release int64) string {
	return fmt.Sprintf("%s-%s-r%d", app, svc, release)
}

// direccion is how another service calls one, as in the cluster.
func direccion(host string, port int) string {
	switch port {
	case 80:
		return "http://" + host
	case 443:
		return "https://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// propio reports whether a service the app needs is one of its own.
func propio(sp spec.Spec, name string) bool {
	if strings.Contains(name, "/") {
		return false
	}
	for _, svc := range sp.Services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

func secretoNombre(app, nombre string) string { return "app/" + app + "/secret/" + nombre }

// secreto is one of the app's secrets, its keys and values (none when not set).
func (l *Local) secreto(ctx context.Context, app, nombre string) (map[string]string, error) {
	raw, ok, err := l.Caja.Leer(ctx, secretoNombre(app, nombre))
	if err != nil || !ok {
		return map[string]string{}, err
	}
	vals := map[string]string{}
	return vals, json.Unmarshal(raw, &vals)
}

// Estado reports the release's rollout: as it went since rendimiento
// started, or, for one from before, whether its containers run.
func (l *Local) Estado(ctx context.Context, app string, release int64) (Estado, string, error) {
	l.mu.Lock()
	e, ok := l.estados[app]
	l.mu.Unlock()
	switch {
	case ok && e.version > release:
		return Reemplazada, "", nil
	case ok && e.version == release:
		return e.estado, e.mensaje, nil
	}
	names, err := l.Motor.Nombres(ctx, etVersion+"="+strconv.FormatInt(release, 10))
	if err != nil {
		return Pendiente, "", err
	}
	for _, n := range names {
		if info, err := l.Motor.Inspeccionar(ctx, n); err == nil && info.Etiquetas[etApp] == app && info.Corriendo {
			return Sana, "", nil
		}
	}
	return Pendiente, "not started yet", nil
}

// Tarea runs a deploy task in a container on the app's network: the
// service's image and environment for a service: task, its own otherwise.
func (l *Local) Tarea(ctx context.Context, a *store.App, rel *store.Release, t spec.Task) store.ReleaseTask {
	done := func(status, msg, log string) store.ReleaseTask {
		end := time.Now()
		return store.ReleaseTask{Status: status, Message: msg, Log: log, FinishedAt: &end}
	}
	image, env := t.Image, map[string]string{}
	if t.Service != "" {
		for _, svc := range rel.Spec.Services {
			if svc.Name == t.Service {
				needs, err := l.necesidades(ctx, a.Name, rel.Spec)
				if err != nil {
					return done(store.TaskFailed, err.Error(), "")
				}
				if env, err = l.entorno(ctx, a.Name, svc, needs); err != nil {
					return done(store.TaskFailed, err.Error(), "")
				}
				image = rel.Images[svc.Name]
			}
		}
	}
	for k, v := range t.Env {
		env[k] = v
	}
	for _, name := range t.Secrets {
		vals, err := l.secreto(ctx, a.Name, name)
		if err != nil {
			return done(store.TaskFailed, err.Error(), "")
		}
		for k, v := range vals {
			env[k] = v
		}
	}
	env["RENDIMIENTO_APP"], env["RENDIMIENTO_RELEASE"], env["GIT_SHA"] = a.Name, strconv.FormatInt(rel.Number, 10), rel.SHA
	timeout := 10 * time.Minute
	if t.Timeout > 0 {
		timeout = time.Duration(t.Timeout) * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var log strings.Builder
	err := l.Motor.Correr(tctx, motor.Contenedor{Nombre: fmt.Sprintf("%s-task-%s-r%d", a.Name, t.Name, rel.Number), Imagen: image,
		Comando: []string{"sh", "-c", t.Command}, Env: env, Red: red(a.Name)}, &log)
	switch {
	case tctx.Err() == context.DeadlineExceeded:
		return done(store.TaskFailed, "timed out", log.String())
	case err != nil:
		return done(store.TaskFailed, "the task failed: "+err.Error(), log.String())
	}
	return done(store.TaskSucceeded, "", log.String())
}

// Quitar removes the app's containers, volumes, network and sealed values.
func (l *Local) Quitar(ctx context.Context, app string) error {
	names, err := l.Motor.Nombres(ctx, etApp+"="+app)
	if err != nil {
		return err
	}
	for _, n := range names {
		_ = l.Motor.Quitar(ctx, n)
	}
	vols, _ := l.Motor.Volumenes(ctx, etApp+"="+app)
	for _, v := range vols {
		_ = l.Motor.QuitarVolumen(ctx, v)
	}
	for _, v := range []string{app + "-postgres-data", app + "-redis-data"} {
		_ = l.Motor.QuitarVolumen(ctx, v)
	}
	_ = l.Motor.QuitarRed(ctx, red(app))
	for _, need := range []string{"postgres", "redis"} {
		_ = l.Caja.Borrar(ctx, "app/"+app+"/need/"+need)
	}
	l.mu.Lock()
	delete(l.estados, app)
	for h := range l.rutas {
		if strings.HasSuffix(h, "."+app+".localhost") || h == app+".localhost" {
			delete(l.rutas, h)
		}
	}
	l.mu.Unlock()
	return nil
}

// servicios are the app's running service containers, newest release first.
func (l *Local) servicios(ctx context.Context, app string) []motor.Info {
	names, _ := l.Motor.Nombres(ctx, etApp+"="+app)
	var out []motor.Info
	for _, n := range names {
		if info, err := l.Motor.Inspeccionar(ctx, n); err == nil {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nombre < out[j].Nombre })
	return out
}

// Situacion is the app's status as the Apps page shows it.
func (l *Local) Situacion(ctx context.Context, app string) (Situacion, error) {
	st := v1alpha1.AppStatus{Phase: v1alpha1.PhaseWaiting}
	l.mu.Lock()
	e, ok := l.estados[app]
	l.mu.Unlock()
	if ok {
		st.Release, st.Message = e.version, e.mensaje
		switch e.estado {
		case Sana:
			st.Phase = v1alpha1.PhaseHealthy
		case Fallida:
			st.Phase = v1alpha1.PhaseDegraded
		default:
			st.Phase = v1alpha1.PhaseProgressing
		}
	}
	for _, info := range l.servicios(ctx, app) {
		if info.Etiquetas[etParte] != "service" {
			continue
		}
		s := v1alpha1.ServiceStatus{Name: info.Etiquetas[etServicio], Replicas: 1, Image: info.Imagen}
		if info.Corriendo {
			s.ReadyReplicas = 1
		}
		if p := info.Etiquetas[etPuerto]; p != "" {
			s.URL = l.url(s.Name + "." + app + ".localhost")
		}
		if !ok {
			if r, err := strconv.ParseInt(info.Etiquetas[etVersion], 10, 64); err == nil && r > st.Release {
				st.Release, st.Phase = r, v1alpha1.PhaseHealthy
			}
		}
		st.Services = append(st.Services, s)
	}
	return Situacion{Status: st}, nil
}

// url is the address of a host through the proxy, with its port.
func (l *Local) url(host string) string {
	if l.Puerto != "" && l.Puerto != "80" {
		host += ":" + l.Puerto
	}
	return "http://" + host + "/"
}

// Recursos is what runs for the app: its services' containers, with their
// release, and its Postgres and Redis.
func (l *Local) Recursos(ctx context.Context, a *store.App) (*Recurso, error) {
	root := &Recurso{Kind: "App", Name: a.Name, Health: "Healthy"}
	for _, info := range l.servicios(ctx, a.Name) {
		h := "Healthy"
		if !info.Corriendo {
			h = "Degraded"
		}
		kind := "Container"
		switch info.Etiquetas[etParte] {
		case "postgres", "redis":
			kind = "Database"
		}
		n := Recurso{Kind: kind, Name: info.Nombre, Health: h, Info: []string{info.Estado}}
		if r := info.Etiquetas[etVersion]; r != "" {
			n.Info = append(n.Info, "release #"+r)
		}
		if h != "Healthy" {
			root.Health = "Degraded"
		}
		root.Children = append(root.Children, n)
	}
	if len(root.Children) == 0 {
		root.Health = "Missing"
	}
	return root, nil
}

// Reiniciar restarts the app's service containers.
func (l *Local) Reiniciar(ctx context.Context, app string) error {
	for _, info := range l.servicios(ctx, app) {
		if info.Etiquetas[etParte] == "service" {
			if err := l.Motor.Reiniciar(ctx, info.Nombre); err != nil {
				return err
			}
		}
	}
	return nil
}

// Impacto lists the volumes removing the app would delete.
func (l *Local) Impacto(ctx context.Context, app string) (*Impacto, error) {
	im := &Impacto{Namespace: app, Volumes: []string{}, Secrets: []string{}}
	vols, err := l.Motor.Volumenes(ctx, etApp+"="+app)
	if err != nil {
		return nil, err
	}
	im.Volumes = append(im.Volumes, vols...)
	return im, nil
}

// Desconectar leaves the app's containers running, as they are.
func (l *Local) Desconectar(context.Context, string) error { return nil }

// LlavesSecreto lists the keys of one of the app's secrets.
func (l *Local) LlavesSecreto(ctx context.Context, app, nombre string) ([]string, error) {
	vals, err := l.secreto(ctx, app, nombre)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// GuardarSecreto adds, replaces or (nil) removes keys of one of the app's
// secrets, sealed in the Caja.
func (l *Local) GuardarSecreto(ctx context.Context, app, nombre string, valores map[string]*string) error {
	vals, err := l.secreto(ctx, app, nombre)
	if err != nil {
		return err
	}
	for k, v := range valores {
		if v == nil {
			delete(vals, k)
		} else {
			vals[k] = *v
		}
	}
	raw, _ := json.Marshal(vals)
	return l.Caja.Guardar(ctx, secretoNombre(app, nombre), raw)
}

// Recargar finds the routes of the services already running, after a
// restart of rendimiento.
func (l *Local) Recargar(ctx context.Context, apps []*store.App) {
	rutas := map[string]string{}
	for _, a := range apps {
		first := map[string]bool{}
		if len(a.Spec.Services) > 0 {
			first[a.Spec.Services[0].Name] = true
		}
		for _, info := range l.servicios(ctx, a.Name) {
			p := info.Etiquetas[etPuerto]
			if info.Etiquetas[etParte] != "service" || p == "" || !info.Corriendo {
				continue
			}
			name := info.Etiquetas[etServicio]
			svc := spec.Service{Name: name}
			for _, s := range a.Spec.Services {
				if s.Name == name {
					svc = s
				}
			}
			for _, h := range hosts(a.Name, svc, first[name]) {
				rutas[h] = "127.0.0.1:" + p
			}
		}
	}
	l.mu.Lock()
	l.rutas = rutas
	l.mu.Unlock()
}

// Ruta is where the proxy sends a host ("" when nothing runs there).
func (l *Local) Ruta(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rutas[strings.ToLower(host)]
}

// puertoLibre asks the system for a free port on 127.0.0.1.
func puertoLibre() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// limites are a size's limits in the engine's terms ("1.5", "512m"; "" for
// none).
func limites(r spec.Resources) (cpus, mem string) {
	if q, err := resource.ParseQuantity(r.CPULimit); err == nil && r.CPULimit != "" {
		cpus = strconv.FormatFloat(float64(q.MilliValue())/1000, 'f', -1, 64)
	}
	if q, err := resource.ParseQuantity(r.MemLimit); err == nil && r.MemLimit != "" {
		mem = strconv.FormatInt(q.Value()/(1<<20), 10) + "m"
	}
	return cpus, mem
}

var _ Destino = (*Local)(nil)
