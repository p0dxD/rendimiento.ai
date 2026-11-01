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
