package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/agentes"
	"github.com/rendimiento-ai/rendimiento/internal/api"
	"github.com/rendimiento-ai/rendimiento/internal/destino"
	"github.com/rendimiento-ai/rendimiento/internal/dns"
	"github.com/rendimiento-ai/rendimiento/internal/events"
	"github.com/rendimiento-ai/rendimiento/internal/generate"
	gh "github.com/rendimiento-ai/rendimiento/internal/github"
	"github.com/rendimiento-ai/rendimiento/internal/i18n"
	"github.com/rendimiento-ai/rendimiento/internal/ia"
	"github.com/rendimiento-ai/rendimiento/internal/motor"
	"github.com/rendimiento-ai/rendimiento/internal/notify"
	"github.com/rendimiento-ai/rendimiento/internal/pipeline"
	"github.com/rendimiento-ai/rendimiento/internal/platform"
	"github.com/rendimiento-ai/rendimiento/internal/problems"
	"github.com/rendimiento-ai/rendimiento/internal/ruta"
	"github.com/rendimiento-ai/rendimiento/internal/secretos"
	"github.com/rendimiento-ai/rendimiento/internal/store"
	"github.com/rendimiento-ai/rendimiento/internal/uptime"
	"github.com/rendimiento-ai/rendimiento/web"
)

// runLocal is rendimiento on one computer, without Kubernetes (MODE=local):
// one process serves the UI and the API, runs CI with the machine's
// container engine (containerd, or Docker), and runs the agents. Its own
// Postgres and image registry are containers it starts and keeps; its
// secrets are sealed in Postgres by a key in DATA_DIR. Apps are built and
// tested here, and run here by the local destination, reached through its
// proxy at <app>.localhost.
func runLocal(ctx context.Context, log *slog.Logger, problemLog *problems.Recorder, plain *slog.Logger) error {
	// The engines call iptables and friends, which live in the sbin folders
	// that a service's PATH may leave out.
	sbin(os.Getenv("PATH"))
	m, err := motor.Buscar()
	if err != nil {
		return err
	}
	if !m.Recomendado() {
		log.Warn("using Docker: it works, but containerd (nerdctl) is recommended")
	}
	log.Info("rendimiento on this computer", "engine", m.Bin)

	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("DATA_DIR: %w", err)
		}
		dir = filepath.Join(base, "rendimiento")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// The secrets key and the database password are made once, kept in
	// DATA_DIR, and never printed.
	keyFile := env("SECRETS_KEY_FILE", filepath.Join(dir, "llave-secretos"))
	if _, err := readOrMake(keyFile, secretos.NuevaLlave); err != nil {
		return fmt.Errorf("secrets key: %w", err)
	}
	setupToken, err := readOrMake(filepath.Join(dir, "ficha-configuracion"), randomHex)
	if err != nil {
		return err
	}
	if t := os.Getenv("SETUP_TOKEN"); t != "" {
		setupToken = t
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		pw, err := readOrMake(filepath.Join(dir, "clave-postgres"), randomHex)
		if err != nil {
			return err
		}
		port := os.Getenv("POSTGRES_PORT")
		if port == "" {
			// 55433, or the port it got the first time because that was taken
			// (a database from before this was kept is on 55433).
			file := filepath.Join(dir, "puerto-postgres")
			if _, err := m.Inspeccionar(ctx, "rendimiento-postgres"); err == nil {
				if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
					_ = os.WriteFile(file, []byte("55433\n"), 0o600)
				}
			}
			if port, err = puertoGuardado(file, 55433); err != nil {
				return err
			}
		}
		if err := m.Asegurar(ctx, motor.Contenedor{Nombre: "rendimiento-postgres", Imagen: env("POSTGRES_IMAGE", "postgres:17-alpine"), Siempre: true,
			Env:      map[string]string{"POSTGRES_USER": "rendimiento", "POSTGRES_PASSWORD": pw, "POSTGRES_DB": "rendimiento"},
			Montajes: []motor.Montaje{{Volumen: "rendimiento-postgres", Ruta: "/var/lib/postgresql/data"}},
			Puertos:  []string{"127.0.0.1:" + port + ":5432"}}); err != nil {
			return fmt.Errorf("the database: %w", err)
		}
		dsn = "postgres://rendimiento:" + pw + "@127.0.0.1:" + port + "/rendimiento?sslmode=disable"
	}
	registry := os.Getenv("REGISTRY")
	if registry == "" {
		registry = "localhost:5000"
	}
	if registry == "localhost:5000" && !registryAnswers(ctx, "http://127.0.0.1:5000/v2/") {
		if err := m.Asegurar(ctx, motor.Contenedor{Nombre: "rendimiento-registro", Imagen: "registry:2", Siempre: true,
			Montajes: []motor.Montaje{{Volumen: "rendimiento-registro", Ruta: "/var/lib/registry"}},
			Puertos:  []string{"127.0.0.1:5000:5000"}}); err != nil {
			return fmt.Errorf("the image registry: %w", err)
		}
	}
	buildkit, err := localBuildkit(ctx, m)
	if err != nil {
		return err
	}

	users := strings.FieldsFunc(os.Getenv("ALLOWED_USERS"), func(r rune) bool { return r == ',' || r == ' ' })
	st, err := openWhenReady(ctx, dsn, time.Minute)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	go problemLog.Run(ctx, st, plain)
	if _, err := st.SeedAdmins(ctx, users); err != nil {
		return fmt.Errorf("members: %w", err)
	}
	if ms, err := st.Members(ctx, store.DefaultOrg); err != nil {
		return fmt.Errorf("members: %w", err)
	} else if len(ms) == 0 {
		return errors.New("nobody can sign in: set ALLOWED_USERS to your GitHub login")
	}

	llave, err := secretos.LeerLlave(keyFile)
	if err != nil {
		return err
	}
	caja, err := secretos.Nueva(st, llave)
	if err != nil {
		return err
	}
	creds, aiKeys := secretos.Credenciales{Caja: caja}, secretos.LlavesIA{Caja: caja}
	holder := &gh.Holder{Accounts: splitList(os.Getenv("GITHUB_ACCOUNTS"))}
	baseURL := strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/")
	if c, err := creds.Load(ctx); err != nil {
		return fmt.Errorf("load GitHub credentials: %w", err)
	} else if c != nil {
		app, err := gh.New(*c)
		if err != nil {
			return err
		}
		holder.Set(app)
	} else {
		log.Warn("GitHub App not configured yet; open "+baseURL+"/setup", "setupToken", filepath.Join(dir, "ficha-configuracion"))
	}

	stepTimeout, err := time.ParseDuration(env("STEP_TIMEOUT", "45m"))
	if err != nil || stepTimeout < time.Minute {
		return fmt.Errorf("STEP_TIMEOUT must be a duration of at least 1m, got %q", os.Getenv("STEP_TIMEOUT"))
	}
	p := &platform.Platform{
		Store: st, GitHub: holder, Generator: generate.Templates{}, Hub: events.NewHub(), Log: log,
		Config: platform.Config{Registry: registry, BaseURL: baseURL, Zone: os.Getenv("DNS_ZONE"), Railpack: os.Getenv("RAILPACK_IMAGE") != ""},
	}
	executor := &pipeline.LocalExecutor{
		Motor: m, BuildkitAddr: buildkit, BuildImage: os.Getenv("BUILDKIT_IMAGE"), GitImage: os.Getenv("GIT_IMAGE"),
		InsecureRegistry: env("REGISTRY_INSECURE", "true") == "true", Token: p.CloneToken, Timeout: stepTimeout,
		RailpackImage: os.Getenv("RAILPACK_IMAGE"), RailpackFrontend: os.Getenv("RAILPACK_FRONTEND"), PostgresImage: os.Getenv("TEST_POSTGRES_IMAGE"),
	}
	parallel, _ := strconv.Atoi(env("MAX_PARALLEL_STEPS", "2"))
	p.Runner = pipeline.NewRunner(executor, p, parallel)
	p.Workers, _ = strconv.Atoi(env("PARALLEL_RUNS", "2"))
	p.PerApp, _ = strconv.Atoi(env("RUNS_PER_APP", "1"))
	p.Caches = executor
	// Releases run here, as containers, reached through the proxy at
	// <app>.localhost. Verification waits for each one to answer (and rolls
	// back one that does not); its window has no public checks to run here.
	loc := &destino.Local{Motor: m, Caja: caja}
	p.Destino = loc
	p.Verify = platform.VerifySettings{Window: time.Minute, Every: 20 * time.Second,
		Check: func(_ context.Context, t uptime.Target) store.Probe {
			return store.Probe{Service: t.Service, Kind: t.Kind, At: time.Now(), OK: true}
		}}
	if apps, err := st.ListApps(ctx); err == nil {
		loc.Recargar(ctx, apps)
	}

	srv := &api.Server{
		Platform: p, Store: st, GitHub: holder, Credentials: creds, DNS: dns.Noop{}, Log: log,
		BaseURL: baseURL, AllowedUsers: users, SetupToken: setupToken, SetupTokenCommand: "cat " + filepath.Join(dir, "ficha-configuracion"),
		AppName: env("GITHUB_APP_NAME", "rendimiento"), UI: web.Dist(),
	}
	aiCap, err := strconv.ParseFloat(env("AI_PLATFORM_CAP", "5"), 64)
	if err != nil || aiCap < 0 {
		return fmt.Errorf("AI_PLATFORM_CAP must be dollars a month, such as 5, got %q", os.Getenv("AI_PLATFORM_CAP"))
	}
	srv.IA = &ia.Gateway{Store: st, Keys: aiKeys,
		LocalURL: os.Getenv("AI_LOCAL_URL"), LocalModel: env("AI_LOCAL_MODEL", ia.DefaultLocalModel),
		PlatformKey: os.Getenv("AI_PLATFORM_KEY"), PlatformModel: env("AI_PLATFORM_MODEL", ia.DefaultAnthropicModel),
		PlatformCapMicros: int64(math.Round(aiCap * 1e6))}
	srv.IAKeys = aiKeys
	srv.Translator, srv.AgentLang = &agentes.Translator{Model: srv.IA}, env("NOTIFY_LANG", "en")
	rutaSrv := &ruta.Server{Store: st, Log: log.With("component", "ruta")}
	rutaSrv.Plataforma = plataformaMCP{Store: st, p: p, srv: srv}
	rutaSrv.Encargos = st
	srv.MCP = rutaSrv.Handler()
	notifier := &notify.Notifier{To: splitList(os.Getenv("NOTIFY_EMAIL_TO")), Log: log.With("component", "notify"), Lang: i18n.Parse(env("NOTIFY_LANG", "en"))}
	if key := os.Getenv("RESEND_API_KEY"); key != "" {
		notifier.Sender = &notify.Resend{APIKey: key, From: env("NOTIFY_EMAIL_FROM", "rendimiento <alerts@example.com>")}
	}
	p.Notify, srv.Notify = notifier, notifier

	// GitHub cannot send webhooks to this computer: ask it for new commits.
	every, err := time.ParseDuration(env("POLL_INTERVAL", "1m"))
	if err != nil || every < 15*time.Second {
		return fmt.Errorf("POLL_INTERVAL must be a duration of at least 15s, got %q", os.Getenv("POLL_INTERVAL"))
	}
	if gh.Local(baseURL) {
		go (&platform.Vigia{P: p, Every: every, Branches: holder.Branches}).Run(ctx)
	}

	errc := make(chan error, 2)
	// The agents and the CI queue, each in the one process holding its lock.
	go func() {
		release, err := st.LockAgents(ctx, 5*time.Second)
		if err != nil {
			return
		}
		defer release()
		lang := env("NOTIFY_LANG", "en")
		diag := &agentes.Diagnostician{Store: st, Model: srv.IA, Logs: srv.StepLogText, Changes: holder, Lang: lang, Log: log.With("component", "agents")}
		coder := &agentes.Coder{Store: st, Repos: holder, Model: srv.IA, Logs: srv.StepLogText, BaseURL: baseURL, Lang: lang, Log: log.With("component", "agents")}
		est := &agentes.Estimator{Store: st, Model: srv.IA, Lang: lang, Log: log.With("component", "agents")}
		go est.Run(ctx)
		go coder.Run(ctx)
		diag.Run(ctx)
	}()
	go func() {
		release, err := st.LockQueue(ctx, 5*time.Second)
		if err != nil {
			return
		}
		defer release()
		if requeued, failed, err := st.RequeueOrphans(ctx); err != nil {
			log.Error("could not recover interrupted runs", "err", err)
		} else if requeued+failed > 0 {
			log.Warn("recovered runs interrupted by the last restart", "requeued", requeued, "failed", failed)
		}
		log.Info("starting CI", "engine", m.Bin, "buildkit", buildkit, "registry", registry)
		p.Work(ctx)
	}()

	proxySrv := &http.Server{Addr: env("PROXY_LISTEN", "127.0.0.1:8000"), Handler: loc.Proxy(), ReadHeaderTimeout: 10 * time.Second}
	if _, port, err := net.SplitHostPort(proxySrv.Addr); err == nil {
		loc.Puerto = port
	}
	go func() {
		log.Info("apps at http://<app>.localhost"+strings.TrimPrefix(proxySrv.Addr, "127.0.0.1"), "addr", proxySrv.Addr)
		if err := proxySrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	p.ResumeVerifications(ctx)
	httpSrv := &http.Server{Addr: env("LISTEN", "127.0.0.1:8080"), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("listening", "addr", httpSrv.Addr, "baseURL", baseURL)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Open pages (live logs) keep their connections: after the wait they are
	// closed, which is a normal stop.
	_ = proxySrv.Shutdown(shutdown)
	if err := httpSrv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	log.Info("stopped")
	return nil
}

// localBuildkit is the BuildKit daemon builds use: BUILDKIT_ADDR, or the
// one containerd's tools set up (rootless, then system-wide), or, under
// Docker, a daemon in a container of its own.
func localBuildkit(ctx context.Context, m *motor.Motor) (string, error) {
	if addr := os.Getenv("BUILDKIT_ADDR"); addr != "" {
		return addr, nil
	}
	for _, sock := range []string{filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "buildkit", "buildkitd.sock"), "/run/buildkit/buildkitd.sock"} {
		if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix://" + sock, nil
		}
	}
	if m.Recomendado() {
		return "", errors.New("no BuildKit: start it with containerd-rootless-setuptool.sh install-buildkit, or set BUILDKIT_ADDR")
	}
	if err := m.Asegurar(ctx, motor.Contenedor{Nombre: "rendimiento-buildkit", Imagen: env("BUILDKIT_IMAGE", "moby/buildkit:v0.18.2"),
		Comando: []string{"buildkitd", "--addr", "tcp://0.0.0.0:1234"}, Privilegiado: true, Siempre: true,
		Montajes: []motor.Montaje{{Volumen: "rendimiento-buildkit", Ruta: "/var/lib/buildkit"}},
		Puertos:  []string{"127.0.0.1:1234:1234"}}); err != nil {
		return "", fmt.Errorf("BuildKit: %w", err)
	}
	return "tcp://127.0.0.1:1234", nil
}

// sbin adds the sbin folders to PATH when it lacks them.
func sbin(path string) {
	parts := filepath.SplitList(path)
	for _, d := range []string{"/usr/local/sbin", "/usr/sbin", "/sbin"} {
		if !slices.Contains(parts, d) {
			parts = append(parts, d)
		}
	}
	os.Setenv("PATH", strings.Join(parts, string(filepath.ListSeparator)))
}

// puertoGuardado is the port kept in file; the first time it is want, or,
// when something else already listens there, a free one, which is kept so
// it stays the same.
func puertoGuardado(file string, want int) (string, error) {
	if raw, err := os.ReadFile(file); err == nil {
		return strings.TrimSpace(string(raw)), nil
	}
	port := want
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", want)); err == nil {
		ln.Close()
	} else if ln, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
	}
	p := strconv.Itoa(port)
	return p, os.WriteFile(file, []byte(p+"\n"), 0o600)
}

// registryAnswers reports whether an image registry already answers there
// (one the machine had, which is then used).
func registryAnswers(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized
}

// readOrMake reads a value kept in a file, or makes it and keeps it there
// (readable only by this user).
func readOrMake(path string, make func() string) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(raw)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	v := make()
	if err := os.WriteFile(path, []byte(v+"\n"), 0o600); err != nil {
		return "", err
	}
	return v, nil
}

// randomHex is 16 random bytes in hex: a password or a token.
func randomHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// openWhenReady opens the database, waiting while its container starts.
func openWhenReady(ctx context.Context, dsn string, wait time.Duration) (*store.Store, error) {
	deadline := time.Now().Add(wait)
	for {
		st, err := store.Open(ctx, dsn)
		if err == nil {
			if err = st.Ping(ctx); err == nil {
				return st, nil
			}
			st.Close()
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}
