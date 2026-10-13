package universe

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/catalog"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

func TestBuild(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	wellness := &store.App{ID: 1, Name: "wellness", Repo: "o/wellness", Spec: spec.Spec{
		Services: []spec.Service{
			{Name: "ui", Language: "node", Needs: []spec.Need{{Kind: spec.NeedService, Service: "api"}}},
			{Name: "api", Language: "python", Needs: []spec.Need{{Kind: spec.NeedPostgres}, {Kind: spec.NeedService, Service: "ai/ollama"}}},
		},
		Jobs: []spec.Job{{Name: "nightly"}},
	}}
	cat := &catalog.Catalog{Entries: []catalog.Entry{
		{ID: "wellness/ui", Name: "ui", Namespace: "wellness", Group: catalog.GroupApps, Origin: catalog.Origin{Kind: "rendimiento", App: "wellness"},
			Public: []string{"https://wellness.example.com"}, Pods: 2, Ready: 2},
		{ID: "wellness/api", Name: "api", Namespace: "wellness", Group: catalog.GroupApps, Origin: catalog.Origin{Kind: "rendimiento", App: "wellness"},
			Category: "web", UsedBy: []catalog.Consumer{{Namespace: "wellness", Workload: "ui", Kind: "Deployment", Env: "API_URL", App: "wellness"}}},
		{ID: "wellness/postgres", Name: "postgres", Namespace: "wellness", Group: catalog.GroupApps, Origin: catalog.Origin{Kind: "rendimiento", App: "wellness"}, Category: "database"},
		// Deployed another way, called by a cron job of the app.
		{ID: "umami/umami", Name: "umami", Namespace: "umami", Group: catalog.GroupOther, Origin: catalog.Origin{Kind: "helm"},
			UsedBy: []catalog.Consumer{{Namespace: "wellness", Workload: "nightly", Kind: "CronJob", Env: "UMAMI_URL", App: "wellness"}}},
		// Plumbing nobody calls stays off the map; plumbing an app calls stays.
		{ID: "ingress-nginx/controller", Name: "controller", Namespace: "ingress-nginx", Group: catalog.GroupInfra, Origin: catalog.Origin{Kind: "helm"}},
		{ID: "monitoring/vmsingle", Name: "vmsingle", Namespace: "monitoring", Group: catalog.GroupInfra, Origin: catalog.Origin{Kind: "helm"},
			UsedBy: []catalog.Consumer{{Namespace: "wellness", Workload: "api", Kind: "Deployment", Env: "METRICS_URL", App: "wellness"}}},
	}}
	probes := []store.Probe{
		{AppID: 1, Service: "ui", Kind: "internal", At: now.Add(-time.Minute), OK: true, LatencyMS: 20},
		{AppID: 1, Service: "ui", Kind: "public", At: now.Add(-time.Minute), OK: false, Error: "HTTP 502"},
		{AppID: 1, Service: "api", Kind: "internal", At: now.Add(-2 * time.Minute), OK: true, LatencyMS: 35},
		{AppID: 9, Service: "gone", Kind: "internal", At: now, OK: true}, // an app that no longer exists
	}

	u := Build(Input{Apps: []*store.App{wellness}, Catalog: cat, Probes: probes, Now: now})

	nodes := map[string]Node{}
	for _, n := range u.Nodes {
		nodes[n.ID] = n
	}
	want := []string{"ai/ollama", "internet", "monitoring/vmsingle", "umami/umami", "wellness/api", "wellness/nightly", "wellness/postgres", "wellness/ui"}
	if len(u.Nodes) != len(want) {
		t.Fatalf("nodes = %v", ids(u.Nodes))
	}
	for i, id := range want {
		if u.Nodes[i].ID != id {
			t.Fatalf("nodes = %v, want %v", ids(u.Nodes), want)
		}
	}
	if n := nodes["wellness/ui"]; n.Kind != "service" || n.Language != "node" || n.Pods != 2 || !n.Catalog || n.Health == nil || n.Health.Up || n.Health.InsideMS != 20 || n.Health.Error != "HTTP 502" {
		t.Fatalf("ui = %+v health %+v", n, n.Health)
	}
	if n := nodes["wellness/api"]; n.Health == nil || !n.Health.Up || n.Health.InsideMS != 35 || n.Category != "web" {
		t.Fatalf("api = %+v health %+v", n, n.Health)
	}
	if n := nodes["wellness/postgres"]; n.Kind != "postgres" || n.System != "wellness" {
		t.Fatalf("postgres = %+v", n)
	}
	if n := nodes["wellness/nightly"]; n.Kind != "workload" {
		t.Fatalf("nightly = %+v", n)
	}
	// A declared need on something the catalog does not list still lands.
	if n := nodes["ai/ollama"]; n.System != "ai" || n.Catalog {
		t.Fatalf("ollama = %+v", n)
	}

	edges := map[[2]string]Edge{}
	for _, e := range u.Edges {
		edges[[2]string{e.From, e.To}] = e
	}
	if e := edges[[2]string{"wellness/ui", "wellness/api"}]; len(e.Sources) != 2 || e.Sources[0] != SourceDeclared || e.Sources[1] != SourceEnv || e.Env != "API_URL" {
		t.Fatalf("ui → api = %+v", e)
	}
	if e := edges[[2]string{"wellness/api", "wellness/postgres"}]; len(e.Sources) != 1 || e.Env != "DATABASE_URL" {
		t.Fatalf("api → postgres = %+v", e)
	}
	if e := edges[[2]string{Internet, "wellness/ui"}]; len(e.Hosts) != 1 || e.Sources[0] != SourceIngress {
		t.Fatalf("internet → ui = %+v", e)
	}
	if e := edges[[2]string{"wellness/nightly", "umami/umami"}]; e.Env != "UMAMI_URL" {
		t.Fatalf("nightly → umami = %+v", e)
	}
	if len(u.Edges) != 6 {
		t.Fatalf("edges = %+v", u.Edges)
	}

	kinds := map[string]string{}
	for _, s := range u.Systems {
		kinds[s.ID] = s.Kind
	}
	if kinds["wellness"] != SystemApp || kinds["umami"] != SystemOther || kinds["monitoring"] != SystemInfra || kinds["ingress-nginx"] != "" || len(u.Systems) != 4 {
		t.Fatalf("systems = %+v", u.Systems)
	}

	// Same input, same JSON.
	a, _ := json.Marshal(u)
	b, _ := json.Marshal(Build(Input{Apps: []*store.App{wellness}, Catalog: cat, Probes: probes, Now: now}))
	if string(a) != string(b) {
		t.Fatal("Build is not deterministic")
	}
}

func TestBuildWithoutCatalog(t *testing.T) {
	a := &store.App{ID: 1, Name: "hello", Spec: spec.Spec{Services: []spec.Service{{Name: "web", Language: "go"}}}}
	u := Build(Input{Apps: []*store.App{a}})
	if len(u.Nodes) != 1 || u.Nodes[0].ID != "hello/web" || len(u.Edges) != 0 || len(u.Systems) != 1 {
		t.Fatalf("universe = %+v", u)
	}
}

// ids lists node IDs, for failure messages.
func ids(ns []Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}
