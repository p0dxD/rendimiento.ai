// Package caminos builds an app's recent history as the Caminos tab draws
// it: the default branch is the camino real, every other branch is a vereda
// that leaves it at the commit it was branched from (or from its parent
// branch, for a stacked pull request) and comes back where its pull request
// was merged, with each run as a stop along the way.
//
// Runs come from the store; whether a branch was merged, and where it
// started, come from GitHub (a Source), which is allowed to fail: the
// history is then drawn with what is known and marked Partial.
package caminos

import (
	"context"
	"sort"
	"time"
)

// LeftAfter is how long a branch that was never merged may go without a run
// before it counts as left on the way ("se quedó en el camino"), unless its
// pull request is still open.
const LeftAfter = 48 * time.Hour

// MaxBranches caps how many branches are looked up on GitHub (the most
// recently active ones).
const MaxBranches = 60

// Branch states.
const (
	Merged = "merged" // its pull request was merged
	Open   = "open"   // not merged yet: its pull request is open, or it ran recently
	Left   = "left"   // never merged, and nobody came back for it
)

// Run is one build, as a stop on the road.
type Run struct {
	ID      int64      `json:"id"`
	SHA     string     `json:"sha"`
	Branch  string     `json:"-"`
	Status  string     `json:"status"`
	Message string     `json:"message,omitempty"`
	Failed  []string   `json:"failed,omitempty"` // the steps that failed
	Release int64      `json:"release,omitempty"` // the release it made, if any
	At      time.Time  `json:"at"`
	End     *time.Time `json:"end,omitempty"`
}

// PullRequest is what the history needs of a pull request.
type PullRequest struct {
	Number   int
	Title    string
	Branch   string // its head branch
	Base     string // the branch it merges into
	State    string // open or closed
	MergedAt *time.Time
	MergeSHA string // the commit that put it on the base branch (merge or squash)
}

// Fork is the commit a branch was started from.
type Fork struct {
	SHA string    `json:"sha"`
	At  time.Time `json:"at"`
}

// Source answers what only GitHub knows.
type Source interface {
	PullRequests(ctx context.Context) ([]PullRequest, error)
	// MergeBase is the best common ancestor of two commits (or branches).
	MergeBase(ctx context.Context, base, head string) (Fork, error)
	// FirstParent is a commit's first parent: for a merge or squash commit,
	// the base branch just before the pull request landed.
	FirstParent(ctx context.Context, sha string) (string, error)
}

// Branch is one vereda.
type Branch struct {
	Name     string     `json:"name"`
	PR       int        `json:"pr,omitempty"`
	Title    string     `json:"title,omitempty"`
	Parent   string     `json:"parent,omitempty"` // a stacked pull request's base branch
	State    string     `json:"state"`
	Fork     *Fork      `json:"fork,omitempty"`
	MergedAt *time.Time `json:"mergedAt,omitempty"`
	MergeSHA string     `json:"mergeSha,omitempty"`
	Runs     []Run      `json:"runs"`
}

// History is everything the tab draws.
type History struct {
	DefaultBranch string    `json:"defaultBranch"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	Main          []Run     `json:"main"`
	Branches      []Branch  `json:"branches"`
	// Partial is set when GitHub could not answer everything: some branches
	// may then lack their pull request or the commit they started from.
	Partial bool `json:"partial,omitempty"`
}

// Build makes the history of runs (any order) of a repository whose default
// branch is def.
func Build(ctx context.Context, src Source, def string, runs []Run, from, now time.Time) *History {
	h := &History{DefaultBranch: def, From: from, To: now, Main: []Run{}, Branches: []Branch{}}
	sort.Slice(runs, func(i, j int) bool { return runs[i].At.Before(runs[j].At) || runs[i].At.Equal(runs[j].At) && runs[i].ID < runs[j].ID })
	byName := map[string]*Branch{}
	var order []*Branch
	for _, r := range runs {
		if r.Branch == def {
			h.Main = append(h.Main, r)
			continue
		}
		b := byName[r.Branch]
		if b == nil {
			b = &Branch{Name: r.Branch}
			byName[r.Branch] = b
			order = append(order, b)
		}
		b.Runs = append(b.Runs, r)
	}
	// Look up the most recently active branches only.
	recent := append([]*Branch(nil), order...)
	sort.SliceStable(recent, func(i, j int) bool { return last(recent[i]).At.After(last(recent[j]).At) })
	if len(recent) > MaxBranches {
		for _, b := range recent[MaxBranches:] {
			delete(byName, b.Name)
		}
		recent = recent[:MaxBranches]
	}

	prs, err := src.PullRequests(ctx)
	if err != nil {
		h.Partial = true
	}
	latest := map[string]PullRequest{} // branch → its newest pull request
	for _, p := range prs {
		if q, ok := latest[p.Branch]; !ok || p.Number > q.Number {
			latest[p.Branch] = p
		}
	}
	for _, b := range recent {
		pr, hasPR := latest[b.Name]
		if hasPR {
			b.PR, b.Title = pr.Number, pr.Title
			if pr.Base != "" && pr.Base != def {
				b.Parent = pr.Base
			}
		}
		head := last(b).SHA
		base := def
		switch {
		case hasPR && pr.MergedAt != nil:
			b.State, b.MergedAt, b.MergeSHA = Merged, pr.MergedAt, pr.MergeSHA
			// Where it started is found against the base just before it
			// landed: after the merge, the base already contains it.
			if parent, err := src.FirstParent(ctx, pr.MergeSHA); err == nil && parent != "" {
				base = parent
			} else {
				h.Partial = true
				base = ""
			}
		case hasPR && pr.State == "open", now.Sub(last(b).At) < LeftAfter:
			b.State = Open
			if b.Parent != "" {
				base = b.Parent
			}
		default:
			b.State = Left
		}
		if base != "" {
			if f, err := src.MergeBase(ctx, base, head); err == nil && f.SHA != "" {
				b.Fork = &f
			} else if err != nil {
				h.Partial = true
			}
		}
	}
	for _, b := range order {
		if _, ok := byName[b.Name]; ok {
			h.Branches = append(h.Branches, *b)
		}
	}
	return h
}

func last(b *Branch) Run { return b.Runs[len(b.Runs)-1] }
