package pipeline

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// StepResult is what an executor reports for one step.
type StepResult struct {
	Status   Status
	Digest   string // sha256:... for successful build steps
	Message  string
	Started  time.Time
	Finished time.Time
}

// Executor runs a single step to completion, writing its logs to w.
type Executor interface {
	Execute(ctx context.Context, runID string, src Source, step Step, w io.Writer) StepResult
}

// Recorder persists step progress and logs (Postgres + SSE fan-out in production).
type Recorder interface {
	StepUpdate(runID string, stepID string, r StepResult)
	StepLog(runID, stepID string) io.WriteCloser
}

// --8<-- [start:runner]

// Runner executes run DAGs with a global cap on concurrent steps, because
// builds on Raspberry Pi nodes are CPU and memory bound.
type Runner struct {
	Exec     Executor
	Recorder Recorder
	sem      chan struct{}
}

// NewRunner returns a Runner that runs at most maxParallel steps at a time across all runs.
func NewRunner(exec Executor, rec Recorder, maxParallel int) *Runner {
	if maxParallel < 1 {
		maxParallel = 1
	}
	return &Runner{Exec: exec, Recorder: rec, sem: make(chan struct{}, maxParallel)}
}

// --8<-- [end:runner]

// Run executes steps respecting DependsOn and returns each step's result.
// A failed step marks everything that depends on it as skipped, and stops
// the rest of the run (unless it is optional): its other steps are skipped
// or cancelled, since the run has failed anyway (a build beside a failed
// test would only hold up the next run).
func (r *Runner) Run(parent context.Context, runID string, src Source, steps []Step) (map[string]StepResult, error) {
	if err := validate(steps); err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(parent)
	defer stop()
	var (
		mu        sync.Mutex
		results   = map[string]StepResult{}
		done      = map[string]chan struct{}{}
		wg        sync.WaitGroup
		stoppedBy string // the step whose failure stopped the run
	)
	// cancelled is the result of a step the run did not let finish.
	cancelled := func() StepResult {
		mu.Lock()
		defer mu.Unlock()
		if stoppedBy != "" && parent.Err() == nil {
			return StepResult{Status: StatusSkipped, Message: fmt.Sprintf("stopped: %s failed", stoppedBy)}
		}
		return StepResult{Status: StatusSkipped, Message: "run cancelled"}
	}
	for _, s := range steps {
		done[s.ID] = make(chan struct{})
	}
	for _, s := range steps {
		wg.Add(1)
		go func(s Step) {
			defer wg.Done()
			defer close(done[s.ID])
			for _, dep := range s.DependsOn {
				select {
				case <-done[dep]:
				case <-ctx.Done():
				}
			}
			mu.Lock()
			blocked := ""
			for _, dep := range s.DependsOn {
				if results[dep].Status != StatusSucceeded {
					blocked = dep
				}
			}
			mu.Unlock()

			var res StepResult
			switch {
			case ctx.Err() != nil:
				res = cancelled()
			case blocked != "":
				res = StepResult{Status: StatusSkipped, Message: blocked + " did not succeed"}
			default:
				select {
				case r.sem <- struct{}{}:
					r.Recorder.StepUpdate(runID, s.ID, StepResult{Status: StatusRunning, Started: time.Now()})
					w := r.Recorder.StepLog(runID, s.ID)
					res = r.Exec.Execute(ctx, runID, src, s, w)
					w.Close()
					<-r.sem
					if res.Status != StatusSucceeded && ctx.Err() != nil {
						// Stopped part way: say why, not how it ended.
						started := res.Started
						res = cancelled()
						res.Started, res.Finished = started, time.Now()
					}
				case <-ctx.Done():
					res = cancelled()
				}
			}
			r.Recorder.StepUpdate(runID, s.ID, res)
			mu.Lock()
			results[s.ID] = res
			if res.Status == StatusFailed && !s.Optional && stoppedBy == "" {
				stoppedBy = s.ID
				stop()
			}
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return results, nil
}

// validate rejects unknown dependencies and cycles.
func validate(steps []Step) error {
	byID := map[string]Step{}
	for _, s := range steps {
		if _, dup := byID[s.ID]; dup {
			return fmt.Errorf("duplicate step %s", s.ID)
		}
		byID[s.ID] = s
	}
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("dependency cycle at %s", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, d := range byID[id].DependsOn {
			if _, ok := byID[d]; !ok {
				return fmt.Errorf("step %s depends on unknown step %s", id, d)
			}
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, s := range steps {
		if err := visit(s.ID); err != nil {
			return err
		}
	}
	return nil
}
