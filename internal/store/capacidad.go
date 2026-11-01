package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Capacity is how much the platform runs at once, set on the Environment
// page. A zero number keeps the environment variable's value.
type Capacity struct {
	// ParallelRuns is how many runs go at once (PARALLEL_RUNS), and
	// RunsPerApp how many of them may be of one app (RUNS_PER_APP).
	ParallelRuns int `json:"parallelRuns"`
	RunsPerApp   int `json:"runsPerApp"`
	// StepCPUMillis is the most CPU a step's pod asks for, in thousandths
	// of a core (0: what each app asks); a step may still use free cores.
	StepCPUMillis int64 `json:"stepCpuMillis"`
	// NodesOff are nodes that take no runs (as BUILD_EXCLUDE_NODES).
	NodesOff  []string  `json:"nodesOff"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// GetCapacity is the capacity set on the Environment page (zero values
// when nothing was set).
func (s *Store) GetCapacity(ctx context.Context) (Capacity, error) {
	var c Capacity
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value, updated_by, updated_at FROM platform_settings WHERE key = 'capacity'`).Scan(&raw, &c.UpdatedBy, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Capacity{NodesOff: []string{}}, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	if c.NodesOff == nil {
		c.NodesOff = []string{}
	}
	return c, err
}

// SaveCapacity keeps the capacity set on the Environment page.
func (s *Store) SaveCapacity(ctx context.Context, c Capacity, by string) error {
	if c.NodesOff == nil {
		c.NodesOff = []string{}
	}
	raw, err := json.Marshal(Capacity{ParallelRuns: c.ParallelRuns, RunsPerApp: c.RunsPerApp, StepCPUMillis: c.StepCPUMillis, NodesOff: c.NodesOff})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO platform_settings (key, value, updated_by, updated_at) VALUES ('capacity', $1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = $1, updated_by = $2, updated_at = now()`, raw, by)
	return err
}
