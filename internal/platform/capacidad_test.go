package platform

import (
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// What the Environment page sets wins over the environment's values, up to
// MaxWorkers; a value it leaves at zero keeps the environment's.
func TestLimitsFollowTheEnvironmentPage(t *testing.T) {
	h := &CapacityHolder{}
	p := &Platform{Workers: 3, PerApp: 2, Capacity: h.Runs}
	if l := p.limits(); l.workers != 3 || l.perApp != 2 {
		t.Fatalf("unset: %+v", l)
	}
	h.Set(store.Capacity{ParallelRuns: 20, RunsPerApp: 1})
	if l := p.limits(); l.workers != MaxWorkers || l.perApp != 1 {
		t.Fatalf("set: %+v", l)
	}
	h.Set(store.Capacity{RunsPerApp: 3})
	if l := p.limits(); l.workers != 3 || l.perApp != 3 {
		t.Fatalf("only per app: %+v", l)
	}
	if nodes, cpu := h.Limits(); len(nodes) != 0 || cpu != 0 {
		t.Fatalf("limits %v %d", nodes, cpu)
	}
}

// A node reserved or used at the limit or more is busy; when every node is,
// the least full one still takes work; at 100 nothing is ever busy.
func TestBusyNodes(t *testing.T) {
	loads := []NodeLoad{{Name: "colorpi", Reserved: 96, Used: 60}, {Name: "worker1", Reserved: 50, Used: 90}, {Name: "worker2", Reserved: 48, Used: 15}, {Name: "worker3", Reserved: 50, Used: -1}}
	if got := BusyNodes(loads, 0); len(got) != 2 || got[0] != "colorpi" || got[1] != "worker1" {
		t.Fatalf("busy at 85: %v", got)
	}
	if got := BusyNodes(loads, 95); len(got) != 1 || got[0] != "colorpi" {
		t.Fatalf("busy at 95: %v", got)
	}
	if got := BusyNodes(loads, 100); len(got) != 0 {
		t.Fatalf("busy at 100: %v", got)
	}
	all := []NodeLoad{{Name: "a", Reserved: 90}, {Name: "b", Reserved: 99}, {Name: "c", Used: 87}}
	if got := BusyNodes(all, 85); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("all busy: %v, want c left free", got)
	}
	h := &CapacityHolder{}
	h.Set(store.Capacity{NodesOff: []string{"colorpi"}})
	busy := []string{"colorpi", "worker1"}
	h.busy.Store(&busy)
	if off, _ := h.Limits(); len(off) != 2 || off[0] != "colorpi" || off[1] != "worker1" {
		t.Fatalf("limits %v", off)
	}
}
