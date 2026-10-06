package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

var update = flag.Bool("update", false, "rewrite golden files")

// secplus/simplerfc-shaped app: two services, one with a volume, secrets and health checks.
const specYAML = `
services:
  - name: web
    port: 3000
    replicas: 2
    size: medium
    domain: secplus.joserod.space
    health: { path: /api/health }
    env: { NODE_ENV: production }
    secrets: [secplus-practice]
    streaming: true
    tlsSecret: secplus-tls
    secretEnv: { PRACTICE_TOKEN: secplus-practice/token }
    secretFiles: [{ secret: secplus-practice, mount: /private }]
  - name: worker
    path: worker
    volume: { size: 5Gi, mount: /data }
`

func TestRenderGolden(t *testing.T) {
	s, err := spec.Parse([]byte(specYAML))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Render(Input{App: "secplus", Spec: *s, Images: map[string]string{
		"web":    "registry.example.lan:5000/secplus-web@sha256:aaaa",
		"worker": "registry.example.lan:5000/secplus-worker@sha256:bbbb",
	}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	got, err := objs.YAML()
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "secplus.golden.yaml")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("render output differs from %s; rerun with -update and review the diff", golden)
	}
}

func TestRenderMissingImage(t *testing.T) {
	s, _ := spec.Parse([]byte("services:\n  - name: web\n"))
	if _, err := Render(Input{App: "x", Spec: *s}, DefaultOptions()); err == nil {
		t.Fatal("expected error for missing image")
	}
}

// An adopted Deployment keeps its immutable selector; pods carry both label
// sets and the Service selects the old one, so old and new pods overlap.
func TestRenderAdoptedSelectorAndExistingClaim(t *testing.T) {
	s, err := spec.Parse([]byte("services:\n  - name: simplerfc\n    port: 3000\n    volume: {existingClaim: simplerfc-data-pvc, mount: /data}\n"))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Render(Input{App: "simplerfc", Spec: *s, Images: map[string]string{"simplerfc": "img@sha256:1"},
		Selectors: map[string]map[string]string{"simplerfc": {"app": "simplerfc"}}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	d, svc := objs.Deployments[0], objs.Services[0]
	if d.Spec.Selector.MatchLabels["app"] != "simplerfc" || len(d.Spec.Selector.MatchLabels) != 1 {
		t.Fatalf("selector = %v", d.Spec.Selector.MatchLabels)
	}
	if l := d.Spec.Template.Labels; l["app"] != "simplerfc" || l[LabelApp] != "simplerfc" {
		t.Fatalf("pod labels = %v", l)
	}
	if svc.Spec.Selector["app"] != "simplerfc" || len(svc.Spec.Selector) != 1 {
		t.Fatalf("service selector = %v", svc.Spec.Selector)
	}
	if len(objs.Volumes) != 0 {
		t.Fatal("existing claim must not create a PVC")
	}
	if c := d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; c != "simplerfc-data-pvc" || d.Spec.Strategy.Type != "Recreate" {
		t.Fatalf("claim = %s strategy = %s", c, d.Spec.Strategy.Type)
	}
	if p := svc.Spec.Ports; len(p) != 2 || p[1].Port != 3000 || p[0].TargetPort.IntValue() != 3000 || p[1].TargetPort.IntValue() != 3000 {
		t.Fatalf("ports = %+v (target must be the number, so unnamed legacy pods keep traffic)", p)
	}
}

func TestRenderPodoi(t *testing.T) {
	s, err := spec.Parse([]byte(`
services:
  - name: podoi-website
    port: 3000
    replicas: 2
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
	objs, err := Render(Input{App: "podoi", Spec: *s, Images: map[string]string{"podoi-website": "img@sha256:1"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	ing := objs.Ingresses[0]
	if len(ing.Spec.Rules) != 2 || ing.Spec.Rules[1].Host != "www.joserod.space" || len(ing.Spec.TLS[0].Hosts) != 2 {
		t.Fatalf("ingress = %+v", ing.Spec)
	}
	db := objs.Deployments[1]
	c := db.Spec.Template.Spec.Containers[0]
	if c.Image != "postgres:15-alpine" || c.ReadinessProbe.TCPSocket == nil || c.ReadinessProbe.HTTPGet != nil {
		t.Fatalf("db container = %+v", c)
	}
	if db.Spec.Template.Spec.SecurityContext != nil {
		t.Fatal("postgres must not get an fsGroup")
	}
	if db.Spec.Strategy.Type != "Recreate" || len(db.Spec.Template.Spec.Volumes) != 2 {
		t.Fatalf("db strategy %s volumes %+v", db.Spec.Strategy.Type, db.Spec.Template.Spec.Volumes)
	}
	if len(objs.Ingresses) != 1 || len(objs.Volumes) != 0 {
		t.Fatal("db must have no ingress and no new volume")
	}
}

func TestRenderJobs(t *testing.T) {
	s, err := spec.Parse([]byte(`
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
    env: { PYTHONUNBUFFERED: "1" }
    secretEnv: { DATABASE_URL: wellness-secret/DATABASE_URL }
  - name: congress-scraper
    schedule: "0 6 * * *"
    path: congress
    resources: { memory: 256Mi, memoryLimit: 1Gi }
`))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Render(Input{App: "wellness", Spec: *s, Images: map[string]string{
		"wellness-api": "reg/wellness-wellness-api@sha256:1", "job:congress-scraper": "reg/wellness-congress-scraper@sha256:2"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	recap, scraper := objs.CronJobs[0], objs.CronJobs[1]
	c := recap.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
	if c.Image != "reg/wellness-wellness-api@sha256:1" || *recap.Spec.TimeZone != "America/New_York" || recap.Spec.ConcurrencyPolicy != "Forbid" ||
		*recap.Spec.JobTemplate.Spec.ActiveDeadlineSeconds != 300 || c.Command[2] != "import main; main.send_weekly_recap()" {
		t.Fatalf("recap = %+v %+v", recap.Spec, c)
	}
	names := []string{}
	for _, e := range c.Env {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "PYTHONUNBUFFERED,DATABASE_URL" {
		t.Fatalf("env = %v", names)
	}
	sc := scraper.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
	if sc.Image != "reg/wellness-congress-scraper@sha256:2" {
		t.Fatal("built job image")
	}
	if sc.Resources.Limits.Memory().String() != "1Gi" || sc.Resources.Requests.Memory().String() != "256Mi" {
		t.Fatalf("job resources = %+v", sc.Resources)
	}
	if _, err := Render(Input{App: "x", Spec: *s, Images: map[string]string{"wellness-api": "a"}}, DefaultOptions()); err == nil {
		t.Fatal("missing job image must error")
	}
}

func TestRenderStockpulse(t *testing.T) {
	s, err := spec.Parse([]byte(`
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
      annotations: { nginx.ingress.kubernetes.io/limit-rps: "5" }
  - name: stockpulse-api
    port: 8000
    health: { path: /api/health, timeout: 5 }
    resources: { cpu: 250m, memory: 1Gi, cpuLimit: none, memoryLimit: 5Gi }
`))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Render(Input{App: "stockpulse", Spec: *s, Images: map[string]string{"stockpulse-ui": "u", "stockpulse-api": "a"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	ing := objs.Ingresses[0]
	if ing.Name != "stockpulse-ingress" || ing.Annotations["nginx.ingress.kubernetes.io/limit-rps"] != "5" || ing.Annotations["nginx.ingress.kubernetes.io/proxy-buffering"] != "off" {
		t.Fatalf("ingress meta = %s %v", ing.Name, ing.Annotations)
	}
	if len(ing.Spec.TLS) != 2 || ing.Spec.TLS[0].SecretName != "stockpulse-tls" || ing.Spec.TLS[1].SecretName != "stockfinancia-tls" || len(ing.Spec.TLS[1].Hosts) != 2 || len(ing.Spec.Rules) != 3 {
		t.Fatalf("ingress tls = %+v", ing.Spec.TLS)
	}
	api := objs.Deployments[1].Spec.Template.Spec.Containers[0]
	if _, hasCPULimit := api.Resources.Limits["cpu"]; hasCPULimit || api.Resources.Limits.Memory().String() != "5Gi" || api.Resources.Requests.Cpu().String() != "250m" {
		t.Fatalf("api resources = %+v", api.Resources)
	}
	if api.ReadinessProbe.TimeoutSeconds != 5 {
		t.Fatal("probe timeout")
	}
	// No health declared: a TCP readiness gate only, never a liveness probe.
	if web := objs.Deployments[0].Spec.Template.Spec.Containers[0]; web.ReadinessProbe == nil || web.ReadinessProbe.TCPSocket == nil || web.LivenessProbe != nil {
		t.Fatalf("default probe = %+v / %+v", web.ReadinessProbe, web.LivenessProbe)
	}
}

func TestRenderRoutes(t *testing.T) {
	s, err := spec.Parse([]byte(`
services:
  - name: wellness-ui
    domain: wellness.jobsentry.net
    aliases: [wellbeingportal.app]
  - name: wellness-api
    domain: api.wellness.jobsentry.net
    routes: [wellness.jobsentry.net/api, wellbeingportal.app/api]
`))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Render(Input{App: "wellness", Spec: *s, Images: map[string]string{"wellness-ui": "u", "wellness-api": "a"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	api := objs.Ingresses[1]
	var got []string
	for _, r := range api.Spec.Rules {
		for _, p := range r.HTTP.Paths {
			got = append(got, r.Host+p.Path+"->"+p.Backend.Service.Name)
		}
	}
	if strings.Join(got, " ") != "api.wellness.jobsentry.net/->wellness-api wellness.jobsentry.net/api->wellness-api wellbeingportal.app/api->wellness-api" {
		t.Fatalf("api rules = %v", got)
	}
	if len(api.Spec.TLS) != 1 || api.Spec.TLS[0].Hosts[0] != "api.wellness.jobsentry.net" {
		t.Fatalf("route hosts must not get their own certificate: %+v", api.Spec.TLS)
	}
}

func TestRenderGPU(t *testing.T) {
	s, err := spec.Parse([]byte(`
services:
  - name: ollama
    image: dustynv/ollama:r36.4.0
    port: 11434
    gpu: 1
    command: [/bin/bash, -c]
    args: ["ollama serve"]
    env: { LD_LIBRARY_PATH: /mine }
    volume: { existingClaim: models, mount: /root/.ollama }
`))
	if err != nil {
		t.Fatal(err)
	}
	in := Input{App: "ai", Spec: *s}
	if _, err := Render(in, DefaultOptions()); err == nil || !strings.Contains(err.Error(), "no GPU profile") {
		t.Fatalf("a GPU service without a GPU profile must fail, got %v", err)
	}
	opt := DefaultOptions()
	opt.GPU = GPUProfile{Resource: "nvidia.com/gpu", RuntimeClass: "nvidia", HostPaths: []string{"/usr/lib/nvidia"}, SharedMemory: "1Gi",
		Env: map[string]string{"NVIDIA_VISIBLE_DEVICES": "all", "LD_LIBRARY_PATH": "/profile"}}
	objs, err := Render(in, opt)
	if err != nil {
		t.Fatal(err)
	}
	d := objs.Deployments[0]
	pod, c := d.Spec.Template.Spec, d.Spec.Template.Spec.Containers[0]
	if d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || pod.RuntimeClassName == nil || *pod.RuntimeClassName != "nvidia" {
		t.Errorf("strategy %s runtime class %v", d.Spec.Strategy.Type, pod.RuntimeClassName)
	}
	if q := c.Resources.Limits["nvidia.com/gpu"]; q.String() != "1" {
		t.Errorf("gpu limit = %s", q.String())
	}
	if strings.Join(c.Command, " ") != "/bin/bash -c" || c.Args[0] != "ollama serve" {
		t.Errorf("command %v args %v", c.Command, c.Args)
	}
	env := map[string]string{}
	for _, e := range c.Env {
		if _, dup := env[e.Name]; dup {
			t.Errorf("duplicate env %s", e.Name)
		}
		env[e.Name] = e.Value
	}
	if env["LD_LIBRARY_PATH"] != "/mine" || env["NVIDIA_VISIBLE_DEVICES"] != "all" {
		t.Errorf("env = %v (the app's value must win over the profile's)", env)
	}
	mounts := map[string]bool{}
	for _, m := range c.VolumeMounts {
		mounts[m.MountPath] = m.ReadOnly
	}
	if ro, ok := mounts["/usr/lib/nvidia"]; !ok || !ro {
		t.Error("driver libraries must be mounted read-only")
	}
	if _, ok := mounts["/dev/shm"]; !ok {
		t.Error("no /dev/shm")
	}
}

func TestRenderNeeds(t *testing.T) {
	s, err := spec.Parse([]byte(`
redis: {maxMemory: 32mb}
services:
  - name: api
    needs: [postgres, redis, {service: jobsentry/ollama-internal, env: OLLAMA_HOST}]
    env: {PGHOST: custom-host}
  - name: worker
    needs: [postgres]
`))
	if err != nil {
		t.Fatal(err)
	}
	in := Input{App: "shop", Spec: *s, Images: map[string]string{"api": "reg/api@sha256:1", "worker": "reg/w@sha256:2"},
		ServiceURLs: map[string]string{"jobsentry/ollama-internal": "http://ollama-internal.jobsentry.svc.cluster.local:11434"}}
	opt := DefaultOptions()
	opt.VolumeLabels = map[string]string{"recurring-job-group.longhorn.io/backup-nightly": "enabled", LabelApp: "not-this"}
	objs, err := Render(in, opt)
	if err != nil {
		t.Fatal(err)
	}
	if v := objs.Volumes[0].Labels; v["recurring-job-group.longhorn.io/backup-nightly"] != "enabled" || v[LabelApp] != "shop" {
		t.Errorf("volume labels = %v; want the configured backup label, and our own labels to win", v)
	}
	names := []string{}
	for _, d := range objs.Deployments {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "api,worker,postgres,redis" {
		t.Fatalf("deployments = %v", names)
	}
	env := map[string]string{}
	for _, e := range objs.Deployments[0].Spec.Template.Spec.Containers[0].Env {
		v := e.Value
		if e.ValueFrom != nil {
			v = e.ValueFrom.SecretKeyRef.Name + "/" + e.ValueFrom.SecretKeyRef.Key
		}
		env[e.Name] = v
	}
	for k, want := range map[string]string{
		"DATABASE_URL": "postgres-credentials/uri", "PGUSER": "postgres-credentials/username",
		"REDIS_URL":   "redis-credentials/uri",
		"OLLAMA_HOST": "http://ollama-internal.jobsentry.svc.cluster.local:11434",
		"PGHOST":      "custom-host", // the app's own value wins
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	pg := objs.Deployments[2]
	if pg.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || !strings.HasPrefix(pg.Spec.Template.Spec.Containers[0].Image, "postgres:17") || pg.Labels[LabelNeed] != "postgres" {
		t.Errorf("postgres deployment: %+v", pg.Spec.Strategy)
	}
	if len(objs.Volumes) != 1 || objs.Volumes[0].Name != "postgres-data" {
		t.Errorf("volumes = %v", objs.Volumes)
	}
	svc := objs.Services[2]
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 5432 || svc.Spec.Ports[0].Name != "postgres" {
		t.Errorf("postgres service ports = %+v", svc.Spec.Ports)
	}
	redis := objs.Deployments[3].Spec.Template.Spec.Containers[0]
	if !strings.Contains(strings.Join(redis.Args, " "), "--maxmemory 32mb") || !strings.Contains(strings.Join(redis.Args, " "), "$(REDIS_PASSWORD)") {
		t.Errorf("redis args = %v", redis.Args)
	}

	delete(in.ServiceURLs, "jobsentry/ollama-internal")
	if _, err := Render(in, DefaultOptions()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("an unresolved service need must fail: %v", err)
	}
}

func TestRenderLAN(t *testing.T) {
	s, err := spec.Parse([]byte("services: [{name: docs, port: 8080, lan: {ip: 192.168.1.81}}, {name: api, lan: {port: 8443}}]"))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Render(Input{App: "site", Spec: *s, Images: map[string]string{"docs": "d", "api": "a"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	var lan []*corev1.Service
	for _, svc := range objs.Services {
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			lan = append(lan, svc)
		}
	}
	if len(lan) != 2 || lan[0].Name != "docs-lan" || lan[0].Annotations["metallb.io/loadBalancerIPs"] != "192.168.1.81" ||
		lan[0].Spec.Ports[0].Port != 80 || lan[0].Spec.Ports[0].TargetPort.IntValue() != 8080 || lan[0].Labels[LabelLAN] != "true" {
		t.Fatalf("docs-lan = %+v", lan)
	}
	if lan[1].Annotations != nil || lan[1].Spec.Ports[0].Port != 8443 {
		t.Errorf("without an ip MetalLB chooses; the port is configurable: %+v", lan[1])
	}
	if _, err := spec.Parse([]byte("services: [{name: a, lan: {ip: 8.8.8.8}}]")); err == nil {
		t.Error("public addresses are refused")
	}
}
