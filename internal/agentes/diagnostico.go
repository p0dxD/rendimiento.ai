// Package agentes holds the agents that work inside the platform (ROLE=agents),
// woken by what happens in it rather than by a person's session. Each asks
// the organization's model through the AI gateway, within its caps.
package agentes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"github.com/p0dxD/rendimiento.ai/internal/ruta"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Store is what the diagnostician reads and keeps (the platform's store).
type Store interface {
	RunsToDiagnose(ctx context.Context, since time.Time, limit int) ([]int64, error)
	GetRun(ctx context.Context, id int64) (*store.Run, error)
	GetAppByID(ctx context.Context, id int64) (*store.App, error)
	GetDiagnosis(ctx context.Context, runID int64) (store.Diagnosis, error)
	SaveDiagnosis(ctx context.Context, d store.Diagnosis) error
	LastGoodSHA(ctx context.Context, appID int64, branch string, before int64) (string, error)
}

// Model asks the organization's model (the AI gateway).
type Model interface {
	Complete(ctx context.Context, orgID int64, source string, req ia.Request) (ia.Response, error)
}

// Changes lists the files that changed between two commits (GitHub).
type Changes interface {
	ChangedFiles(ctx context.Context, installation int64, repo, base, head string) ([]string, bool, error)
}

// Diagnostician explains failed runs: why each failed and how to fix it.
type Diagnostician struct {
	Store Store
	Model Model
	// Logs is a step's whole log (from the archive too).
	Logs    func(ctx context.Context, runID int64, step string) (string, error)
	Changes Changes // nil: no list of changed files
	// Lang is the language it answers in: es (Mexican Spanish) or en.
	Lang string
	Log  *slog.Logger
}

// Source is how the diagnostician signs its calls to the model.
const Source = "agent: diagnostician"

// Limits on what goes to the model: the end of each failed step's log,
// and all of it together.
const (
	logLines   = 120
	logChars   = 12000
	maxChanged = 40
)

// Run diagnoses failed runs (and the ones people ask for) every 30 seconds
// until ctx ends.
func (d *Diagnostician) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		ids, err := d.Store.RunsToDiagnose(ctx, time.Now().Add(-6*time.Hour), 3)
		if err != nil && ctx.Err() == nil {
			d.Log.Warn("diagnostician: listing runs", "err", err)
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			dctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
			if err := d.Diagnose(dctx, id); err != nil {
				d.Log.Warn("diagnostician: diagnosing a run", "run", id, "err", err)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Diagnose explains one failed run and keeps the answer.
func (d *Diagnostician) Diagnose(ctx context.Context, runID int64) error {
	run, err := d.Store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	app, err := d.Store.GetAppByID(ctx, run.AppID)
	if err != nil {
		return err
	}
	prev, _ := d.Store.GetDiagnosis(ctx, runID)
	out := store.Diagnosis{RunID: runID, AskedBy: prev.AskedBy}
	if run.Status != store.RunFailed {
		out.Status, out.Note = store.DiagnosisFailed, "the run did not fail"
		return d.Store.SaveDiagnosis(ctx, out)
	}
	res, err := d.Model.Complete(ctx, app.OrgID, Source, ia.Request{
		System: d.system(), MaxTokens: 700,
		Messages: []ia.Message{{Role: "user", Content: d.context(ctx, app, run)}},
	})
	out.Provider, out.Model, out.CostMicros, out.Note = res.Provider, res.Model, res.CostMicros, res.Note
	if err != nil {
		out.Status, out.Note = store.DiagnosisFailed, err.Error()
		if errors.Is(err, ia.ErrNoModel) {
			out.Note = "no model is available for this organization (AI page)"
		}
	} else {
		out.Status, out.Text = store.DiagnosisDone, strings.TrimSpace(res.Text)
	}
	return d.Store.SaveDiagnosis(context.WithoutCancel(ctx), out)
}

func (d *Diagnostician) system() string {
	if d.Lang == "es" {
		return `Usted es el agente diagnosticador de rendimiento, una plataforma que construye, prueba y despliega aplicaciones. Explique por qué falló esta ejecución y cómo arreglarlo, en español de México, de usted.
- Primero la causa más probable, citando la línea del registro que la muestra.
- Luego los pasos para arreglarlo, concretos (archivo, comando, cambio).
- Si los archivos que cambiaron explican la falla, dígalo.
- Si no hay información suficiente, dígalo en vez de adivinar. No invente archivos ni comandos.
- A lo más 200 palabras.`
	}
	return `You are rendimiento's diagnostician agent; rendimiento is a platform that builds, tests and deploys apps. Explain why this run failed and how to fix it.
- First the most likely cause, quoting the log line that shows it.
- Then concrete steps to fix it (file, command, change).
- If the changed files explain the failure, say so.
- If there is not enough information, say so instead of guessing. Do not invent files or commands.
- At most 200 words.`
}

// ansi matches terminal color codes in logs.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// context is what the model reads: the run, its failed steps with the end
// of their logs (lines that look like secrets left out), and the files
// changed since the branch's last good run.
func (d *Diagnostician) context(ctx context.Context, app *store.App, run *store.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "App: %s (repository %s)\nRun #%d on branch %s, commit %s, event %s.\n", app.Name, app.Repo, run.ID, run.Branch, short(run.SHA), run.Event)
	if run.Message != "" {
		fmt.Fprintf(&b, "Run message: %s\n", run.Message)
	}
	var services []string
	for _, s := range app.Spec.Services {
		services = append(services, s.Name)
	}
	if len(services) > 0 {
		fmt.Fprintf(&b, "Services: %s\n", strings.Join(services, ", "))
	}
	if d.Changes != nil && app.InstallationID != 0 {
		if base, err := d.Store.LastGoodSHA(ctx, app.ID, run.Branch, run.ID); err == nil && base != run.SHA {
			if files, _, err := d.Changes.ChangedFiles(ctx, app.InstallationID, app.Repo, base, run.SHA); err == nil && len(files) > 0 {
				if len(files) > maxChanged {
					files = append(files[:maxChanged], fmt.Sprintf("… and %d more", len(files)-maxChanged))
				}
				fmt.Fprintf(&b, "\nFiles changed since the last good run (%s):\n- %s\n", short(base), strings.Join(files, "\n- "))
			}
		}
	}
	budget := logChars
	for _, st := range run.Steps {
		if st.Status != "failed" {
			continue
		}
		fmt.Fprintf(&b, "\nFailed step %s (%s)", st.ID, st.Kind)
		if st.Message != "" {
			fmt.Fprintf(&b, ": %s", st.Message)
		}
		b.WriteString("\n")
		if d.Logs == nil || budget <= 0 {
			continue
		}
		log, err := d.Logs(ctx, run.ID, st.ID)
		if err != nil {
			continue
		}
		tail := clean(log, logLines)
		if len(tail) > budget {
			tail = tail[len(tail)-budget:]
		}
		budget -= len(tail)
		fmt.Fprintf(&b, "End of its log:\n```\n%s\n```\n", tail)
	}
	return b.String()
}

// clean is the last n lines of a log, without color codes and without the
// lines that look like secrets (they may go to an outside provider).
func clean(log string, n int) string {
	lines := strings.Split(strings.TrimRight(ansi.ReplaceAllString(log, ""), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := lines[:0]
	for _, l := range lines {
		if ruta.LooksSecret(l) {
			out = append(out, "[a line that looked like a secret was left out]")
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
