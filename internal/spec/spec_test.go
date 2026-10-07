package spec

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	s, err := Parse([]byte("services:\n  - name: web\n"))
	if err != nil {
		t.Fatal(err)
	}
	svc := s.Services[0]
	if svc.Path != "." || svc.Port != 8080 || svc.Size != SizeSmall || svc.Replicas != 1 || svc.Build.Dockerfile != "Dockerfile" {
		t.Fatalf("defaults not applied: %+v", svc)
	}
}

func TestParseFull(t *testing.T) {
	in := `
services:
  - name: web
    language: node
    port: 3000
    test: { image: node:20-bookworm, command: npm ci && npm test }
    size: medium
    replicas: 2
    domain: secplus.joserod.space
    health: { path: /api/health }
    env: { NODE_ENV: production }
    secrets: [secplus-practice]
    volume: { size: 5Gi, mount: /data }
`
	s, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if s.Services[0].Volume.Size != "5Gi" || s.Services[0].Size.Resources().MemLimit != "512Mi" {
		t.Fatalf("unexpected: %+v", s.Services[0])
	}
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]string{
		"services: []":                                                     "at least one service",
		"services:\n  - name: Web":                                         "lowercase DNS label",
		"services:\n  - name: a\n  - name: a":                              "duplicated",
		"services:\n  - name: a\n    path: ../x":                           "relative",
		"services:\n  - name: a\n    size: huge":                           "small, medium or large",
		"services:\n  - name: a\n    domain: not_a_host":                   "valid hostname",
		"services:\n  - name: a\n    volume: {size: 5, mount: /d}":         "5Gi",
		"services:\n  - name: a\n    unknown: 1":                           "unknown field",
		"services:\n  - name: a\n    secretEnv: {TOKEN: nokey}":            "<secret>/<key>",
		"services:\n  - name: a\n    secretEnv: {1X: s/k}":                 "secretEnv key",
		"services:\n  - name: a\n    secretFiles: [{secret: s, mount: /}]": "absolute directory",
		"services:\n  - name: a\n    volume: {size: 1Gi, mount: /d}\n    secretFiles: [{secret: s, mount: /d}]": "already used",
		"services:\n  - name: a\n    volume: {mount: /d}":                                                       "5Gi",
		"services:\n  - name: a\n    volume: {existingClaim: Bad_Claim, mount: /d}":                             "valid claim name",
	}
	for in, want := range cases {
		_, err := Parse([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want containing %q", in, err, want)
		}
	}
}

// secplus as it runs today: env from one key, the whole secret as files, streaming.
func TestSecplusShape(t *testing.T) {
	s, err := Parse([]byte(`
services:
  - name: web
    port: 3000
    domain: secplus.joserod.space
    streaming: true
    tlsSecret: secplus-tls
    env: { PRACTICE_FILE: /private/practice.json }
    secretEnv: { PRACTICE_TOKEN: secplus-practice/token }
    secretFiles: [{ secret: secplus-practice, mount: /private }]
    secrets: [web-extra]
`))
	if err != nil {
		t.Fatal(err)
	}
	svc := s.Services[0]
	if got := strings.Join(svc.SecretNames(), ","); got != "secplus-practice,web-extra" {
		t.Fatalf("SecretNames = %s", got)
	}
	if svc.TLSSecretName() != "secplus-tls" || (Service{Name: "x"}).TLSSecretName() != "x-tls" {
		t.Fatal("TLSSecretName")
	}
}

func TestExistingClaim(t *testing.T) {
	s, err := Parse([]byte("services:\n  - name: web\n    volume: {existingClaim: simplerfc-data-pvc, mount: /data}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Services[0].ClaimName() != "simplerfc-data-pvc" || (Service{Name: "db", Volume: &Volume{Size: "1Gi"}}).ClaimName() != "db-data" {
		t.Fatal("ClaimName")
	}
}

// podoi as it runs today: a built website on two hosts, and Postgres from a
// ready-made image on its existing volume.
func TestPodoiShape(t *testing.T) {
	s, err := Parse([]byte(`
services:
  - name: podoi-website
    port: 3000
    domain: joserod.space
    aliases: [www.joserod.space]
    tlsSecret: podoi-website-tls
  - name: blog-postgres
    image: postgres:15-alpine
    port: 5432
    health: { tcp: true }
    volume: { existingClaim: blog-postgres-pvc, mount: /var/lib/postgresql/data }
    configFiles: [{ configMap: blog-postgres-init, mount: /docker-entrypoint-initdb.d }]
`))
	if err != nil {
		t.Fatal(err)
	}
	web, db := s.Services[0], s.Services[1]
	if strings.Join(web.Hosts(), ",") != "joserod.space,www.joserod.space" {
		t.Fatalf("hosts = %v", web.Hosts())
	}
	if db.VolumeFSGroup() != nil {
		t.Fatal("postgres must not get an fsGroup")
	}
	built := Service{Name: "x", Volume: &Volume{Size: "1Gi", Mount: "/d"}}
	if g := built.VolumeFSGroup(); g == nil || *g != 1001 {
		t.Fatal("built apps keep fsGroup 1001")
	}
	off := int64(-1)
	built.Volume.FSGroup = &off
	if built.VolumeFSGroup() != nil {
		t.Fatal("fsGroup -1 disables it")
	}
	for in, want := range map[string]string{
		"services:\n  - name: a\n    aliases: [b.example.com]":                            "need a domain",
		"services:\n  - name: a\n    domain: a.example.com\n    aliases: [a.example.com]": "more than once",
		"services:\n  - name: a\n    health: {tcp: true, path: /x}":                       "either path or tcp",
		"services:\n  - name: a\n    image: redis\n    test: {image: x, command: y}":      "no test step",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", in, err, want)
		}
	}
}

func TestJobs(t *testing.T) {
	s, err := Parse([]byte(`
services:
  - name: wellness-api
    port: 8000
jobs:
  - name: wellness-weekly-recap
    schedule: "0 9 * * MON"
    timeZone: America/New_York
    service: wellness-api
    command: [python, -c, "import main; main.send_weekly_recap()"]
    timeout: 300
    secretEnv: { DATABASE_URL: wellness-secret/DATABASE_URL }
  - name: congress-scraper
    schedule: "0 6 * * *"
    path: congress
`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Jobs[1].Build.Dockerfile != "Dockerfile" || s.Jobs[0].Size != SizeSmall || s.Jobs[1].ImageKey() != "job:congress-scraper" {
		t.Fatalf("defaults = %+v", s.Jobs)
	}
	for in, want := range map[string]string{
		"services:\n  - name: a\njobs:\n  - {name: j, schedule: '0 * * * *'}":                    "exactly one of",
		"services:\n  - name: a\njobs:\n  - {name: j, schedule: '0 * * * *', service: nope}":     "not a service",
		"services:\n  - name: a\njobs:\n  - {name: j, schedule: 'every hour', image: x}":         "not a cron schedule",
		"services:\n  - name: a\njobs:\n  - {name: a, schedule: '@daily', image: x}":             "already used",
		"services:\n  - name: a\njobs:\n  - {name: j, schedule: '@daily', image: x, service: a}": "exactly one of",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", in, err, want)
		}
	}
}

// stockpulse as it runs today.
func TestStockpulseShape(t *testing.T) {
	s, err := Parse([]byte(`
services:
  - name: stockpulse-ui
    port: 80
    domain: stockpulse.joserod.space
    aliases: [stockfinancia.com, www.stockfinancia.com]
    tlsSecret: stockpulse-tls
    streaming: true
    ingress:
      name: stockpulse-ingress
      tlsSecrets: { stockfinancia.com: stockfinancia-tls, www.stockfinancia.com: stockfinancia-tls }
      annotations:
        nginx.ingress.kubernetes.io/limit-rps: "5"
        nginx.ingress.kubernetes.io/limit-connections: "10"
        nginx.ingress.kubernetes.io/proxy-body-size: 10m
  - name: stockpulse-api
    port: 8000
    health: { path: /api/health, timeout: 5 }
    resources: { cpu: 250m, memory: 1Gi, cpuLimit: none, memoryLimit: 5Gi }
`))
	if err != nil {
		t.Fatal(err)
	}
	ui, api := s.Services[0], s.Services[1]
	if ui.IngressName() != "stockpulse-ingress" || (Service{Name: "x"}).IngressName() != "x" {
		t.Fatal("IngressName")
	}
	g := ui.TLSGroups()
	if len(g) != 2 || g[0][0] != "stockpulse-tls" || len(g[1][1].([]string)) != 2 || g[1][0] != "stockfinancia-tls" {
		t.Fatalf("TLSGroups = %v", g)
	}
	r := ResourcesFor(api.Size, api.Resources)
	if r.CPURequest != "250m" || r.MemRequest != "1Gi" || r.CPULimit != "" || r.MemLimit != "5Gi" {
		t.Fatalf("resources = %+v", r)
	}
	for in, want := range map[string]string{
		"services:\n  - name: a\n    ingress: {annotations: {nginx.ingress.kubernetes.io/configuration-snippet: x}}": "no snippets",
		"services:\n  - name: a\n    ingress: {annotations: {cert-manager.io/cluster-issuer: x}}":                    "not allowed",
		"services:\n  - name: a\n    domain: a.example.com\n    ingress: {tlsSecrets: {b.example.com: s}}":           "not one of the service's hosts",
		"services:\n  - name: a\n    resources: {memory: lots}":                                                      "not a valid quantity",
		"services:\n  - name: a\n    resources: {cpu: none}":                                                         "not a valid quantity",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", in, err, want)
		}
	}
}

func TestTouches(t *testing.T) {
	cases := []struct {
		file, dir string
		watch     []string
		want      bool
	}{
		{"ui/src/App.tsx", "ui", nil, true},
		{"api/main.py", "ui", nil, false},
		{"uikit/x.ts", "ui", nil, false}, // prefix of a name is not a match
		{"shared/util.py", "api", []string{"shared"}, true},
		{"README.md", ".", nil, true},
		{"congress/main.py", "congress/", nil, true},
	}
	for _, c := range cases {
		if got := Touches(c.file, c.dir, c.watch); got != c.want {
			t.Errorf("Touches(%q, %q, %v) = %v", c.file, c.dir, c.watch, got)
		}
	}
}

// wellness: /api on the website's hosts goes to the API.
func TestRoutes(t *testing.T) {
	s, err := Parse([]byte(`
services:
  - name: wellness-ui
    domain: wellness.jobsentry.net
    aliases: [wellbeingportal.app]
  - name: wellness-api
    domain: api.wellness.jobsentry.net
    routes: [wellness.jobsentry.net/api, wellbeingportal.app/api/]
`))
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(s.Services[1].AllRoutes())
	if got != "[{api.wellness.jobsentry.net /} {wellness.jobsentry.net /api} {wellbeingportal.app /api}]" {
		t.Fatalf("routes = %s", got)
	}
	for in, want := range map[string]string{
		"services:\n  - name: a\n    routes: [other.example.com/api]":                                                                               "not the domain or alias",
		"services:\n  - name: a\n    domain: a.example.com\n  - name: b\n    routes: [a.example.com]":                                               "needs a path",
		"services:\n  - name: a\n    domain: a.example.com\n  - name: b\n    routes: [a.example.com/x]\n  - name: c\n    routes: [a.example.com/x]": "already routed",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", in, err, want)
		}
	}
}

func TestValidateBuildGPUCatalog(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"services: [{name: a, build: {builder: nix}}]", "must be dockerfile or railpack"},
		{"services: [{name: a, build: {builder: dockerfile, start: x}}]", "only applies to Railpack"},
		{"services: [{name: a, gpu: 1, replicas: 2}]", "one replica"},
		{"services: [{name: a, gpu: 9}]", "between 0 and 8"},
		{"services: [{name: a, catalog: {env: 1BAD}}]", "not a valid variable"},
		{"services: [{name: a, catalog: {path: analyze}}]", "must start with /"},
		{"services: [{name: a, catalog: {docs: 'javascript:x'}}]", "http(s) link"},
		{"services: [{name: a, build: {args: {bad-key: x}}}]", "build.args key"},
	} {
		if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.yaml, err, tc.want)
		}
	}
	if _, err := Parse([]byte("services: [{name: a, build: {builder: railpack, start: 'node server.js'}, gpu: 1, catalog: {env: A_URL, path: /x, docs: 'https://d'}}]")); err != nil {
		t.Error(err)
	}
}

func TestNeeds(t *testing.T) {
	s, err := Parse([]byte(`
postgres: {version: "16", size: 10Gi}
services:
  - name: api
    needs:
      - postgres
      - redis
      - {service: jobsentry/ollama-internal, env: OLLAMA_HOST}
      - {service: web}
  - name: web
    needs: [{postgres: {env: DB_URL}}]
`))
	if err != nil {
		t.Fatal(err)
	}
	api := s.Services[0].Needs
	if len(api) != 4 || api[0].Kind != NeedPostgres || api[1].EnvName() != "REDIS_URL" || api[2].EnvName() != "OLLAMA_HOST" || api[3].EnvName() != "WEB_URL" {
		t.Fatalf("needs = %+v", api)
	}
	if s.Services[1].Needs[0].EnvName() != "DB_URL" || !s.Needs(NeedPostgres) || s.Postgres.Size != "10Gi" {
		t.Error("postgres with a custom variable")
	}
	// Round trip: short forms stay short.
	out, _ := s.Marshal()
	if !strings.Contains(string(out), "- postgres\n") || !strings.Contains(string(out), "service: jobsentry/ollama-internal") {
		t.Errorf("marshalled:\n%s", out)
	}
	if _, err := Parse(out); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ yaml, want string }{
		{"services: [{name: a, needs: [mysql]}]", "not something rendimiento provides"},
		{"services: [{name: a, needs: [{service: Bad/Name}]}]", "must be namespace/name"},
		{"services: [{name: a, needs: [{service: nope}]}]", "not a service of this app"},
		{"services: [{name: a, needs: [postgres, {postgres: {env: DATABASE_URL}}]}]", "set by two needs"},
		{"services: [{name: postgres}, {name: b, needs: [postgres]}]", "rename the service"},
		{"postgres: {size: big}\nservices: [{name: a, needs: [postgres]}]", "must look like 5Gi"},
		{"services: [{name: a, needs: [{colour: blue}]}]", "a need is"},
	} {
		if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.yaml, err, tc.want)
		}
	}
}

func TestTasks(t *testing.T) {
	s, err := Parse([]byte(`services:
  - name: api
    path: api
  - name: db
    image: postgres:17-alpine
tasks:
  - name: mobile
    image: node:20-bookworm
    path: mobile
    command: npx eas-cli build --platform all --non-interactive --no-wait
    secretEnv: { EXPO_TOKEN: expo/token }
  - name: smoke
    image: curlimages/curl:8.10.1
    command: curl -fsS https://example.com/health
    after: [api, mobile]
    when: always
    optional: true
    secrets: [smoke-env]
`))
	if err != nil {
		t.Fatal(err)
	}
	m, smoke := s.Tasks[0], s.Tasks[1]
	if m.When != TaskOnDeploy || m.Size != SizeMedium || m.Path != "mobile" || smoke.Path != "." || !smoke.Optional {
		t.Fatalf("defaults: %+v %+v", m, smoke)
	}
	if got := strings.Join(s.SecretNames(), ","); got != "expo,smoke-env" {
		t.Fatalf("SecretNames = %s", got)
	}

	for _, tc := range []struct{ tasks, want string }{
		{"[{name: api, image: i, command: c}]", `"api" is already used by a service`},
		{"[{name: t, image: i}]", "needs both image and command"},
		{"[{name: t, image: i, command: c, when: nightly}]", "must be deploy or always"},
		{"[{name: t, image: i, command: c, after: [nope]}]", `"nope" is not a service, build or task`},
		{"[{name: t, image: i, command: c, after: [db]}]", "no build to wait for"},
		{"[{name: t, image: i, command: c, after: [t]}]", "cannot wait for itself"},
		{"[{name: a, image: i, command: c, after: [b]}, {name: b, image: i, command: c, after: [a]}]", "cycle"},
		{"[{name: t, image: i, command: c, path: ../x}]", "relative to the repo root"},
		{"[{name: t, image: i, command: c, secretEnv: {TOKEN: nokey}}]", "must be <secret>/<key>"},
	} {
		_, err := Parse([]byte("services: [{name: api}, {name: db, image: postgres}]\ntasks: " + tc.tasks))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.tasks, err, tc.want)
		}
	}
}

func TestPostDeployTasks(t *testing.T) {
	s, err := Parse([]byte(`services: [{name: api}, {name: db, image: postgres:17}]
tasks:
  - {name: migrate, stage: post-deploy, service: api, command: ./migrate up}
  - {name: smoke, stage: post-deploy, image: curl, command: curl -f http://api, after: [migrate]}
  - {name: lint, image: node, command: npm run lint}
`))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Tasks[0].PostDeploy() || !s.Tasks[1].PostDeploy() || s.Tasks[2].PostDeploy() || s.Tasks[2].Stage != StageBuild {
		t.Fatalf("stages: %+v", s.Tasks)
	}
	for _, tc := range []struct{ tasks, want string }{
		{"[{name: t, stage: post-deploy, command: c}]", "exactly one of image or service"},
		{"[{name: t, stage: post-deploy, image: i, service: api, command: c}]", "exactly one of image or service"},
		{"[{name: t, stage: post-deploy, service: nope, command: c}]", `service "nope" is not a service`},
		{"[{name: t, stage: post-deploy, image: i, command: c, path: web}]", "only apply to build tasks"},
		{"[{name: t, stage: later, image: i, command: c}]", "must be build, pre-deploy or post-deploy"},
		{"[{name: t, image: i, service: api, command: c}]", "only applies to pre- and post-deploy tasks"},
		{"[{name: a, image: i, command: c}, {name: b, stage: post-deploy, image: i, command: c, after: [a]}]", "is not a post-deploy task"},
		{"[{name: a, stage: post-deploy, image: i, command: c}, {name: b, image: i, command: c, after: [a]}]", "a build task cannot wait for it"},
		{"[{name: t, stage: post-deploy, image: i, command: c, after: [api]}]", "is not a post-deploy task"},
		{"[{name: a, stage: pre-deploy, image: i, command: c}, {name: b, stage: post-deploy, image: i, command: c, after: [a]}]", "is not a post-deploy task"},
		{"[{name: t, stage: pre-deploy, command: c}]", "exactly one of image or service"},
	} {
		_, err := Parse([]byte("services: [{name: api}]\ntasks: " + tc.tasks))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.tasks, err, tc.want)
		}
	}
}

func TestBuilds(t *testing.T) {
	s, err := Parse([]byte(`
services: [{name: docs}]
builds:
  - name: platform
    test:
      image: golang:1.27
      command: hack/ci-test.sh
      postgres: true
      cache: true
      env: { CGO_ENABLED: "0" }
      resources: { cpu: "1", memoryLimit: 5Gi }
tasks:
  - {name: smoke, image: curl, command: curl -f x, after: [platform]}
`))
	if err != nil {
		t.Fatal(err)
	}
	b := s.Builds[0]
	if b.Path != "." || b.Build.Dockerfile != "Dockerfile" || b.ImageKey() != "build:platform" || !b.Test.Postgres || !b.Test.Cache {
		t.Fatalf("build = %+v", b)
	}
	if r := b.Test.Requests(); r != (Resources{"1", "256Mi", "", "5Gi"}) {
		t.Fatalf("test resources = %+v", r)
	}
	if r := (Test{Size: SizeLarge}).Requests(); r != SizeLarge.Resources() {
		t.Fatalf("sized test = %+v", r)
	}
	for in, want := range map[string]string{
		"services: [{name: a}]\nbuilds: [{name: a}]":                                          "already used by a service",
		"services: [{name: a}]\nbuilds: [{name: B}]":                                          "lowercase DNS label",
		"services: [{name: a}]\nbuilds: [{name: b, path: ../x}]":                              "relative to the repo root",
		"services: [{name: a}]\nbuilds: [{name: b, test: {image: x}}]":                        "builds[0].test needs both image and command",
		"services: [{name: a}]\nbuilds: [{name: b, test: {image: x, command: y, size: xl}}]":  "builds[0].test.size",
		"services: [{name: a, test: {image: x, command: y, timeout: -1}}]":                    "services[0].test.timeout",
		"services: [{name: a}]\nbuilds: [{name: b}]\ntasks: [{name: b, image: x, command: y}]": "already used by a build",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", in, err, want)
		}
	}
}
