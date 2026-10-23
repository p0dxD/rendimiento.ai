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
	"slices"
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
	SearchRuta(ctx context.Context, orgID int64, kind, text string, limit int) ([]store.RutaEntry, error)
}

// KnownTag marks the Ruta's known failures: what people confirmed or
// corrected about earlier diagnoses, which the diagnostician reads.
const KnownTag = "falla-conocida"

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
	text, signature, clues := d.context(ctx, app, run)
	out.Signature = signature
	if known := d.known(ctx, app, run, clues); known != "" {
		text += known
	}
	res, err := d.Model.Complete(ctx, app.OrgID, Source, ia.Request{
		System: d.system(), MaxTokens: 700,
		Messages: []ia.Message{{Role: "user", Content: text}},
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
- Si una falla conocida de la Ruta se parece, úsela antes que su propia idea, y dígalo.
- Si no hay información suficiente, dígalo en vez de adivinar. No invente archivos, configuración ni comandos.
- A lo más 200 palabras.`
	}
	return `You are rendimiento's diagnostician agent; rendimiento is a platform that builds, tests and deploys apps. Explain why this run failed and how to fix it.
- First the most likely cause, quoting the log line that shows it.
- Then concrete steps to fix it (file, command, change).
- If the changed files explain the failure, say so.
- If a known failure from the Ruta looks alike, prefer it to your own idea, and say so.
- If there is not enough information, say so instead of guessing. Do not invent files, configuration or commands.
- At most 200 words.`
}

// ansi matches terminal color codes in logs.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// context is what the model reads: the run, its failed steps with the end
// of their logs (lines that look like secrets left out), and the files
// changed since the branch's last good run; and the failure's signature,
// its telling line.
func (d *Diagnostician) context(ctx context.Context, app *store.App, run *store.Run) (text, signature, clues string) {
	signature = run.Message
	var errs []string
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
			if signature == "" {
				signature = st.Message
			}
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
		if lines := telling(tail); len(lines) > 0 {
			signature, errs = lines[0], append(errs, lines...)
		}
		if len(tail) > budget {
			tail = tail[len(tail)-budget:]
		}
		budget -= len(tail)
		fmt.Fprintf(&b, "End of its log:\n```\n%s\n```\n", tail)
	}
	if len(signature) > 200 {
		signature = signature[:200]
	}
	return b.String(), signature, signature + " " + strings.Join(errs, " ")
}

// errorish matches a log line that tells what went wrong.
var errorish = regexp.MustCompile(`(?i)(error|fail|exception|panic|timed out|timeout|refused|denied|not found|cannot|unable)`)

// summary lines repeat what failed without saying why (test runners'
// totals, the platform's own notes).
var summary = regexp.MustCompile(`^(--- FAIL|FAIL\b|=== |ok\s|rendimiento:|make: \*\*\*|npm ERR! (A complete log|code|errno))`)

// telling are the lines of a log that say what went wrong, the last first,
// at most five; summary lines only if nothing else does.
func telling(log string) []string {
	lines := strings.Split(log, "\n")
	var out, sums []string
	for i := len(lines) - 1; i >= 0 && len(out) < 5; i-- {
		l := strings.TrimSpace(lines[i])
		switch {
		case l == "" || !errorish.MatchString(l):
		case summary.MatchString(l):
			sums = append(sums, l)
		default:
			out = append(out, l)
		}
	}
	if len(out) == 0 && len(sums) > 0 {
		return sums[:1]
	}
	return out
}

// word is a distinctive word of a failure: letters, digits and . _ : -, 5
// characters or more.
var word = regexp.MustCompile(`[A-Za-z0-9_.:-]{5,}`)

// common words that say nothing about which failure it is.
var common = map[string]bool{"error": true, "failed": true, "failure": true, "while": true, "which": true, "there": true,
	"exited": true, "status": true, "rendimiento": true, "could": true, "about": true, "after": true}

// known is what the Ruta knows of failures like this one: the known
// failures (tagged falla-conocida) sharing the most words with the
// signature, the step messages and the app's name, at most three.
func (d *Diagnostician) known(ctx context.Context, app *store.App, run *store.Run, clues string) string {
	entries, err := d.Store.SearchRuta(ctx, app.OrgID, "", KnownTag, 50)
	if err != nil || len(entries) == 0 {
		return ""
	}
	for _, st := range run.Steps {
		if st.Status == "failed" {
			clues += " " + st.ID + " " + st.Message
		}
	}
	words := map[string]bool{}
	for _, w := range word.FindAllString(strings.ToLower(clues), -1) {
		if w = strings.Trim(w, ".:-"); len(w) >= 5 && !common[w] {
			words[w] = true
		}
	}
	type hit struct {
		e     store.RutaEntry
		score int
	}
	var hits []hit
	for _, e := range entries {
		text := strings.ToLower(e.Title + " " + e.Body + " " + strings.Join(e.Tags, " "))
		score := 0
		for w := range words {
			if strings.Contains(text, w) {
				score++
			}
		}
		if slices.Contains(e.Tags, strings.ToLower(app.Name)) {
			score++
		}
		if score >= 2 {
			hits = append(hits, hit{e, score})
		}
	}
	if len(hits) == 0 {
		return ""
	}
	slices.SortFunc(hits, func(a, b hit) int { return b.score - a.score })
	var out strings.Builder
	out.WriteString("\nKnown failures in the Ruta that look alike (what people confirmed before):\n")
	for i, h := range hits {
		if i == 3 {
			break
		}
		body := h.e.Body
		if len(body) > 800 {
			body = body[:800] + "…"
		}
		fmt.Fprintf(&out, "- %s\n%s\n", h.e.Title, body)
	}
	return out.String()
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
