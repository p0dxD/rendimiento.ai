// Package spec defines rendimiento.yaml, the only file an app repo needs.
//
// +kubebuilder:object:generate=true
package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

const FileName = "rendimiento.yaml"

// Spec is a parsed rendimiento.yaml.
type Spec struct {
	Services []Service `json:"services"`
	// Jobs run on a schedule (Kubernetes CronJobs).
	Jobs []Job `json:"jobs,omitempty"`
	// SharedNamespace means the app's namespace also holds things rendimiento
	// does not manage (e.g. a service still deployed by ArgoCD, which owns the
	// namespace). rendimiento then never creates, labels, owns or deletes the
	// namespace: it must exist, and deleting the app removes only its own objects.
	SharedNamespace bool `json:"sharedNamespace,omitempty"`
	// Postgres and Redis tune what services get from `needs: [postgres]`
	// and `needs: [redis]`; both are optional.
	Postgres *PostgresOptions `json:"postgres,omitempty"`
	Redis    *RedisOptions    `json:"redis,omitempty"`
	// Tasks are commands run as CI steps next to the tests and builds
	// (a mobile build, a smoke test, a release script).
	Tasks []Task `json:"tasks,omitempty"`
}

// Task is a command run as a step of every CI run: the commit is checked
// out, and the command runs in Image from Path. Unlike a test, a task can
// read the app's secrets, so by default it only runs for pushes to the
// default branch. A failed task fails the run (no release) unless Optional.
type Task struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Command string `json:"command"`
	// Path is the working directory, relative to the repo root (default
	// "."). With Watch, it is also what counts as a change: on a push that
	// touches neither, the task is skipped.
	Path  string   `json:"path,omitempty"`
	Watch []string `json:"watch,omitempty"`
	// After lists services (their build) and tasks this task waits for.
	After []string `json:"after,omitempty"`
	// When the task runs: "deploy" (default: pushes to the default branch)
	// or "always" (every run, including branches and pull requests). Not
	// named "on": YAML 1.1 reads that key as the boolean true.
	When string `json:"when,omitempty"`
	// Optional tasks may fail without failing the run.
	Optional bool `json:"optional,omitempty"`
	Size     Size `json:"size,omitempty"`
	// Resources overrides the size preset's requests and limits.
	Resources *ResourceOverride `json:"resources,omitempty"`
	// Timeout stops the task after this many seconds (default and maximum:
	// the platform's step timeout).
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Secrets are loaded whole as environment variables.
	Secrets []string `json:"secrets,omitempty"`
	// SecretEnv sets single variables from secret keys, as NAME: secret/key.
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
}

const (
	TaskOnDeploy = "deploy"
	TaskOnAlways = "always"
)

// SecretNames lists every secret the task reads.
func (t Task) SecretNames() []string {
	return secretNames(t.Secrets, t.SecretEnv, nil)
}

// SecretNames lists every secret the app's services, jobs and tasks read:
// the ones that can be set from the app's settings.
func (s Spec) SecretNames() []string {
	var names []string
	for _, svc := range s.Services {
		names = append(names, svc.SecretNames()...)
	}
	for _, j := range s.Jobs {
		names = append(names, secretNames(j.Secrets, j.SecretEnv, nil)...)
	}
	for _, t := range s.Tasks {
		names = append(names, t.SecretNames()...)
	}
	return secretNames(names, nil, nil)
}

// PostgresOptions configure the app's database (one per app, shared by
// every service that needs it).
type PostgresOptions struct {
	// Version is the postgres image's major version (default 17).
	Version string `json:"version,omitempty"`
	// Size of its volume (default 5Gi).
	Size      string            `json:"size,omitempty"`
	Resources *ResourceOverride `json:"resources,omitempty"`
}

// RedisOptions configure the app's cache (one per app, shared by every service that needs it).
type RedisOptions struct {
	Version string `json:"version,omitempty"` // default 7
	// MaxMemory caps the cache; older keys are evicted (default 64mb).
	MaxMemory string            `json:"maxMemory,omitempty"`
	Resources *ResourceOverride `json:"resources,omitempty"`
}

const (
	NeedPostgres = "postgres"
	NeedRedis    = "redis"
	NeedService  = "service"
)

// Need is something a service depends on: rendimiento provides it and
// injects how to reach it. In YAML it is either a word ("postgres",
// "redis") or an object: {service: namespace/name, env: VAR}.
type Need struct {
	Kind string `json:"-"`
	// Service is another service to call: "namespace/name", or "name" for
	// a service of the same app.
	Service string `json:"service,omitempty"`
	// Env overrides the variable the address goes in (DATABASE_URL,
	// REDIS_URL, or <NAME>_URL for services).
	Env string `json:"env,omitempty"`
}

// UnmarshalJSON accepts a word ("postgres") or an object ({service: ns/name, env: VAR}).
func (n *Need) UnmarshalJSON(b []byte) error {
	var word string
	if err := json.Unmarshal(b, &word); err == nil {
		n.Kind = word
		return nil
	}
	var obj struct {
		Postgres *struct {
			Env string `json:"env"`
		} `json:"postgres"`
		Redis *struct {
			Env string `json:"env"`
		} `json:"redis"`
		Service string `json:"service"`
		Env     string `json:"env"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return fmt.Errorf("a need is postgres, redis, {service: namespace/name} or {postgres: {env: VAR}}: %w", err)
	}
	switch {
	case obj.Service != "":
		n.Kind, n.Service, n.Env = NeedService, obj.Service, obj.Env
	case obj.Postgres != nil:
		n.Kind, n.Env = NeedPostgres, obj.Postgres.Env
	case obj.Redis != nil:
		n.Kind, n.Env = NeedRedis, obj.Redis.Env
	default:
		return errors.New("a need is postgres, redis, {service: namespace/name} or {postgres: {env: VAR}}")
	}
	return nil
}

// MarshalJSON writes the short form when it can, so generated files stay readable.
func (n Need) MarshalJSON() ([]byte, error) {
	switch {
	case n.Kind == NeedService:
		return json.Marshal(struct {
			Service string `json:"service"`
			Env     string `json:"env,omitempty"`
		}{n.Service, n.Env})
	case n.Env != "":
		return json.Marshal(map[string]map[string]string{n.Kind: {"env": n.Env}})
	}
	return json.Marshal(n.Kind)
}

// EnvName is the variable a need's address is injected into.
func (n Need) EnvName() string {
	if n.Env != "" {
		return n.Env
	}
	switch n.Kind {
	case NeedPostgres:
		return "DATABASE_URL"
	case NeedRedis:
		return "REDIS_URL"
	}
	_, name, found := strings.Cut(n.Service, "/")
	if !found {
		name = n.Service
	}
	return strings.Trim(nonEnvChars.ReplaceAllString(strings.ToUpper(name), "_"), "_") + "_URL"
}

var nonEnvChars = regexp.MustCompile(`[^A-Z0-9]+`)

// Needs reports whether any service of the app needs kind.
func (s *Spec) Needs(kind string) bool {
	for _, svc := range s.Services {
		for _, n := range svc.Needs {
			if n.Kind == kind {
				return true
			}
		}
	}
	return false
}

// Job is a scheduled task. Its image comes from exactly one of: Service (use
// that service's released image), Path (build this directory), or Image.
type Job struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	// TimeZone for the schedule, e.g. America/New_York (default: cluster time, UTC).
	TimeZone string   `json:"timeZone,omitempty"`
	Service  string   `json:"service,omitempty"`
	Path     string   `json:"path,omitempty"`
	Watch    []string `json:"watch,omitempty"`
	Build    Build    `json:"build,omitempty"`
	Image    string   `json:"image,omitempty"`
	Command  []string `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Size     Size     `json:"size,omitempty"`
	// Resources overrides the size preset's requests and limits.
	Resources *ResourceOverride `json:"resources,omitempty"`
	// Timeout stops a run after this many seconds (0 = no limit).
	Timeout   int               `json:"timeout,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
	Secrets   []string          `json:"secrets,omitempty"`
}

// Touches reports whether a change to repo file f affects something built
// from dir or any of the watch paths ("." means the whole repo).
func Touches(f, dir string, watch []string) bool {
	for _, p := range append([]string{dir}, watch...) {
		p = strings.Trim(p, "/")
		if p == "." || p == "" || f == p || strings.HasPrefix(f, p+"/") {
			return true
		}
	}
	return false
}

// ImageKey is the key of a built job's image in a release's image map.
func (j Job) ImageKey() string { return "job:" + j.Name }

// Service is one deployable part of an app: built from a folder of the repo (or a ready-made image)
// and run as a Deployment with a Service.
type Service struct {
	Name string `json:"name"`
	// Image runs a ready-made image (e.g. postgres:15-alpine) instead of
	// building the repo: no build or test step, released as given.
	Image string `json:"image,omitempty"`
	Path  string `json:"path,omitempty"`
	// Watch lists extra repo paths whose changes also rebuild this service
	// (code shared with other folders). Its own path always counts.
	Watch    []string          `json:"watch,omitempty"`
	Language string            `json:"language,omitempty"`
	Port     int               `json:"port,omitempty"`
	Build    Build             `json:"build,omitempty"`
	Test     *Test             `json:"test,omitempty"`
	Size     Size              `json:"size,omitempty"`
	Replicas int               `json:"replicas,omitempty"`
	Domain   string            `json:"domain,omitempty"`
	Health   *Health           `json:"health,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	// Secrets are loaded whole as environment variables (envFrom).
	Secrets []string `json:"secrets,omitempty"`
	// SecretEnv sets single variables from secret keys, as NAME: secret/key.
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
	// SecretFiles mounts secrets as read-only files.
	SecretFiles []SecretFile `json:"secretFiles,omitempty"`
	// ConfigFiles mounts existing ConfigMaps as read-only files.
	ConfigFiles []ConfigFile `json:"configFiles,omitempty"`
	// Aliases are extra hostnames served like Domain, on the same certificate.
	Aliases []string `json:"aliases,omitempty"`
	// Routes send a path on a host owned by another service of this app to
	// this service, as "host/path" (e.g. "wellness.jobsentry.net/api").
	// They use the certificate of the service that owns the host.
	Routes []string `json:"routes,omitempty"`
	Volume *Volume  `json:"volume,omitempty"`
	// Resources overrides the size preset's requests and limits.
	Resources *ResourceOverride `json:"resources,omitempty"`
	// Ingress fine-tunes how the service is exposed on its domain.
	Ingress *IngressOptions `json:"ingress,omitempty"`
	// Streaming keeps responses unbuffered and long-lived at the ingress
	// (server-sent events, streamed AI answers, websockets).
	Streaming bool `json:"streaming,omitempty"`
	// TLSSecret names the certificate secret (default <name>-tls); set it to
	// keep an existing certificate when migrating an app.
	TLSSecret string `json:"tlsSecret,omitempty"`
	// Command and Args override the image's entrypoint and arguments
	// (mostly for ready-made images).
	Command []string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// GPU requests this many GPUs. How a GPU is attached (runtime class,
	// driver libraries) is cluster configuration, not part of the app.
	GPU int `json:"gpu,omitempty"`
	// Needs are what the service depends on (postgres, redis, other
	// services); rendimiento provides them and injects their addresses.
	// Items are words or objects, so the CRD leaves them unstructured.
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Needs []Need `json:"needs,omitempty"`
	// LAN also exposes the service on the local network through the
	// cluster's load balancer (MetalLB): no domain or certificate needed.
	LAN *LANOptions `json:"lan,omitempty"`
	// Catalog documents the service for other apps on the Services page:
	// what it is, which variable consumers usually set and what it offers.
	Catalog *Catalog `json:"catalog,omitempty"`
}

// Categories group services on the Services page by what they do.
var Categories = map[string]bool{"database": true, "messaging": true, "ai": true, "storage": true,
	"monitoring": true, "web": true, "devtools": true, "platform": true}

// LANOptions expose a service on the local network.
type LANOptions struct {
	// IP to request from the load balancer's pool; empty lets it choose.
	IP string `json:"ip,omitempty"`
	// Port on that IP (default 80), forwarded to the service's port.
	Port int `json:"port,omitempty"`
}

// Catalog is what the Services page shows about a service besides what it
// can work out itself (addresses, ports, origin, who uses it).
type Catalog struct {
	// Category overrides the guessed one: database, messaging, ai, storage,
	// monitoring, web, devtools or platform.
	Category    string `json:"category,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Env is the variable consumers usually put the address in (OLLAMA_HOST).
	Env string `json:"env,omitempty"`
	// Path is appended to the address in the suggested value (/analyze).
	Path string `json:"path,omitempty"`
	// Docs links to the API documentation.
	Docs string `json:"docs,omitempty"`
	// Endpoints are short descriptions such as "POST /analyze: classify a message".
	Endpoints []string `json:"endpoints,omitempty"`
}

// ResourceOverride replaces individual values of the size preset. Limits
// may be "none" to leave them unset (e.g. no CPU limit).
type ResourceOverride struct {
	CPU         string `json:"cpu,omitempty"`
	Memory      string `json:"memory,omitempty"`
	CPULimit    string `json:"cpuLimit,omitempty"`
	MemoryLimit string `json:"memoryLimit,omitempty"`
}

// IngressOptions fine-tune the ingress of a service with a domain.
type IngressOptions struct {
	// Name keeps an existing ingress's name so a migration updates it in place.
	Name string `json:"name,omitempty"`
	// Annotations are extra ingress-nginx settings (rate limits, body size...).
	// Only nginx.ingress.kubernetes.io/* keys; snippets are not allowed.
	Annotations map[string]string `json:"annotations,omitempty"`
	// TLSSecrets puts some hosts on their own certificate: host → secret.
	TLSSecrets map[string]string `json:"tlsSecrets,omitempty"`
}

// ResourcesFor resolves the service's requests and limits ("" = unset).
func ResourcesFor(size Size, o *ResourceOverride) Resources {
	r := size.Resources()
	if o == nil {
		return r
	}
	pick := func(cur, override string) string {
		switch override {
		case "":
			return cur
		case "none":
			return ""
		}
		return override
	}
	r.CPURequest, r.MemRequest = pick(r.CPURequest, o.CPU), pick(r.MemRequest, o.Memory)
	r.CPULimit, r.MemLimit = pick(r.CPULimit, o.CPULimit), pick(r.MemLimit, o.MemoryLimit)
	return r
}

// Route is one host + path prefix a service answers on.
type Route struct {
	Host, Path string
}

// ParseRoute splits "host/path" (path defaults to "/").
func ParseRoute(r string) Route {
	host, path, found := strings.Cut(r, "/")
	if !found {
		return Route{Host: host, Path: "/"}
	}
	return Route{Host: host, Path: "/" + strings.TrimSuffix(path, "/")}
}

// AllRoutes is everything the service's ingress routes to it: "/" on its
// domain and aliases, then its extra routes.
func (s Service) AllRoutes() []Route {
	var out []Route
	for _, h := range s.Hosts() {
		out = append(out, Route{Host: h, Path: "/"})
	}
	for _, r := range s.Routes {
		out = append(out, ParseRoute(r))
	}
	return out
}

// HasIngress reports whether the service is reachable from outside.
func (s Service) HasIngress() bool { return s.Domain != "" || len(s.Routes) > 0 }

// IngressName is the name of the service's ingress.
func (s Service) IngressName() string {
	if s.Ingress != nil && s.Ingress.Name != "" {
		return s.Ingress.Name
	}
	return s.Name
}

// TLSGroups returns the service's hosts grouped by certificate secret, in
// host order: [{secret, hosts}].
func (s Service) TLSGroups() [][2]any {
	var order []string
	groups := map[string][]string{}
	for _, h := range s.Hosts() {
		secret := s.TLSSecretName()
		if s.Ingress != nil && s.Ingress.TLSSecrets[h] != "" {
			secret = s.Ingress.TLSSecrets[h]
		}
		if _, ok := groups[secret]; !ok {
			order = append(order, secret)
		}
		groups[secret] = append(groups[secret], h)
	}
	out := make([][2]any, 0, len(order))
	for _, sec := range order {
		out = append(out, [2]any{sec, groups[sec]})
	}
	return out
}

// ConfigFile mounts an existing ConfigMap as read-only files.
type ConfigFile struct {
	ConfigMap string `json:"configMap"`
	Mount     string `json:"mount"`
}

// SecretFile mounts an existing Secret as read-only files.
type SecretFile struct {
	Secret string `json:"secret"`
	Mount  string `json:"mount"`
}

// SecretNames lists every secret the service reads, however it reads it.
func (s Service) SecretNames() []string {
	return secretNames(s.Secrets, s.SecretEnv, s.SecretFiles)
}

// secretNames is the sorted, de-duplicated set of secrets named whole, in
// NAME: secret/key references, or as mounted files.
func secretNames(whole []string, env map[string]string, files []SecretFile) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range whole {
		add(n)
	}
	for _, ref := range env {
		name, _, _ := strings.Cut(ref, "/")
		add(name)
	}
	for _, f := range files {
		add(f.Secret)
	}
	sort.Strings(out)
	return out
}

// TLSSecretName is the certificate secret for the service's domain.
func (s Service) TLSSecretName() string {
	if s.TLSSecret != "" {
		return s.TLSSecret
	}
	return s.Name + "-tls"
}

// Build says how a service's image is built.
type Build struct {
	// Builder is "dockerfile", "railpack" or empty for automatic: the
	// Dockerfile when the service folder has one, Railpack otherwise.
	Builder    string            `json:"builder,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	// Start overrides the start command Railpack detects.
	Start string `json:"start,omitempty"`
}

const (
	BuilderAuto       = ""
	BuilderDockerfile = "dockerfile"
	BuilderRailpack   = "railpack"
)

func (b Build) validate(p string) []error {
	var errs []error
	switch b.Builder {
	case BuilderAuto, BuilderDockerfile, BuilderRailpack:
	default:
		errs = append(errs, fmt.Errorf("%s.build.builder %q must be dockerfile or railpack", p, b.Builder))
	}
	if b.Start != "" && b.Builder == BuilderDockerfile {
		errs = append(errs, fmt.Errorf("%s.build.start only applies to Railpack builds (the Dockerfile sets its own CMD)", p))
	}
	for k := range b.Args {
		if !envKey.MatchString(k) {
			errs = append(errs, fmt.Errorf("%s.build.args key %q is invalid", p, k))
		}
	}
	return errs
}

// Test is a command run in a container image before the build; a failure stops the build.
type Test struct {
	Image   string `json:"image"`
	Command string `json:"command"`
}

// Health is the readiness and liveness check of a service.
type Health struct {
	// Path is an HTTP GET check; TCP checks the port accepts connections
	// (databases and other non-HTTP services).
	Path string `json:"path,omitempty"`
	TCP  bool   `json:"tcp,omitempty"`
	// Timeout per check in seconds (Kubernetes default: 1).
	Timeout int `json:"timeout,omitempty"`
}

// Volume gives a service persistent storage (a Longhorn volume by default).
type Volume struct {
	// Size of a new volume; not needed with ExistingClaim.
	Size  string `json:"size,omitempty"`
	Mount string `json:"mount"`
	// ExistingClaim mounts a PersistentVolumeClaim that already exists (its
	// data is kept) instead of creating <name>-data. Used when migrating.
	ExistingClaim string `json:"existingClaim,omitempty"`
	// FSGroup makes the volume writable by this group. Defaults to 1001 for
	// built apps and to none for ready-made images: databases such as
	// Postgres refuse to start on a group-writable data directory.
	FSGroup *int64 `json:"fsGroup,omitempty"`
}

// VolumeFSGroup is the pod fsGroup for the service's volume, or nil.
func (s Service) VolumeFSGroup() *int64 {
	if s.Volume == nil {
		return nil
	}
	if s.Volume.FSGroup != nil {
		if *s.Volume.FSGroup < 0 {
			return nil // explicitly disabled with fsGroup: -1
		}
		return s.Volume.FSGroup
	}
	if s.Image != "" {
		return nil
	}
	g := int64(1001)
	return &g
}

// Hosts are every hostname the service answers on: domain, then aliases.
func (s Service) Hosts() []string {
	if s.Domain == "" {
		return nil
	}
	return append([]string{s.Domain}, s.Aliases...)
}

// ClaimName is the PersistentVolumeClaim the service mounts.
func (s Service) ClaimName() string {
	if s.Volume != nil && s.Volume.ExistingClaim != "" {
		return s.Volume.ExistingClaim
	}
	return s.Name + "-data"
}

// Size is a preset of CPU and memory requests and limits, sized for Raspberry Pi nodes.
type Size string

const (
	SizeSmall  Size = "small"
	SizeMedium Size = "medium"
	SizeLarge  Size = "large"
)

// Resources are requests/limits for a size, sized for Raspberry Pi nodes.
type Resources struct {
	CPURequest, MemRequest, CPULimit, MemLimit string
}

var sizes = map[Size]Resources{
	SizeSmall:  {"50m", "64Mi", "500m", "256Mi"},
	SizeMedium: {"100m", "256Mi", "1", "512Mi"},
	SizeLarge:  {"250m", "512Mi", "2", "1Gi"},
}

// Resources returns the size's requests and limits.
func (s Size) Resources() Resources { return sizes[s] }

var (
	dnsLabel  = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
	hostname  = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)
	envKey    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	quantity  = regexp.MustCompile(`^[0-9]+(Mi|Gi|Ti)$`)
	claimName = regexp.MustCompile(`^[a-z0-9]([-.a-z0-9]{0,251}[a-z0-9])?$`)
	// Secret keys may contain dots (practice.json), unlike DNS labels.
	secretKey = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

// Parse reads rendimiento.yaml, applies defaults and validates it.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	if err := yaml.UnmarshalStrict(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", FileName, err)
	}
	s.Default()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Marshal writes the spec as YAML.
func (s *Spec) Marshal() ([]byte, error) { return yaml.Marshal(s) }

// Default fills in defaults (path, port, size, replicas, Dockerfile) in place.
func (s *Spec) Default() {
	for i := range s.Tasks {
		t := &s.Tasks[i]
		if t.Path == "" {
			t.Path = "."
		}
		if t.When == "" {
			t.When = TaskOnDeploy
		}
		if t.Size == "" {
			t.Size = SizeMedium // commands like npm and eas-cli need more than small's 256Mi
		}
	}
	for i := range s.Jobs {
		j := &s.Jobs[i]
		if j.Size == "" {
			j.Size = SizeSmall
		}
		if j.Path != "" && j.Build.Dockerfile == "" {
			j.Build.Dockerfile = "Dockerfile"
		}
	}
	for i := range s.Services {
		svc := &s.Services[i]
		if svc.Path == "" {
			svc.Path = "."
		}
		if svc.Port == 0 {
			svc.Port = 8080
		}
		if svc.Size == "" {
			svc.Size = SizeSmall
		}
		if svc.Replicas == 0 {
			svc.Replicas = 1
		}
		if svc.Build.Dockerfile == "" {
			svc.Build.Dockerfile = "Dockerfile"
		}
	}
}

// Validate reports every problem in the spec at once, each with its path (services[1].port …).
func (s *Spec) Validate() error {
	if len(s.Services) == 0 {
		return errors.New("at least one service is required")
	}
	var errs []error
	seen := map[string]bool{}
	domains := map[string]bool{}
	for i, svc := range s.Services {
		p := fmt.Sprintf("services[%d]", i)
		if !dnsLabel.MatchString(svc.Name) {
			errs = append(errs, fmt.Errorf("%s.name %q must be a lowercase DNS label", p, svc.Name))
		}
		if seen[svc.Name] {
			errs = append(errs, fmt.Errorf("%s.name %q is duplicated", p, svc.Name))
		}
		seen[svc.Name] = true
		for _, w := range svc.Watch {
			if w == "" || strings.HasPrefix(w, "/") || strings.Contains(w, "..") {
				errs = append(errs, fmt.Errorf("%s.watch %q must be a path relative to the repo root", p, w))
			}
		}
		if strings.HasPrefix(svc.Path, "/") || strings.Contains(svc.Path, "..") {
			errs = append(errs, fmt.Errorf("%s.path %q must be relative to the repo root", p, svc.Path))
		}
		if svc.Port < 1 || svc.Port > 65535 {
			errs = append(errs, fmt.Errorf("%s.port %d out of range", p, svc.Port))
		}
		if _, ok := sizes[svc.Size]; !ok {
			errs = append(errs, fmt.Errorf("%s.size %q must be small, medium or large", p, svc.Size))
		}
		if svc.Replicas < 0 || svc.Replicas > 10 {
			errs = append(errs, fmt.Errorf("%s.replicas %d must be between 0 and 10", p, svc.Replicas))
		}
		if len(svc.Aliases) > 0 && svc.Domain == "" {
			errs = append(errs, fmt.Errorf("%s.aliases need a domain", p))
		}
		for _, h := range svc.Hosts() {
			if !hostname.MatchString(h) {
				errs = append(errs, fmt.Errorf("%s: %q is not a valid hostname", p, h))
			}
			if domains[h] {
				errs = append(errs, fmt.Errorf("%s: host %q is used more than once", p, h))
			}
			domains[h] = true
		}
		if h := svc.Health; h != nil {
			switch {
			case h.TCP && h.Path != "":
				errs = append(errs, fmt.Errorf("%s.health: use either path or tcp", p))
			case !h.TCP && !strings.HasPrefix(h.Path, "/"):
				errs = append(errs, fmt.Errorf("%s.health.path must start with /", p))
			}
		}
		errs = append(errs, validateResources(p, svc.Resources)...)
		if in := svc.Ingress; in != nil {
			if in.Name != "" && !dnsLabel.MatchString(in.Name) {
				errs = append(errs, fmt.Errorf("%s.ingress.name %q must be a lowercase DNS label", p, in.Name))
			}
			for k := range in.Annotations {
				if !strings.HasPrefix(k, "nginx.ingress.kubernetes.io/") || strings.HasSuffix(k, "-snippet") {
					errs = append(errs, fmt.Errorf("%s.ingress.annotations: %q is not allowed (only nginx.ingress.kubernetes.io/* settings, no snippets)", p, k))
				}
			}
			hosts := map[string]bool{}
			for _, h := range svc.Hosts() {
				hosts[h] = true
			}
			for h, sec := range in.TLSSecrets {
				if !hosts[h] {
					errs = append(errs, fmt.Errorf("%s.ingress.tlsSecrets: %q is not one of the service's hosts", p, h))
				}
				if !dnsLabel.MatchString(sec) {
					errs = append(errs, fmt.Errorf("%s.ingress.tlsSecrets.%s %q must be a lowercase DNS label", p, h, sec))
				}
			}
		}
		if h := svc.Health; h != nil && (h.Timeout < 0 || h.Timeout > 60) {
			errs = append(errs, fmt.Errorf("%s.health.timeout must be between 1 and 60 seconds", p))
		}
		if svc.Image != "" && svc.Test != nil {
			errs = append(errs, fmt.Errorf("%s: a service with a ready-made image has no test step", p))
		}
		if svc.Image == "" {
			errs = append(errs, svc.Build.validate(p)...)
		}
		if l := svc.LAN; l != nil {
			if ip := net.ParseIP(l.IP); l.IP != "" && (ip == nil || ip.To4() == nil || !ip.IsPrivate()) {
				errs = append(errs, fmt.Errorf("%s.lan.ip %q must be a private IPv4 address from the load balancer's pool", p, l.IP))
			}
			if l.Port < 0 || l.Port > 65535 {
				errs = append(errs, fmt.Errorf("%s.lan.port %d out of range", p, l.Port))
			}
		}
		if svc.GPU < 0 || svc.GPU > 8 {
			errs = append(errs, fmt.Errorf("%s.gpu %d must be between 0 and 8", p, svc.GPU))
		}
		if svc.GPU > 0 && svc.Replicas > 1 {
			errs = append(errs, fmt.Errorf("%s: GPU services run one replica (each replica needs its own GPU)", p))
		}
		if c := svc.Catalog; c != nil {
			if c.Category != "" && !Categories[c.Category] {
				errs = append(errs, fmt.Errorf("%s.catalog.category %q must be one of database, messaging, ai, storage, monitoring, web, devtools, platform", p, c.Category))
			}
			if c.Env != "" && !envKey.MatchString(c.Env) {
				errs = append(errs, fmt.Errorf("%s.catalog.env %q is not a valid variable name", p, c.Env))
			}
			if c.Path != "" && !strings.HasPrefix(c.Path, "/") {
				errs = append(errs, fmt.Errorf("%s.catalog.path must start with /", p))
			}
			if c.Docs != "" && !strings.HasPrefix(c.Docs, "https://") && !strings.HasPrefix(c.Docs, "http://") {
				errs = append(errs, fmt.Errorf("%s.catalog.docs must be an http(s) link", p))
			}
		}
		for i, f := range svc.ConfigFiles {
			if !dnsLabel.MatchString(f.ConfigMap) {
				errs = append(errs, fmt.Errorf("%s.configFiles[%d].configMap %q must be a lowercase DNS label", p, i, f.ConfigMap))
			}
			if !strings.HasPrefix(f.Mount, "/") || f.Mount == "/" {
				errs = append(errs, fmt.Errorf("%s.configFiles[%d].mount must be an absolute directory other than /", p, i))
			}
		}
		if svc.Test != nil && (svc.Test.Image == "" || svc.Test.Command == "") {
			errs = append(errs, fmt.Errorf("%s.test needs both image and command", p))
		}
		for k := range svc.Env {
			if !envKey.MatchString(k) {
				errs = append(errs, fmt.Errorf("%s.env key %q is invalid", p, k))
			}
		}
		for _, sec := range svc.Secrets {
			if !dnsLabel.MatchString(sec) {
				errs = append(errs, fmt.Errorf("%s.secrets entry %q must be a lowercase DNS label", p, sec))
			}
		}
		for k, ref := range svc.SecretEnv {
			name, key, ok := strings.Cut(ref, "/")
			if !envKey.MatchString(k) {
				errs = append(errs, fmt.Errorf("%s.secretEnv key %q is invalid", p, k))
			}
			if !ok || !dnsLabel.MatchString(name) || !secretKey.MatchString(key) {
				errs = append(errs, fmt.Errorf("%s.secretEnv.%s must be <secret>/<key>, got %q", p, k, ref))
			}
		}
		mounts := map[string]bool{}
		if svc.Volume != nil {
			mounts[svc.Volume.Mount] = true
		}
		for i, f := range svc.SecretFiles {
			if !dnsLabel.MatchString(f.Secret) {
				errs = append(errs, fmt.Errorf("%s.secretFiles[%d].secret %q must be a lowercase DNS label", p, i, f.Secret))
			}
			if !strings.HasPrefix(f.Mount, "/") || f.Mount == "/" {
				errs = append(errs, fmt.Errorf("%s.secretFiles[%d].mount must be an absolute directory other than /", p, i))
			}
			if mounts[f.Mount] {
				errs = append(errs, fmt.Errorf("%s.secretFiles[%d].mount %s is already used", p, i, f.Mount))
			}
			mounts[f.Mount] = true
		}
		if svc.TLSSecret != "" && !dnsLabel.MatchString(svc.TLSSecret) {
			errs = append(errs, fmt.Errorf("%s.tlsSecret %q must be a lowercase DNS label", p, svc.TLSSecret))
		}
		if v := svc.Volume; v != nil {
			switch {
			case v.ExistingClaim != "":
				if !claimName.MatchString(v.ExistingClaim) {
					errs = append(errs, fmt.Errorf("%s.volume.existingClaim %q is not a valid claim name", p, v.ExistingClaim))
				}
				if v.Size != "" && !quantity.MatchString(v.Size) {
					errs = append(errs, fmt.Errorf("%s.volume.size %q must look like 5Gi", p, v.Size))
				}
			case !quantity.MatchString(v.Size):
				errs = append(errs, fmt.Errorf("%s.volume.size %q must look like 5Gi", p, v.Size))
			}
			if !strings.HasPrefix(v.Mount, "/") {
				errs = append(errs, fmt.Errorf("%s.volume.mount must be an absolute path", p))
			}
		}
	}
	errs = append(errs, s.validateNeeds(seen)...)
	errs = append(errs, s.validateRoutes()...)
	errs = append(errs, s.validateJobs(seen)...)
	errs = append(errs, s.validateTasks()...)
	return errors.Join(errs...)
}

func (s *Spec) validateTasks() []error {
	var errs []error
	built := map[string]bool{} // services with a build step
	used := map[string]string{}
	for _, svc := range s.Services {
		used[svc.Name] = "service"
		built[svc.Name] = svc.Image == ""
	}
	for _, j := range s.Jobs {
		used[j.Name] = "job"
	}
	tasks := map[string]Task{}
	for i, t := range s.Tasks {
		p := fmt.Sprintf("tasks[%d]", i)
		if !dnsLabel.MatchString(t.Name) || len(t.Name) > 40 {
			errs = append(errs, fmt.Errorf("%s.name %q must be a lowercase DNS label of at most 40 characters", p, t.Name))
		}
		if kind, dup := used[t.Name]; dup {
			errs = append(errs, fmt.Errorf("%s.name %q is already used by a %s", p, t.Name, kind))
		}
		used[t.Name] = "task"
		tasks[t.Name] = t
		if t.Image == "" || strings.TrimSpace(t.Command) == "" {
			errs = append(errs, fmt.Errorf("%s needs both image and command", p))
		}
		if strings.HasPrefix(t.Path, "/") || strings.Contains(t.Path, "..") {
			errs = append(errs, fmt.Errorf("%s.path %q must be relative to the repo root", p, t.Path))
		}
		for _, w := range t.Watch {
			if w == "" || strings.HasPrefix(w, "/") || strings.Contains(w, "..") {
				errs = append(errs, fmt.Errorf("%s.watch %q must be a path relative to the repo root", p, w))
			}
		}
		if t.When != TaskOnDeploy && t.When != TaskOnAlways {
			errs = append(errs, fmt.Errorf("%s.when %q must be deploy or always", p, t.When))
		}
		if _, ok := sizes[t.Size]; !ok {
			errs = append(errs, fmt.Errorf("%s.size %q must be small, medium or large", p, t.Size))
		}
		errs = append(errs, validateResources(p, t.Resources)...)
		if t.Timeout < 0 {
			errs = append(errs, fmt.Errorf("%s.timeout must be a number of seconds", p))
		}
		for k := range t.Env {
			if !envKey.MatchString(k) {
				errs = append(errs, fmt.Errorf("%s.env key %q is invalid", p, k))
			}
		}
		for _, sec := range t.Secrets {
			if !dnsLabel.MatchString(sec) {
				errs = append(errs, fmt.Errorf("%s.secrets entry %q must be a lowercase DNS label", p, sec))
			}
		}
		for k, ref := range t.SecretEnv {
			name, key, ok := strings.Cut(ref, "/")
			if !envKey.MatchString(k) || !ok || !dnsLabel.MatchString(name) || !secretKey.MatchString(key) {
				errs = append(errs, fmt.Errorf("%s.secretEnv.%s must be <secret>/<key>, got %q", p, k, ref))
			}
		}
	}
	for i, t := range s.Tasks {
		for _, a := range t.After {
			switch {
			case a == t.Name:
				errs = append(errs, fmt.Errorf("tasks[%d].after: a task cannot wait for itself", i))
			case used[a] == "service" && !built[a]:
				errs = append(errs, fmt.Errorf("tasks[%d].after: service %q runs a ready-made image, so there is no build to wait for", i, a))
			case used[a] != "service" && used[a] != "task":
				errs = append(errs, fmt.Errorf("tasks[%d].after: %q is not a service or task of this app", i, a))
			}
		}
	}
	// A cycle among tasks would leave every task in it waiting forever.
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(name string) bool
	visit = func(name string) bool {
		switch state[name] {
		case 1:
			return false
		case 2:
			return true
		}
		state[name] = 1
		for _, a := range tasks[name].After {
			if _, isTask := tasks[a]; isTask && a != name && !visit(a) {
				return false
			}
		}
		state[name] = 2
		return true
	}
	for _, t := range s.Tasks {
		if state[t.Name] == 0 && !visit(t.Name) {
			errs = append(errs, fmt.Errorf("tasks: %q is part of a cycle of after: references", t.Name))
			break
		}
	}
	return errs
}

var serviceRef = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?/)?[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
var majorVersion = regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]{1,2})?$`)

func (s *Spec) validateNeeds(services map[string]bool) []error {
	var errs []error
	for i, svc := range s.Services {
		p := fmt.Sprintf("services[%d].needs", i)
		envs := map[string]bool{}
		for j, n := range svc.Needs {
			q := fmt.Sprintf("%s[%d]", p, j)
			switch n.Kind {
			case NeedPostgres, NeedRedis:
			case NeedService:
				if !serviceRef.MatchString(n.Service) {
					errs = append(errs, fmt.Errorf("%s.service %q must be namespace/name or a service of this app", q, n.Service))
				} else if !strings.Contains(n.Service, "/") && !services[n.Service] {
					errs = append(errs, fmt.Errorf("%s.service %q is not a service of this app (use namespace/name for others)", q, n.Service))
				}
			default:
				errs = append(errs, fmt.Errorf("%s: %q is not something rendimiento provides (postgres, redis, or {service: namespace/name})", q, n.Kind))
				continue
			}
			env := n.EnvName()
			if !envKey.MatchString(env) {
				errs = append(errs, fmt.Errorf("%s.env %q is not a valid variable name", q, env))
			}
			if envs[env] {
				errs = append(errs, fmt.Errorf("%s: %s is set by two needs; give one an env", q, env))
			}
			envs[env] = true
		}
	}
	for _, kind := range []string{NeedPostgres, NeedRedis} {
		if s.Needs(kind) && services[kind] {
			errs = append(errs, fmt.Errorf("a service is named %q, which the app's %s would also be called; rename the service", kind, kind))
		}
	}
	if o := s.Postgres; o != nil {
		if o.Version != "" && !majorVersion.MatchString(o.Version) {
			errs = append(errs, fmt.Errorf("postgres.version %q must be a version such as 17", o.Version))
		}
		if o.Size != "" && !quantity.MatchString(o.Size) {
			errs = append(errs, fmt.Errorf("postgres.size %q must look like 5Gi", o.Size))
		}
		errs = append(errs, validateResources("postgres", o.Resources)...)
	}
	if o := s.Redis; o != nil {
		if o.Version != "" && !majorVersion.MatchString(o.Version) {
			errs = append(errs, fmt.Errorf("redis.version %q must be a version such as 7", o.Version))
		}
		if o.MaxMemory != "" && !regexp.MustCompile(`^[0-9]+(kb|mb|gb)$`).MatchString(o.MaxMemory) {
			errs = append(errs, fmt.Errorf("redis.maxMemory %q must look like 64mb", o.MaxMemory))
		}
		errs = append(errs, validateResources("redis", o.Resources)...)
	}
	return errs
}

func validateResources(p string, o *ResourceOverride) []error {
	if o == nil {
		return nil
	}
	var errs []error
	for field, v := range map[string]string{"cpu": o.CPU, "memory": o.Memory, "cpuLimit": o.CPULimit, "memoryLimit": o.MemoryLimit} {
		if v == "" || (v == "none" && strings.HasSuffix(field, "Limit")) {
			continue
		}
		if _, err := resourceQuantity(v); err != nil {
			errs = append(errs, fmt.Errorf("%s.resources.%s %q is not a valid quantity", p, field, v))
		}
	}
	return errs
}

// resourceQuantity checks a Kubernetes quantity like 250m, 1Gi or 2.
var quantityRe = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?)(m|Ki|Mi|Gi|Ti|k|M|G|T)?$`)

func resourceQuantity(v string) (string, error) {
	if !quantityRe.MatchString(v) {
		return "", fmt.Errorf("invalid quantity %q", v)
	}
	return v, nil
}

func (s *Spec) validateRoutes() []error {
	var errs []error
	owned := map[string]bool{}
	for _, svc := range s.Services {
		for _, h := range svc.Hosts() {
			owned[h] = true
		}
	}
	seen := map[Route]string{}
	for i, svc := range s.Services {
		for _, r := range svc.AllRoutes() {
			if prev, dup := seen[r]; dup && prev != svc.Name {
				errs = append(errs, fmt.Errorf("services[%d]: %s%s is already routed to %s", i, r.Host, r.Path, prev))
			}
			seen[r] = svc.Name
		}
		for _, raw := range svc.Routes {
			r := ParseRoute(raw)
			switch {
			case !hostname.MatchString(r.Host):
				errs = append(errs, fmt.Errorf("services[%d].routes: %q has no valid host", i, raw))
			case !owned[r.Host]:
				errs = append(errs, fmt.Errorf("services[%d].routes: %s is not the domain or alias of a service in this app (routes use its certificate)", i, r.Host))
			case r.Path == "/":
				errs = append(errs, fmt.Errorf("services[%d].routes: %q needs a path, e.g. %s/api", i, raw, r.Host))
			}
		}
	}
	return errs
}

// cronField is a loose check: five space-separated fields or an @macro.
var cronField = regexp.MustCompile(`^(@(yearly|annually|monthly|weekly|daily|midnight|hourly)|(\S+\s+){4}\S+)$`)

func (s *Spec) validateJobs(services map[string]bool) []error {
	var errs []error
	seen := map[string]bool{}
	for i, j := range s.Jobs {
		p := fmt.Sprintf("jobs[%d]", i)
		if !dnsLabel.MatchString(j.Name) || len(j.Name) > 52 {
			errs = append(errs, fmt.Errorf("%s.name %q must be a lowercase DNS label of at most 52 characters", p, j.Name))
		}
		if seen[j.Name] || services[j.Name] {
			errs = append(errs, fmt.Errorf("%s.name %q is already used by a job or service", p, j.Name))
		}
		seen[j.Name] = true
		if !cronField.MatchString(strings.TrimSpace(j.Schedule)) {
			errs = append(errs, fmt.Errorf("%s.schedule %q is not a cron schedule", p, j.Schedule))
		}
		sources := 0
		for _, set := range []bool{j.Service != "", j.Path != "", j.Image != ""} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			errs = append(errs, fmt.Errorf("%s needs exactly one of service, path or image", p))
		}
		if j.Service != "" && !services[j.Service] {
			errs = append(errs, fmt.Errorf("%s.service %q is not a service in this app", p, j.Service))
		}
		if strings.HasPrefix(j.Path, "/") || strings.Contains(j.Path, "..") {
			errs = append(errs, fmt.Errorf("%s.path %q must be relative to the repo root", p, j.Path))
		}
		if _, ok := sizes[j.Size]; !ok {
			errs = append(errs, fmt.Errorf("%s.size %q must be small, medium or large", p, j.Size))
		}
		errs = append(errs, validateResources(p, j.Resources)...)
		if j.Path != "" {
			errs = append(errs, j.Build.validate(p)...)
		}
		for k, ref := range j.SecretEnv {
			name, key, ok := strings.Cut(ref, "/")
			if !envKey.MatchString(k) || !ok || !dnsLabel.MatchString(name) || !secretKey.MatchString(key) {
				errs = append(errs, fmt.Errorf("%s.secretEnv.%s must be <secret>/<key>, got %q", p, k, ref))
			}
		}
		for k := range j.Env {
			if !envKey.MatchString(k) {
				errs = append(errs, fmt.Errorf("%s.env key %q is invalid", p, k))
			}
		}
	}
	return errs
}
