package destino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/motor"
	"github.com/rendimiento-ai/rendimiento/internal/spec"
	"github.com/rendimiento-ai/rendimiento/internal/spec/ejemplos"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// engine pretends to be nerdctl: it keeps the containers it is asked to
// run, with their labels and env, and answers ps and inspect from them.
type engine struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer
	calls      []string
	// crash makes the service containers it starts stop at once, writing it.
	crash string
	// proc is what cat /proc/net/tcp shows inside a container.
	proc string
}

type fakeContainer struct {
	labels  map[string]string
	env     map[string]string
	image   string
	running bool
}

func (e *engine) exec(_ context.Context, _ string, args []string, out io.Writer) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, strings.Join(args, " "))
	switch args[0] {
	case "run":
		c := &fakeContainer{labels: map[string]string{}, env: map[string]string{}, running: true}
		name := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--name":
				name, i = args[i+1], i+1
			case "--label":
				k, v, _ := strings.Cut(args[i+1], "=")
				c.labels[k], i = v, i+1
			case "-e":
				k, v, _ := strings.Cut(args[i+1], "=")
				c.env[k], i = v, i+1
			case "-v", "-p", "--network", "--cpus", "--memory", "--restart", "--entrypoint", "-w", "--hostname", "--network-alias":
				i++
			case "-d", "--rm", "--quiet", "--privileged":
			default:
				if c.image == "" {
					c.image = args[i]
				}
			}
		}
		if e.crash != "" && c.labels[etParte] == "service" {
			c.running = false
		}
		e.containers[name] = c
	case "logs":
		fmt.Fprintln(out, e.crash)
	case "exec":
		fmt.Fprint(out, e.proc)
	case "rm":
		delete(e.containers, args[len(args)-1])
	case "ps":
		filter := strings.TrimPrefix(args[3], "label=")
		k, v, _ := strings.Cut(filter, "=")
		for name, c := range e.containers {
			if c.labels[k] == v {
				fmt.Fprintln(out, name)
			}
		}
	case "inspect":
		name := args[len(args)-1]
		c, ok := e.containers[name]
		if !ok {
			return fmt.Errorf("no such container")
		}
		if args[2] == "container" && len(args) == 4 { // Asegurar's existence check
			return nil
		}
		status := "running"
		if !c.running {
			status = "exited"
		}
		raw, _ := json.Marshal(map[string]any{"State": map[string]any{"Running": c.running, "Status": status},
			"Config": map[string]any{"Image": c.image, "Labels": c.labels}})
		fmt.Fprintln(out, string(raw))
	}
	return nil
}

type memCaja map[string][]byte

func (m memCaja) Leer(_ context.Context, n string) ([]byte, bool, error) {
	v, ok := m[n]
	return v, ok, nil
}
func (m memCaja) Guardar(_ context.Context, n string, v []byte) error { m[n] = v; return nil }
func (m memCaja) Borrar(_ context.Context, n string) error            { delete(m, n); return nil }

func waitEstado(t *testing.T, l *Local, app string, rel int64, want Estado) string {
	t.Helper()
	for i := 0; i < 300; i++ {
		st, msg, err := l.Estado(context.Background(), app, rel)
		if err == nil && st == want {
			return msg
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, msg, _ := l.Estado(context.Background(), app, rel)
	t.Fatalf("release %d: %v %q, want %v", rel, st, msg, want)
	return ""
}

// A release runs as a container per service, with its secrets and its
// Postgres; the proxy sends <app>.localhost to it; the next release starts
// beside it and the old one goes once the new one answers; one that never
// answers leaves the running one in place.
func TestLocal(t *testing.T) {
	ctx := context.Background()
	e := &engine{containers: map[string]*fakeContainer{}}
	answers := true
	var mu sync.Mutex
	l := &Local{Motor: &motor.Motor{Bin: "nerdctl", Exec: e.exec}, Caja: memCaja{}, Esperar: 300 * time.Millisecond,
		Responde: func(context.Context, string, *spec.Health) bool { mu.Lock(); defer mu.Unlock(); return answers }}

	sp := spec.Spec{Services: []spec.Service{{Name: "web", Port: 3000, Secrets: []string{"llaves"},
		Needs: []spec.Need{{Kind: spec.NeedPostgres}}, Env: map[string]string{"MODO": "prod"}}}}
	sp.Default()
	if err := l.Crear(ctx, &store.App{Name: "tienda"}, false); err != nil {
		t.Fatal(err)
	}
	v := "abc"
	if err := l.GuardarSecreto(ctx, "tienda", "llaves", map[string]*string{"API_KEY": &v}); err != nil {
		t.Fatal(err)
	}
	if keys, _ := l.LlavesSecreto(ctx, "tienda", "llaves"); strings.Join(keys, ",") != "API_KEY" {
		t.Fatalf("secret keys: %v", keys)
	}

	rel1 := &store.Release{Number: 1, Spec: sp, Images: map[string]string{"web": "localhost:5000/tienda-web@sha256:1"}}
	if err := l.Aplicar(ctx, "tienda", rel1); err != nil {
		t.Fatal(err)
	}
	waitEstado(t, l, "tienda", 1, Sana)
	web := e.containers["tienda-web-r1"]
	if web == nil || web.image != "localhost:5000/tienda-web@sha256:1" {
		t.Fatalf("containers: %v", e.containers)
	}
	for k, want := range map[string]string{"PORT": "3000", "MODO": "prod", "API_KEY": "abc"} {
		if web.env[k] != want {
			t.Errorf("env %s = %q, want %q", k, web.env[k], want)
		}
	}
	if !strings.HasPrefix(web.env["DATABASE_URL"], "postgresql://app:") || !strings.Contains(web.env["DATABASE_URL"], "@tienda-postgres:5432/tienda") {
		t.Errorf("DATABASE_URL: %q", web.env["DATABASE_URL"])
	}
	if e.containers["tienda-postgres"] == nil {
		t.Fatal("no Postgres for the app")
	}
	route := l.Ruta("tienda.localhost:8000")
	if route == "" || route != l.Ruta("web.tienda.localhost") || !strings.HasPrefix(route, "127.0.0.1:") {
		t.Fatalf("routes: %q %q", route, l.Ruta("web.tienda.localhost"))
	}
	l.Puerto = "8000"
	if st, _ := l.Situacion(ctx, "tienda"); st.Status.Release != 1 || len(st.Status.Services) != 1 || st.Status.Services[0].ReadyReplicas != 1 ||
		st.Status.Services[0].URL != "http://web.tienda.localhost:8000/" {
		t.Fatalf("situacion: %+v", st)
	}

	// The next release replaces the first once it answers.
	rel2 := &store.Release{Number: 2, Spec: sp, Images: map[string]string{"web": "localhost:5000/tienda-web@sha256:2"}}
	_ = l.Aplicar(ctx, "tienda", rel2)
	waitEstado(t, l, "tienda", 2, Sana)
	if e.containers["tienda-web-r1"] != nil || e.containers["tienda-web-r2"] == nil || e.containers["tienda-postgres"] == nil {
		t.Fatalf("after release 2: %v", e.containers)
	}
	if st, _, _ := l.Estado(ctx, "tienda", 1); st != Reemplazada {
		t.Fatalf("release 1 after 2: %v", st)
	}
	if l.Ruta("tienda.localhost") == route {
		t.Fatal("the route still goes to release 1")
	}

	// A release that never answers is removed; release 2 keeps running.
	mu.Lock()
	answers = false
	mu.Unlock()
	route2 := l.Ruta("tienda.localhost")
	_ = l.Aplicar(ctx, "tienda", &store.Release{Number: 3, Spec: sp, Images: map[string]string{"web": "localhost:5000/tienda-web@sha256:3"}})
	if msg := waitEstado(t, l, "tienda", 3, Fallida); !strings.Contains(msg, "did not answer") {
		t.Fatalf("release 3: %q", msg)
	}
	if e.containers["tienda-web-r3"] != nil || e.containers["tienda-web-r2"] == nil || l.Ruta("tienda.localhost") != route2 {
		t.Fatalf("after a failed release: %v, route %q", e.containers, l.Ruta("tienda.localhost"))
	}

	// One service calls another by its name, as in the cluster.
	two := spec.Spec{Services: []spec.Service{
		{Name: "web", Port: 3000, Needs: []spec.Need{{Kind: spec.NeedService, Service: "api"}}},
		{Name: "api", Port: 8000},
	}}
	two.Default()
	mu.Lock()
	answers = true
	mu.Unlock()
	_ = l.Aplicar(ctx, "tienda", &store.Release{Number: 4, Spec: two, Images: map[string]string{"web": "w@sha256:4", "api": "a@sha256:4"}})
	waitEstado(t, l, "tienda", 4, Sana)
	if got := e.containers["tienda-web-r4"].env["API_URL"]; got != "http://api:8000" {
		t.Fatalf("API_URL: %q", got)
	}
	cross := spec.Spec{Services: []spec.Service{{Name: "web", Port: 3000, Needs: []spec.Need{{Kind: spec.NeedService, Service: "otra/api"}}}}}
	cross.Default()
	if err := l.Aplicar(ctx, "tienda", &store.Release{Number: 5, Spec: cross, Images: map[string]string{"web": "w@sha256:5"}}); err == nil || !strings.Contains(err.Error(), "own app") {
		t.Fatalf("another app's service: %v", err)
	}

	// The proxy sends requests to the running release.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hola de "+r.Host) }))
	defer backend.Close()
	l.mu.Lock()
	l.rutas["tienda.localhost"] = strings.TrimPrefix(backend.URL, "http://")
	l.mu.Unlock()
	rec := httptest.NewRecorder()
	l.Proxy().ServeHTTP(rec, httptest.NewRequest("GET", "http://tienda.localhost:8000/", nil))
	if !strings.HasPrefix(rec.Body.String(), "hola de") {
		t.Fatalf("proxy: %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	l.Proxy().ServeHTTP(rec, httptest.NewRequest("GET", "http://nadie.localhost:8000/", nil))
	if rec.Code != 404 {
		t.Fatalf("an unknown host: %d", rec.Code)
	}

	// Removing the app removes all of it.
	if err := l.Quitar(ctx, "tienda"); err != nil {
		t.Fatal(err)
	}
	if len(e.containers) != 0 || l.Ruta("tienda.localhost") != "" {
		t.Fatalf("after Quitar: %v", e.containers)
	}
}

// Every example that runs something rolls out on the local destination.
func TestLocalEjemplos(t *testing.T) {
	todos, err := ejemplos.Todos()
	if err != nil {
		t.Fatal(err)
	}
	for _, ej := range todos {
		if !ej.Spec.Runs() {
			continue
		}
		e := &engine{containers: map[string]*fakeContainer{}}
		l := &Local{Motor: &motor.Motor{Bin: "nerdctl", Exec: e.exec}, Caja: memCaja{}, Esperar: 300 * time.Millisecond,
			Responde: func(context.Context, string, *spec.Health) bool { return true }}
		images := map[string]string{}
		for _, s := range ej.Spec.Services {
			images[s.Name] = s.Name + "@sha256:1"
		}
		if err := l.Aplicar(context.Background(), "ej-"+ej.Nombre, &store.Release{Number: 1, Spec: *ej.Spec, Images: images}); err != nil {
			t.Errorf("%s: %v", ej.Nombre, err)
			continue
		}
		waitEstado(t, l, "ej-"+ej.Nombre, 1, Sana)
	}
}

// A service that keeps stopping fails its release with what it wrote, read
// before its container is removed.
func TestLocalSeDetiene(t *testing.T) {
	e := &engine{containers: map[string]*fakeContainer{}, crash: "Error: listen EACCES: permission denied 0.0.0.0:80"}
	l := &Local{Motor: &motor.Motor{Bin: "nerdctl", Exec: e.exec}, Caja: memCaja{}, Esperar: time.Second,
		Responde: func(context.Context, string, *spec.Health) bool { return false }}
	sp := spec.Spec{Services: []spec.Service{{Name: "ui", Port: 80}}}
	sp.Default()
	_ = l.Aplicar(context.Background(), "portal", &store.Release{Number: 1, Spec: sp, Images: map[string]string{"ui": "ui@sha256:1"}})
	msg := waitEstado(t, l, "portal", 1, Fallida)
	if !strings.Contains(msg, "it stopped") || !strings.Contains(msg, "EACCES") {
		t.Fatalf("message: %q", msg)
	}
}

// procTCP is /proc/net/tcp with a server listening on 0.0.0.0:4000 and a
// connection to Redis (not listening).
const procTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0FA0 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0 100 0 0 10 0
   1: 0300140A:A1B2 0200140A:18EB 01 00000000:00000000 00:00000000 00000000  1000        0 2 1 0 20 4 30 10 -1
`

// A service that runs but does not answer on its port fails with where it
// listens instead, and with the localhost it cannot reach: ping-demo's
// backend listened on 4000 for port: 3000 and looked for Redis on
// localhost.
func TestLocalNoResponde(t *testing.T) {
	e := &engine{containers: map[string]*fakeContainer{}, proc: procTCP}
	l := &Local{Motor: &motor.Motor{Bin: "nerdctl", Exec: func(ctx context.Context, bin string, args []string, out io.Writer) error {
		if args[0] == "logs" {
			fmt.Fprintln(out, "listening on 4000\n[ioredis] Unhandled error event: Error: connect ECONNREFUSED 127.0.0.1:6379")
			return nil
		}
		return e.exec(ctx, bin, args, out)
	}}, Caja: memCaja{}, Esperar: 200 * time.Millisecond,
		Responde: func(context.Context, string, *spec.Health) bool { return false }}
	sp := spec.Spec{Services: []spec.Service{{Name: "backend", Port: 3000}}}
	sp.Default()
	_ = l.Aplicar(context.Background(), "ping-demo", &store.Release{Number: 1, Spec: sp, Images: map[string]string{"backend": "b@sha256:1"}})
	msg := waitEstado(t, l, "ping-demo", 1, Fallida)
	for _, want := range []string{"did not answer", "nothing listens on port 3000", "it listens on 4000", "$PORT",
		"localhost:6379", "REDIS_URL", "ECONNREFUSED"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
}

func TestPuertoEquivocado(t *testing.T) {
	socks := escuchando(procTCP + "   2: 0100007F:1F90 00000000:0000 0A 0 0 0 0 0 0\n" +
		"   0: 00000000000000000000000001000000:0050 00000000000000000000000000000000:0000 0A 0 0 0 0 0 0\n")
	if len(socks) != 3 || socks[0] != (escucha{puerto: 4000}) || !socks[1].local || !socks[2].local || socks[2].puerto != 80 {
		t.Fatalf("socks = %+v", socks)
	}
	for _, tc := range []struct {
		port int
		want string
	}{
		{4000, ""},
		{8080, "only on 127.0.0.1"},
		{3000, "it listens on 80, 4000, 8080"},
	} {
		if got := puertoEquivocado(tc.port, socks); tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("port %d: %q, want %q", tc.port, got, tc.want)
		}
	}
	if got := puertoEquivocado(3000, nil); !strings.Contains(got, "nor on any other") {
		t.Errorf("nothing listening: %q", got)
	}
}

func TestPistaLocalhost(t *testing.T) {
	for log, want := range map[string]string{
		"Error: connect ECONNREFUSED 127.0.0.1:6379":                               "REDIS_URL",
		`dial tcp [::1]:5432: connect: connection refused`:                         "DATABASE_URL",
		"could not connect to localhost:8081: Connection refused":                  "localhost:8081",
		"psql: error: connection to server at \"db\" (10.0.0.2), port 5432 failed": "",
	} {
		if got := pistaLocalhost(log); want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", log, got, want)
		}
	}
}

// The TCP check counts a connection that the other end closes at once (a
// published port with nothing listening behind it) as no answer.
func TestRespondeTCP(t *testing.T) {
	escuchar := func(cerrar bool) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				if cerrar {
					c.Close()
				} else {
					defer c.Close()
				}
			}
		}()
		return ln.Addr().String()
	}
	if !responde(context.Background(), escuchar(false), nil) {
		t.Error("a server that waits for the client answers")
	}
	if responde(context.Background(), escuchar(true), nil) {
		t.Error("a connection closed at once is no answer")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if responde(context.Background(), addr, nil) {
		t.Error("nothing listening is no answer")
	}
}
