// Package universe is the view of every app from above: the services each
// one runs, the databases and caches they use, what calls what, and what
// the internet reaches. It joins what rendimiento already knows (each
// rendimiento.yaml's needs, the services catalog, the uptime checks) into
// one graph for the Universe page.
package universe

import (
	"sort"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/catalog"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Internet is the node that stands for visitors from outside the cluster.
const Internet = "internet"

// Where an edge comes from.
const (
	// SourceDeclared is a `needs:` entry in rendimiento.yaml.
	SourceDeclared = "declared"
	// SourceEnv is an environment variable pointing at the service.
	SourceEnv = "env"
	// SourceIngress is a public address routed to the service.
	SourceIngress = "ingress"
)

// Kinds of systems: a rendimiento app, an add-on, or something deployed
// another way (ArgoCD, Helm, kubectl), or cluster plumbing.
const (
	SystemApp   = "app"
	SystemAddon = "addon"
	SystemOther = "other"
	SystemInfra = "infrastructure"
)

// Universe is every system, its nodes and the edges between them.
type Universe struct {
	Systems   []System  `json:"systems"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	Generated time.Time `json:"generated"`
}

// System is a group of nodes: one app, one add-on, or one namespace.
type System struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// App is set for rendimiento apps, for the link to the app's page.
	App  string `json:"app,omitempty"`
	Repo string `json:"repo,omitempty"`
}

// Node is a service, a database or cache, a workload that only calls
// others (a cron job), or the internet.
type Node struct {
	ID     string `json:"id"` // namespace/name, or "internet"
	Name   string `json:"name"`
	System string `json:"system,omitempty"`
	// Kind is service, postgres, redis, workload or internet.
	Kind     string   `json:"kind"`
	Language string   `json:"language,omitempty"`
	Category string   `json:"category,omitempty"`
	Public   []string `json:"public,omitempty"`
	Pods     int      `json:"pods"`
	Ready    int      `json:"ready"`
	// Catalog is true when the Services page has an entry for the node.
	Catalog bool    `json:"catalog,omitempty"`
	Health  *Health `json:"health,omitempty"`
}

// Health is the latest uptime checks of a service.
type Health struct {
	Up bool      `json:"up"`
	At time.Time `json:"at"`
	// InsideMS and PublicMS are the latest successful check times, 0 when
	// that check failed or does not exist.
	InsideMS int    `json:"insideMs,omitempty"`
	PublicMS int    `json:"publicMs,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Edge is one node calling another.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Sources says how the edge is known: declared, env, ingress.
	Sources []string `json:"sources"`
	// Env is the variable the caller finds the address in, when known.
	Env string `json:"env,omitempty"`
	// Hosts are the public addresses, for edges from the internet.
	Hosts []string `json:"hosts,omitempty"`
}

// Input is what the universe is made from.
type Input struct {
	Apps    []*store.App
	Catalog *catalog.Catalog // nil when the catalog is off
	// Probes are the latest uptime checks, one per app, service and kind.
	Probes []store.Probe
	Now    time.Time
}

// Build joins the apps, the catalog and the uptime checks into one graph.
// Nodes and edges come out sorted, so the same cluster gives the same JSON.
func Build(in Input) Universe {
	b := builder{nodes: map[string]*Node{}, systems: map[string]*System{}, edges: map[[2]string]*Edge{}}
	appByID := map[int64]*store.App{}

	// The apps and what their rendimiento.yaml declares.
	for _, a := range in.Apps {
		appByID[a.ID] = a
		b.system(System{ID: a.Name, Title: a.Name, Kind: SystemApp, App: a.Name, Repo: a.Repo})
		for _, svc := range a.Spec.Services {
			n := b.node(a.Name+"/"+svc.Name, svc.Name, a.Name)
			n.Kind, n.Language = "service", svc.Language
		}
		for _, kind := range []string{spec.NeedPostgres, spec.NeedRedis} {
			if a.Spec.Needs(kind) {
				b.node(a.Name+"/"+kind, kind, a.Name).Kind = kind
			}
		}
		for _, j := range a.Spec.Jobs {
			b.node(a.Name+"/"+j.Name, j.Name, a.Name).Kind = "workload"
		}
		for _, svc := range a.Spec.Services {
			from := a.Name + "/" + svc.Name
			for _, n := range svc.Needs {
				to := a.Name + "/" + n.Kind
				if n.Kind == spec.NeedService {
					to = n.Service
					if !strings.Contains(to, "/") {
						to = a.Name + "/" + to
					}
				}
				b.edge(from, to, SourceDeclared, n.EnvName())
			}
		}
	}

	// What runs in the cluster, and what calls it.
	if in.Catalog != nil {
		for _, e := range in.Catalog.Entries {
			sys := e.Namespace
			if e.Origin.App != "" && (e.Origin.Kind == "rendimiento" || e.Origin.Kind == "addon") {
				sys = e.Origin.App
			}
			b.system(System{ID: sys, Title: sys, Kind: systemKind(e)})
			n := b.node(e.ID, e.Name, sys)
			n.Catalog, n.Category, n.Public, n.Pods, n.Ready = true, e.Category, e.Public, e.Pods, e.Ready
			if n.Kind == "" {
				n.Kind = "service"
			}
			for _, c := range e.UsedBy {
				from := c.Namespace + "/" + c.Workload
				csys := c.Namespace
				if c.App != "" {
					csys = c.App
				}
				if b.nodes[from] == nil {
					b.system(System{ID: csys, Title: csys, Kind: SystemOther})
					b.node(from, c.Workload, csys).Kind = "workload"
				}
				b.edge(from, e.ID, SourceEnv, c.Env)
			}
			if len(e.Public) > 0 {
				b.edge(Internet, e.ID, SourceIngress, "").Hosts = e.Public
			}
		}
	}

	// A need on a service outside rendimiento's apps that the catalog did
	// not list still gets a node, so the edge has somewhere to land.
	for k := range b.edges {
		for _, id := range k {
			if b.nodes[id] == nil && id != Internet {
				ns, name, _ := strings.Cut(id, "/")
				b.system(System{ID: ns, Title: ns, Kind: SystemOther})
				b.node(id, name, ns).Kind = "service"
			}
		}
	}
	if len(b.edgesFrom(Internet)) > 0 {
		b.nodes[Internet] = &Node{ID: Internet, Name: "Internet", Kind: Internet}
	}
	b.dropLoneInfrastructure()
	b.health(in.Probes, appByID)

	u := Universe{Systems: []System{}, Nodes: []Node{}, Edges: []Edge{}, Generated: in.Now}
	used := map[string]bool{}
	for _, n := range b.nodes {
		u.Nodes = append(u.Nodes, *n)
		used[n.System] = true
	}
	for _, s := range b.systems {
		if used[s.ID] {
			u.Systems = append(u.Systems, *s)
		}
	}
	for _, e := range b.edges {
		sort.Strings(e.Sources)
		u.Edges = append(u.Edges, *e)
	}
	sort.Slice(u.Systems, func(i, j int) bool { return u.Systems[i].ID < u.Systems[j].ID })
	sort.Slice(u.Nodes, func(i, j int) bool { return u.Nodes[i].ID < u.Nodes[j].ID })
	sort.Slice(u.Edges, func(i, j int) bool {
		if u.Edges[i].From != u.Edges[j].From {
			return u.Edges[i].From < u.Edges[j].From
		}
		return u.Edges[i].To < u.Edges[j].To
	})
	return u
}

// systemKind is the kind of system a catalog entry belongs to.
func systemKind(e catalog.Entry) string {
	switch {
	case e.Origin.Kind == "rendimiento":
		return SystemApp
	case e.Origin.Kind == "addon":
		return SystemAddon
	case e.Group == catalog.GroupInfra:
		return SystemInfra
	}
	return SystemOther
}

// builder collects nodes, systems and edges by ID while the graph is made.
type builder struct {
	nodes   map[string]*Node
	systems map[string]*System
	edges   map[[2]string]*Edge
}

// system adds s, keeping the first one seen for an ID (apps come first,
// so an app's system keeps its kind and repo).
func (b *builder) system(s System) {
	if b.systems[s.ID] == nil {
		b.systems[s.ID] = &s
	}
}

// node is the node with id, added in system when new.
func (b *builder) node(id, name, system string) *Node {
	n := b.nodes[id]
	if n == nil {
		n = &Node{ID: id, Name: name, System: system}
		b.nodes[id] = n
	}
	return n
}

// edge records from calling to, merging what each source says about it.
func (b *builder) edge(from, to, source, env string) *Edge {
	k := [2]string{from, to}
	e := b.edges[k]
	if e == nil {
		e = &Edge{From: from, To: to}
		b.edges[k] = e
	}
	if !contains(e.Sources, source) {
		e.Sources = append(e.Sources, source)
	}
	if e.Env == "" {
		e.Env = env
	}
	return e
}

// edgesFrom lists the edges leaving id.
func (b *builder) edgesFrom(id string) []*Edge {
	var out []*Edge
	for k, e := range b.edges {
		if k[0] == id {
			out = append(out, e)
		}
	}
	return out
}

// dropLoneInfrastructure removes cluster plumbing nobody outside it calls:
// ingress controllers, cert-manager and the like are noise on the map
// unless an app depends on them.
func (b *builder) dropLoneInfrastructure() {
	infra := func(id string) bool {
		n := b.nodes[id]
		return n != nil && b.systems[n.System] != nil && b.systems[n.System].Kind == SystemInfra
	}
	keep := map[string]bool{}
	for k := range b.edges {
		from, to := k[0], k[1]
		if from == Internet {
			continue
		}
		if infra(to) && !infra(from) {
			keep[to] = true
		}
		if infra(from) && !infra(to) {
			keep[from] = true
		}
	}
	for id := range b.nodes {
		if infra(id) && !keep[id] {
			delete(b.nodes, id)
		}
	}
	for k := range b.edges {
		if (k[0] != Internet && b.nodes[k[0]] == nil) || b.nodes[k[1]] == nil {
			delete(b.edges, k)
		}
	}
}

// health attaches each service's latest uptime checks: up when every
// latest check passed.
func (b *builder) health(probes []store.Probe, apps map[int64]*store.App) {
	for _, p := range probes {
		a := apps[p.AppID]
		if a == nil {
			continue
		}
		n := b.nodes[a.Name+"/"+p.Service]
		if n == nil {
			continue
		}
		if n.Health == nil {
			n.Health = &Health{Up: true}
		}
		h := n.Health
		if p.At.After(h.At) {
			h.At = p.At
		}
		if !p.OK {
			h.Up = false
			if h.Error == "" {
				h.Error = p.Error
			}
			continue
		}
		if p.Kind == "public" { // uptime.KindPublic; the other is "internal"
			h.PublicMS = p.LatencyMS
		} else {
			h.InsideMS = p.LatencyMS
		}
	}
}

// contains reports whether list has s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
