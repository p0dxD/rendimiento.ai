// Package detect inspects a repository tree and guesses how each deployable
// service in it is built, tested and served. It is rule-based and works on
// any fs.FS, so it runs the same against a local checkout or a GitHub tree.
package detect

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Language is a detected language or runtime family.
type Language string

const (
	Go     Language = "go"
	Node   Language = "node"
	Python Language = "python"
	Java   Language = "java"
	Static Language = "static"
	Docker Language = "docker" // only a Dockerfile, language unknown
)

// Result describes one deployable service found in the repo.
type Result struct {
	Path      string   `json:"path"`
	Language  Language `json:"language"`
	Framework string   `json:"framework,omitempty"` // next, vite, express, fastapi, flask, django, spring, maven, gradle
	Version   string   `json:"version,omitempty"`   // language/runtime major version
	Port      int      `json:"port"`
	// Dockerfile is true when the repo already ships one; we never overwrite it.
	Dockerfile  bool   `json:"dockerfile"`
	TestImage   string `json:"testImage,omitempty"`
	TestCommand string `json:"testCommand,omitempty"`
	// Standalone is set for Next.js apps configured with output: 'standalone'.
	Standalone bool `json:"standalone,omitempty"`
	// Entrypoint is the Python app module, e.g. app.main:app or mysite.wsgi.
	Entrypoint string `json:"entrypoint,omitempty"`
	// Needs are services the code uses (postgres, redis), found from its
	// dependencies; the wizard offers them pre-selected.
	Needs   []string `json:"needs,omitempty"`
	Reasons []string `json:"reasons"`
}

var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"coverage": true, "docs": true, "test": true, "tests": true, "scripts": true,
	"argocd": true, "deploy": true, "k8s": true, "charts": true, "public": true,
}

// Detect returns the services found at the repo root or, when the root is
// not itself a service, in its immediate subdirectories (monorepos).
func Detect(fsys fs.FS) ([]Result, error) {
	if r, ok := detectDir(fsys, "."); ok {
		return []Result{r}, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") || skipDirs[n] {
			continue
		}
		if r, ok := detectDir(fsys, n); ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func detectDir(fsys fs.FS, dir string) (Result, bool) {
	r := Result{Path: dir}
	switch {
	case exists(fsys, dir, "go.mod"):
		detectGo(fsys, dir, &r)
	case exists(fsys, dir, "package.json"):
		if !detectNode(fsys, dir, &r) {
			return r, false
		}
	case exists(fsys, dir, "pyproject.toml"), exists(fsys, dir, "requirements.txt"):
		detectPython(fsys, dir, &r)
	case exists(fsys, dir, "pom.xml"), exists(fsys, dir, "build.gradle"), exists(fsys, dir, "build.gradle.kts"):
		detectJava(fsys, dir, &r)
	case exists(fsys, dir, "index.html"):
		r.Language, r.Port = Static, 8080
		r.Reasons = append(r.Reasons, "index.html at the root: static site")
	}
	if exists(fsys, dir, "Dockerfile") {
		r.Dockerfile = true
		r.Reasons = append(r.Reasons, "existing Dockerfile will be used as-is")
		if p := exposedPort(read(fsys, dir, "Dockerfile")); p > 0 {
			r.Port = p
			r.Reasons = append(r.Reasons, "port "+strconv.Itoa(p)+" from Dockerfile EXPOSE")
		}
		if r.Language == "" {
			r.Language = Docker
		}
	}
	if r.Language == "" {
		return r, false
	}
	detectNeeds(fsys, dir, &r)
	if r.Port == 0 {
		r.Port = 8080
	}
	return r, true
}

var goDirective = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

func detectGo(fsys fs.FS, dir string, r *Result) {
	r.Language, r.Port = Go, 8080
	r.Version = "1.24"
	if m := goDirective.FindSubmatch(read(fsys, dir, "go.mod")); m != nil {
		r.Version = string(m[1])
	}
	r.TestImage = "golang:" + r.Version
	r.TestCommand = "go test ./..."
	r.Reasons = append(r.Reasons, "go.mod found (go "+r.Version+")")
}

type packageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
}

var noTestScript = "no test specified"

// detectNode returns false for packages that are not deployable services
// (mobile apps, libraries without a start or build script).
func detectNode(fsys fs.FS, dir string, r *Result) bool {
	var pkg packageJSON
	if err := json.Unmarshal(read(fsys, dir, "package.json"), &pkg); err != nil {
		return false
	}
	has := func(dep string) bool {
		_, a := pkg.Dependencies[dep]
		_, b := pkg.DevDependencies[dep]
		return a || b
	}
	if has("react-native") || has("expo") {
		return false
	}
	r.Language, r.Version = Node, nodeMajor(pkg.Engines["node"])
	switch {
	case has("next"):
		r.Framework, r.Port = "next", 3000
		for _, cfg := range []string{"next.config.ts", "next.config.js", "next.config.mjs"} {
			if bytes.Contains(read(fsys, dir, cfg), []byte("standalone")) {
				r.Standalone = true
			}
		}
	case has("vite") && pkg.Scripts["start"] == "":
		r.Framework, r.Port = "vite", 8080 // built to static files, served by nginx
	case has("@nestjs/core"):
		r.Framework, r.Port = "nest", 3000
	case has("express"), has("fastify"), has("hono"), has("koa"):
		r.Framework, r.Port = "express", 3000
	default:
		if pkg.Scripts["start"] == "" && pkg.Scripts["build"] == "" {
			return false
		}
		r.Port = 3000
	}
	r.Reasons = append(r.Reasons, "package.json found (framework: "+orNone(r.Framework)+", node "+r.Version+")")
	if t := pkg.Scripts["test"]; t != "" && !strings.Contains(t, noTestScript) {
		r.TestImage = "node:" + r.Version + "-bookworm"
		r.TestCommand = installCmd(fsys, dir) + " && npm test"
	}
	return true
}

var digits = regexp.MustCompile(`\d+`)

func nodeMajor(engine string) string {
	if m := digits.FindString(engine); m != "" {
		if n, _ := strconv.Atoi(m); n >= 18 {
			return m
		}
	}
	return "20"
}

func installCmd(fsys fs.FS, dir string) string {
	switch {
	case exists(fsys, dir, "pnpm-lock.yaml"):
		return "corepack enable && pnpm install --frozen-lockfile"
	case exists(fsys, dir, "yarn.lock"):
		return "corepack enable && yarn install --frozen-lockfile"
	case exists(fsys, dir, "package-lock.json"):
		return "npm ci"
	}
	return "npm install"
}

func detectPython(fsys fs.FS, dir string, r *Result) {
	r.Language, r.Version, r.Port = Python, "3.12", 8000
	deps := strings.ToLower(string(read(fsys, dir, "requirements.txt")) + string(read(fsys, dir, "pyproject.toml")))
	switch {
	case strings.Contains(deps, "fastapi"):
		r.Framework = "fastapi"
	case strings.Contains(deps, "django"):
		r.Framework = "django"
	case strings.Contains(deps, "flask"):
		r.Framework, r.Port = "flask", 5000
	}
	r.Entrypoint = pythonEntrypoint(fsys, dir, r.Framework)
	r.Reasons = append(r.Reasons, "Python project (framework: "+orNone(r.Framework)+")")
	if strings.Contains(deps, "pytest") || exists(fsys, dir, "tests") {
		r.TestImage = "python:" + r.Version + "-slim"
		install := "pip install -r requirements.txt"
		if !exists(fsys, dir, "requirements.txt") {
			install = "pip install ."
		}
		r.TestCommand = install + " pytest && pytest -q"
	}
}

func pythonEntrypoint(fsys fs.FS, dir, framework string) string {
	if framework == "django" {
		matches, _ := fs.Glob(fsys, path.Join(dir, "*", "wsgi.py"))
		if len(matches) > 0 {
			return path.Base(path.Dir(matches[0])) + ".wsgi"
		}
		return ""
	}
	for _, f := range []string{"app/main.py", "src/main.py", "main.py", "app.py", "server.py"} {
		if exists(fsys, dir, f) {
			return strings.ReplaceAll(strings.TrimSuffix(f, ".py"), "/", ".") + ":app"
		}
	}
	return ""
}

func detectJava(fsys fs.FS, dir string, r *Result) {
	r.Language, r.Version, r.Port = Java, "21", 8080
	if exists(fsys, dir, "pom.xml") {
		r.Framework = "maven"
		r.TestImage, r.TestCommand = "maven:3-eclipse-temurin-21", "mvn -B -q test"
	} else {
		r.Framework = "gradle"
		r.TestImage, r.TestCommand = "gradle:8-jdk21", "gradle test --no-daemon"
	}
	r.Reasons = append(r.Reasons, "Java project built with "+r.Framework+" (JDK 21)")
}

var expose = regexp.MustCompile(`(?i)^\s*EXPOSE\s+(\d+)`)

func exposedPort(dockerfile []byte) int {
	sc := bufio.NewScanner(bytes.NewReader(dockerfile))
	port := 0
	for sc.Scan() {
		if m := expose.FindStringSubmatch(sc.Text()); m != nil {
			port, _ = strconv.Atoi(m[1]) // last EXPOSE wins (final stage)
		}
	}
	return port
}

func exists(fsys fs.FS, dir, name string) bool {
	_, err := fs.Stat(fsys, path.Join(dir, name))
	return err == nil
}

// needDrivers are client libraries that mean the code talks to a database
// or cache, by dependency file.
var needDrivers = []struct {
	need, file string
	pattern    *regexp.Regexp
}{
	{"postgres", "package.json", regexp.MustCompile(`"(pg|postgres|pg-promise|@neondatabase/serverless)"\s*:`)},
	{"postgres", "requirements.txt", regexp.MustCompile(`(?mi)^\s*(psycopg2?(-binary)?|psycopg\[|asyncpg|pg8000)\b`)},
	{"postgres", "pyproject.toml", regexp.MustCompile(`(?i)["']?(psycopg2?(-binary)?|asyncpg|pg8000)\b`)},
	{"postgres", "go.mod", regexp.MustCompile(`github\.com/(jackc/pgx|lib/pq)\b`)},
	{"postgres", "pom.xml", regexp.MustCompile(`<artifactId>postgresql</artifactId>`)},
	{"redis", "package.json", regexp.MustCompile(`"(redis|ioredis)"\s*:`)},
	{"redis", "requirements.txt", regexp.MustCompile(`(?mi)^\s*redis\b`)},
	{"redis", "pyproject.toml", regexp.MustCompile(`(?i)["']redis\b`)},
	{"redis", "go.mod", regexp.MustCompile(`github\.com/(redis/go-redis|go-redis/redis|gomodule/redigo)\b`)},
	{"redis", "pom.xml", regexp.MustCompile(`<artifactId>(jedis|lettuce-core|spring-boot-starter-data-redis)</artifactId>`)},
}

func detectNeeds(fsys fs.FS, dir string, r *Result) {
	seen := map[string]bool{}
	for _, d := range needDrivers {
		if seen[d.need] {
			continue
		}
		if b := read(fsys, dir, d.file); len(b) > 0 && d.pattern.Match(b) {
			seen[d.need] = true
			r.Needs = append(r.Needs, d.need)
			r.Reasons = append(r.Reasons, "uses "+d.need+" (a client library in "+d.file+")")
		}
	}
}

func read(fsys fs.FS, dir, name string) []byte {
	b, _ := fs.ReadFile(fsys, path.Join(dir, name))
	return b
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
