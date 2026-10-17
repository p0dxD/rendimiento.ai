package platform

import (
	"context"
	"sync"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/caminos"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// CaminosDays is how far back the Caminos tab looks.
const CaminosDays = 14

// caminosTTL is how long a built history is served before it is rebuilt
// (sooner when a newer run appears).
const caminosTTL = 2 * time.Minute

// caminosCache keeps each app's last history, and what GitHub said about
// commits, which never changes: a merge commit's first parent, and the merge
// base of two commits.
type caminosCache struct {
	mu      sync.Mutex
	byApp   map[int64]caminosEntry
	parents map[string]string
	bases   map[string]caminos.Fork
}

type caminosEntry struct {
	lastRun int64
	at      time.Time
	h       *caminos.History
}

// Caminos is an app's history of the last CaminosDays: its runs on the
// default branch and on every other branch, and where each branch left and
// rejoined it.
func (p *Platform) Caminos(ctx context.Context, a *store.App) (*caminos.History, error) {
	now := time.Now()
	from := now.Add(-CaminosDays * 24 * time.Hour)
	runs, err := p.Store.RunHistory(ctx, a.ID, from)
	if err != nil {
		return nil, err
	}
	var lastRun int64
	if len(runs) > 0 {
		lastRun = runs[len(runs)-1].ID
	}
	c := &p.caminos
	c.mu.Lock()
	if e, ok := c.byApp[a.ID]; ok && e.lastRun == lastRun && now.Sub(e.at) < caminosTTL {
		c.mu.Unlock()
		return e.h, nil
	}
	c.mu.Unlock()

	list := make([]caminos.Run, len(runs))
	for i, r := range runs {
		list[i] = caminos.Run{ID: r.ID, SHA: r.SHA, Branch: r.Branch, Status: r.Status, Message: r.Message,
			Failed: r.Failed, Release: r.Release, At: r.CreatedAt, End: r.FinishedAt}
	}
	src := &ghSource{p: p, inst: a.InstallationID, repo: a.Repo}
	h := caminos.Build(ctx, src, a.DefaultBranch, list, from, now)

	c.mu.Lock()
	if c.byApp == nil {
		c.byApp = map[int64]caminosEntry{}
	}
	c.byApp[a.ID] = caminosEntry{lastRun: lastRun, at: now, h: h}
	c.mu.Unlock()
	return h, nil
}

// ghSource answers caminos.Build from GitHub, remembering what cannot change.
type ghSource struct {
	p    *Platform
	inst int64
	repo string
}

func (g *ghSource) PullRequests(ctx context.Context) ([]caminos.PullRequest, error) {
	prs, err := g.p.GitHub.RecentPullRequests(ctx, g.inst, g.repo)
	if err != nil {
		return nil, err
	}
	out := make([]caminos.PullRequest, len(prs))
	for i, pr := range prs {
		out[i] = caminos.PullRequest{Number: pr.Number, Title: pr.Title, Branch: pr.Head.Ref, Base: pr.Base.Ref,
			State: pr.State, MergedAt: pr.MergedAt, MergeSHA: pr.MergeCommitSHA}
	}
	return out, nil
}

func (g *ghSource) FirstParent(ctx context.Context, sha string) (string, error) {
	c := &g.p.caminos
	key := g.repo + "@" + sha
	c.mu.Lock()
	if p, ok := c.parents[key]; ok {
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()
	parent, err := g.p.GitHub.FirstParent(ctx, g.inst, g.repo, sha)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.parents == nil || len(c.parents) > 20000 {
		c.parents = map[string]string{}
	}
	c.parents[key] = parent
	c.mu.Unlock()
	return parent, nil
}

func (g *ghSource) MergeBase(ctx context.Context, base, head string) (caminos.Fork, error) {
	c := &g.p.caminos
	// Only two commits' merge base never changes; a branch name moves.
	fixed := isSHA(base) && isSHA(head)
	key := g.repo + "@" + base + "..." + head
	if fixed {
		c.mu.Lock()
		if f, ok := c.bases[key]; ok {
			c.mu.Unlock()
			return f, nil
		}
		c.mu.Unlock()
	}
	sha, at, err := g.p.GitHub.MergeBase(ctx, g.inst, g.repo, base, head)
	if err != nil {
		return caminos.Fork{}, err
	}
	f := caminos.Fork{SHA: sha, At: at}
	if fixed && sha != "" {
		c.mu.Lock()
		if c.bases == nil || len(c.bases) > 20000 {
			c.bases = map[string]caminos.Fork{}
		}
		c.bases[key] = f
		c.mu.Unlock()
	}
	return f, nil
}

// isSHA reports whether s is a full commit SHA (40 hex digits).
func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
