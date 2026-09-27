// Package catalog lists the services apps can integrate with: what each one
// is, where it comes from (a rendimiento app, ArgoCD, Helm, kubectl), what it
// exposes (addresses, ports, public URLs, LAN IPs), how to call it from
// rendimiento.yaml, and which workloads already do.
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

type Group string

const (
	GroupApps  Group = "apps"           // deployed by rendimiento
	GroupOther Group = "other"          // deployed some other way
	GroupInfra Group = "infrastructure" // the cluster's own plumbing
)

// Origin says where a service comes from.
type Origin struct {
	// Kind is rendimiento, argocd, helm or manual.
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	App     string `json:"app,omitempty"`
	Repo    string `json:"repo,omitempty"`
	// RepoURL links to the code the image is built from.
	RepoURL string `json:"repoUrl,omitempty"`
	Release int64  `json:"release,omitempty"`
	// ArgoApp and HelmRelease name the ArgoCD Application / Helm release.
	ArgoApp     string  `json:"argoApp,omitempty"`
	HelmRelease string  `json:"helmRelease,omitempty"`
	Images      []Image `json:"images,omitempty"`
}

type Image struct {
	Ref string `json:"ref"`
	// Link is the image's page on its registry, when it has a public one.
	Link string `json:"link,omitempty"`
	// Built is true for images rendimiento built from the app's repo.
	Built bool `json:"built,omitempty"`
}

type Port struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Target   string `json:"target"`
	Protocol string `json:"protocol"`
}

// Consumer is a workload whose environment points at the service.
type Consumer struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Kind      string `json:"kind"`
	Env       string `json:"env"`
	// App is the rendimiento app the workload belongs to, if any.
	App string `json:"app,omitempty"`
}

type Entry struct {
	ID        string `json:"id"` // namespace/name
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Group     Group  `json:"group"`
	// Category is what the service does (database, ai, ...): guessed from
	// its protocol, name and images unless its rendimiento.yaml says.
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Origin      Origin `json:"origin"`
	// Protocol is a best guess from the port (http, postgres, redis...).
	Protocol string `json:"protocol"`
	// URL is the address to use from any namespace; ShortURL works from
	// services of the same app (same namespace).
	URL      string   `json:"url"`
	ShortURL string   `json:"shortUrl"`
	Ports    []Port   `json:"ports"`
	Public   []string `json:"public,omitempty"`
	// LAN is set when a LoadBalancer also exposes it on the local network.
	LAN       string   `json:"lan,omitempty"`
	Health    string   `json:"health,omitempty"`
	Docs      string   `json:"docs,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"`
	// Env is the variable name suggested to consumers; Snippet is ready to
	// paste into rendimiento.yaml.
	Env     string     `json:"env"`
	Snippet string     `json:"snippet"`
	UsedBy  []Consumer `json:"usedBy"`
	Pods    int        `json:"pods"`
	Ready   int        `json:"ready"`
	// NetworkPolicies in the namespace; zero means any pod can connect.
	NetworkPolicies int `json:"networkPolicies"`
}

type Catalog struct {
	Entries   []Entry   `json:"entries"`
	Generated time.Time `json:"generated"`
}

// DefaultInfra are namespaces holding cluster plumbing rather than services
// apps would call.
var DefaultInfra = []string{"kube-system", "kube-public", "kube-node-lease", "default", "cert-manager", "ingress-nginx",
	"longhorn-system", "metallb-system", "argocd", "monitoring", "devops-tools", "rendimiento-system", "rendimiento-builds"}

type Builder struct {
	Kube  client.Client
	Infra []string
	TTL   time.Duration

	mu     sync.Mutex
	cached *Catalog
}

// Get returns a cached catalog unless it is older than TTL or refresh is set.
func (b *Builder) Get(ctx context.Context, refresh bool) (*Catalog, error) {
	ttl := b.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !refresh && b.cached != nil && time.Since(b.cached.Generated) < ttl {
		return b.cached, nil
	}
	c, err := b.build(ctx)
	if err != nil {
		return nil, err
	}
	b.cached = c
	return c, nil
}

type snapshot struct {
	apps        []v1alpha1.App
	services    []corev1.Service
	ingresses   []networkingv1.Ingress
	pods        []corev1.Pod
	deployments []appsv1.Deployment
	sets        []appsv1.StatefulSet
	cronjobs    []batchv1.CronJob
	policies    map[string]int
}

func (b *Builder) build(ctx context.Context) (*Catalog, error) {
	var s snapshot
	var apps v1alpha1.AppList
	var svcs corev1.ServiceList
	var ings networkingv1.IngressList
	var pods corev1.PodList
	var deps appsv1.DeploymentList
	var sets appsv1.StatefulSetList
	var cjs batchv1.CronJobList
	var pols networkingv1.NetworkPolicyList
	for _, l := range []client.ObjectList{&apps, &svcs, &ings, &pods, &deps, &sets, &cjs, &pols} {
		if err := b.Kube.List(ctx, l); err != nil {
			return nil, fmt.Errorf("list %T: %w", l, err)
		}
	}
	s.apps, s.services, s.ingresses, s.pods = apps.Items, svcs.Items, ings.Items, pods.Items
	s.deployments, s.sets, s.cronjobs = deps.Items, sets.Items, cjs.Items
	s.policies = map[string]int{}
	for _, p := range pols.Items {
		s.policies[p.Namespace]++
	}
	infra := b.Infra
	if infra == nil {
		infra = DefaultInfra
	}
	return assemble(s, infra), nil
}

// assemble builds the catalog from a snapshot of the cluster.
func assemble(s snapshot, infra []string) *Catalog {
	isInfra := map[string]bool{}
	for _, ns := range infra {
		isInfra[ns] = true
	}
	appSvc := map[string]appService{} // namespace/name → rendimiento service
	appOfNS := map[string]string{}
	for _, a := range s.apps {
		appOfNS[a.Name] = a.Name
		for _, svc := range a.Spec.Services {
			appSvc[a.Name+"/"+svc.Name] = appService{app: a, svc: svc}
		}
	}
	workloads := collectWorkloads(s, appOfNS)

	out := &Catalog{Generated: time.Now(), Entries: []Entry{}}
	for _, svc := range s.services {
		if svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Ports) == 0 {
			continue
		}
		e := Entry{
			ID: svc.Namespace + "/" + svc.Name, Name: svc.Name, Namespace: svc.Namespace, Title: svc.Name,
			NetworkPolicies: s.policies[svc.Namespace], UsedBy: []Consumer{},
		}
		for _, p := range svc.Spec.Ports {
			e.Ports = append(e.Ports, Port{Name: p.Name, Port: p.Port, Target: p.TargetPort.String(), Protocol: guessProtocol(p)})
		}
		primary := svc.Spec.Ports[0]
		as, managed := appSvc[e.ID]
		switch {
		case managed && svc.Labels[render.LabelManagedBy] == render.ManagedBy:
			e.Group = GroupApps
			e.Origin = appOrigin(as)
			// The app's own port, not the port-80 alias, is what callers used before.
			for _, p := range svc.Spec.Ports {
				if int(p.Port) == as.svc.Port {
					primary = p
				}
			}
			if as.svc.Health != nil && as.svc.Health.Path != "" {
				e.Health = as.svc.Health.Path
			}
			if c := as.svc.Catalog; c != nil {
				e.Title = firstNonEmpty(c.Title, e.Title)
				e.Description, e.Docs, e.Endpoints, e.Env = c.Description, c.Docs, c.Endpoints, c.Env
			}
		case isInfra[svc.Namespace]:
			e.Group = GroupInfra
			e.Origin = foreignOrigin(&svc.ObjectMeta)
		default:
			e.Group = GroupOther
			e.Origin = foreignOrigin(&svc.ObjectMeta)
		}
		e.Protocol = guessProtocol(primary)
		host := svc.Name + "." + svc.Namespace + ".svc.cluster.local"
		e.URL = address(e.Protocol, host, primary.Port)
		e.ShortURL = address(e.Protocol, svc.Name, primary.Port)
		if e.Group == GroupApps {
			if c := as.svc.Catalog; c != nil && c.Path != "" {
				e.URL += c.Path
				e.ShortURL += c.Path
			}
		}
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			for _, ing := range svc.Status.LoadBalancer.Ingress {
				if ing.IP != "" {
					e.LAN = fmt.Sprintf("%s:%d", ing.IP, primary.Port)
				}
			}
		}
		e.Public = publicURLs(s.ingresses, svc)
		pods := selected(s.pods, svc)
		e.Pods = len(pods)
		for _, p := range pods {
			if podReady(p) {
				e.Ready++
			}
		}
		if e.Group != GroupApps {
			e.Origin.Images = podImages(pods)
		}
		if e.Env == "" {
			e.Env = suggestEnv(svc.Name, e.Protocol)
		}
		e.Category = categorize(e)
		if e.Group == GroupApps && as.svc.Catalog != nil && as.svc.Catalog.Category != "" {
			e.Category = as.svc.Catalog.Category
		}
		e.Snippet = snippet(e)
		e.UsedBy = consumers(workloads, svc, e.LAN)
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Group != b.Group {
			return groupRank(a.Group) < groupRank(b.Group)
		}
		if len(a.UsedBy) != len(b.UsedBy) {
			return len(a.UsedBy) > len(b.UsedBy)
		}
		return a.ID < b.ID
	})
	return out
}

type appService struct {
	app v1alpha1.App
	svc spec.Service
}

func groupRank(g Group) int {
	switch g {
	case GroupApps:
		return 0
	case GroupOther:
		return 1
	}
	return 2
}

func appOrigin(as appService) Origin {
	o := Origin{Kind: "rendimiento", App: as.app.Name, Repo: as.app.Spec.Repo, Release: as.app.Spec.Release}
	ref := as.app.Spec.Images[as.svc.Name]
	if as.svc.Image != "" {
		if ref == "" {
			ref = as.svc.Image
		}
		o.Images = []Image{{Ref: ref, Link: imageLink(as.svc.Image)}}
		o.Summary = fmt.Sprintf("Ready-made image %s, run by the %s app", as.svc.Image, as.app.Name)
		return o
	}
	if as.app.Spec.Repo != "" {
		o.RepoURL = "https://github.com/" + as.app.Spec.Repo
		if p := strings.Trim(as.svc.Path, "./"); p != "" {
			o.RepoURL += "/tree/HEAD/" + p
		}
	}
	if ref != "" {
		o.Images = []Image{{Ref: ref, Built: true}}
	}
	where := as.app.Spec.Repo
	if p := strings.Trim(as.svc.Path, "./"); p != "" {
		where += " (" + p + ")"
	}
	o.Summary = fmt.Sprintf("Built by rendimiento from %s, part of the %s app", where, as.app.Name)
	return o
}

// foreignOrigin works out who deployed something rendimiento does not manage.
func foreignOrigin(m metav1Object) Origin {
	o := Origin{Kind: "manual", Summary: "Deployed by hand (kubectl or a script); nothing reconciles it"}
	helm := m.GetAnnotations()["meta.helm.sh/release-name"]
	if helm == "" && m.GetLabels()["app.kubernetes.io/managed-by"] == "Helm" {
		helm = m.GetLabels()["app.kubernetes.io/instance"]
	}
	if helm != "" {
		o.Kind, o.HelmRelease = "helm", helm
		o.Summary = fmt.Sprintf("Helm release %s", helm)
	}
	if id := m.GetAnnotations()["argocd.argoproj.io/tracking-id"]; id != "" {
		app, _, _ := strings.Cut(id, ":")
		o.Kind, o.ArgoApp = "argocd", app
		o.Summary = fmt.Sprintf("ArgoCD application %s", app)
		if helm != "" {
			o.Summary += " (Helm chart)"
		}
	}
	return o
}

type metav1Object interface {
	GetLabels() map[string]string
	GetAnnotations() map[string]string
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// guessProtocol names the protocol from the port name or well-known number.
func guessProtocol(p corev1.ServicePort) string {
	name := strings.ToLower(p.Name)
	switch {
	case strings.Contains(name, "grpc"):
		return "grpc"
	case strings.Contains(name, "postgres"), p.Port == 5432:
		return "postgres"
	case strings.Contains(name, "redis"), p.Port == 6379:
		return "redis"
	case strings.Contains(name, "mysql"), p.Port == 3306:
		return "mysql"
	case strings.Contains(name, "mongo"), p.Port == 27017:
		return "mongodb"
	case strings.Contains(name, "https"), p.Port == 443, p.Port == 8443:
		return "https"
	case p.Protocol == corev1.ProtocolUDP:
		return "udp"
	}
	return "http"
}

func address(protocol, host string, port int32) string {
	switch protocol {
	case "http", "https", "postgres", "redis", "mysql", "mongodb", "grpc":
		if (protocol == "http" && port == 80) || (protocol == "https" && port == 443) {
			return protocol + "://" + host
		}
		return fmt.Sprintf("%s://%s:%d", protocol, host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

var nonEnv = regexp.MustCompile(`[^A-Z0-9]+`)

func suggestEnv(name, protocol string) string {
	switch protocol {
	case "postgres", "mysql":
		return "DATABASE_URL"
	case "redis":
		return "REDIS_URL"
	case "mongodb":
		return "MONGODB_URI"
	}
	return strings.Trim(nonEnv.ReplaceAllString(strings.ToUpper(name), "_"), "_") + "_URL"
}

// snippet is the rendimiento.yaml fragment for a service that calls this one.
func snippet(e Entry) string {
	switch e.Protocol {
	case "postgres", "mysql", "mongodb", "redis":
		return fmt.Sprintf(`# Credentials belong in a secret, not in git. Create one whose value is
#   %s
# with the user and password filled in, then:
secretEnv:
  %s: <secret-name>/<key>
`, withCredentials(e.URL), e.Env)
	}
	return fmt.Sprintf("env:\n  %s: %s\n", e.Env, e.URL)
}

func withCredentials(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return u
	}
	db := ""
	if scheme != "redis" {
		db = "/<database>"
	}
	return scheme + "://<user>:<password>@" + rest + db
}

func publicURLs(ings []networkingv1.Ingress, svc corev1.Service) []string {
	var out []string
	seen := map[string]bool{}
	for _, ing := range ings {
		if ing.Namespace != svc.Namespace {
			continue
		}
		for _, r := range ing.Spec.Rules {
			if r.HTTP == nil || r.Host == "" {
				continue
			}
			for _, p := range r.HTTP.Paths {
				if p.Backend.Service == nil || p.Backend.Service.Name != svc.Name {
					continue
				}
				u := "https://" + r.Host
				if p.Path != "" && p.Path != "/" {
					u += p.Path
				}
				if !seen[u] {
					seen[u] = true
					out = append(out, u)
				}
			}
		}
	}
	return out
}

func selected(pods []corev1.Pod, svc corev1.Service) []corev1.Pod {
	if len(svc.Spec.Selector) == 0 {
		return nil
	}
	var out []corev1.Pod
	for _, p := range pods {
		if p.Namespace != svc.Namespace || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		match := true
		for k, v := range svc.Spec.Selector {
			if p.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, p)
		}
	}
	return out
}

func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podImages(pods []corev1.Pod) []Image {
	var out []Image
	seen := map[string]bool{}
	for _, p := range pods {
		for _, c := range p.Spec.Containers {
			if !seen[c.Image] {
				seen[c.Image] = true
				out = append(out, Image{Ref: c.Image, Link: imageLink(c.Image)})
			}
		}
	}
	return out
}

// imageLink points at an image's public page (Docker Hub, Quay, GitHub).
func imageLink(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	first, rest, hasSlash := strings.Cut(ref, "/")
	if !hasSlash {
		return "https://hub.docker.com/_/" + ref
	}
	switch {
	case first == "docker.io":
		return imageLink(rest)
	case first == "quay.io":
		return "https://quay.io/repository/" + rest
	case first == "ghcr.io":
		owner, pkg, _ := strings.Cut(rest, "/")
		return "https://github.com/" + owner + "/" + pkg
	case strings.ContainsAny(first, ".:") || first == "localhost":
		return "" // private registry
	}
	return "https://hub.docker.com/r/" + ref
}

// ---- categories ----

// categoryRules are checked in order against the protocol, the service's
// name and namespace, and its images; the first match wins.
var categoryRules = []struct {
	category string
	match    *regexp.Regexp
}{
	{"database", regexp.MustCompile(`postgres|mysql|mariadb|mongo|clickhouse|cockroach|couchdb|cassandra|sqlite|(^|[^a-z])db($|[^a-z])`)},
	{"messaging", regexp.MustCompile(`redis|valkey|memcache|nats|rabbitmq|kafka|mosquitto|mqtt`)},
	{"ai", regexp.MustCompile(`ollama|litellm|llm|vllm|linguistic|openai|whisper|comfyui|stable-diffusion`)},
	{"storage", regexp.MustCompile(`minio|(^|[^a-z])s3($|[^a-z])|registry|longhorn|nfs|seaweed`)},
	{"monitoring", regexp.MustCompile(`grafana|prometheus|victoria|vmagent|vmalert|vmsingle|alertmanager|exporter|kube-state-metrics|loki|telegraf|umami|uptime|metrics`)},
	{"devtools", regexp.MustCompile(`jenkins|argocd|buildkit|renovate|gitea|sonarqube|hajimari`)},
	{"platform", regexp.MustCompile(`ingress|cert-manager|metallb|coredns|kube-dns|traefik|webhook|^kubernetes$|metrics-server|dex`)},
}

func categorize(e Entry) string {
	hay := []string{e.Protocol, e.Name, e.Namespace + "/" + e.Name}
	for _, i := range e.Origin.Images {
		hay = append(hay, imageRepo(i.Ref))
	}
	text := strings.ToLower(strings.Join(hay, " "))
	for _, r := range categoryRules {
		if r.match.MatchString(text) {
			return r.category
		}
	}
	return "web"
}

// imageRepo drops the registry host, tag and digest: what the image is
// ("dustynv/ollama"), not where it is stored ("registry.example.lan:5000").
func imageRepo(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	if first, rest, ok := strings.Cut(ref, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return rest
	}
	return ref
}

// ---- consumers ----

type workload struct {
	namespace, name, kind, app string
	env                        []corev1.EnvVar
}

func collectWorkloads(s snapshot, appOfNS map[string]string) []workload {
	var out []workload
	add := func(ns, name, kind string, spec corev1.PodSpec) {
		w := workload{namespace: ns, name: name, kind: kind, app: appOfNS[ns]}
		for _, c := range append(spec.InitContainers, spec.Containers...) {
			w.env = append(w.env, c.Env...)
		}
		out = append(out, w)
	}
	for _, d := range s.deployments {
		add(d.Namespace, d.Name, "Deployment", d.Spec.Template.Spec)
	}
	for _, st := range s.sets {
		add(st.Namespace, st.Name, "StatefulSet", st.Spec.Template.Spec)
	}
	for _, cj := range s.cronjobs {
		add(cj.Namespace, cj.Name, "CronJob", cj.Spec.JobTemplate.Spec.Template.Spec)
	}
	return out
}

var addressKey = regexp.MustCompile(`(?i)(HOST|URL|URI|ADDR|SERVER|ENDPOINT|SERVICE|DSN)`)

// consumers finds workloads whose plain environment values point at svc:
// by its cluster DNS name from anywhere, by its short name from the same
// namespace, or by its LAN address. Values read from secrets are not seen.
func consumers(ws []workload, svc corev1.Service, lan string) []Consumer {
	name, ns := regexp.QuoteMeta(svc.Name), regexp.QuoteMeta(svc.Namespace)
	full := regexp.MustCompile(`(^|[^a-z0-9.-])` + name + `\.` + ns + `(\.svc(\.cluster\.local)?)?\.?([:/]|$)`)
	shortURL := regexp.MustCompile(`(//|@)` + name + `([:/]|$)`)
	bare := regexp.MustCompile(`^` + name + `(:[0-9]+)?$`)
	lanIP := ""
	if lan != "" {
		lanIP, _, _ = strings.Cut(lan, ":")
	}
	out := []Consumer{}
	for _, w := range ws {
		same := w.namespace == svc.Namespace
		if same && w.name == svc.Name {
			continue // the service's own workload
		}
		for _, e := range w.env {
			v := e.Value
			hit := full.MatchString(v) ||
				(same && (shortURL.MatchString(v) || (bare.MatchString(v) && addressKey.MatchString(e.Name)))) ||
				(lanIP != "" && strings.Contains(v, lanIP))
			if hit {
				out = append(out, Consumer{Namespace: w.namespace, Workload: w.name, Kind: w.kind, Env: e.Name, App: w.app})
			}
		}
	}
	return out
}
