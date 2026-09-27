package renovate

import (
	"strings"
	"testing"
	"time"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

func TestParse(t *testing.T) {
	log := strings.Join([]string{
		`{"name":"renovate","level":30,"msg":"Renovate started","time":"2026-09-27T05:00:01.000Z"}`,
		`{"level":30,"msg":"PR created","repository":"p0dxD/stockpulse","pr":42,"prTitle":"Update dependency lxml to v6.1.4","time":"2026-09-27T05:01:00.000Z"}`,
		`{"level":30,"msg":"PR automerged","repository":"p0dxD/stockpulse","pr":{"number":40},"prTitle":"Update fastapi","time":"2026-09-27T05:01:30.000Z"}`,
		`{"level":50,"msg":"Repository has unknown error","repository":"p0dxD/main_configs","err":{"message":"boom"},"time":"2026-09-27T05:02:00.000Z"}`,
		`{"level":30,"msg":"Repository finished","repository":"p0dxD/stockpulse","result":"done","time":"2026-09-27T05:03:00.000Z"}`,
		`plain text line`,
	}, "\n")
	text, res := Parse(strings.NewReader(log), []string{"p0dxD/stockpulse", "p0dxD/main_configs", "p0dxD/untouched"})
	sp := res["p0dxD/stockpulse"]
	if sp.Result != "done" || len(sp.PRs) != 1 || sp.PRs[0] != "Update dependency lxml to v6.1.4" || len(sp.Automerged) != 1 {
		t.Errorf("stockpulse = %+v", sp)
	}
	if mc := res["p0dxD/main_configs"]; len(mc.Errors) != 1 || !strings.Contains(mc.Errors[0], "boom") {
		t.Errorf("main_configs = %+v", mc)
	}
	if _, ok := res["p0dxD/untouched"]; !ok {
		t.Error("every requested repo gets a result")
	}
	for _, want := range []string{"05:01:00 INFO  [p0dxD/stockpulse] PR created: Update dependency lxml", "ERROR [p0dxD/main_configs] Repository has unknown error (boom)", "plain text line"} {
		if !strings.Contains(text, want) {
			t.Errorf("log missing %q:\n%s", want, text)
		}
	}
}

func TestSettings(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 27, 5, 0, 0, 0, time.UTC)
	if next := s.Next(from); !next.Equal(from.Add(24 * time.Hour)) {
		t.Errorf("next = %s", next)
	}
	for _, bad := range []Settings{
		{Schedule: "every day", Image: "x", Config: "x"},
		{Schedule: "@daily", Image: "", Config: "x"},
		{Schedule: "@daily", Image: "x", Config: "x", ExtraRepos: []string{"nope"}},
		{Schedule: "@daily", Image: "x", Config: " "},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be invalid", bad)
		}
	}
}

func TestPod(t *testing.T) {
	r := &Runner{Namespace: "rendimiento-builds", ExcludeNodes: []string{"podoi-ai"}}
	p := r.pod("renovate-1-2", nil, DefaultSettings(), `["o/a"]`, &gh.BotIdentity{Login: "app[bot]", Email: "1+app[bot]@users.noreply.github.com"}, nil, 55*time.Minute)
	env := map[string]string{}
	for _, e := range p.Spec.Containers[0].Env {
		env[e.Name] = e.Value
		if e.Name == "RENOVATE_TOKEN" && (e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil) {
			t.Error("the token must come from the secret")
		}
	}
	if env["RENOVATE_REPOSITORIES"] != `["o/a"]` || env["RENOVATE_GIT_AUTHOR"] != "app[bot] <1+app[bot]@users.noreply.github.com>" || env["LOG_FORMAT"] != "json" {
		t.Errorf("env = %v", env)
	}
	if *p.Spec.AutomountServiceAccountToken || *p.Spec.ActiveDeadlineSeconds != 3300 || p.Spec.Affinity == nil {
		t.Error("pod must have no API credentials, a deadline and the node exclusion")
	}
}

func TestParseKeepsErrorDetails(t *testing.T) {
	line := `{"level":20,"msg":"Unexpected GraphQL errors","repository":"o/a","errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration"}],"time":"2026-09-27T21:30:00.000Z"}`
	text, _ := Parse(strings.NewReader(line), []string{"o/a"})
	if !strings.Contains(text, "Resource not accessible by integration") {
		t.Errorf("details dropped: %s", text)
	}
}

func TestMissingPermissions(t *testing.T) {
	got := MissingPermissions(map[string]string{"contents": "write", "pull_requests": "write", "checks": "write", "metadata": "read"})
	if strings.Join(got, "|") != "Issues: Read and write|Commit statuses: Read-only" {
		t.Errorf("missing = %v", got)
	}
	if got := MissingPermissions(map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "statuses": "read", "checks": "write"}); len(got) != 0 {
		t.Errorf("all granted, got %v", got)
	}
}
