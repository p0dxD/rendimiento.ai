package platform

import (
	"context"
	"sync"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"

	. "github.com/p0dxD/rendimiento.ai/internal/i18n" //nolint:revive // M marks messages for people
)

// Deploy tasks run against a release, before or after it goes live, in its
// destination (see destino.Destino.Tarea): there they reach the app's
// services and databases, and a task with service: runs in that service's
// image with its environment, which is what a migration needs.

// stageTasks are the release's tasks of one stage (pre- or post-deploy).
func stageTasks(rel *store.Release, stage string) []spec.Task {
	var out []spec.Task
	for _, t := range rel.Spec.Tasks {
		if t.Stage == stage {
			out = append(out, t)
		}
	}
	return out
}

// postDeployTasks are the release's post-deploy tasks.
func postDeployTasks(rel *store.Release) []spec.Task { return stageTasks(rel, spec.StagePostDeploy) }

// runPostDeploy runs a release's post-deploy tasks (see runStage).
func (p *Platform) runPostDeploy(ctx context.Context, app *store.App, rel *store.Release) []store.ReleaseTask {
	return p.runStage(ctx, app, rel, spec.StagePostDeploy)
}

// runStage runs a release's tasks of one stage, each once its after: tasks
// succeeded (a task whose dependency failed is skipped), and records each
// one's state as it goes.
func (p *Platform) runStage(ctx context.Context, app *store.App, rel *store.Release, stage string) []store.ReleaseTask {
	tasks := stageTasks(rel, stage)
	run := p.Verify.RunTask
	if run == nil {
		run = p.Target().Tarea
	}
	results := map[string]store.ReleaseTask{}
	done := map[string]bool{}
	for len(done) < len(tasks) && ctx.Err() == nil {
		// The tasks whose dependencies have all finished run together.
		var ready []spec.Task
		for _, t := range tasks {
			if done[t.Name] {
				continue
			}
			waiting := false
			for _, a := range t.After {
				waiting = waiting || !done[a]
			}
			if !waiting {
				ready = append(ready, t)
			}
		}
		if len(ready) == 0 {
			break // a cycle; validation prevents it
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, t := range ready {
			blocked := ""
			for _, a := range t.After {
				if results[a].Status != store.TaskSucceeded {
					blocked = a
				}
			}
			if blocked != "" {
				r := store.ReleaseTask{ReleaseID: rel.ID, Name: t.Name, Stage: stage, Optional: t.Optional, Status: store.TaskSkipped, Message: M("%s did not succeed", blocked)}
				p.saveTask(r)
				results[t.Name], done[t.Name] = r, true
				continue
			}
			wg.Add(1)
			go func(t spec.Task) {
				defer wg.Done()
				now := time.Now()
				p.saveTask(store.ReleaseTask{ReleaseID: rel.ID, Name: t.Name, Stage: stage, Optional: t.Optional, Status: store.TaskRunning, StartedAt: &now})
				r := run(ctx, app, rel, t)
				r.ReleaseID, r.Name, r.Stage, r.Optional, r.StartedAt = rel.ID, t.Name, stage, t.Optional, &now
				if r.FinishedAt == nil {
					end := time.Now()
					r.FinishedAt = &end
				}
				p.saveTask(r)
				mu.Lock()
				results[t.Name] = r
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		for _, t := range ready {
			done[t.Name] = true
		}
	}
	out := make([]store.ReleaseTask, 0, len(results))
	for _, t := range tasks {
		if r, ok := results[t.Name]; ok {
			out = append(out, r)
		}
	}
	return out
}

// saveTask records a deploy task's state; a failure to record is only
// logged.
func (p *Platform) saveTask(t store.ReleaseTask) {
	if err := p.Store.SaveReleaseTask(context.Background(), t); err != nil {
		p.Log.Warn("could not record a post-deploy task", "task", t.Name, "err", err)
	}
}

// judgeTasks folds post-deploy results into a verification: a failed
// required task fails the release; a failed optional one is a warning.
func judgeTasks(results []store.ReleaseTask) (failures, warnings []string) {
	for _, r := range results {
		if r.Status == store.TaskSucceeded {
			continue
		}
		stage := r.Stage
		if stage == "" {
			stage = spec.StagePostDeploy
		}
		line := M("%s task %s %s", stage, r.Name, r.Status)
		if r.Message != "" {
			line = M("%s task %s %s: %s", stage, r.Name, r.Status, r.Message)
		}
		if r.Optional {
			warnings = append(warnings, M("%s (optional)", line))
		} else {
			failures = append(failures, line)
		}
	}
	return failures, warnings
}
