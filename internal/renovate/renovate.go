// Package renovate is the Renovate add-on: it keeps the dependencies of the
// apps it is switched on for up to date by running Renovate on a schedule.
// Each run is a pod in the isolated build namespace, authenticated with a
// fresh one-hour GitHub App installation token, so there is no personal
// access token to expire or leak. Renovate's branches are built and checked
// by rendimiento like any other branch, and patch/minor updates auto-merge
// once those checks pass (per the configured rules).
package renovate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

const Name = "renovate"

// Settings are the add-on's configuration.
type Settings struct {
	// Schedule is a cron expression in UTC ("0 5 * * *" = daily 05:00 UTC).
	Schedule string `json:"schedule"`
	Image    string `json:"image"`
	// ExtraRepos are repositories that are not rendimiento apps (owner/name).
	ExtraRepos []string `json:"extraRepos"`
	// Config is Renovate's global configuration (config.js). Token,
	// repositories and the bot identity are set by rendimiento.
	Config string `json:"config"`
	// LogLevel is info (default) or debug, to see why a repository fails.
	LogLevel string `json:"logLevel,omitempty"`
}

// DefaultSettings are the add-on's settings before anyone changes them.
func DefaultSettings() Settings {
	return Settings{Schedule: "0 5 * * *", Image: "renovate/renovate:44", ExtraRepos: []string{}, Config: DefaultConfig}
}

// DefaultConfig is a sensible starting point: recommended presets, patch and
// minor updates auto-merged once checks pass, language runtimes in Docker
// images left for review, and images rendimiento builds left alone.
const DefaultConfig = `module.exports = {
  platform: 'github',
  onboarding: true,
  onboardingConfig: { extends: ['config:recommended'] },
  extends: ['config:recommended', ':dependencyDashboard'],
  prConcurrentLimit: 5,
  prHourlyLimit: 2,
  packageRules: [
    // Patch and minor updates merge themselves once rendimiento's checks pass.
    { matchUpdateTypes: ['patch', 'minor'], automerge: true },
    // A "minor" Docker tag of a language runtime (python 3.12 -> 3.13) is a
    // feature release that can break builds: review it by hand.
    { matchDatasources: ['docker'], matchUpdateTypes: ['minor', 'major'], automerge: false },
  ],
}
`

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Validate checks the schedule, image, repositories and config.
func (s Settings) Validate() error {
	if _, err := parser.Parse(s.Schedule); err != nil {
		return fmt.Errorf("schedule %q is not a cron expression: %w", s.Schedule, err)
	}
	if strings.TrimSpace(s.Image) == "" {
		return errors.New("image is required")
	}
	for _, r := range s.ExtraRepos {
		owner, name, ok := strings.Cut(r, "/")
		if !ok || owner == "" || name == "" || strings.ContainsAny(r, " \t") {
			return fmt.Errorf("repository %q must look like owner/name", r)
		}
	}
	switch s.LogLevel {
	case "", "info", "debug":
	default:
		return fmt.Errorf("log level %q must be info or debug", s.LogLevel)
	}
	if strings.TrimSpace(s.Config) == "" {
		return errors.New("config is required (use the default if unsure)")
	}
	return nil
}

// Next is the first scheduled time after t.
func (s Settings) Next(t time.Time) time.Time {
	sched, err := parser.Parse(s.Schedule)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(t)
}

// Load returns the add-on's state, with defaults if it was never set up.
func Load(ctx context.Context, st *store.Store) (enabled bool, s Settings, row *store.Addon, err error) {
	row, err = st.GetAddon(ctx, Name)
	if errors.Is(err, store.ErrNotFound) {
		return false, DefaultSettings(), nil, nil
	}
	if err != nil {
		return false, Settings{}, nil, err
	}
	s = DefaultSettings()
	if err := json.Unmarshal(row.Settings, &s); err != nil {
		return false, Settings{}, nil, err
	}
	return row.Enabled, s, row, nil
}

var ErrBusy = errors.New("a Renovate run is already in progress")

// Runner starts Renovate runs on schedule or on request, one at a time.
type Runner struct {
	Kube         kubernetes.Interface
	Namespace    string // the build namespace (egress to the internet only)
	ExcludeNodes []string
	Store        *store.Store
	GitHub       *gh.Holder
	Log          *slog.Logger
	// Timeout bounds a run; installation tokens expire after an hour.
	Timeout time.Duration

	mu      sync.Mutex
	running bool
}

// Loop starts scheduled runs until ctx ends. Only one process may run it.
func (r *Runner) Loop(ctx context.Context) {
	if n, err := r.Store.FailRunningAddonRuns(ctx); err == nil && n > 0 {
		r.Log.Warn("renovate: closed runs interrupted by a restart", "runs", n)
	}
	r.cleanup(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if err := r.tick(ctx, time.Now()); err != nil {
			r.Log.Warn("renovate: schedule check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick starts a scheduled Renovate run when its time has come. Only one
// replica wins each scheduled time (ClaimSchedule); right after Renovate is
// switched on, the schedule starts counting instead of running at once.
func (r *Runner) tick(ctx context.Context, now time.Time) error {
	enabled, s, row, err := Load(ctx, r.Store)
	if err != nil || !enabled || row == nil {
		return err
	}
	if row.LastScheduled == nil {
		// First check after switching on: start counting from now rather
		// than running immediately.
		_, err := r.Store.ClaimSchedule(ctx, Name, nil, now)
		return err
	}
	if next := s.Next(*row.LastScheduled); next.IsZero() || now.Before(next) {
		return nil
	}
	won, err := r.Store.ClaimSchedule(ctx, Name, row.LastScheduled, now)
	if err != nil || !won {
		return err
	}
	_, err = r.Start(ctx, "schedule")
	if errors.Is(err, ErrBusy) {
		return nil
	}
	return err
}

type target struct {
	installation int64
	repos        []string
}

// Repos lists what a run would cover: apps with Renovate switched on, plus
// the extra repositories.
func (r *Runner) Repos(ctx context.Context, s Settings) ([]string, error) {
	apps, err := r.Store.AppsWithAddon(ctx, Name)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range apps {
		if !seen[strings.ToLower(a.Repo)] {
			seen[strings.ToLower(a.Repo)] = true
			out = append(out, a.Repo)
		}
	}
	for _, repo := range s.ExtraRepos {
		if !seen[strings.ToLower(repo)] {
			seen[strings.ToLower(repo)] = true
			out = append(out, repo)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Start begins a run in the background and returns its record.
func (r *Runner) Start(ctx context.Context, trigger string) (*store.AddonRun, error) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil, ErrBusy
	}
	r.running = true
	r.mu.Unlock()
	release := func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }

	_, s, _, err := Load(ctx, r.Store)
	if err != nil {
		release()
		return nil, err
	}
	repos, err := r.Repos(ctx, s)
	if err != nil {
		release()
		return nil, err
	}
	if len(repos) == 0 {
		release()
		return nil, errors.New("no repositories: switch Renovate on for an app or add an extra repository")
	}
	targets, err := r.targets(ctx, repos)
	if err != nil {
		release()
		return nil, err
	}
	run := &store.AddonRun{Addon: Name, Trigger: trigger, Repos: repos}
	if err := r.Store.CreateAddonRun(ctx, run); err != nil {
		release()
		return nil, err
	}
	go func() {
		defer release()
		r.execute(context.Background(), run, s, targets)
	}()
	return run, nil
}

// targets groups repositories by the GitHub App installation that can reach them.
func (r *Runner) targets(ctx context.Context, repos []string) ([]target, error) {
	app, err := r.GitHub.Get()
	if err != nil {
		return nil, err
	}
	insts, err := app.Installations(ctx)
	if err != nil {
		return nil, err
	}
	byOwner := map[string]int64{}
	for _, i := range insts {
		byOwner[strings.ToLower(i.Account.Login)] = i.ID
	}
	groups := map[int64][]string{}
	var order []int64
	for _, repo := range repos {
		owner, _, _ := strings.Cut(repo, "/")
		id, ok := byOwner[strings.ToLower(owner)]
		if !ok {
			return nil, fmt.Errorf("the GitHub App is not installed on %s (needed for %s)", owner, repo)
		}
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], repo)
	}
	var out []target
	for _, id := range order {
		out = append(out, target{installation: id, repos: groups[id]})
	}
	return out, nil
}

// execute is one Renovate run: a pod per account it covers, one after
// another, then the run's summary and results are saved (55 minutes at most
// by default).
func (r *Runner) execute(ctx context.Context, run *store.AddonRun, s Settings, targets []target) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 55 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := map[string]json.RawMessage{}
	var logs strings.Builder
	var notes []string
	status := "succeeded"
	for _, t := range targets {
		res, log, note, err := r.runPod(ctx, run.ID, t, s, timeout)
		logs.WriteString(log)
		for repo, v := range res {
			b, _ := json.Marshal(v)
			results[repo] = b
		}
		if note != "" {
			notes = append(notes, note)
		}
		if err != nil {
			status = "failed"
			notes = append(notes, err.Error())
		}
	}
	msg := summarize(results)
	if len(notes) > 0 {
		msg = strings.Join(append(notes, msg), "; ")
	}
	if err := r.Store.FinishAddonRun(context.Background(), run.ID, status, msg, results, truncate(logs.String(), 512<<10)); err != nil {
		r.Log.Error("renovate: record run", "run", run.ID, "err", err)
	}
	r.Log.Info("renovate run finished", "run", run.ID, "status", status, "message", msg)
}

// required are the GitHub App permissions Renovate needs: it reads each
// repository's issues first thing (the dependency dashboard), and checks
// commit statuses before auto-merging.
var required = []struct{ key, level, label string }{
	{"contents", "write", "Contents: Read and write"},
	{"pull_requests", "write", "Pull requests: Read and write"},
	{"issues", "write", "Issues: Read and write"},
	{"statuses", "read", "Commit statuses: Read-only"},
	{"checks", "read", "Checks: Read-only"},
}

// MissingPermissions lists what the installation still lacks.
func MissingPermissions(granted map[string]string) []string {
	var out []string
	for _, r := range required {
		g := granted[r.key]
		if g == "write" || g == r.level {
			continue
		}
		out = append(out, r.label)
	}
	return out
}

// Problems explains what would stop a run right now, such as permissions
// the GitHub App's installations have not been granted yet.
func (r *Runner) Problems(ctx context.Context) []string {
	app, err := r.GitHub.Get()
	if err != nil {
		return []string{"The GitHub App is not set up yet."}
	}
	insts, err := app.Installations(ctx)
	if err != nil {
		return []string{"Could not list the GitHub App's installations: " + err.Error()}
	}
	var out []string
	for _, i := range insts {
		perms, err := app.InstallationPermissions(ctx, i.ID)
		if err != nil {
			out = append(out, fmt.Sprintf("Could not read permissions for %s: %v", i.Account.Login, err))
			continue
		}
		if missing := MissingPermissions(perms); len(missing) > 0 {
			out = append(out, fmt.Sprintf("The GitHub App on %s is missing %s.", i.Account.Login, strings.Join(missing, ", ")))
		}
	}
	return out
}

// RepoResult is what one run did to one repository.
type RepoResult struct {
	Result     string   `json:"result,omitempty"` // Renovate's own: done, onboarded, disabled…
	PRs        []string `json:"prs,omitempty"`    // PRs created
	Automerged []string `json:"automerged,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

// summarize is a run's one line: pull requests opened, merged by itself,
// and repositories that failed.
func summarize(results map[string]json.RawMessage) string {
	prs, merged, failed := 0, 0, 0
	for _, raw := range results {
		var r RepoResult
		_ = json.Unmarshal(raw, &r)
		prs += len(r.PRs)
		merged += len(r.Automerged)
		if len(r.Errors) > 0 {
			failed++
		}
	}
	msg := fmt.Sprintf("%d repositories, %d PRs opened, %d merged", len(results), prs, merged)
	if failed > 0 {
		msg += fmt.Sprintf(", %d with errors", failed)
	}
	return msg
}

// runPod runs Renovate for one account's repositories in a pod, with a
// fresh token of that installation (as the App's bot), and reads back what
// it did from its log.
func (r *Runner) runPod(ctx context.Context, runID int64, t target, s Settings, timeout time.Duration) (map[string]*RepoResult, string, string, error) {
	app, err := r.GitHub.Get()
	if err != nil {
		return nil, "", "", err
	}
	token, err := app.FreshInstallationToken(ctx, t.installation)
	if err != nil {
		return nil, "", "", fmt.Errorf("installation token: %w", err)
	}
	bot, err := app.Bot(ctx)
	if err != nil {
		return nil, "", "", fmt.Errorf("bot identity: %w", err)
	}
	var note string
	extraEnv := []corev1.EnvVar{}
	perms, err := app.InstallationPermissions(ctx, t.installation)
	if err != nil {
		return nil, "", "", fmt.Errorf("read the GitHub App's permissions: %w", err)
	}
	if missing := MissingPermissions(perms); len(missing) > 0 {
		return nil, "", "", fmt.Errorf("the GitHub App needs more permissions: %s", strings.Join(missing, ", "))
	}
	reposJSON, _ := json.Marshal(t.repos)
	name := fmt.Sprintf("renovate-%d-%d", runID, t.installation)
	labels := map[string]string{"app.kubernetes.io/managed-by": "rendimiento", "rendimiento.ai/addon": Name}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.Namespace, Labels: labels},
		StringData: map[string]string{"token": token, "config.js": s.Config},
	}
	if _, err := r.Kube.CoreV1().Secrets(r.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return nil, "", note, fmt.Errorf("create secret: %w", err)
	}
	defer func() {
		bg := context.Background()
		_ = r.Kube.CoreV1().Pods(r.Namespace).Delete(bg, name, metav1.DeleteOptions{})
		_ = r.Kube.CoreV1().Secrets(r.Namespace).Delete(bg, name, metav1.DeleteOptions{})
	}()
	pod := r.pod(name, labels, s, string(reposJSON), bot, extraEnv, timeout)
	if _, err := r.Kube.CoreV1().Pods(r.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, "", note, fmt.Errorf("create pod: %w", err)
	}
	final, err := r.wait(ctx, name)
	log, results := r.collect(name, t.repos)
	if err != nil {
		return results, log, note, err
	}
	if final.Status.Phase != corev1.PodSucceeded {
		return results, log, note, fmt.Errorf("renovate exited with an error (see the log)")
	}
	return results, log, note, nil
}

// pod is the Renovate pod: its configuration and token from a secret of
// the same name, the repositories to look at, and a deadline.
func (r *Runner) pod(name string, labels map[string]string, s Settings, repos string, bot *gh.BotIdentity, extraEnv []corev1.EnvVar, timeout time.Duration) *corev1.Pod {
	deadline := int64(timeout.Seconds())
	env := append([]corev1.EnvVar{
		{Name: "RENOVATE_CONFIG_FILE", Value: "/config/config.js"},
		{Name: "RENOVATE_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: "token"}}},
		{Name: "RENOVATE_PLATFORM", Value: "github"},
		{Name: "RENOVATE_AUTODISCOVER", Value: "false"},
		{Name: "RENOVATE_REPOSITORIES", Value: repos},
		{Name: "RENOVATE_USERNAME", Value: bot.Login},
		{Name: "RENOVATE_GIT_AUTHOR", Value: bot.Login + " <" + bot.Email + ">"},
		// Commit through the API: commits show as verified and need no git credentials.
		{Name: "RENOVATE_PLATFORM_COMMIT", Value: "enabled"},
		{Name: "LOG_LEVEL", Value: firstNonEmpty(s.LogLevel, "info")},
		{Name: "LOG_FORMAT", Value: "json"},
	}, extraEnv...)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.Namespace, Labels: labels},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        &deadline,
			AutomountServiceAccountToken: ptr(false),
			Containers: []corev1.Container{{
				Name:  "renovate",
				Image: s.Image,
				Env:   env,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/config", ReadOnly: true}},
			}},
			Volumes: []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: name, Items: []corev1.KeyToPath{{Key: "config.js", Path: "config.js"}}}}}},
		},
	}
	if len(r.ExcludeNodes) > 0 {
		p.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "kubernetes.io/hostname", Operator: corev1.NodeSelectorOpNotIn, Values: r.ExcludeNodes}},
			}}},
		}}
	}
	return p
}

// wait checks the pod every 5 seconds until it ends.
func (r *Runner) wait(ctx context.Context, name string) (*corev1.Pod, error) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		p, err := r.Kube.CoreV1().Pods(r.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && (p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed) {
			return p, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("run timed out: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// collect reads the pod's JSON log into readable text and per-repo results.
func (r *Runner) collect(name string, repos []string) (string, map[string]*RepoResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stream, err := r.Kube.CoreV1().Pods(r.Namespace).GetLogs(name, &corev1.PodLogOptions{Container: "renovate"}).Stream(ctx)
	if err != nil {
		return "could not read the log: " + err.Error() + "\n", nil
	}
	defer stream.Close()
	return Parse(stream, repos)
}

// Parse turns Renovate's JSON log (LOG_FORMAT=json) into readable lines
// and a result per repository.
func Parse(rd io.Reader, repos []string) (string, map[string]*RepoResult) {
	results := map[string]*RepoResult{}
	for _, repo := range repos {
		results[repo] = &RepoResult{}
	}
	get := func(repo string) *RepoResult {
		if results[repo] == nil {
			results[repo] = &RepoResult{}
		}
		return results[repo]
	}
	var out strings.Builder
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		var e struct {
			Level      int             `json:"level"`
			Msg        string          `json:"msg"`
			Time       string          `json:"time"`
			Repository string          `json:"repository"`
			Result     string          `json:"result"`
			PR         json.RawMessage `json:"pr"` // a number, or an object in some messages
			PRTitle    string          `json:"prTitle"`
			Branch     string          `json:"branch"`
			Err        json.RawMessage `json:"err"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Msg == "" {
			out.WriteString(line + "\n")
			continue
		}
		stamp := e.Time
		if ts, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
			stamp = ts.UTC().Format("15:04:05")
		}
		level := map[int]string{10: "TRACE", 20: "DEBUG", 30: "INFO", 40: "WARN", 50: "ERROR", 60: "FATAL"}[e.Level]
		pr := string(e.PR)
		if len(pr) == 0 || pr[0] == '{' {
			pr = ""
		}
		repo := ""
		if e.Repository != "" {
			repo = " [" + e.Repository + "]"
		}
		text := e.Msg
		if e.PRTitle != "" {
			text += ": " + e.PRTitle
		}
		if len(e.Err) > 0 {
			var er struct {
				Message    string `json:"message"`
				StatusCode int    `json:"statusCode"`
				URL        string `json:"url"`
				Body       struct {
					Message string `json:"message"`
				} `json:"body"`
			}
			if json.Unmarshal(e.Err, &er) == nil && er.Message != "" {
				detail := er.Message
				if er.Body.Message != "" && er.Body.Message != er.Message {
					detail += ": " + er.Body.Message
				}
				if er.StatusCode != 0 {
					detail += fmt.Sprintf("; HTTP %d", er.StatusCode)
				}
				if er.URL != "" {
					detail += " " + er.URL
				}
				text += " (" + detail + ")"
			}
		}
		if e.Level >= 40 || strings.Contains(strings.ToLower(e.Msg), "error") {
			text += extraFields(line)
		}
		fmt.Fprintf(&out, "%s %-5s%s %s\n", stamp, level, repo, text)
		if e.Repository == "" {
			continue
		}
		res := get(e.Repository)
		switch {
		case e.Msg == "Repository finished" && e.Result != "":
			res.Result = e.Result
		case e.Msg == "PR created":
			res.PRs = append(res.PRs, firstNonEmpty(e.PRTitle, e.Branch, pr))
		case strings.Contains(strings.ToLower(e.Msg), "automerged") || e.Msg == "Branch automerged" || e.Msg == "PR automerged":
			res.Automerged = append(res.Automerged, firstNonEmpty(e.PRTitle, e.Branch, pr))
		}
		if e.Level >= 50 {
			res.Errors = append(res.Errors, text)
		}
	}
	return out.String(), results
}

// standardFields are rendered already (or are noise) in a formatted line.
var standardFields = map[string]bool{"name": true, "hostname": true, "pid": true, "level": true, "msg": true, "time": true,
	"v": true, "logContext": true, "repository": true, "err": true, "prTitle": true, "result": true}

// extraFields renders a log entry's remaining fields (e.g. the "errors" of
// a failed GitHub GraphQL call) compactly, so warnings and errors say why.
func extraFields(line string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &m) != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if !standardFields[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := string(m[k])
		if len(v) > 600 {
			v = v[:600] + "…"
		}
		fmt.Fprintf(&b, " %s=%s", k, v)
	}
	return b.String()
}

// firstNonEmpty is the first of v that is not "".
func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// truncate keeps the start and end of a long log.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := max / 2
	return s[:half] + "\n… (log truncated) …\n" + s[len(s)-half:]
}

// cleanup deletes Renovate pods and secrets a previous process left behind.
func (r *Runner) cleanup(ctx context.Context) {
	sel := metav1.ListOptions{LabelSelector: "app.kubernetes.io/managed-by=rendimiento,rendimiento.ai/addon=" + Name}
	if pods, err := r.Kube.CoreV1().Pods(r.Namespace).List(ctx, sel); err == nil {
		for _, p := range pods.Items {
			_ = r.Kube.CoreV1().Pods(r.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{})
		}
	}
	if secrets, err := r.Kube.CoreV1().Secrets(r.Namespace).List(ctx, sel); err == nil {
		for _, s := range secrets.Items {
			_ = r.Kube.CoreV1().Secrets(r.Namespace).Delete(ctx, s.Name, metav1.DeleteOptions{})
		}
	}
}

// Running reports whether a run is in progress in this process.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// ptr returns a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }
