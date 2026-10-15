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
	// SourceTraced is a call seen in traces (services with observe).
	SourceTraced = "traced"
)

// Outside is the system of what traced services call outside the cluster.
const Outside = "outside"

// Kinds of systems: a rendimiento app, an add-on, or something deployed
// another way (ArgoCD, Helm, kubectl), or cluster plumbing.
const (
	SystemApp   = "app"
	SystemAddon = "addon"
	SystemOther = "other"
	SystemInfra = "infrastructure"
)

// Universe is every system, its nodes and the edges between them, and the
// barrios the systems are grouped in.
type Universe struct {
	Barrios   []Barrio  `json:"barrios"`
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
	// Barrio is the ID of the barrio the system is in.
	Barrio string `json:"barrio"`
}

// Kinds of barrios.
const (
	// BarrioDeclared is named by the apps' rendimiento.yaml (barrio:).
	BarrioDeclared = "declared"
	// BarrioAuto is apps without a barrio that call each other.
	BarrioAuto = "auto"
	// BarrioCentre is the apps that call no other app and declare no barrio.
	BarrioCentre = "centre"
	// BarrioOutskirts is everything rendimiento does not deploy: add-ons,
	// what was deployed another way, calls outside the cluster.
	BarrioOutskirts = "outskirts"
)

// Barrio is a group of systems on the map, so a cluster with many apps
// can be seen a group at a time.
type Barrio struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// Node is a service, a database or cache, a workload that only calls
// others (a cron job), or the internet.
type Node struct {
	ID     string `json:"id"` // namespace/name, or "internet"
	Name   string `json:"name"`
	System string `json:"system,omitempty"`
	// Kind is service, postgres, redis, workload, external (called
	// outside the cluster, seen in traces) or internet.
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
	// Metrics are measured from traces, when the caller or callee is observed.
	Metrics *EdgeMetrics `json:"metrics,omitempty"`
}

// Input is what the universe is made from.
type Input struct {
	Apps    []*store.App
	Catalog *catalog.Catalog // nil when the catalog is off
	// Probes are the latest uptime checks, one per app, service and kind.
	Probes []store.Probe
	// Traced are the calls seen in traces, keyed by the collector's client
	// and server names (nil without metrics).
	Traced map[[2]string]EdgeMetrics
	Now    time.Time
}

// Build joins the apps, the catalog and the uptime checks into one graph.
// Nodes and edges come out sorted, so the same cluster gives the same JSON.
func Build(in Input) Universe {
	b := builder{nodes: map[string]*Node{}, systems: map[string]*System{}, edges: map[[2]string]*Edge{}}
	appByID := map[int64]*store.App{}
	declared := map[string]string{}

	// The apps and what their rendimiento.yaml declares.
	for _, a := range in.Apps {
		appByID[a.ID] = a
		declared[a.Name] = a.Spec.Barrio
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
	b.traced(in.Traced)
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
	u.Barrios = barrios(u.Systems, u.Nodes, u.Edges, declared)
	return u
}

// barrios puts every system in a barrio: the one its app declares; else,
// for an app that calls other apps or is called by them, the barrio of
// the apps it talks to most (label propagation, weighted by calls per
// minute where traces measured them); else the centre. What is not a
// rendimiento app goes to the outskirts. It sets each system's Barrio and
// returns the barrios in use: declared, found, centre, outskirts.
func barrios(systems []System, nodes []Node, edges []Edge, declared map[string]string) []Barrio {
	systemOf := map[string]string{}
	for _, n := range nodes {
		systemOf[n.ID] = n.System
	}
	isApp := map[string]bool{}
	for _, s := range systems {
		isApp[s.ID] = s.Kind == SystemApp
	}
	size := map[string]int{}
	for _, n := range nodes {
		size[n.System]++
	}
	// How much each pair of apps talks.
	weight := map[string]map[string]float64{}
	link := func(a, b string, w float64) {
		if weight[a] == nil {
			weight[a] = map[string]float64{}
		}
		weight[a][b] += w
	}
	for _, e := range edges {
		a, b := systemOf[e.From], systemOf[e.To]
		if a == b || !isApp[a] || !isApp[b] {
			continue
		}
		w := 1.0
		if e.Metrics != nil {
			w += e.Metrics.PerMinute
		}
		link(a, b, w)
		link(b, a, w)
	}

	label := map[string]string{}
	found := map[string]Barrio{}
	var free []string
	for _, s := range systems {
		if !isApp[s.ID] {
			continue
		}
		if d := strings.TrimSpace(declared[s.ID]); d != "" {
			id := "barrio:" + strings.ToLower(d)
			label[s.ID] = id
			if _, ok := found[id]; !ok {
				found[id] = Barrio{ID: id, Title: d, Kind: BarrioDeclared}
			}
			continue
		}
		if len(weight[s.ID]) > 0 {
			label[s.ID] = "auto:" + s.ID
			free = append(free, s.ID) // systems are sorted, so this is too
		}
	}
	for round := 0; round < 10; round++ {
		changed := false
		for _, id := range free {
			score := map[string]float64{}
			for other, w := range weight[id] {
				if l := label[other]; l != "" {
					score[l] += w
				}
			}
			best, top := label[id], score[label[id]]
			for l, w := range score {
				if w > top || (w == top && l < best) {
					best, top = l, w
				}
			}
			if best != label[id] {
				label[id], changed = best, true
			}
		}
		if !changed {
			break
		}
	}
	// A found barrio is named after its biggest app.
	members := map[string][]string{}
	for _, id := range free {
		members[label[id]] = append(members[label[id]], id)
	}
	for l, ids := range members {
		if !strings.HasPrefix(l, "auto:") {
			continue
		}
		name := ids[0]
		for _, id := range ids[1:] {
			if size[id] > size[name] {
				name = id
			}
		}
		id := "auto:" + name
		for _, m := range ids {
			label[m] = id
		}
		found[id] = Barrio{ID: id, Title: name, Kind: BarrioAuto}
	}

	centre, outskirts := false, false
	for i := range systems {
		s := &systems[i]
		switch {
		case !isApp[s.ID]:
			s.Barrio, outskirts = BarrioOutskirts, true
		case label[s.ID] != "":
			s.Barrio = label[s.ID]
		default:
			s.Barrio, centre = BarrioCentre, true
		}
	}
	out := make([]Barrio, 0, len(found)+2)
	for _, b := range found {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == BarrioDeclared
		}
		return out[i].Title < out[j].Title
	})
	if centre {
		out = append(out, Barrio{ID: BarrioCentre, Title: "Centre", Kind: BarrioCentre})
	}
	if outskirts {
		out = append(out, Barrio{ID: BarrioOutskirts, Title: "Outskirts", Kind: BarrioOutskirts})
	}
	return out
}

// traced adds the numbers measured from traces to the edges they belong
// to, and the calls only traces know about (to a service nobody declared,
// or outside the cluster) as edges of their own.
func (b *builder) traced(calls map[[2]string]EdgeMetrics) {
	for k, m := range calls {
		from := b.resolve(k[0], "")
		if from == "" {
			continue
		}
		system := ""
		if n := b.nodes[from]; n != nil {
			system = n.System
		}
		to := b.resolve(k[1], system)
		if to == "" {
			to = "outside/" + k[1]
			b.system(System{ID: Outside, Title: "Outside the cluster", Kind: SystemOther})
			b.node(to, k[1], Outside).Kind = "external"
		}
		if from == to {
			continue
		}
		m := m
		b.edge(from, to, SourceTraced, "").Metrics = &m
	}
}

// resolve finds the node a collector name stands for: "app/service" (what
// observe names services), "user" (a call from outside, the internet), a
// cluster address (api.wellness.svc.cluster.local:8000, api.wellness), a
// short name in the caller's system (api:8000), or a database the
// caller's app runs (postgresql, redis). It returns "" when none matches.
func (b *builder) resolve(name, callerSystem string) string {
	if name == "user" {
		b.nodes[Internet] = &Node{ID: Internet, Name: "Internet", Kind: Internet}
		return Internet
	}
	if b.nodes[name] != nil {
		return name
	}
	host := name
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host, _, _ = strings.Cut(host, "/")
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimSuffix(host, "."), ".cluster.local")
	host = strings.TrimSuffix(host, ".svc")
	if svc, ns, ok := strings.Cut(host, "."); ok && !strings.Contains(ns, ".") {
		if id := ns + "/" + svc; b.nodes[id] != nil {
			return id
		}
	}
	if callerSystem == "" {
		return ""
	}
	db := map[string]string{"postgresql": "postgres", "postgres": "postgres", "redis": "redis"}[host]
	for _, short := range []string{host, db} {
		if id := callerSystem + "/" + short; short != "" && b.nodes[id] != nil {
			return id
		}
	}
	return ""
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
