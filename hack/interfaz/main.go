// Command interfaz is the server the browser tests (web/e2e) drive: the real
// API and the built UI, on an empty Postgres (DATABASE_URL), as on a
// computer without Kubernetes (MODE=local's destination, without
// containers). GitHub is a fake that serves every example of
// internal/spec/ejemplos as a repository, plus one without a
// rendimiento.yaml, so "New app" can be walked with each kind of app.
//
// It signs in the person "prueba" and writes the cookie as Playwright's
// storage state (-estado), then serves until stopped.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/api"
	"github.com/rendimiento-ai/rendimiento/internal/dns"
	"github.com/rendimiento-ai/rendimiento/internal/events"
	"github.com/rendimiento-ai/rendimiento/internal/generate"
	gh "github.com/rendimiento-ai/rendimiento/internal/github"
	"github.com/rendimiento-ai/rendimiento/internal/ia"
	"github.com/rendimiento-ai/rendimiento/internal/platform"
	"github.com/rendimiento-ai/rendimiento/internal/spec"
	"github.com/rendimiento-ai/rendimiento/internal/spec/ejemplos"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// cuenta is the GitHub account of the fake installation, and persona the
// person signed in.
const (
	cuenta  = "ejemplos"
	persona = "prueba"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "where to listen")
	ui := flag.String("ui", "web/dist", "the built UI")
	estado := flag.String("estado", "web/e2e/.estado.json", "where to write Playwright's storage state (the session cookie)")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := run(*addr, *ui, *estado, log); err != nil {
		fmt.Fprintln(os.Stderr, "interfaz:", err)
		os.Exit(1)
	}
}

func run(addr, ui, estado string, log *slog.Logger) error {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set: an empty Postgres database")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if _, err := st.SeedAdmins(ctx, []string{persona}); err != nil {
		return err
	}

	repos, err := repositorios()
	if err != nil {
		return err
	}
	falso := httptest.NewServer(githubFalso(repos, log))
	defer falso.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	creds := gh.Credentials{AppID: 1, Slug: "rendimiento-pruebas", ClientID: "pruebas", WebhookSecret: "pruebas",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		HTMLURL:    falso.URL + "/apps/rendimiento-pruebas"}
	app, err := gh.New(creds)
	if err != nil {
		return err
	}
	app.BaseURL = falso.URL
	holder := &gh.Holder{}
	holder.Set(app)

	base := "http://" + addr
	p := &platform.Platform{
		Store: st, GitHub: holder, Generator: generate.Templates{}, Hub: events.NewHub(), Log: log,
		Config: platform.Config{Registry: "localhost:5000", BaseURL: base},
	}
	srv := &api.Server{
		Platform: p, Store: st, GitHub: holder, Credentials: &credenciales{c: &creds}, DNS: dns.Noop{}, Log: log,
		BaseURL: base, AllowedUsers: []string{persona}, SetupToken: "pruebas", AppName: creds.Slug, UI: os.DirFS(ui),
		IA: &ia.Gateway{Store: st, LocalModel: ia.DefaultLocalModel, PlatformModel: ia.DefaultAnthropicModel},
	}
	cookie, err := srv.Session(ctx, persona, 24*time.Hour)
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(addr)
	if err := guardarEstado(estado, host, cookie); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Printf("interfaz: listening on %s, signed in as %s (%s)\n", base, persona, estado)
	return http.Serve(ln, srv.Handler())
}

// guardarEstado writes Playwright's storage state: the session cookie.
func guardarEstado(file, host string, c *http.Cookie) error {
	type cookie struct {
		Name     string  `json:"name"`
		Value    string  `json:"value"`
		Domain   string  `json:"domain"`
		Path     string  `json:"path"`
		Expires  float64 `json:"expires"`
		HTTPOnly bool    `json:"httpOnly"`
		Secure   bool    `json:"secure"`
		SameSite string  `json:"sameSite"`
	}
	b, err := json.MarshalIndent(map[string]any{
		"cookies": []cookie{{Name: c.Name, Value: c.Value, Domain: host, Path: "/", Expires: -1, HTTPOnly: true, SameSite: "Lax"}},
		"origins": []any{},
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o600)
}

// credenciales keeps the fake App's credentials in memory.
type credenciales struct{ c *gh.Credentials }

func (m *credenciales) Load(context.Context) (*gh.Credentials, error)   { return m.c, nil }
func (m *credenciales) Save(_ context.Context, c *gh.Credentials) error { m.c = c; return nil }

// repo is a fake repository: its files by path.
type repo struct {
	gh.Repo
	files map[string][]byte
}

// commit names the repository's only commit: the one of its files.
func (r *repo) commit() string {
	names := make([]string, 0, len(r.files))
	for n := range r.files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha1.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write(r.files[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// repositorios are the fake installation's repositories: one per example,
// carrying it as its rendimiento.yaml, and a Node app without one (the
// wizard proposes a rendimiento.yaml for it).
func repositorios() (map[string]*repo, error) {
	todos, err := ejemplos.Todos()
	if err != nil {
		return nil, err
	}
	out := map[string]*repo{}
	add := func(name string, files map[string][]byte) {
		full := cuenta + "/" + name
		out[full] = &repo{Repo: gh.Repo{ID: int64(len(out) + 1), FullName: full, Name: name, DefaultBranch: "main",
			HTMLURL: "https://github.com/" + full, PushedAt: time.Now().Add(-time.Duration(len(out)) * time.Hour).Format(time.RFC3339)},
			files: files}
	}
	for _, e := range todos {
		add(e.Nombre, map[string][]byte{spec.FileName: e.Crudo, "README.md": []byte("# " + e.Nombre + "\n")})
	}
	add("tienda-node", map[string][]byte{
		"package.json": []byte(`{"name": "tienda", "version": "1.0.0", "scripts": {"start": "node server.js"}, "dependencies": {"express": "^4.21.0"}}`),
		"server.js":    []byte("require('express')().get('/', (_, r) => r.send('hola')).listen(process.env.PORT || 3000);\n"),
	})
	return out, nil
}

// githubFalso answers the GitHub API calls the wizard makes: the App's
// installations and tokens, the installation's repositories, and their
// trees and files. Anything else is logged and answered 404, so a test
// that needs more says so.
func githubFalso(repos map[string]*repo, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	jsonOK := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	instalacion := map[string]any{"id": 1, "account": map[string]string{"login": cuenta},
		"permissions": map[string]string{"contents": "write", "pull_requests": "write", "checks": "write", "metadata": "read"}}
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, map[string]any{"id": 1, "slug": "rendimiento-pruebas", "name": "rendimiento-pruebas"})
	})
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, _ *http.Request) { jsonOK(w, []any{instalacion}) })
	mux.HandleFunc("GET /app/installations/1", func(w http.ResponseWriter, _ *http.Request) { jsonOK(w, instalacion) })
	mux.HandleFunc("POST /app/installations/1/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, map[string]any{"token": "pruebas", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		list := []gh.Repo{}
		for _, r := range repos {
			list = append(list, r.Repo)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		jsonOK(w, map[string]any{"total_count": len(list), "repositories": list})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if rp := repos[r.PathValue("owner")+"/"+r.PathValue("name")]; rp != nil {
			jsonOK(w, rp.Repo)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/git/trees/{ref}", func(w http.ResponseWriter, r *http.Request) {
		rp := repos[r.PathValue("owner")+"/"+r.PathValue("name")]
		if rp == nil {
			http.NotFound(w, r)
			return
		}
		tree := []map[string]any{}
		dirs := map[string]bool{}
		for p, b := range rp.files {
			for d := p; strings.Contains(d, "/"); {
				d = d[:strings.LastIndex(d, "/")]
				dirs[d] = true
			}
			tree = append(tree, map[string]any{"path": p, "type": "blob", "sha": blobSHA(b), "size": len(b)})
		}
		for d := range dirs {
			tree = append(tree, map[string]any{"path": d, "type": "tree", "sha": blobSHA([]byte(d))})
		}
		jsonOK(w, map[string]any{"sha": rp.commit(), "tree": tree, "truncated": false})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/git/blobs/{sha}", func(w http.ResponseWriter, r *http.Request) {
		if rp := repos[r.PathValue("owner")+"/"+r.PathValue("name")]; rp != nil {
			for _, b := range rp.files {
				if blobSHA(b) == r.PathValue("sha") {
					jsonOK(w, map[string]any{"content": base64.StdEncoding.EncodeToString(b), "encoding": "base64"})
					return
				}
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/commits/{ref}", func(w http.ResponseWriter, r *http.Request) {
		if rp := repos[r.PathValue("owner")+"/"+r.PathValue("name")]; rp != nil {
			jsonOK(w, map[string]any{"sha": rp.commit()})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/contents/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if rp := repos[r.PathValue("owner")+"/"+r.PathValue("name")]; rp != nil {
			if b, ok := rp.files[r.PathValue("path")]; ok {
				jsonOK(w, map[string]any{"content": base64.StdEncoding.EncodeToString(b), "encoding": "base64", "sha": blobSHA(b)})
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls", func(w http.ResponseWriter, _ *http.Request) { jsonOK(w, []any{}) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Warn("fake GitHub has no answer for this call", "method", r.Method, "path", r.URL.Path)
		http.NotFound(w, r)
	})
	return mux
}

// blobSHA names a file's contents, as git does (any stable name will do).
func blobSHA(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}
