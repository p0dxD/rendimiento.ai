// Package environment inspects the cluster and integrations rendimiento
// depends on and reports what is installed, what is missing and how to fix
// it, plus node and workload health. Providers (cluster, DNS, source,
// registry) are described through interfaces so they can be swapped.
package environment

import (
	"context"
	"sort"
	"sync"
	"time"

	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/internal/dns"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

// Status is the state of one check or provider.
type Status string

const (
	OK      Status = "ok"
	Warning Status = "warning" // works, but something needs attention
	Missing Status = "missing" // not installed / not configured
	Error   Status = "error"   // installed but broken
)

// rank orders statuses from best to worst for roll-ups.
func (s Status) rank() int {
	switch s {
	case OK:
		return 0
	case Warning:
		return 1
	case Missing:
		return 2
	}
	return 3
}

func worst(a, b Status) Status {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// Category groups checks on the Environment page.
type Category string

const (
	CatCluster      Category = "Cluster"
	CatNetworking   Category = "Networking"
	CatTLS          Category = "TLS"
	CatBuild        Category = "Build"
	CatIntegrations Category = "Integrations"
)

// Check is one requirement and its state.
type Check struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category Category `json:"category"`
	// Required checks block deploys when not OK; optional ones only warn.
	Required bool     `json:"required"`
	Status   Status   `json:"status"`
	Summary  string   `json:"summary"`
	Details  []string `json:"details,omitempty"`
	Version  string   `json:"version,omitempty"`
	// Fix explains how to resolve a non-OK status (install command, setting).
	Fix string `json:"fix,omitempty"`
}

// Option is one choice for a provider slot; unavailable ones are on the roadmap.
type Option struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// Provider is one swappable slot: where apps run, who serves DNS, etc.
type Provider struct {
	Slot    string   `json:"slot"` // cluster | dns | source | registry
	Title   string   `json:"title"`
	Active  string   `json:"active"` // option id in use
	Name    string   `json:"name"`   // display name of what is in use
	Status  Status   `json:"status"`
	Detail  string   `json:"detail"`
	Options []Option `json:"options"`
}

// Node is one cluster node with its capacity, usage and health.
type Node struct {
	Name          string   `json:"name"`
	Roles         []string `json:"roles"`
	Ready         bool     `json:"ready"`
	Arch          string   `json:"arch"`
	OS            string   `json:"os"`
	Kubelet       string   `json:"kubelet"`
	Pressure      []string `json:"pressure,omitempty"` // MemoryPressure, DiskPressure, PIDPressure
	Pods          int      `json:"pods"`
	CPUCores      float64  `json:"cpuCores"`
	CPUUsed       *float64 `json:"cpuUsed,omitempty"` // cores; nil without metrics-server
	MemBytes      int64    `json:"memBytes"`
	MemUsed       *int64   `json:"memUsed,omitempty"`
	Unschedulable bool     `json:"unschedulable,omitempty"`
	GPUs          int64    `json:"gpus,omitempty"` // nvidia.com/gpu capacity
}

// Problem is an unhealthy workload or certificate somewhere in the cluster.
type Problem struct {
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason"`
	Since     time.Time `json:"since,omitempty"`
}

// ClusterInfo summarizes the cluster (version, node and pod counts).
type ClusterInfo struct {
	Distribution string `json:"distribution"` // k3s, eks, gke, ...
	Name         string `json:"name"`         // display name
	Version      string `json:"version"`
	Nodes        int    `json:"nodes"`
	NodesReady   int    `json:"nodesReady"`
	Pods         int    `json:"pods"`
	Namespaces   int    `json:"namespaces"`
}

// Report is everything the Environment page shows.
type Report struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	Overall     Status      `json:"overall"`
	Summary     string      `json:"summary"`
	Cluster     ClusterInfo `json:"cluster"`
	Providers   []Provider  `json:"providers"`
	Checks      []Check     `json:"checks"`
	Nodes       []Node      `json:"nodes"`
	Problems    []Problem   `json:"problems"`
	// DDNS is set when the DNS provider follows the public IP.
	DDNS *dns.DDNSStatus `json:"ddns,omitempty"`
}

// ddnsReporter is implemented by DNS providers that support dynamic DNS.
type ddnsReporter interface{ DDNSStatus() dns.DDNSStatus }

// Invalidate drops the cached report, e.g. after a DNS sync.
func (c *Checker) Invalidate() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// Config is what the platform expects from the environment.
type Config struct {
	IngressClass     string
	ClusterIssuer    string
	StorageClass     string
	BuildkitAddr     string // tcp://host:port
	BuildkitPool     string // headless Service listing the pool's daemons
	Registry         string // host:port
	RegistryInsecure bool
	BuildNamespace   string
	PlatformURL      string
	ExcludeNodes     []string // nodes build pods avoid
	// NotifyTo are the notification email recipients; NotifyReady is true
	// when they and an email provider are configured.
	NotifyTo    []string
	NotifyReady bool
	// LogArchive describes the step log archive (nil: not configured).
	LogArchive func(ctx context.Context) (ok bool, summary string)
}

// Pinger is satisfied by the store.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Checker builds environment reports and caches them for TTL.
type Checker struct {
	Kube      client.Client
	Discovery discovery.DiscoveryInterface
	DNS       dns.Provider
	GitHub    *gh.Holder
	DB        Pinger
	Config    Config
	// TTL caches reports; checks hit several APIs and the network.
	TTL time.Duration

	mu     sync.Mutex
	cached *Report
}

// Report returns a fresh or cached environment report.
func (c *Checker) Report(ctx context.Context, refresh bool) *Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.TTL
	if ttl == 0 {
		ttl = 15 * time.Second
	}
	if !refresh && c.cached != nil && time.Since(c.cached.GeneratedAt) < ttl {
		return c.cached
	}
	r := c.build(ctx)
	c.cached = r
	return r
}

func (c *Checker) build(ctx context.Context) *Report {
	r := &Report{GeneratedAt: time.Now()}
	snap := c.snapshot(ctx)
	r.Cluster = snap.cluster
	r.Nodes = snap.nodes
	r.Problems = snap.problems

	// Checks are independent and several touch the network: run them concurrently,
	// each with its own deadline so one hung dependency cannot stall the page.
	checks := c.checks(snap)
	results := make([]Check, len(checks))
	var wg sync.WaitGroup
	for i, fn := range checks {
		wg.Add(1)
		go func(i int, fn func(context.Context) Check) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			results[i] = fn(cctx)
		}(i, fn)
	}
	wg.Wait()
	r.Checks = results
	r.Problems = append(r.Problems, snap.certProblems...)
	sort.SliceStable(r.Problems, func(i, j int) bool {
		if r.Problems[i].Namespace != r.Problems[j].Namespace {
			return r.Problems[i].Namespace < r.Problems[j].Namespace
		}
		return r.Problems[i].Name < r.Problems[j].Name
	})
	r.Providers = c.providers(ctx, snap, results)
	if d, ok := c.DNS.(ddnsReporter); ok {
		if st := d.DDNSStatus(); st.Enabled {
			r.DDNS = &st
		}
	}

	r.Overall = OK
	blocking, warnings := 0, 0
	for _, ch := range results {
		switch {
		case ch.Status == OK:
		case ch.Required:
			blocking++
			r.Overall = worst(r.Overall, ch.Status)
		default:
			warnings++
			r.Overall = worst(r.Overall, Warning)
		}
	}
	switch {
	case blocking > 0:
		r.Summary = plural(blocking, "required component needs", "required components need") + " attention before apps can deploy reliably"
	case warnings > 0 || len(r.Problems) > 0:
		r.Overall = worst(r.Overall, Warning)
		r.Summary = "Ready to deploy"
		if warnings > 0 {
			r.Summary += "; " + plural(warnings, "optional item", "optional items") + " to review"
		}
		if len(r.Problems) > 0 {
			r.Summary += "; " + plural(len(r.Problems), "unhealthy workload or certificate", "unhealthy workloads or certificates") + " in the cluster"
		}
	default:
		r.Summary = "Everything rendimiento needs is installed and healthy"
	}
	return r
}
