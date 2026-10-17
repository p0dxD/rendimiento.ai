package caminos

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	prs       []PullRequest
	prErr     error
	parents   map[string]string // merge commit → first parent
	bases     map[[2]string]Fork
	baseCalls [][2]string
}

func (f *fakeSource) PullRequests(context.Context) ([]PullRequest, error) { return f.prs, f.prErr }
func (f *fakeSource) FirstParent(_ context.Context, sha string) (string, error) {
	if p, ok := f.parents[sha]; ok {
		return p, nil
	}
	return "", errors.New("no such commit")
}
func (f *fakeSource) MergeBase(_ context.Context, base, head string) (Fork, error) {
	f.baseCalls = append(f.baseCalls, [2]string{base, head})
	if b, ok := f.bases[[2]string{base, head}]; ok {
		return b, nil
	}
	return Fork{}, errors.New("no merge base")
}

func TestBuild(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return now.Add(time.Duration(-h) * time.Hour) }
	merged := at(20)
	src := &fakeSource{
		prs: []PullRequest{
			{Number: 3, Branch: "arreglo", Base: "main", State: "closed", MergedAt: &merged, MergeSHA: "m3", Title: "Fix it"},
			{Number: 1, Branch: "arreglo", Base: "main", State: "closed", Title: "an older PR from the same branch"},
			{Number: 4, Branch: "encima", Base: "arreglo", State: "open", Title: "On top"},
			{Number: 5, Branch: "abierta", Base: "main", State: "open"},
		},
		parents: map[string]string{"m3": "p3"},
		bases: map[[2]string]Fork{
			{"p3", "a2"}:      {SHA: "f1", At: at(30)},
			{"arreglo", "e1"}: {SHA: "a1", At: at(26)},
			{"main", "o1"}:    {SHA: "f2", At: at(200)},
		},
	}
	runs := []Run{
		{ID: 5, SHA: "a2", Branch: "arreglo", Status: "succeeded", At: at(22)},
		{ID: 2, SHA: "a1", Branch: "arreglo", Status: "failed", Failed: []string{"web:test"}, At: at(25)},
		{ID: 1, SHA: "m0", Branch: "main", Status: "succeeded", Release: 9, At: at(40)},
		{ID: 6, SHA: "m3", Branch: "main", Status: "succeeded", Release: 10, At: at(19)},
		{ID: 7, SHA: "e1", Branch: "encima", Status: "succeeded", At: at(3)},
		{ID: 3, SHA: "o1", Branch: "olvidada", Status: "failed", At: at(150)},
		{ID: 8, SHA: "x1", Branch: "abierta", Status: "succeeded", At: at(100)},
	}
	h := Build(context.Background(), src, "main", runs, at(14*24), now)

	if len(h.Main) != 2 || h.Main[0].ID != 1 || h.Main[1].Release != 10 {
		t.Fatalf("main = %+v", h.Main)
	}
	if len(h.Branches) != 4 {
		t.Fatalf("branches = %+v", h.Branches)
	}
	by := map[string]Branch{}
	for _, b := range h.Branches {
		by[b.Name] = b
	}
	// in order of their first run
	if h.Branches[0].Name != "olvidada" || h.Branches[1].Name != "abierta" || h.Branches[2].Name != "arreglo" {
		t.Fatalf("order: %s %s %s", h.Branches[0].Name, h.Branches[1].Name, h.Branches[2].Name)
	}
	a := by["arreglo"]
	if a.State != Merged || a.PR != 3 || a.Title != "Fix it" || a.MergeSHA != "m3" || a.Fork == nil || a.Fork.SHA != "f1" {
		t.Fatalf("merged branch = %+v", a)
	}
	if len(a.Runs) != 2 || a.Runs[0].ID != 2 || a.Runs[1].ID != 5 {
		t.Fatalf("its runs, oldest first: %+v", a.Runs)
	}
	// A merged branch's start is looked up against the base before the merge.
	e := by["encima"]
	if e.State != Open || e.Parent != "arreglo" || e.PR != 4 || e.Fork == nil || e.Fork.SHA != "a1" {
		t.Fatalf("stacked branch = %+v", e)
	}
	if o := by["olvidada"]; o.State != Left || o.PR != 0 || o.Fork == nil || o.Fork.SHA != "f2" {
		t.Fatalf("left branch = %+v", o)
	}
	// An open pull request keeps a quiet branch open.
	if ab := by["abierta"]; ab.State != Open {
		t.Fatalf("open PR = %+v", ab)
	}
	if !h.Partial {
		t.Fatal("a merge base GitHub could not find should mark the history partial")
	}
}

func TestBuildWithoutGitHub(t *testing.T) {
	now := time.Now()
	src := &fakeSource{prErr: errors.New("GitHub is down")}
	h := Build(context.Background(), src, "main", []Run{
		{ID: 1, SHA: "a", Branch: "nueva", Status: "succeeded", At: now.Add(-time.Hour)},
		{ID: 2, SHA: "b", Branch: "vieja", Status: "failed", At: now.Add(-100 * time.Hour)},
	}, now.Add(-14*24*time.Hour), now)
	if !h.Partial || len(h.Branches) != 2 || h.Branches[0].State != Left || h.Branches[1].State != Open {
		t.Fatalf("history without GitHub = %+v", h)
	}
	if h.Main == nil {
		t.Fatal("main must be an empty list, not null")
	}
}

func TestBuildCapsBranches(t *testing.T) {
	now := time.Now()
	var runs []Run
	for i := 0; i < MaxBranches+5; i++ {
		runs = append(runs, Run{ID: int64(i + 1), SHA: "s", Branch: "b" + string(rune('a'+i%26)) + string(rune('a'+i/26)), At: now.Add(time.Duration(-i) * time.Minute)})
	}
	src := &fakeSource{}
	h := Build(context.Background(), src, "main", runs, now.Add(-time.Hour), now)
	if len(h.Branches) != MaxBranches || len(src.baseCalls) != MaxBranches {
		t.Fatalf("%d branches, %d lookups", len(h.Branches), len(src.baseCalls))
	}
}
