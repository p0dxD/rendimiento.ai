package detect

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		fs   fstest.MapFS
		want []Result
	}{
		{
			name: "next standalone with Dockerfile (secplus)",
			fs: fstest.MapFS{
				"package.json":      file(`{"scripts":{"build":"next build","test":"vitest run"},"dependencies":{"next":"16.3.5","react":"19"}}`),
				"package-lock.json": file(`{}`),
				"next.config.ts":    file(`export default { output: "standalone" }`),
				"Dockerfile":        file("FROM node:20-alpine AS builder\nEXPOSE 3000\n"),
			},
			want: []Result{{Path: ".", Language: Node, Framework: "next", Version: "20", Port: 3000, Dockerfile: true, Standalone: true,
				TestImage: "node:20-bookworm", TestCommand: "npm ci && npm test"}},
		},
		{
			name: "go service without Dockerfile",
			fs:   fstest.MapFS{"go.mod": file("module x\n\ngo 1.23\n"), "main.go": file("package main")},
			want: []Result{{Path: ".", Language: Go, Version: "1.23", Port: 8080, TestImage: "golang:1.23", TestCommand: "go test ./..."}},
		},
		{
			name: "monorepo api (fastapi) + web (next) + mobile skipped (rendimiento/stockpulse)",
			fs: fstest.MapFS{
				"README.md":                   file("x"),
				"api/requirements.txt":        file("fastapi\nuvicorn\npytest\n"),
				"api/app/main.py":             file("app = FastAPI()"),
				"api/Dockerfile":              file("FROM python:3.12-slim\nEXPOSE 8000\n"),
				"web/package.json":            file(`{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15"},"engines":{"node":">=22"}}`),
				"mobile/package.json":         file(`{"dependencies":{"expo":"50","react-native":"0.7"}}`),
				"node_modules/x/package.json": file(`{"scripts":{"start":"x"}}`),
			},
			want: []Result{
				{Path: "api", Language: Python, Framework: "fastapi", Version: "3.12", Port: 8000, Dockerfile: true, Entrypoint: "app.main:app",
					TestImage: "python:3.12-slim", TestCommand: "pip install -r requirements.txt pytest && pytest -q"},
				{Path: "web", Language: Node, Framework: "next", Version: "22", Port: 3000},
			},
		},
		{
			name: "vite spa, flask, maven",
			fs: fstest.MapFS{
				"ui/package.json":      file(`{"scripts":{"build":"vite build","test":"echo \"Error: no test specified\" && exit 1"},"devDependencies":{"vite":"5"}}`),
				"svc/requirements.txt": file("Flask==3.0\n"),
				"svc/app.py":           file("app = Flask(__name__)"),
				"java/pom.xml":         file("<project/>"),
				"lib/package.json":     file(`{"name":"lib"}`),
			},
			want: []Result{
				{Path: "java", Language: Java, Framework: "maven", Version: "21", Port: 8080, TestImage: "maven:3-eclipse-temurin-21", TestCommand: "mvn -B -q test"},
				{Path: "svc", Language: Python, Framework: "flask", Version: "3.12", Port: 5000, Entrypoint: "app:app"},
				{Path: "ui", Language: Node, Framework: "vite", Version: "20", Port: 8080},
			},
		},
		{
			name: "nothing deployable",
			fs:   fstest.MapFS{"README.md": file("hi")},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Detect(tc.fs)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d results %+v, want %d", len(got), got, len(tc.want))
			}
			for i := range got {
				g := got[i]
				g.Reasons = nil
				if !reflect.DeepEqual(g, tc.want[i]) {
					t.Errorf("result %d:\n got  %+v\n want %+v", i, g, tc.want[i])
				}
			}
		})
	}
}
