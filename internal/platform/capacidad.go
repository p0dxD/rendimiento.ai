package platform

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// CapacityHolder keeps the capacity set on the Environment page, read again
// every 15 seconds, so every process (the workers, the control service)
// follows a change within that time without a restart. With Kube, it also
// measures which nodes are busy then, and keeps steps and builds off them.
type CapacityHolder struct {
	Store interface {
		GetCapacity(ctx context.Context) (store.Capacity, error)
	}
	Kube kubernetes.Interface
	// Exclude are the nodes BUILD_EXCLUDE_NODES keeps off, never candidates.
	Exclude []string
	v       atomic.Pointer[store.Capacity]
	busy    atomic.Pointer[[]string]
}

// DefaultBusyPercent is how full a node may be before it takes nothing new.
const DefaultBusyPercent = 85

// Run reads the capacity now and every 15 seconds until ctx ends, and
// measures the nodes each time.
func (h *CapacityHolder) Run(ctx context.Context) {
	for {
		if c, err := h.Store.GetCapacity(ctx); err == nil {
			h.Set(c)
		}
		h.measure(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// Set is a capacity just saved, followed at once in this process.
func (h *CapacityHolder) Set(c store.Capacity) { h.v.Store(&c) }

// Get is the capacity now (zero values before it was read).
func (h *CapacityHolder) Get() store.Capacity {
	if c := h.v.Load(); c != nil {
		return *c
	}
	return store.Capacity{}
}

// Busy are the nodes that were too full at the last measure.
func (h *CapacityHolder) Busy() []string {
	if b := h.busy.Load(); b != nil {
		return *b
	}
	return []string{}
}

// Runs is how many runs go at once and per app (zero: the environment's).
func (h *CapacityHolder) Runs() (workers, perApp int) {
	c := h.Get()
	return c.ParallelRuns, c.RunsPerApp
}

// Limits are the nodes that take nothing new (turned off, or busy now) and
// the most CPU a step asks for.
func (h *CapacityHolder) Limits() ([]string, int64) {
	c := h.Get()
	off := append([]string{}, c.NodesOff...)
	for _, n := range h.Busy() {
		if !slices.Contains(off, n) {
			off = append(off, n)
		}
	}
	return off, c.StepCPUMillis
}

// NodeLoad is how full a node is: its CPU reserved by its pods' requests,
// and in use (-1 when the metrics server does not say), in percent.
type NodeLoad struct {
	Name     string
	Reserved int
	Used     int
}

// BusyNodes are the loads at pct or more, reserved or used. When every
// node is that full, the least full one is left out, so steps still go
// somewhere.
func BusyNodes(loads []NodeLoad, pct int) []string {
	if pct <= 0 {
		pct = DefaultBusyPercent
	}
	busy := []string{}
	if pct >= 100 || len(loads) == 0 {
		return busy
	}
	full := func(l NodeLoad) int { return max(l.Reserved, l.Used) }
	for _, l := range loads {
		if full(l) >= pct {
			busy = append(busy, l.Name)
		}
	}
	if len(busy) == len(loads) {
		least := slices.MinFunc(loads, func(a, b NodeLoad) int { return full(a) - full(b) })
		busy = slices.DeleteFunc(busy, func(n string) bool { return n == least.Name })
	}
	sort.Strings(busy)
	return busy
}

// measure finds the busy nodes among those that take runs: not tainted
// against pods (the control plane), not cordoned, not turned off.
func (h *CapacityHolder) measure(ctx context.Context) {
	if h.Kube == nil {
		return
	}
	c := h.Get()
	nodes, err := h.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	pods, err := h.Kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "status.phase!=Succeeded,status.phase!=Failed"})
	if err != nil {
		return
	}
	reserved := map[string]int64{}
	for _, p := range pods.Items {
		for _, ct := range p.Spec.Containers {
			reserved[p.Spec.NodeName] += ct.Resources.Requests.Cpu().MilliValue()
		}
	}
	used := h.nodeUse(ctx)
	var loads []NodeLoad
	for _, n := range nodes.Items {
		if n.Spec.Unschedulable || slices.Contains(c.NodesOff, n.Name) || slices.Contains(h.Exclude, n.Name) ||
			slices.ContainsFunc(n.Spec.Taints, func(t corev1.Taint) bool { return t.Effect == corev1.TaintEffectNoSchedule }) {
			continue
		}
		alloc := n.Status.Allocatable.Cpu().MilliValue()
		if alloc <= 0 {
			continue
		}
		l := NodeLoad{Name: n.Name, Reserved: int(reserved[n.Name] * 100 / alloc), Used: -1}
		if u, ok := used[n.Name]; ok {
			l.Used = int(u * 100 / alloc)
		}
		loads = append(loads, l)
	}
	busy := BusyNodes(loads, c.BusyPercent)
	h.busy.Store(&busy)
}

// nodeUse is each node's CPU in use (thousandths of a core), from the
// metrics server; empty without it.
func (h *CapacityHolder) nodeUse(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	raw, err := h.Kube.CoreV1().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").DoRaw(ctx)
	if err != nil {
		return out
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Usage struct {
				CPU string `json:"cpu"`
			} `json:"usage"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return out
	}
	for _, it := range list.Items {
		if q, err := resource.ParseQuantity(it.Usage.CPU); err == nil {
			out[it.Metadata.Name] = q.MilliValue()
		}
	}
	return out
}
