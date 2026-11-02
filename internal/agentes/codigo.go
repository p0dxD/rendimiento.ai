package agentes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// CoderName is the agent key the coding agent signs with, and CoderSource
// how it signs its calls to the model.
const (
	CoderName   = "agente de código"
	CoderSource = "agent: coder"
)

// CoderStore is what the coding agent reads and keeps.
type CoderStore interface {
	EncargoForCoder(ctx context.Context) (store.Encargo, error)
	EncargosOfSystemKey(ctx context.Context, name string) ([]store.Encargo, error)
	SystemKey(ctx context.Context, orgID int64, name string) (store.AgentKey, error)
	TakeCargo(ctx context.Context, keyID int64, purpose string) (store.Cargo, error)
	OpenCargo(ctx context.Context, keyID int64) (store.Cargo, error)
	HandOver(ctx context.Context, id int64, entrega, pendientes string) error
	TakeEncargo(ctx context.Context, id, keyID, cargoID int64, who string) (store.Encargo, error)
	WaitEncargo(ctx context.Context, id, keyID, runID int64, who string) (store.Encargo, error)
	ResumeEncargo(ctx context.Context, id, keyID int64, who, note string) (store.Encargo, error)
	DeliverEncargo(ctx context.Context, id, keyID int64, who, result string, links []string) (store.Encargo, error)
	GiveBackEncargo(ctx context.Context, id, keyID int64, who, note string) (store.Encargo, error)
	AddEncargoSpent(ctx context.Context, id, micros int64) error
	GetApp(ctx context.Context, name string) (*store.App, error)
	ListRuns(ctx context.Context, appID int64, limit int) ([]*store.Run, error)
	GetRun(ctx context.Context, id int64) (*store.Run, error)
	GetDiagnosis(ctx context.Context, runID int64) (store.Diagnosis, error)
}

// Repos is the apps' repositories on GitHub, as the platform's GitHub App.
type Repos interface {
	Files(ctx context.Context, installation int64, repo, ref string, maxFile int) (map[string][]byte, error)
	BranchSHA(ctx context.Context, installation int64, repo, branch string) (string, error)
	CreateBranch(ctx context.Context, installation int64, repo, branch, sha string) error
	CommitFiles(ctx context.Context, installation int64, repo, branch, message string, files map[string][]byte, remove []string) (string, error)
}

// Coder is the platform's coding agent: it does encargos on a branch of the
// app's repository, through GitHub, and tests them with the platform's own
// CI, the way a person's agent does; it never runs code itself, and never
// touches the default branch. It stops at twice the encargo's estimate and
// gives it to the person's own agent with what it did and what is left.
type Coder struct {
	Store CoderStore
	Repos Repos
	Model interface {
		CompleteTools(ctx context.Context, orgID int64, source string, req ia.ToolRequest) (ia.ToolResponse, error)
	}
	// Logs is a step's whole log.
	Logs func(ctx context.Context, runID int64, step string) (string, error)
	// BaseURL is the platform's address, for links to runs.
	BaseURL string
	// Lang is the language it writes to people in: es or en.
	Lang string
	Log  *slog.Logger
	// Poll is how often it looks at a run it waits for (15 seconds).
	Poll time.Duration
}

// Limits on the work: the turns, the most a run is waited for, and what a
// file, a read and a search may be.
const (
	maxTurns    = 80
	runWait     = 50 * time.Minute
	noRunWait   = 5 * time.Minute // for the push to start a run
	maxFileSize = 256 << 10
	readChars   = 40_000
	maxMatches  = 120
	minBudget   = 300_000 // 30 cents, for an encargo with no estimate
	defaultCost = 500_000 // what an encargo with no estimate is taken to cost
)

// Run does the encargos it should, one at a time, until ctx ends. First it
// gives back any it had when the platform stopped.
func (c *Coder) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	c.recover(ctx)
	for {
		e, err := c.Store.EncargoForCoder(ctx)
		switch {
		case err == nil:
			wctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
			if err := c.Do(wctx, e); err != nil {
				c.Log.Warn("coder: an encargo", "encargo", e.ID, "err", err)
			}
			cancel()
			continue // the next one at once
		case !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil:
			c.Log.Warn("coder: looking for encargos", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// recover gives back the encargos the coding agent had when the platform
// stopped (an update): their branch keeps what it committed.
func (c *Coder) recover(ctx context.Context) {
	list, err := c.Store.EncargosOfSystemKey(ctx, CoderName)
	if err != nil {
		return
	}
	for _, e := range list {
		note := c.say("La plataforma se reinició mientras lo hacía; lo que alcanzó a probar está en la rama %s.",
			"The platform restarted while it worked on it; what it got to test is on branch %s.", branchFor(e.ID))
		if _, err := c.Store.GiveBackEncargo(ctx, e.ID, *e.KeyID, CoderName, note); err == nil {
			if cg, err := c.Store.OpenCargo(ctx, *e.KeyID); err == nil {
				_ = c.Store.HandOver(ctx, cg.ID, note, "")
			}
		}
	}
}

func (c *Coder) say(es, en string, args ...any) string {
	if c.Lang == "es" {
		return fmt.Sprintf(es, args...)
	}
	return fmt.Sprintf(en, args...)
}

func branchFor(id int64) string { return fmt.Sprintf("encargo-%d", id) }

// job is one encargo being done: the repository as it is on the branch
// with what changed since the last commit, and what it spent.
type job struct {
	c        *Coder
	e        store.Encargo
	app      *store.App
	key      store.AgentKey
	branch   string
	from     string // the branch it was made from: the default one, or the failed run's
	files    map[string][]byte
	changed  map[string][]byte
	removed  map[string]bool
	spent    int64
	budget   int64
	lastSHA  string
	lastRun  *store.Run
	finished bool
	outcome  string // delivered, gave back
}

// Do does one encargo: takes it in a cargo, works on its branch, and
// delivers it or gives it back.
func (c *Coder) Do(ctx context.Context, e store.Encargo) error {
	if c.Poll == 0 {
		c.Poll = 15 * time.Second
	}
	key, err := c.Store.SystemKey(ctx, e.OrgID, CoderName)
	if err != nil {
		return err
	}
	cg, err := c.Store.TakeCargo(ctx, key.ID, c.say("Encargo %d: %s", "Encargo %d: %s", e.ID, e.Title))
	if errors.Is(err, store.ErrCargoOpen) {
		cg, err = c.Store.OpenCargo(ctx, key.ID)
	}
	if err != nil {
		return err
	}
	budget := 2 * int64(defaultCost)
	if e.EstimateMicros != nil {
		budget = max(2**e.EstimateMicros, minBudget)
	}
	if e, err = c.Store.TakeEncargo(ctx, e.ID, key.ID, cg.ID, CoderName); err != nil {
		return err
	}
	j := &job{c: c, e: e, key: key, branch: branchFor(e.ID), changed: map[string][]byte{}, removed: map[string]bool{}, budget: budget}
	err = j.work(ctx)
	if !j.finished {
		why := c.say("No pude terminarlo", "Could not finish it")
		if err != nil {
			why += ": " + err.Error()
		}
		j.giveBack(context.WithoutCancel(ctx), why)
	}
	entrega := c.say("Encargo %d (%s): %s. Gastó %s de %s.", "Encargo %d (%s): %s. Spent %s of %s.",
		e.ID, e.Title, j.outcome, ia.Dollars(j.spent), ia.Dollars(j.budget))
	return c.Store.HandOver(context.WithoutCancel(ctx), cg.ID, entrega, "")
}

func (j *job) giveBack(ctx context.Context, why string) {
	c := j.c
	note := why + ". " + c.say("Lo que hice está en la rama %s; gasté %s de %s.", "What I did is on branch %s; spent %s of %s.",
		j.branch, ia.Dollars(j.spent), ia.Dollars(j.budget))
	if _, err := c.Store.GiveBackEncargo(ctx, j.e.ID, j.key.ID, CoderName, note); err != nil {
		c.Log.Warn("coder: giving an encargo back", "encargo", j.e.ID, "err", err)
	}
	j.finished, j.outcome = true, c.say("lo devolvió a su agente", "gave it back to your agent")
}

// work prepares the branch and talks with the model until it delivers,
// gives up, or runs out of turns or budget.
func (j *job) work(ctx context.Context) error {
	c := j.c
	app, err := c.Store.GetApp(ctx, j.e.App)
	if err != nil || app.OrgID != j.e.OrgID {
		return fmt.Errorf("no app %q", j.e.App)
	}
	j.app = app
	// An encargo about a run that failed on another branch is about that
	// branch's code: the error lives there, not on the default branch.
	j.from = app.DefaultBranch
	if j.e.RunID != nil {
		if run, err := c.Store.GetRun(ctx, *j.e.RunID); err == nil && run.AppID == app.ID && run.Branch != "" {
			j.from = run.Branch
		}
	}
	base, err := c.Repos.BranchSHA(ctx, app.InstallationID, app.Repo, j.from)
	if err != nil {
		return err
	}
	if err := c.Repos.CreateBranch(ctx, app.InstallationID, app.Repo, j.branch, base); err != nil {
		return err
	}
	if j.files, err = c.Repos.Files(ctx, app.InstallationID, app.Repo, j.branch, maxFileSize); err != nil {
		return err
	}
	turns := []ia.Turn{ia.UserText(j.brief())}
	nudged := 0
	for turn := 0; turn < maxTurns; turn++ {
		if j.spent >= j.budget {
			j.commitLeft(ctx)
			j.giveBack(context.WithoutCancel(ctx), c.say("Llegué al doble de lo estimado", "Reached twice the estimate"))
			return nil
		}
		res, err := c.Model.CompleteTools(ctx, j.e.OrgID, CoderSource, ia.ToolRequest{System: j.system(), Tools: coderTools, Turns: turns,
			Model: WorkModel, MaxTokens: 16000})
		j.spent += res.CostMicros
		_ = c.Store.AddEncargoSpent(context.WithoutCancel(ctx), j.e.ID, res.CostMicros)
		if err != nil {
			return err
		}
		if res.Stop == "refusal" {
			return errors.New(c.say("el modelo se negó a seguir", "the model refused to go on"))
		}
		turns = append(turns, ia.Turn{Role: "assistant", Content: res.Content})
		if len(res.Calls) == 0 {
			if nudged++; nudged > 2 {
				return errors.New(c.say("el modelo dejó de usar sus herramientas", "the model stopped using its tools"))
			}
			turns = append(turns, ia.UserText("Keep going with your tools. When the run passed, call deliver; if you cannot finish, call give_up."))
			continue
		}
		var results []ia.ToolResult
		for _, call := range res.Calls {
			out, isErr := j.tool(ctx, call)
			results = append(results, ia.ToolResult{ToolUseID: call.ID, Content: out, IsError: isErr})
		}
		if j.finished {
			return nil
		}
		turns = append(turns, ia.UserResults(results))
	}
	j.commitLeft(ctx)
	return errors.New(c.say("se acabaron los turnos", "ran out of turns"))
}

// commitLeft keeps what changed since the last commit on the branch, for
// the person's agent to go on from.
func (j *job) commitLeft(ctx context.Context) {
	if len(j.changed) == 0 && len(j.removed) == 0 {
		return
	}
	_, _ = j.commit(context.WithoutCancel(ctx), j.c.say("Encargo %d: trabajo sin terminar", "Encargo %d: unfinished work", j.e.ID))
}

// brief is the first message: the encargo, the app, and the repository's
// own instructions for agents.
func (j *job) brief() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Encargo %d: %s\n\nApp: %s (repository %s, default branch %s). You work on branch %s, made from %s.\n",
		j.e.ID, j.e.Title, j.app.Name, j.app.Repo, j.app.DefaultBranch, j.branch, j.from)
	if j.from != j.app.DefaultBranch {
		fmt.Fprintf(&b, "That is the branch where run #%d failed: the code to fix is there, and your branch will be merged into it.\n", *j.e.RunID)
	}
	if j.e.Body != "" {
		fmt.Fprintf(&b, "\nThe detail:\n%s\n", truncate(j.e.Body, 8000))
	}
	if j.e.Comment != "" {
		fmt.Fprintf(&b, "\nA person sent it back before, saying:\n%s\n", j.e.Comment)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if f, ok := j.files[name]; ok {
			fmt.Fprintf(&b, "\nThe repository's %s:\n%s\n", name, truncate(string(f), 8000))
		}
	}
	fmt.Fprintf(&b, "\nThe repository has %d text files. Top level: %s\n", len(j.files), strings.Join(j.top(), ", "))
	return b.String()
}

// top lists the repository's top-level files and folders.
func (j *job) top() []string {
	seen := map[string]bool{}
	for p := range j.files {
		first, rest, dir := strings.Cut(p, "/")
		if dir && rest != "" {
			first += "/"
		}
		seen[first] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (j *job) system() string {
	lang := "English"
	if j.c.Lang == "es" {
		lang = "Mexican Spanish, using usted"
	}
	return fmt.Sprintf(`You are rendimiento's coding agent. rendimiento is a platform that builds, tests and deploys apps. A person asked for an encargo (a piece of work) on one app, and you do it on your own branch of the app's repository, through tools: you read and change files, then commit and the platform's CI tests your branch.

How to work:
- Read before you change: find the files involved (search, list_files, read_file) and follow the repository's conventions and its AGENTS.md or CLAUDE.md, if it has them.
- Make the smallest change that does what was asked, in the style of the code around it. Change or add tests when behavior changes.
- You cannot run code. commit_and_test commits your changes to your branch and waits for the platform's CI run on it, then tells you how it ended and, if it failed, the end of the failed steps' logs. Fix and test again until it passes.
- Never write secrets (passwords, tokens, keys) into files. Never touch any branch but yours.
- When the run passed, call deliver with a short summary for the person, in %s: what you changed and why, how it was tested, and what they should look at. If you cannot finish (the encargo is unclear, needs a decision, or keeps failing), call give_up with the reason and what is left, in %s.
- Work efficiently: this encargo was estimated at about %s, and you stop at %s. Prefer search and reading parts of files over reading everything.`,
		lang, lang, ia.Dollars(j.budget/2), ia.Dollars(j.budget))
}

var coderTools = []ia.Tool{
	{Name: "list_files", Description: "List the repository's files under a folder (all of them when it is empty), optionally only those whose name matches a glob like *.go.",
		InputSchema: schema(map[string]any{"folder": str("a folder, like web/src; empty for all"), "glob": str("a name pattern, like *.tsx")})},
	{Name: "read_file", Description: "Read a file as it is now on your branch (with your changes), whole or from line to line.",
		InputSchema: schema(map[string]any{"path": str("the file's path"), "from_line": num("first line, from 1"), "to_line": num("last line")}, "path")},
	{Name: "search", Description: "Search the repository's files for a regular expression (Go syntax); answers path:line: text, up to 120 matches.",
		InputSchema: schema(map[string]any{"pattern": str("a regular expression"), "folder": str("only under this folder")}, "pattern")},
	{Name: "write_file", Description: "Write a whole file (a new one, or replacing it).",
		InputSchema: schema(map[string]any{"path": str("the file's path"), "content": str("the whole content")}, "path", "content")},
	{Name: "edit_file", Description: "Replace one exact piece of a file with another; old_text must appear exactly once.",
		InputSchema: schema(map[string]any{"path": str("the file's path"), "old_text": str("the exact text to replace"), "new_text": str("what replaces it")}, "path", "old_text", "new_text")},
	{Name: "delete_file", Description: "Delete a file.",
		InputSchema: schema(map[string]any{"path": str("the file's path")}, "path")},
	{Name: "commit_and_test", Description: "Commit your changes to your branch with a message, and wait for the platform's CI run on it (minutes): how it ended and, if it failed, the end of the failed steps' logs.",
		InputSchema: schema(map[string]any{"message": str("the commit message, in the repository's style")}, "message")},
	{Name: "deliver", Description: "Hand the encargo over to the person, once the latest run passed.",
		InputSchema: schema(map[string]any{"summary": str("what you changed and why, how it was tested, what to look at")}, "summary")},
	{Name: "give_up", Description: "Give the encargo back to the person's own agent, when you cannot finish it.",
		InputSchema: schema(map[string]any{"reason": str("why, and what is left")}, "reason")},
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
func num(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }

// tool runs one tool call: its answer, and whether it is an error.
func (j *job) tool(ctx context.Context, call ia.ToolCall) (string, bool) {
	var in struct {
		Folder, Glob, Path, Pattern, Content, Message, Summary, Reason string
		OldText                                                        string `json:"old_text"`
		NewText                                                        string `json:"new_text"`
		FromLine                                                       int    `json:"from_line"`
		ToLine                                                         int    `json:"to_line"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return "the input is not valid JSON: " + err.Error(), true
	}
	in.Path = strings.TrimPrefix(path.Clean("/"+in.Path), "/")
	switch call.Name {
	case "list_files":
		return j.list(in.Folder, in.Glob), false
	case "read_file":
		return j.read(in.Path, in.FromLine, in.ToLine)
	case "search":
		return j.search(in.Pattern, in.Folder)
	case "write_file":
		if in.Path == "" || strings.HasPrefix(in.Path, ".git/") {
			return "not a file you may write", true
		}
		j.changed[in.Path], j.removed[in.Path] = []byte(in.Content), false
		return fmt.Sprintf("wrote %s (%d bytes)", in.Path, len(in.Content)), false
	case "edit_file":
		f, ok := j.file(in.Path)
		if !ok {
			return "no such file: " + in.Path, true
		}
		if n := strings.Count(string(f), in.OldText); n != 1 || in.OldText == "" {
			return fmt.Sprintf("old_text appears %d times in %s; it must appear exactly once", n, in.Path), true
		}
		j.changed[in.Path] = []byte(strings.Replace(string(f), in.OldText, in.NewText, 1))
		return "edited " + in.Path, false
	case "delete_file":
		if _, ok := j.file(in.Path); !ok {
			return "no such file: " + in.Path, true
		}
		delete(j.changed, in.Path)
		j.removed[in.Path] = true
		return "deleted " + in.Path, false
	case "commit_and_test":
		return j.commitAndTest(ctx, in.Message)
	case "deliver":
		return j.deliver(ctx, in.Summary)
	case "give_up":
		j.commitLeft(ctx)
		j.giveBack(context.WithoutCancel(ctx), in.Reason)
		return "given back", false
	}
	return "no such tool: " + call.Name, true
}

// file is a file as it is now: with the changes, without the removed.
func (j *job) file(p string) ([]byte, bool) {
	if j.removed[p] {
		return nil, false
	}
	if f, ok := j.changed[p]; ok {
		return f, true
	}
	f, ok := j.files[p]
	return f, ok
}

// paths are the files as they are now, sorted.
func (j *job) paths() []string {
	seen := map[string]bool{}
	for p := range j.files {
		seen[p] = true
	}
	for p := range j.changed {
		seen[p] = true
	}
	out := []string{}
	for p := range seen {
		if !j.removed[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func under(p, folder string) bool {
	folder = strings.Trim(folder, "/")
	return folder == "" || folder == "." || strings.HasPrefix(p, folder+"/")
}

func (j *job) list(folder, glob string) string {
	var out []string
	for _, p := range j.paths() {
		if !under(p, folder) {
			continue
		}
		if ok, _ := path.Match(glob, path.Base(p)); glob != "" && !ok {
			continue
		}
		out = append(out, p)
	}
	if len(out) > 400 {
		return strings.Join(out[:400], "\n") + fmt.Sprintf("\n… and %d more; narrow it with folder or glob", len(out)-400)
	}
	if len(out) == 0 {
		return "no files"
	}
	return strings.Join(out, "\n")
}

func (j *job) read(p string, from, to int) (string, bool) {
	f, ok := j.file(p)
	if !ok {
		return "no such file: " + p, true
	}
	if from <= 0 && to <= 0 {
		if len(f) > readChars {
			return string(f[:readChars]) + fmt.Sprintf("\n… the file has %d bytes; read the rest with from_line and to_line", len(f)), false
		}
		return string(f), false
	}
	lines := strings.Split(string(f), "\n")
	from = max(from, 1)
	if to <= 0 || to > len(lines) {
		to = len(lines)
	}
	if from > to {
		return fmt.Sprintf("the file has %d lines", len(lines)), true
	}
	text := strings.Join(lines[from-1:to], "\n")
	if len(text) > readChars {
		text = text[:readChars] + "\n… (cut)"
	}
	return text, false
}

func (j *job) search(pattern, folder string) (string, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "not a valid regular expression: " + err.Error(), true
	}
	var out []string
	for _, p := range j.paths() {
		if !under(p, folder) {
			continue
		}
		f, _ := j.file(p)
		for i, line := range strings.Split(string(f), "\n") {
			if re.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d: %s", p, i+1, truncate(strings.TrimSpace(line), 200)))
				if len(out) >= maxMatches {
					return strings.Join(out, "\n") + "\n… more; narrow the pattern or the folder", false
				}
			}
		}
	}
	if len(out) == 0 {
		return "no matches", false
	}
	return strings.Join(out, "\n"), false
}

// commit commits what changed to the branch.
func (j *job) commit(ctx context.Context, message string) (string, error) {
	var remove []string
	for p, gone := range j.removed {
		if gone {
			remove = append(remove, p)
		}
	}
	sort.Strings(remove)
	sha, err := j.c.Repos.CommitFiles(ctx, j.app.InstallationID, j.app.Repo, j.branch, message, j.changed, remove)
	if err != nil {
		return "", err
	}
	for p, f := range j.changed {
		j.files[p] = f
	}
	for _, p := range remove {
		delete(j.files, p)
	}
	j.changed, j.removed, j.lastSHA, j.lastRun = map[string][]byte{}, map[string]bool{}, sha, nil
	return sha, nil
}

// commitAndTest commits and waits for the platform's run on the commit.
func (j *job) commitAndTest(ctx context.Context, message string) (string, bool) {
	c := j.c
	if len(j.changed) == 0 && len(j.removed) == 0 {
		return "nothing changed since the last commit", true
	}
	sha, err := j.commit(ctx, strings.TrimSpace(message)+"\n\n"+c.say("Encargo %d, hecho por el agente de código de rendimiento.", "Encargo %d, done by rendimiento's coding agent.", j.e.ID))
	if err != nil {
		return "the commit failed: " + err.Error(), true
	}
	run, err := j.waitRun(ctx, sha)
	if err != nil {
		return fmt.Sprintf("committed %s, but: %s", sha[:7], err.Error()), true
	}
	j.lastRun = run
	if run.Status == store.RunSucceeded {
		return fmt.Sprintf("committed %s; run #%d passed. You may deliver.", sha[:7], run.ID), false
	}
	return j.failure(ctx, sha, run), true
}

// waitRun waits for the run on a commit to end, marking the encargo as
// waiting on it meanwhile.
func (j *job) waitRun(ctx context.Context, sha string) (*store.Run, error) {
	c := j.c
	start := time.Now()
	var run *store.Run
	for time.Since(start) < runWait {
		if run == nil && time.Since(start) > noRunWait {
			break
		}
		if run == nil {
			runs, err := c.Store.ListRuns(ctx, j.app.ID, 30)
			if err != nil {
				return nil, err
			}
			for _, r := range runs {
				if r.SHA == sha {
					run = r
					_, _ = c.Store.WaitEncargo(ctx, j.e.ID, j.key.ID, r.ID, CoderName)
					break
				}
			}
		} else {
			r, err := c.Store.GetRun(ctx, run.ID)
			if err != nil {
				return nil, err
			}
			run = r
		}
		if run != nil && run.Status != store.RunQueued && run.Status != store.RunRunning {
			_, _ = c.Store.ResumeEncargo(ctx, j.e.ID, j.key.ID, CoderName, fmt.Sprintf("#%d: %s", run.ID, run.Status))
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.Poll):
		}
	}
	if run == nil {
		return nil, errors.New("no run started for the commit; the app may not build this branch")
	}
	_, _ = c.Store.ResumeEncargo(ctx, j.e.ID, j.key.ID, CoderName, "")
	return nil, fmt.Errorf("run #%d did not end in %s", run.ID, runWait)
}

// failure is how a run failed: its failed steps with the end of their logs,
// and the diagnostician's explanation if it has one.
func (j *job) failure(ctx context.Context, sha string, run *store.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "committed %s; run #%d %s.\n", sha[:7], run.ID, run.Status)
	shown := 0
	for _, s := range run.Steps {
		if s.Status != store.RunFailed || shown == 3 {
			continue
		}
		shown++
		fmt.Fprintf(&b, "\nStep %s failed: %s\n", s.ID, s.Message)
		if j.c.Logs != nil {
			if text, err := j.c.Logs(ctx, run.ID, s.ID); err == nil {
				lines := strings.Split(strings.TrimRight(ansi.ReplaceAllString(text, ""), "\n"), "\n")
				b.WriteString(strings.Join(lines[max(0, len(lines)-80):], "\n"))
				b.WriteString("\n")
			}
		}
	}
	if d, err := j.c.Store.GetDiagnosis(ctx, run.ID); err == nil && d.Text != "" {
		fmt.Fprintf(&b, "\nThe platform's diagnostician says:\n%s\n", d.Text)
	}
	return truncate(b.String(), 20000)
}

// deliver hands the encargo over, once the latest commit's run passed.
func (j *job) deliver(ctx context.Context, summary string) (string, bool) {
	c := j.c
	if len(j.changed) > 0 || len(j.removed) > 0 {
		return "you have changes not committed; commit_and_test them first", true
	}
	if j.lastRun == nil || j.lastRun.Status != store.RunSucceeded {
		return "the latest commit has no passing run yet; commit_and_test first", true
	}
	result := strings.TrimSpace(summary) + "\n\n" + c.say("Gastó %s (estimado: %s).", "Spent %s (estimated: %s).", ia.Dollars(j.spent), ia.Dollars(j.budget/2))
	// The pull request goes into the branch it was made from.
	links := []string{fmt.Sprintf("https://github.com/%s/pull/new/%s", j.app.Repo, j.branch)}
	if j.from != j.app.DefaultBranch {
		links[0] = fmt.Sprintf("https://github.com/%s/compare/%s...%s?expand=1", j.app.Repo, j.from, j.branch)
	}
	if c.BaseURL != "" {
		links = append(links, fmt.Sprintf("%s/apps/%s/runs/%d", strings.TrimRight(c.BaseURL, "/"), j.app.Name, j.lastRun.ID))
	}
	if _, err := c.Store.DeliverEncargo(context.WithoutCancel(ctx), j.e.ID, j.key.ID, CoderName, result, links); err != nil {
		return "could not deliver: " + err.Error(), true
	}
	j.finished, j.outcome = true, c.say("lo entregó", "delivered it")
	return "delivered", false
}
