package platform

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// CapacityHolder keeps the capacity set on the Environment page, read again
// every 15 seconds, so every process (the workers, the control service)
// follows a change within that time without a restart.
type CapacityHolder struct {
	Store interface {
		GetCapacity(ctx context.Context) (store.Capacity, error)
	}
	v atomic.Pointer[store.Capacity]
}

// Run reads the capacity now and every 15 seconds until ctx ends.
func (h *CapacityHolder) Run(ctx context.Context) {
	for {
		if c, err := h.Store.GetCapacity(ctx); err == nil {
			h.Set(c)
		}
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

// Runs is how many runs go at once and per app (zero: the environment's).
func (h *CapacityHolder) Runs() (workers, perApp int) {
	c := h.Get()
	return c.ParallelRuns, c.RunsPerApp
}

// Limits are the nodes that take no runs and the most CPU a step asks for.
func (h *CapacityHolder) Limits() ([]string, int64) {
	c := h.Get()
	return c.NodesOff, c.StepCPUMillis
}
