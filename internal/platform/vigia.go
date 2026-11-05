package platform

import (
	"context"
	"time"

	gh "github.com/rendimiento-ai/rendimiento/internal/github"
)

// Vigia asks GitHub for new commits where GitHub cannot send its webhooks
// (rendimiento on a computer, without a tunnel): every Every it lists each
// app's branches, and a branch whose head moved is handled as a push. The
// first look only notes where each branch is, so starting does not run
// every branch again, and neither does onboarding an app; a default
// branch whose head has no run yet does run.
type Vigia struct {
	P     *Platform
	Every time.Duration // default 1m
	// Branches lists a repo's branches (the GitHub App's, faked in tests).
	Branches func(ctx context.Context, installation int64, repo string) ([]gh.Branch, error)

	seen  map[string]string // "repo branch" → head
	repos map[string]bool   // repos looked at before
}

// Run looks until ctx ends.
func (v *Vigia) Run(ctx context.Context) {
	every := v.Every
	if every <= 0 {
		every = time.Minute
	}
	for {
		v.Mirar(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Mirar looks once at every app's branches.
func (v *Vigia) Mirar(ctx context.Context) {
	if v.seen == nil {
		v.seen, v.repos = map[string]string{}, map[string]bool{}
	}
	apps, err := v.P.Store.ListApps(ctx)
	if err != nil {
		v.P.Log.Warn("watching GitHub: could not list the apps", "err", err)
		return
	}
	repos := map[string]bool{}
	for _, app := range apps {
		if repos[app.Repo] || app.InstallationID == 0 {
			continue // one look per repo: a push queues every app of it
		}
		repos[app.Repo] = true
		branches, err := v.Branches(ctx, app.InstallationID, app.Repo)
		if err != nil {
			v.P.Log.Warn("watching GitHub: could not list the branches", "repo", app.Repo, "err", err)
			continue
		}
		// The first look at a repo (at startup, or once its app is
		// onboarded) only notes where its branches are.
		first := !v.repos[app.Repo]
		v.repos[app.Repo] = true
		for _, b := range branches {
			ran := func() bool { return v.ran(ctx, app.ID, b.Commit.SHA) }
			if !v.nuevo(first, app.Repo+" "+b.Name, b.Commit.SHA, b.Name == app.DefaultBranch, ran) {
				continue
			}
			if _, err := v.P.HandlePush(ctx, PushEvent{Installation: app.InstallationID, Repo: app.Repo, Branch: b.Name, SHA: b.Commit.SHA}); err != nil {
				v.P.Log.Warn("watching GitHub: could not queue the push", "repo", app.Repo, "branch", b.Name, "err", err)
			}
		}
	}
}

// nuevo notes a branch's head and reports whether it is a push to handle:
// a head that moved, or a branch that appeared. On the first look only a
// default branch whose head never ran counts.
func (v *Vigia) nuevo(first bool, key, sha string, isDefault bool, ran func() bool) bool {
	before, known := v.seen[key]
	v.seen[key] = sha
	switch {
	case known:
		return before != sha
	case first:
		return isDefault && !ran()
	}
	return true
}

// ran reports whether the app has a run of this commit already.
func (v *Vigia) ran(ctx context.Context, appID int64, sha string) bool {
	runs, err := v.P.Store.ListRuns(ctx, appID, 20)
	if err != nil {
		return true // unknown: better not to run it twice
	}
	for _, r := range runs {
		if r.SHA == sha {
			return true
		}
	}
	return false
}
