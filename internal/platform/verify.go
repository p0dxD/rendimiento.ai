package platform

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	"github.com/p0dxD/rendimiento.ai/internal/notify"
	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/store"
	"github.com/p0dxD/rendimiento.ai/internal/uptime"
)

// VerifySettings configure release verification: after a release is
// live, its services are checked for Window; a release that breaks a
// service that worked before it is rolled back to the last good release.
type VerifySettings struct {
	// Window is how long a release is watched (0 turns verification off;
	// an app's verify.window overrides it).
	Window time.Duration
	// Every is the time between rounds of checks (default 20s).
	Every time.Duration
	// RolloutTimeout is how long a release may take to become healthy
	// before it counts as failed (default 10m).
	RolloutTimeout time.Duration
	// Check runs one check (uptime.Checker.Check in production).
	Check func(ctx context.Context, t uptime.Target) store.Probe
	// Rollout reports a release's rollout; nil reads the App object.
	Rollout func(ctx context.Context, app string, release int64) (RolloutState, string, error)
}

// RolloutState is where a release's rollout stands.
type RolloutState int

// Rollout states.
const (
	RolloutPending RolloutState = iota
	RolloutHealthy
	RolloutFailed
	RolloutSuperseded // a newer release replaced it
)

// errSuperseded stops a verification when a newer release replaces it.
var errSuperseded = errors.New("superseded")

// startVerification watches a new release in the background. Manual and
// automatic rollbacks, first releases with nothing to return to, and apps
// with verification turned off are recorded as skipped.
func (p *Platform) startVerification(ctx context.Context, app *store.App, rel *store.Release) {
	v := rel.Spec.Verify
	window := p.Verify.Window
	if v != nil && v.Window > 0 {
		window = time.Duration(v.Window) * time.Second
	}
	switch {
	case p.Verify.Window == 0 || p.Verify.Check == nil:
		p.setVerification(app, rel, store.VerifySkipped, "release verification is off on this platform")
		return
	case v != nil && v.Disabled:
		p.setVerification(app, rel, store.VerifySkipped, "verification is off for this app (verify.disabled)")
		return
	case rel.RollbackOf != nil:
		return // the rollback records its own status
	}
	vctx, cancel := context.WithCancelCause(ctx)
	p.mu.Lock()
	if p.verifying == nil {
		p.verifying = map[int64]context.CancelCauseFunc{}
	}
	if prev, ok := p.verifying[app.ID]; ok {
		prev(errSuperseded)
	}
	p.verifying[app.ID] = cancel
	p.mu.Unlock()
	p.setVerification(app, rel, store.VerifyRunning, fmt.Sprintf("watching the new release for %s", fmtDur(window)))
	go func() {
		defer func() {
			p.mu.Lock()
			if p.verifying != nil {
				delete(p.verifying, app.ID)
			}
			p.mu.Unlock()
			cancel(nil)
		}()
		p.verify(vctx, app, rel, window)
	}()
}

// stopVerification ends an app's running verification as superseded (a
// newer release, or a rollback by hand, replaced the release).
func (p *Platform) stopVerification(appID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.verifying[appID]; ok {
		c(errSuperseded)
		delete(p.verifying, appID)
	}
}

func (p *Platform) verify(ctx context.Context, app *store.App, rel *store.Release, window time.Duration) {
	log := p.Log.With("app", app.Name, "release", rel.Number)
	superseded := func() bool {
		if errors.Is(context.Cause(ctx), errSuperseded) {
			p.setVerification(app, rel, store.VerifySuperseded, "a newer release replaced it before verification finished")
			return true
		}
		return false
	}

	// 1. The rollout: every pod on the new version and ready.
	healthy, why := p.waitRollout(ctx, app.Name, rel.Number)
	if superseded() || ctx.Err() != nil {
		return // on shutdown the release stays "verifying" and is resumed at startup
	}
	if why == errSuperseded.Error() {
		p.setVerification(app, rel, store.VerifySuperseded, "a newer release replaced it before verification finished")
		return
	}
	if !healthy {
		p.verificationFailed(ctx, app, rel, why)
		return
	}

	// 2. The window: check every service of the release every few seconds.
	baseline, err := p.Store.ProbeStats(ctx, app.ID, rel.CreatedAt.Add(-time.Hour), rel.CreatedAt)
	if err != nil {
		log.Warn("verification: no baseline", "err", err)
	}
	every := p.Verify.Every
	if every <= 0 {
		every = 20 * time.Second
	}
	targets := uptime.Targets([]*store.App{{ID: app.ID, Name: app.Name, Spec: rel.Spec}})
	got := map[[2]string]*windowStats{}
	end := time.Now().Add(window)
	for {
		for _, t := range targets {
			r := p.Verify.Check(ctx, t)
			k := [2]string{t.Service, t.Kind}
			if got[k] == nil {
				got[k] = &windowStats{}
			}
			got[k].add(r)
		}
		if time.Now().After(end) {
			break
		}
		select {
		case <-ctx.Done():
			superseded()
			return
		case <-time.After(every):
		}
	}

	// 3. The verdict.
	ok, reason, warnings := judge(baseline, got)
	if ok {
		msg := fmt.Sprintf("all %d checks stayed healthy for %s", len(targets), fmtDur(window))
		if len(targets) == 0 {
			msg = "rolled out healthy (no services to check)"
		}
		if len(warnings) > 0 {
			msg += "; " + strings.Join(warnings, "; ")
		}
		p.setVerification(app, rel, store.VerifyPassed, msg)
		log.Info("release verified", "message", msg)
		return
	}
	if len(warnings) > 0 {
		reason += "; " + strings.Join(warnings, "; ")
	}
	p.verificationFailed(ctx, app, rel, reason)
}

// verificationFailed rolls back to the last good release, or only reports
// when rollback is off or there is nothing to return to.
func (p *Platform) verificationFailed(ctx context.Context, app *store.App, rel *store.Release, reason string) {
	log := p.Log.With("app", app.Name, "release", rel.Number)
	kept := func(why string) {
		p.setVerification(app, rel, store.VerifyFailedKept, reason+" ("+why+")")
		log.Warn("release failed verification; kept", "reason", reason, "why", why)
		p.Notify.Notify(p.verifyMessage(app, rel, reason, nil, why))
	}
	if !rel.Spec.Verify.AutoRollback() {
		kept("automatic rollback is off: verify.rollback")
		return
	}
	good, err := p.lastGoodRelease(ctx, app.ID, rel.Number)
	if err != nil {
		kept("no earlier good release to roll back to")
		return
	}
	back, err := p.rollbackTo(ctx, app, good, fmt.Sprintf("automatic rollback: release #%d failed verification", rel.Number))
	if err != nil {
		kept("the automatic rollback failed: " + err.Error())
		log.Error("automatic rollback failed", "err", err)
		return
	}
	p.setVerification(app, rel, store.VerifyFailed, fmt.Sprintf("%s; rolled back to release #%d (as #%d)", reason, good.Number, back.Number))
	log.Warn("release failed verification; rolled back", "reason", reason, "to", good.Number, "as", back.Number)
	p.Notify.Notify(p.verifyMessage(app, rel, reason, &[2]int64{good.Number, back.Number}, ""))
}

// verifyMessage is the email for a release that failed verification:
// rolled back (to = {good release, new release number}) or kept (why).
func (p *Platform) verifyMessage(app *store.App, rel *store.Release, reason string, to *[2]int64, why string) notify.Message {
	facts := []notify.Fact{
		{Label: "App", Value: app.Name},
		{Label: "Failed release", Value: fmt.Sprintf("#%d (%s)", rel.Number, shortSHA(rel.SHA))},
	}
	m := notify.Message{
		Key: fmt.Sprintf("verify:%s:%d", app.Name, rel.Number), Details: strings.ReplaceAll(reason, "; ", "\n"),
		ActionURL: p.Config.BaseURL + "/apps/" + app.Name + "/releases", ActionLabel: "See releases",
	}
	if to != nil {
		facts = append(facts, notify.Fact{Label: "Now running", Value: fmt.Sprintf("release #%d (the images of #%d)", to[1], to[0])})
		m.Tone, m.Subject = notify.Critical, fmt.Sprintf("↩ %s: release #%d rolled back", app.Name, rel.Number)
		m.Title = fmt.Sprintf("Release #%d was rolled back", rel.Number)
		m.Summary = fmt.Sprintf("It broke a service that worked before it went live, so rendimiento put release #%d back. Nothing needs doing to recover; fix the cause and push again.", to[0])
	} else {
		facts = append(facts, notify.Fact{Label: "Kept because", Value: why})
		m.Tone, m.Subject = notify.Warning, fmt.Sprintf("⚠ %s: release #%d failed verification", app.Name, rel.Number)
		m.Title = fmt.Sprintf("Release #%d failed verification", rel.Number)
		m.Summary = "It broke a service that worked before it went live, and it is still running."
	}
	m.Facts = facts
	return m
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// lastGoodRelease is the newest release before number that did not fail
// verification (releases from before verification existed count as good).
func (p *Platform) lastGoodRelease(ctx context.Context, appID, number int64) (*store.Release, error) {
	rels, err := p.Store.ListReleases(ctx, appID, 100)
	if err != nil {
		return nil, err
	}
	for _, r := range rels {
		if r.Number >= number {
			continue
		}
		switch r.VerifyStatus {
		case "", store.VerifyPassed, store.VerifySkipped:
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}

// waitRollout waits until the release is fully rolled out and healthy, it
// fails, or RolloutTimeout passes. It reports health and, if not, why.
func (p *Platform) waitRollout(ctx context.Context, app string, number int64) (bool, string) {
	timeout := p.Verify.RolloutTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	state := p.Verify.Rollout
	if state == nil {
		state = p.rolloutState
	}
	deadline := time.Now().Add(timeout)
	for {
		st, msg, err := state(ctx, app, number)
		switch {
		case err != nil:
			p.Log.Warn("verification: reading the rollout", "app", app, "err", err)
		case st == RolloutHealthy:
			return true, ""
		case st == RolloutFailed:
			return false, "the rollout failed: " + msg
		case st == RolloutSuperseded:
			return false, errSuperseded.Error()
		}
		if time.Now().After(deadline) {
			return false, fmt.Sprintf("it did not become healthy within %s (%s)", fmtDur(timeout), msg)
		}
		select {
		case <-ctx.Done():
			return false, ""
		case <-time.After(5 * time.Second):
		}
	}
}

// rolloutState reads the App object: healthy once the controller reports
// the release Healthy, failed when it reports it Degraded or in Error.
func (p *Platform) rolloutState(ctx context.Context, app string, number int64) (RolloutState, string, error) {
	var cr v1alpha1.App
	if err := p.Kube.Get(ctx, client.ObjectKey{Name: app}, &cr); err != nil {
		return RolloutPending, "", err
	}
	if cr.Spec.Release != number {
		return RolloutSuperseded, "", nil
	}
	if cr.Status.Release != number || cr.Status.ObservedGeneration < cr.Generation {
		return RolloutPending, "the controller has not applied it yet", nil
	}
	switch cr.Status.Phase {
	case v1alpha1.PhaseHealthy:
		return RolloutHealthy, "", nil
	case v1alpha1.PhaseDegraded, v1alpha1.PhaseError:
		msg := cr.Status.Message
		for _, s := range cr.Status.Services {
			if s.Message != "" {
				msg = s.Name + ": " + s.Message
				break
			}
		}
		return RolloutFailed, msg, nil
	}
	return RolloutPending, string(cr.Status.Phase), nil
}

// windowStats accumulates one check's results during a verification window.
type windowStats struct {
	total, ok int
	latency   []int // of successful checks
	lastError string
}

func (w *windowStats) add(r store.Probe) {
	w.total++
	if r.OK {
		w.ok++
		w.latency = append(w.latency, r.LatencyMS)
	} else {
		w.lastError = r.Error
	}
}

func (w *windowStats) p95() int {
	if len(w.latency) == 0 {
		return 0
	}
	l := append([]int(nil), w.latency...)
	sort.Ints(l)
	return l[(len(l)*95+99)/100-1]
}

// judge decides a verification. A check fails the release when at least 3
// of its checks, and at least a fifth, failed, unless it was already
// failing in the hour before the release (under 90% up), which is
// reported but not blamed on the release. Being much slower (p95 three
// times the baseline and a second more) is only reported.
func judge(baseline map[[2]string]store.CheckStats, got map[[2]string]*windowStats) (bool, string, []string) {
	keys := make([][2]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	var failures, warnings []string
	for _, k := range keys {
		w := got[k]
		b, had := baseline[k]
		name := fmt.Sprintf("%s (%s)", k[0], k[1])
		fails := w.total - w.ok
		if fails >= 3 && fails*5 >= w.total {
			if had && b.Total > 0 && float64(b.OK)/float64(b.Total) < 0.9 {
				warnings = append(warnings, fmt.Sprintf("%s is failing, but it was already failing before this release", name))
				continue
			}
			before := "it had no checks before (a new service)"
			if had && b.Total > 0 {
				before = fmt.Sprintf("it was %.0f%% up in the hour before", 100*float64(b.OK)/float64(b.Total))
			}
			failures = append(failures, fmt.Sprintf("%s: %d of %d checks failed (%s); %s", name, fails, w.total, w.lastError, before))
			continue
		}
		if p95 := w.p95(); had && b.P95MS > 0 && p95 > 3*b.P95MS && p95-b.P95MS > 1000 {
			warnings = append(warnings, fmt.Sprintf("%s is slower: p95 %s, was %s", name, fmtMS(p95), fmtMS(b.P95MS)))
		}
	}
	if len(failures) > 0 {
		return false, strings.Join(failures, "; "), warnings
	}
	return true, "", warnings
}

// setVerification records the state and tells open App pages.
func (p *Platform) setVerification(app *store.App, rel *store.Release, status, message string) {
	if err := p.Store.SetVerification(context.Background(), rel.ID, status, message); err != nil {
		p.Log.Warn("could not record verification", "app", app.Name, "release", rel.Number, "err", err)
	}
	rel.VerifyStatus, rel.VerifyMessage = status, message
	p.Hub.Publish(appTopic(app.Name), events.Event{Type: "release", Data: rel})
}

// ResumeVerifications restarts verifications interrupted by a restart: a
// release that is still the app's current one is watched again from the
// start; an older one is marked superseded.
func (p *Platform) ResumeVerifications(ctx context.Context) {
	rels, err := p.Store.VerifyingReleases(ctx)
	if err != nil {
		p.Log.Warn("could not resume verifications", "err", err)
		return
	}
	for _, rel := range rels {
		app, err := p.Store.GetAppByID(ctx, rel.AppID)
		if err != nil {
			continue
		}
		latest, err := p.Store.LatestRelease(ctx, app.ID)
		if err != nil || latest.ID != rel.ID {
			p.setVerification(app, rel, store.VerifySuperseded, "a newer release replaced it before verification finished")
			continue
		}
		p.Log.Info("resuming release verification", "app", app.Name, "release", rel.Number)
		p.startVerification(ctx, app, rel)
	}
}

func fmtDur(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return d.Round(time.Second).String()
}

func fmtMS(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}

// notifyRunFailed emails a failed run of the default branch: nothing was
// released. It names the steps that failed, with their messages.
func (p *Platform) notifyRunFailed(app *store.App, run *store.Run, msg string, steps []pipeline.Step, results map[string]pipeline.StepResult) {
	var failed []string
	for _, s := range steps {
		if r := results[s.ID]; r.Status == pipeline.StatusFailed && !s.Optional {
			line := s.ID
			if r.Message != "" {
				line += ": " + r.Message
			}
			failed = append(failed, line)
		}
	}
	details := strings.Join(failed, "\n")
	if details == "" {
		details = msg
	}
	p.Notify.Notify(notify.Message{
		Tone: notify.Critical, Key: fmt.Sprintf("run:%s:%s", app.Name, run.SHA),
		Subject: fmt.Sprintf("✗ %s: build failed on %s", app.Name, run.Branch),
		Title:   fmt.Sprintf("The %s build failed", app.Name),
		Summary: fmt.Sprintf("A push to %s did not build, so nothing was released; the app keeps running its current release.", run.Branch),
		Facts: []notify.Fact{
			{Label: "App", Value: app.Name},
			{Label: "Commit", Value: fmt.Sprintf("%s on %s", shortSHA(run.SHA), run.Branch)},
			{Label: "Run", Value: fmt.Sprintf("#%d", run.ID)},
		},
		Details:   details,
		ActionURL: fmt.Sprintf("%s/apps/%s/runs/%d", p.Config.BaseURL, app.Name, run.ID), ActionLabel: "Open the run",
	})
}
