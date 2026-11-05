package agentes

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/ia"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// EstimateSource is how the estimator signs its calls to the model.
const EstimateSource = "agent: estimator"

// WorkModel is the model the platform's AI would do an encargo with, and
// what an estimate is priced at. An encargo's work grows faster than its
// difficulty (more files read, more tries): at difficulty d it reads about
// 1,000·d² tokens at full price (most of what it reads comes from the cache,
// at a tenth) and writes about 100·d². The first real encargo (8, a one-line
// fix rated 5) read 57,000 tokens, wrote 2,100 and cost $0.026; this table
// prices a 5 at $0.075, a 2 at $0.012 and a 10 at $0.30.
const (
	WorkModel     = "claude-sonnet-5-5"
	workTokensIn  = 1_000
	workTokensOut = 100
)

// EstimatorStore is what the estimator reads and keeps.
type EstimatorStore interface {
	EncargosToEstimate(ctx context.Context, limit int) ([]store.Encargo, error)
	SaveEstimate(ctx context.Context, id int64, difficulty int, micros int64, reason, route string) error
	AISettingsOf(ctx context.Context, orgID int64) (store.AISettings, error)
}

// Estimator rates each new encargo: how hard it is (1 to 10), what the
// platform's AI would spend doing it, and so who should: the platform's AI
// when it is under the organization's amount, the person's own agent (paid
// by the month, not by the call) when it is over.
type Estimator struct {
	Store EstimatorStore
	Model interface {
		Complete(ctx context.Context, orgID int64, source string, req ia.Request) (ia.Response, error)
		Price(model string, in, out int64) int64
	}
	// Lang is the language of its reasons: es (Mexican Spanish) or en.
	Lang string
	Log  *slog.Logger
}

// Run estimates new encargos every 30 seconds until ctx ends.
func (e *Estimator) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		list, err := e.Store.EncargosToEstimate(ctx, 5)
		if err != nil && ctx.Err() == nil {
			e.Log.Warn("estimator: listing encargos", "err", err)
		}
		for _, enc := range list {
			if ctx.Err() != nil {
				return
			}
			ectx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := e.Estimate(ectx, enc); err != nil {
				e.Log.Warn("estimator: estimating an encargo", "encargo", enc.ID, "err", err)
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

// Cost is what the platform's AI would spend on an encargo of difficulty d.
func (e *Estimator) Cost(d int) int64 {
	return e.Model.Price(WorkModel, int64(workTokensIn*d*d), int64(workTokensOut*d*d))
}

var estimateJSON = regexp.MustCompile(`"difficulty"\s*:\s*(\d+)\s*,\s*"reason"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// Estimate rates one encargo and keeps the answer; when the model cannot
// answer, it keeps why, so the encargo is not asked about again.
func (e *Estimator) Estimate(ctx context.Context, enc store.Encargo) error {
	set, err := e.Store.AISettingsOf(ctx, enc.OrgID)
	if err != nil {
		return err
	}
	text := "Title: " + enc.Title + "\n"
	if enc.App != "" {
		text += "App: " + enc.App + "\n"
	}
	if enc.Body != "" {
		text += "\n" + truncate(enc.Body, 8000)
	}
	res, err := e.Model.Complete(ctx, enc.OrgID, EstimateSource, ia.Request{System: e.system(), MaxTokens: 1000,
		Messages: []ia.Message{{Role: "user", Content: text}}})
	if err != nil {
		return e.Store.SaveEstimate(context.WithoutCancel(ctx), enc.ID, 0, 0, err.Error(), "")
	}
	m := estimateJSON.FindStringSubmatch(res.Text)
	d := 0
	if m != nil {
		d, _ = strconv.Atoi(m[1])
	}
	if d < 1 || d > 10 {
		return e.Store.SaveEstimate(ctx, enc.ID, 0, 0, "the model gave no difficulty: "+truncate(strings.TrimSpace(res.Text), 200), "")
	}
	reason, _ := strconv.Unquote(`"` + m[2] + `"`)
	cost, route := e.Cost(d), store.RouteLocal
	if cost <= set.EncargoAutoMicros {
		route = store.RoutePlatform
	}
	return e.Store.SaveEstimate(ctx, enc.ID, d, cost, truncate(strings.TrimSpace(reason), 300), route)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (e *Estimator) system() string {
	lang := "English"
	if e.Lang == "es" {
		lang = "Mexican Spanish (usted)"
	}
	return fmt.Sprintf(`You rate encargos for rendimiento, a platform that builds, tests and deploys apps. An encargo is work a person asks of a coding agent: changing an app's code, its configuration or its tests. Rate how hard it is for a coding agent, from 1 to 10:
1-2: a small, clear change in one place (a text, a value, a one-line fix).
3-4: a change in a few files that is clear what to do.
5-6: a feature or a fix that needs reading around, several files and new tests.
7-8: a change across parts of the system, or a cause still to be found.
9-10: a design change, a migration, or something vague and large.
Rate the size of the fix, not the length of the detail: a failed run with its log, and its cause already explained (a wrong formula in one function), is a clear small fix, 2 or 3, however long the log.
When what to do is too vague to know, rate it higher, not lower.
Answer only with JSON: {"difficulty": <1-10>, "reason": "<one sentence in %s>"}`, lang)
}
