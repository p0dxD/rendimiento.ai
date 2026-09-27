package generate

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/p0dxD/rendimiento.ai/internal/detect"
)

func gen(t *testing.T, repo string, fsys fstest.MapFS) *Plan {
	t.Helper()
	rs, err := detect.Detect(fsys)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Templates{}.Generate(context.Background(), Input{RepoName: repo, Zone: "joserod.space", FS: fsys, Results: rs})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func f(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestGoRepoGetsDockerfile(t *testing.T) {
	p := gen(t, "Hello_World", fstest.MapFS{"go.mod": f("module x\ngo 1.23\n")})
	svc := p.Spec.Services[0]
	if svc.Name != "hello-world" || svc.Domain != "hello-world.joserod.space" || svc.Port != 8080 {
		t.Fatalf("unexpected service %+v", svc)
	}
	df := p.Files["Dockerfile"]
	for _, want := range []string{"FROM golang:1.23 AS build", "EXPOSE 8080", "distroless"} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, df)
		}
	}
}

func TestExistingDockerfileIsKept(t *testing.T) {
	p := gen(t, "secplus", fstest.MapFS{
		"package.json": f(`{"scripts":{"build":"next build","test":"vitest run"},"dependencies":{"next":"16"}}`),
		"Dockerfile":   f("FROM node\nEXPOSE 3000\n"),
	})
	if len(p.Files) != 0 {
		t.Fatalf("should not generate files, got %v", p.Files)
	}
	if p.Spec.Services[0].Test == nil {
		t.Fatal("expected test step")
	}
}

func TestMonorepoDomainsAndPython(t *testing.T) {
	p := gen(t, "stockpulse", fstest.MapFS{
		"api/requirements.txt": f("fastapi\n"),
		"api/app/main.py":      f(""),
		"ui/package.json":      f(`{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5"}}`),
	})
	got := map[string]string{}
	for _, s := range p.Spec.Services {
		got[s.Name] = s.Domain
	}
	if got["ui"] != "stockpulse.joserod.space" || got["api"] != "api-stockpulse.joserod.space" {
		t.Fatalf("domains = %v", got)
	}
	if df := p.Files["api/Dockerfile"]; !strings.Contains(df, `"uvicorn", "app.main:app"`) || !strings.Contains(df, "requirements.txt uvicorn") {
		t.Errorf("python Dockerfile:\n%s", df)
	}
	if df := p.Files["ui/Dockerfile"]; !strings.Contains(df, "nginx-unprivileged") || !strings.Contains(df, "listen 8080") {
		t.Errorf("vite Dockerfile:\n%s", df)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"jobsentry.net": "jobsentry-net", "123abc": "app-123abc", "My App": "my-app"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
