package agentes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type coderStore struct {
	runID     *int64 // the run the encargo is about
	runs      []*store.Run
	delivered string
	links     []string
	gaveBack  string
	spent     int64
	handover  string
	waited    []int64
}

func (s *coderStore) EncargoForCoder(context.Context) (store.Encargo, error) {
	return store.Encargo{}, store.ErrNotFound
}
func (s *coderStore) EncargosOfSystemKey(context.Context, string) ([]store.Encargo, error) {
	return nil, nil
}
func (s *coderStore) SystemKey(_ context.Context, org int64, name string) (store.AgentKey, error) {
	return store.AgentKey{ID: 9, Name: name, OrgID: org}, nil
}
func (s *coderStore) TakeCargo(_ context.Context, key int64, purpose string) (store.Cargo, error) {
	return store.Cargo{ID: 3, KeyID: key, Purpose: purpose}, nil
}
func (s *coderStore) OpenCargo(context.Context, int64) (store.Cargo, error) {
	return store.Cargo{}, store.ErrNotFound
}
func (s *coderStore) HandOver(_ context.Context, _ int64, entrega, _ string) error {
	s.handover = entrega
	return nil
}
func (s *coderStore) TakeEncargo(_ context.Context, id, _, _ int64, _ string) (store.Encargo, error) {
	return store.Encargo{ID: id, OrgID: 1, App: "tienda", Title: "Saludar en español", Status: store.EncargoTaken, RunID: s.runID}, nil
}
func (s *coderStore) WaitEncargo(_ context.Context, _, _, run int64, _ string) (store.Encargo, error) {
	s.waited = append(s.waited, run)
	return store.Encargo{}, nil
}
func (s *coderStore) ResumeEncargo(context.Context, int64, int64, string, string) (store.Encargo, error) {
	return store.Encargo{}, nil
}
func (s *coderStore) DeliverEncargo(_ context.Context, _, _ int64, _, result string, links []string) (store.Encargo, error) {
	s.delivered, s.links = result, links
	return store.Encargo{}, nil
}
func (s *coderStore) GiveBackEncargo(_ context.Context, _, _ int64, _, note string) (store.Encargo, error) {
	s.gaveBack = note
	return store.Encargo{}, nil
}
func (s *coderStore) AddEncargoSpent(_ context.Context, _, micros int64) error {
	s.spent += micros
	return nil
}
func (s *coderStore) GetApp(_ context.Context, name string) (*store.App, error) {
	return &store.App{ID: 4, Name: name, OrgID: 1, Repo: "o/tienda", InstallationID: 7, DefaultBranch: "main"}, nil
}
func (s *coderStore) ListRuns(context.Context, int64, int) ([]*store.Run, error) { return s.runs, nil }
func (s *coderStore) GetRun(_ context.Context, id int64) (*store.Run, error) {
	for _, r := range s.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}
func (s *coderStore) GetDiagnosis(context.Context, int64) (store.Diagnosis, error) {
	return store.Diagnosis{Text: "La prueba espera «Hola»."}, nil
}

// repos is a repository in memory: each commit is kept, and the platform's
// CI answers with the next result for it.
type repos struct {
	files   map[string][]byte
	commits []map[string][]byte
	removed [][]string
	results []string
	st      *coderStore
	from    string // the sha the agent's branch was made from
}

func (r *repos) Files(context.Context, int64, string, string, int) (map[string][]byte, error) {
	out := map[string][]byte{}
	for k, v := range r.files {
		out[k] = v
	}
	return out, nil
}
func (r *repos) BranchSHA(_ context.Context, _ int64, _, branch string) (string, error) {
	return "head-of-" + branch, nil
}
func (r *repos) CreateBranch(_ context.Context, _ int64, _, _, sha string) error {
	r.from = sha
	return nil
}
func (r *repos) CommitFiles(_ context.Context, _ int64, _, branch, _ string, files map[string][]byte, remove []string) (string, error) {
	if branch != "encargo-12" {
		return "", fmt.Errorf("committed to %s", branch)
	}
	r.commits, r.removed = append(r.commits, files), append(r.removed, remove)
	sha := fmt.Sprintf("sha%04d", len(r.commits))
	if n := len(r.commits); n <= len(r.results) {
		run := &store.Run{ID: int64(900 + n), SHA: sha, Status: r.results[n-1]}
		if run.Status == store.RunFailed {
			run.Steps = []store.Step{{ID: "web:test", Status: store.RunFailed, Message: "exited with code 1"}}
		}
		r.st.runs = append(r.st.runs, run)
	}
	return sha, nil
}

// script answers each turn with the next tool calls, and checks what came back.
type script struct {
	t     *testing.T
	turns [][]ia.ToolCall
	seen  []string // each tool result, in order
	cost  int64
	n     int
}

func call(name string, input any) ia.ToolCall {
	b, _ := json.Marshal(input)
	return ia.ToolCall{ID: "toolu_" + name, Name: name, Input: b}
}

func (s *script) CompleteTools(_ context.Context, _ int64, source string, req ia.ToolRequest) (ia.ToolResponse, error) {
	if source != CoderSource || req.Model != WorkModel || len(req.Tools) == 0 {
		s.t.Fatalf("asked %s with %s", source, req.Model)
	}
	last := req.Turns[len(req.Turns)-1]
	var results []ia.ToolResult
	if json.Unmarshal(last.Content, &results) == nil {
		for _, r := range results {
			if r.Type == "tool_result" {
				s.seen = append(s.seen, r.Content)
			}
		}
	}
	if s.n >= len(s.turns) {
		return ia.ToolResponse{Response: ia.Response{CostMicros: s.cost}, Content: json.RawMessage(`[]`), Stop: "end_turn"}, nil
	}
	calls := s.turns[s.n]
	s.n++
	content, _ := json.Marshal([]map[string]string{{"type": "text", "text": "sigo"}})
	return ia.ToolResponse{Response: ia.Response{CostMicros: s.cost}, Content: content, Calls: calls, Stop: "tool_use"}, nil
}

func newCoder(st *coderStore, r *repos, m *script) *Coder {
	return &Coder{Store: st, Repos: r, Model: m, BaseURL: "https://rendimiento.example.com", Lang: "es", Log: slog.Default(),
		Poll: time.Millisecond, Logs: func(context.Context, int64, string) (string, error) {
			return "--- FAIL: TestSaludo\n    saludo_test.go:9: got \"Hi\", want \"Hola\"\n", nil
		}}
}

// The coding agent searches, edits, commits and reads why the run failed,
// fixes it, and delivers once a run passed, with the branch and the run.
func TestCoderDelivers(t *testing.T) {
	st := &coderStore{}
	r := &repos{st: st, files: map[string][]byte{"AGENTS.md": []byte("Pruebas con go test."), "saludo.go": []byte("package tienda\n\nconst Saludo = \"Hi\"\n")},
		results: []string{store.RunFailed, store.RunSucceeded}}
	est := int64(400_000)
	m := &script{t: t, cost: 50_000, turns: [][]ia.ToolCall{
		{call("search", map[string]string{"pattern": "Saludo ="})},
		{call("deliver", map[string]string{"summary": "antes de tiempo"})},
		{call("write_file", map[string]string{"path": "saludo.go", "content": "package tienda\n\nconst Saludo = \"Hola\"\n"}), call("delete_file", map[string]string{"path": "AGENTS.md"})},
		{call("commit_and_test", map[string]string{"message": "Saludar en español"})},
		{call("edit_file", map[string]string{"path": "saludo.go", "old_text": "\"Hola\"", "new_text": "\"¡Hola!\""})},
		{call("commit_and_test", map[string]string{"message": "Con signos"})},
		{call("deliver", map[string]string{"summary": "Saludo en español."})},
	}}
	c := newCoder(st, r, m)
	if err := c.Do(context.Background(), store.Encargo{ID: 12, OrgID: 1, App: "tienda", EstimateMicros: &est}); err != nil {
		t.Fatal(err)
	}
	if len(m.seen) < 6 || !strings.Contains(m.seen[0], "saludo.go:3:") || !strings.Contains(m.seen[1], "no passing run") {
		t.Fatalf("results %q", m.seen)
	}
	if fail := m.seen[4]; !strings.Contains(fail, "run #901 failed") || !strings.Contains(fail, "want \"Hola\"") || !strings.Contains(fail, "La prueba espera") {
		t.Fatalf("the failure it read: %s", fail)
	}
	if len(r.commits) != 2 || string(r.commits[0]["saludo.go"]) != "package tienda\n\nconst Saludo = \"Hola\"\n" || r.removed[0][0] != "AGENTS.md" ||
		string(r.commits[1]["saludo.go"]) != "package tienda\n\nconst Saludo = \"¡Hola!\"\n" || len(r.removed[1]) != 0 {
		t.Fatalf("commits %q, removed %v", r.commits, r.removed)
	}
	if !strings.Contains(st.delivered, "Saludo en español.") || !strings.Contains(st.delivered, "Gastó $0.35") ||
		len(st.links) != 2 || st.links[0] != "https://github.com/o/tienda/pull/new/encargo-12" || st.links[1] != "https://rendimiento.example.com/apps/tienda/runs/902" {
		t.Fatalf("delivered %q %v", st.delivered, st.links)
	}
	if st.spent != 350_000 || st.gaveBack != "" || len(st.waited) != 2 || !strings.Contains(st.handover, "lo entregó") {
		t.Fatalf("spent %d, gave back %q, waited %v, handover %q", st.spent, st.gaveBack, st.waited, st.handover)
	}
}

// At twice the estimate it stops: what it changed is committed on its
// branch, and the encargo goes to the person's own agent with a note.
func TestCoderStopsAtTwiceTheEstimate(t *testing.T) {
	st := &coderStore{}
	r := &repos{st: st, files: map[string][]byte{"saludo.go": []byte("const Saludo = \"Hi\"\n")}}
	est := int64(300_000)
	m := &script{t: t, cost: 250_000, turns: [][]ia.ToolCall{
		{call("edit_file", map[string]string{"path": "saludo.go", "old_text": "Hi", "new_text": "Hola"})},
		{call("read_file", map[string]string{"path": "saludo.go"})},
		{call("read_file", map[string]string{"path": "saludo.go"})},
	}}
	if err := newCoder(st, r, m).Do(context.Background(), store.Encargo{ID: 12, OrgID: 1, App: "tienda", EstimateMicros: &est}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.gaveBack, "doble de lo estimado") || !strings.Contains(st.gaveBack, "encargo-12") || st.delivered != "" {
		t.Fatalf("gave back %q, delivered %q", st.gaveBack, st.delivered)
	}
	if len(r.commits) != 1 || string(r.commits[0]["saludo.go"]) != "const Saludo = \"Hola\"\n" || m.n != 3 {
		t.Fatalf("commits %q after %d turns", r.commits, m.n)
	}
}

// An encargo about a run that failed on another branch is worked on a
// branch made from that one, and its pull request goes into it.
func TestCoderWorksFromTheFailedRunsBranch(t *testing.T) {
	run := int64(808)
	st := &coderStore{runID: &run, runs: []*store.Run{{ID: 808, AppID: 4, Branch: "propina-redonda", SHA: "abc", Status: store.RunFailed}}}
	r := &repos{st: st, files: map[string][]byte{"saludo.go": []byte("const Saludo = \"Hi\"\n")}, results: []string{store.RunSucceeded}}
	m := &script{t: t, cost: 10_000, turns: [][]ia.ToolCall{
		{call("edit_file", map[string]string{"path": "saludo.go", "old_text": "Hi", "new_text": "Hola"})},
		{call("commit_and_test", map[string]string{"message": "Saludo"})},
		{call("deliver", map[string]string{"summary": "Listo."})},
	}}
	if err := newCoder(st, r, m).Do(context.Background(), store.Encargo{ID: 12, OrgID: 1, App: "tienda", RunID: &run}); err != nil {
		t.Fatal(err)
	}
	if r.from != "head-of-propina-redonda" {
		t.Fatalf("made from %q, want the failed run's branch", r.from)
	}
	if len(st.links) == 0 || st.links[0] != "https://github.com/o/tienda/compare/propina-redonda...encargo-12?expand=1" {
		t.Fatalf("links %v", st.links)
	}
}
