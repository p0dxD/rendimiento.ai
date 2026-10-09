// Package generate turns detection results into a proposed rendimiento.yaml
// plus any files the repo is missing (Dockerfiles). Generator is an interface
// so an AI-backed implementation can refine the template output later.
package generate

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"text/template"

	"github.com/p0dxD/rendimiento.ai/internal/detect"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/templates"
)

// Input is what generation works from: the repo, its files and what detection found.
type Input struct {
	RepoName string // e.g. "secplus"
	Zone     string // DNS zone for default domains, e.g. "joserod.space"
	FS       fs.FS
	Results  []detect.Result
}

// Plan is what the wizard shows and, once confirmed, commits in a PR.
type Plan struct {
	Spec  spec.Spec         `json:"spec"`
	Files map[string]string `json:"files"` // repo-relative path → content, only files that do not exist yet
}

// Generator proposes a rendimiento.yaml and missing files for a repo. Templates is the
// implementation; an AI-backed one can be added behind this interface.
type Generator interface {
	Generate(ctx context.Context, in Input) (*Plan, error)
}

// Templates is the deterministic, rule-based generator.
type Templates struct{}

var tmpl = template.Must(template.ParseFS(templates.Dockerfiles, "dockerfiles/*.tmpl"))

// Generate proposes one service per detected directory and a Dockerfile from templates for those
// without one.
func (Templates) Generate(_ context.Context, in Input) (*Plan, error) {
	if len(in.Results) == 0 {
		return nil, fmt.Errorf("no deployable service detected in %s", in.RepoName)
	}
	repo := Slug(in.RepoName)
	plan := &Plan{Files: map[string]string{}}
	frontDoor := pickFrontDoor(in.Results)
	for i, r := range in.Results {
		name := repo
		if r.Path != "." {
			name = Slug(path.Base(r.Path))
		}
		svc := spec.Service{
			Name:     name,
			Path:     r.Path,
			Language: string(r.Language),
			Port:     r.Port,
			Size:     sizeFor(r),
			Replicas: 1,
		}
		if r.TestCommand != "" {
			svc.Test = &spec.Test{Image: r.TestImage, Command: r.TestCommand}
		}
		for _, n := range r.Needs {
			svc.Needs = append(svc.Needs, spec.Need{Kind: n})
		}
		if in.Zone != "" {
			if i == frontDoor {
				svc.Domain = repo + "." + in.Zone
			} else {
				svc.Domain = name + "-" + repo + "." + in.Zone
			}
		}
		if !r.Dockerfile {
			df, err := dockerfile(in.FS, r)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", name, err)
			}
			plan.Files[path.Join(r.Path, "Dockerfile")] = df
		}
		plan.Spec.Services = append(plan.Spec.Services, svc)
	}
	plan.Spec.Default()
	if err := plan.Spec.Validate(); err != nil {
		return nil, err
	}
	return plan, nil
}

// pickFrontDoor chooses which service gets the bare <repo>.<zone> domain:
// the first UI-like service, else the first service.
func pickFrontDoor(rs []detect.Result) int {
	for i, r := range rs {
		switch r.Framework {
		case "next", "vite":
			return i
		}
		if r.Language == detect.Static {
			return i
		}
	}
	return 0
}

// sizeFor is the starting size for a language: Java large, Go, static and
// Vite sites small, the rest medium.
func sizeFor(r detect.Result) spec.Size {
	switch r.Language {
	case detect.Java:
		return spec.SizeLarge
	case detect.Go, detect.Static:
		return spec.SizeSmall
	}
	if r.Framework == "vite" {
		return spec.SizeSmall
	}
	return spec.SizeMedium
}

type dfData struct {
	Version, Cmd, NeedsServer string
	Port                      int
	HasRequirements           bool
}

// dockerfile writes a Dockerfile for the detected language from the
// templates (an error for a language with none).
func dockerfile(fsys fs.FS, r detect.Result) (string, error) {
	d := dfData{Version: r.Version, Port: r.Port}
	var name string
	switch r.Language {
	case detect.Go:
		name = "go"
	case detect.Node:
		switch {
		case r.Framework == "next" && r.Standalone:
			name = "node-next-standalone"
		case r.Framework == "vite":
			name = "node-static"
		default:
			name = "node"
		}
	case detect.Static:
		name = "static"
	case detect.Java:
		name = "java-" + r.Framework
	case detect.Python:
		name = "python"
		_, err := fs.Stat(fsys, path.Join(r.Path, "requirements.txt"))
		d.HasRequirements = err == nil
		var err2 error
		d.Cmd, d.NeedsServer, err2 = pythonCmd(r)
		if err2 != nil {
			return "", err2
		}
	default:
		return "", fmt.Errorf("no template for language %q; add a Dockerfile to the repo", r.Language)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name+".tmpl", d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// pythonCmd returns the container CMD (JSON form) and any server package
// that must be installed alongside the app's own requirements.
func pythonCmd(r detect.Result) (cmd, extra string, err error) {
	if r.Entrypoint == "" {
		return "", "", fmt.Errorf("could not find the Python app module; add a Dockerfile or main.py")
	}
	bind := fmt.Sprintf("0.0.0.0:%d", r.Port)
	switch r.Framework {
	case "fastapi":
		return fmt.Sprintf(`["uvicorn", %q, "--host", "0.0.0.0", "--port", "%d"]`, r.Entrypoint, r.Port), "uvicorn", nil
	default: // flask, django, generic WSGI
		return fmt.Sprintf(`["gunicorn", "--bind", %q, %q]`, bind, r.Entrypoint), "gunicorn", nil
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// Slug converts a repo or directory name into a DNS label.
func Slug(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "app-" + s
	}
	return strings.TrimRight(s, "-")
}
