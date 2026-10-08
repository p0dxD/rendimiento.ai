package mercado

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/generate"
	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Archivo is where a repository keeps its page: the form's answers.
const Archivo = "pagina.json"

// Mercado opens and keeps the pages. Each page's repository is created in
// Org, through the GitHub App's installation there.
type Mercado struct {
	Org      string
	GitHub   *gh.Holder
	Platform *platform.Platform
	Store    *store.Store
	// Zones are the domains pages can be published under (the DNS provider's).
	Zones func(ctx context.Context) ([]string, error)
	Log   *slog.Logger
}

// Abrir is the form's first answers: the page and its address.
type Abrir struct {
	Pagina  Pagina `json:"pagina"`
	Dominio string `json:"dominio"` // e.g. ana.example.com
}

// Abrir opens a new page: a repository in Org with the page, its
// rendimiento.yaml and its Dockerfile, registered as an app. The commit
// is a push like any other, so the page builds and goes live from it.
func (m *Mercado) Abrir(ctx context.Context, login string, req Abrir) (*store.App, error) {
	p := req.Pagina
	p.Default()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	dominio := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Dominio), "."))
	if err := m.dominioLibre(ctx, dominio); err != nil {
		return nil, err
	}
	fotos, err := Fotos(&p)
	if err != nil {
		return nil, err
	}
	// The address's first word names the app and its repository too.
	name, _, _ := strings.Cut(dominio, ".")
	if name == "nuevo" || name == "preview" { // the Mercado's own addresses in the UI and API
		return nil, errors.New(M("there is already an app named %q; choose another address", name))
	}
	if _, err := m.Store.GetApp(ctx, name); err == nil {
		return nil, errors.New(M("there is already an app named %q; choose another address", name))
	}
	c, inst, err := m.GitHub.ForOwner(ctx, m.Org)
	if err != nil {
		return nil, err
	}
	repo, err := c.CreateOrgRepo(ctx, m.Org, name, p.Nombre+" · "+enIdioma(p.Idioma, "a page of the rendimiento Mercado", "una página del Mercado de rendimiento"))
	if err != nil {
		var ae *gh.APIError
		if errors.As(err, &ae) && ae.Status == 422 {
			return nil, errors.New(M("%s already has a repository named %q; choose another address", m.Org, name))
		}
		return nil, fmt.Errorf("create the repository: %w", err)
	}
	sp := especificacion(dominio)
	app, err := m.Platform.Register(ctx, platform.OnboardRequest{Installation: inst, Repo: repo.FullName, DefaultBranch: repo.DefaultBranch, Name: name, Spec: sp})
	if err != nil {
		return nil, err
	}
	if err := m.Store.CreatePuesto(ctx, &store.Puesto{AppID: app.ID, Owner: login, Tipo: p.Tipo, Nombre: p.Nombre}); err != nil {
		return nil, err
	}
	files, err := archivos(&p, fotos, sp)
	if err != nil {
		return nil, err
	}
	if _, err := c.CommitFiles(ctx, repo.FullName, repo.DefaultBranch, enIdioma(p.Idioma, "Open the page ", "Abrir la página ")+p.Nombre, files, nil); err != nil {
		if derr := m.Platform.DeleteApp(ctx, app); derr != nil {
			m.Log.Warn("could not remove the app of a page that failed to open", "app", app.Name, "err", derr)
		}
		return nil, fmt.Errorf("the repository %s was created, but saving the page in it failed: %w", repo.FullName, err)
	}
	// Whoever opened it may also change it in GitHub; an invitation is
	// emailed. Not being able to send one (an owner of Org already has
	// access) does not stop the page.
	if err := c.AddCollaborator(ctx, repo.FullName, login, "maintain"); err != nil {
		m.Log.Info("page owner not invited to its repository", "repo", repo.FullName, "login", login, "err", err)
	}
	return app, nil
}

// Leer reads a page from its repository's default branch.
func (m *Mercado) Leer(ctx context.Context, app *store.App) (*Pagina, error) {
	raw, err := m.Platform.GitHub.FileAt(ctx, app.InstallationID, app.Repo, Archivo, app.DefaultBranch)
	if err != nil {
		return nil, err
	}
	var p Pagina
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", Archivo, err)
	}
	return &p, nil
}

// Guardar saves a changed page as one commit: its HTML and answers, new
// photos, and without the photos it no longer uses. The push publishes it.
func (m *Mercado) Guardar(ctx context.Context, app *store.App, p Pagina) error {
	p.Default()
	if err := p.Validate(); err != nil {
		return err
	}
	antes, err := m.Leer(ctx, app)
	if err != nil {
		return err
	}
	fotos, err := Fotos(&p)
	if err != nil {
		return err
	}
	usadas := map[string]bool{}
	for _, f := range fotosDe(&p) {
		usadas[f] = true
	}
	var quitar []string
	for _, f := range fotosDe(antes) {
		if !usadas[f] {
			quitar = append(quitar, f)
		}
	}
	files, err := archivos(&p, fotos, nil)
	if err != nil {
		return err
	}
	owner, _, _ := strings.Cut(app.Repo, "/")
	c, _, err := m.GitHub.ForOwner(ctx, owner)
	if err != nil {
		return err
	}
	if _, err := c.CommitFiles(ctx, app.Repo, app.DefaultBranch, enIdioma(p.Idioma, "Update the page ", "Actualizar la página ")+p.Nombre, files, quitar); err != nil {
		return err
	}
	return m.Store.UpdatePuesto(ctx, app.ID, p.Tipo, p.Nombre)
}

// dominioLibre checks that a page can be published at dominio: under one
// of the zones, and not taken by another app.
func (m *Mercado) dominioLibre(ctx context.Context, dominio string) error {
	if dominio == "" {
		return errors.New(M("the page needs an address"))
	}
	zones, err := m.Zones(ctx)
	if err != nil {
		return err
	}
	if !enZona(dominio, zones) {
		return errors.New(M("%q is not an address the Mercado can publish: use a single word (letters, numbers, hyphens) under one of %s", dominio, strings.Join(zones, ", ")))
	}
	apps, err := m.Store.ListApps(ctx)
	if err != nil {
		return err
	}
	for _, a := range apps {
		for _, s := range a.Spec.Services {
			for _, h := range s.Hosts() {
				if strings.EqualFold(h, dominio) {
					return errors.New(M("%s is already taken by %s", dominio, a.Name))
				}
			}
		}
	}
	// Sites rendimiento does not manage may answer on it too.
	var ings networkingv1.IngressList
	if err := m.Platform.Kube.List(ctx, &ings); err != nil {
		return err
	}
	for _, ing := range ings.Items {
		for _, r := range ing.Spec.Rules {
			if strings.EqualFold(r.Host, dominio) {
				return errors.New(M("%s is already taken by %s", dominio, ing.Namespace+"/"+ing.Name))
			}
		}
	}
	return nil
}

// enZona tells whether dominio is one word (an app's name) under one of zones.
func enZona(dominio string, zones []string) bool {
	for _, z := range zones {
		if sub, found := strings.CutSuffix(dominio, "."+z); found && sub != "" && !strings.Contains(sub, ".") && generate.Slug(sub) == sub {
			return true
		}
	}
	return false
}

// especificacion is a page's rendimiento.yaml: one small static service.
func especificacion(dominio string) spec.Spec {
	sp := spec.Spec{Services: []spec.Service{{
		Name: "pagina", Path: ".", Port: 8080, Size: spec.SizeSmall, Replicas: 1, Domain: dominio,
		Build:     spec.Build{Builder: spec.BuilderDockerfile, Dockerfile: "Dockerfile"},
		Health:    &spec.Health{Path: "/"},
		Resources: &spec.ResourceOverride{CPU: "10m", Memory: "32Mi", MemoryLimit: "64Mi"},
	}}}
	sp.Default()
	return sp
}

// archivos are the files a commit of the page writes. sp is set only for
// the first commit; afterwards rendimiento.yaml belongs to the repository.
func archivos(p *Pagina, fotos map[string][]byte, sp *spec.Spec) (map[string][]byte, error) {
	html, err := Render(p, "")
	if err != nil {
		return nil, err
	}
	answers, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"index.html": html, Archivo: append(answers, '\n')}
	for name, raw := range fotos {
		files[name] = raw
	}
	if sp != nil {
		raw, err := sp.Marshal()
		if err != nil {
			return nil, err
		}
		files[spec.FileName] = append([]byte("# Made by the rendimiento Mercado: a static page, served by nginx.\n"), raw...)
		files["Dockerfile"] = []byte(dockerfile)
		files[".dockerignore"] = []byte(".git\nDockerfile\n.dockerignore\nrendimiento.yaml\nREADME.md\n")
		files["README.md"] = []byte(readme(p))
	}
	return files, nil
}

const dockerfile = `# The page is plain HTML and photos; nginx serves them on port 8080.
FROM nginxinc/nginx-unprivileged:stable-alpine
COPY . /usr/share/nginx/html
`

func readme(p *Pagina) string {
	return "# " + p.Nombre + `

A page made in the rendimiento Mercado. Change it from the Mercado's form, or
here: ` + "`pagina.json`" + ` holds the form's answers and ` + "`index.html`" + ` is the
page made from them (the form rewrites it on every save). Photos are in
` + "`fotos/`" + `. Every push to the default branch publishes the page.
`
}

// enIdioma picks the words the page's owner reads in GitHub (its commits
// and description), in the page's language.
func enIdioma(idioma, en, es string) string {
	if idioma == "en" {
		return en
	}
	return es
}
