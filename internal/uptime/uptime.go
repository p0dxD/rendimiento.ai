// Package uptime checks every app's services once a minute and keeps the
// results: whether each answered, how fast, and when it was down. The App
// page's Reliability tab charts them, and they are exported as Prometheus
// metrics for Grafana.
//
// Each service gets up to two checks:
//   - internal: its health check (or, without one, a TCP connection to its
//     port) through its cluster Service, as the platform sees it;
//   - public: its domain through the public internet and Cloudflare, as a
//     visitor sees it, at the health path when it has one.
package uptime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/p0dxD/rendimiento.ai/internal/notify"
	"github.com/p0dxD/rendimiento.ai/internal/store"

	. "github.com/p0dxD/rendimiento.ai/internal/i18n" //nolint:revive // M marks messages for people
)

// Kinds of checks.
const (
	KindInternal = "internal"
	KindPublic   = "public"
)

// Target is one check: a URL to GET, or a host:port to connect to.
type Target struct {
	AppID   int64
	App     string
	Service string
	Kind    string
	URL     string // http(s)://… for HTTP checks
	Addr    string // host:port for TCP checks
	// AnyAnswer accepts any HTTP status below 500: for a public check of
	// "/" on a service without a health path, where an API's 404 still
	// proves DNS, Cloudflare, ingress and the app all answered.
	AnyAnswer bool
}

// Targets lists the checks for a set of apps. Services scaled to zero are
// skipped (they are down on purpose).
func Targets(apps []*store.App) []Target {
	var out []Target
	for _, a := range apps {
		for _, svc := range a.Spec.Services {
			if svc.Replicas == 0 {
				continue
			}
			host := svc.Name + "." + a.Name + ".svc.cluster.local"
			t := Target{AppID: a.ID, App: a.Name, Service: svc.Name, Kind: KindInternal}
			switch h := svc.Health; {
			case h != nil && h.Path != "":
				t.URL = "http://" + host + h.Path // the Service's port 80 forwards to the app's port
			default:
				t.Addr = net.JoinHostPort(host, fmt.Sprint(svc.Port))
			}
			out = append(out, t)
			if svc.Domain != "" {
				path, anyAnswer := "/", true
				if svc.Health != nil && svc.Health.Path != "" {
					path, anyAnswer = svc.Health.Path, false
				}
				out = append(out, Target{AppID: a.ID, App: a.Name, Service: svc.Name, Kind: KindPublic,
					URL: "https://" + svc.Domain + path, AnyAnswer: anyAnswer})
			}
		}
	}
	return out
}

// Checker runs single checks.
type Checker struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// NewChecker returns a Checker that does not follow redirects (a redirect,
// say to a login page, means the service answered) and gives each check
// timeout to complete.
func NewChecker(timeout time.Duration) *Checker {
	return &Checker{Timeout: timeout, HTTP: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{DisableKeepAlives: true, Proxy: nil},
	}}
}

// Check runs one check. An HTTP check succeeds on any 2xx or 3xx status
// (below 500 with AnyAnswer); a TCP check when the connection opens.
func (c *Checker) Check(ctx context.Context, t Target) store.Probe {
	p := store.Probe{AppID: t.AppID, Service: t.Service, Kind: t.Kind, At: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	start := time.Now()
	if t.Addr != "" {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", t.Addr)
		p.LatencyMS = int(time.Since(start).Milliseconds())
		if err != nil {
			p.Error = shortError(err)
			return p
		}
		conn.Close()
		p.OK = true
		return p
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	req.Header.Set("User-Agent", "rendimiento-uptime/1")
	req.Header.Set("Cache-Control", "no-cache") // see the origin, not a cached copy
	resp, err := c.HTTP.Do(req)
	p.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		p.Error = shortError(err)
		return p
	}
	resp.Body.Close()
	p.Status = resp.StatusCode
	p.OK = resp.StatusCode >= 200 && resp.StatusCode < 400 || t.AnyAnswer && resp.StatusCode < 500
	if !p.OK {
		p.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return p
}

// shortError keeps the useful end of an error (Go wraps URLs and addresses
// around the cause).
func shortError(err error) string {
	var dns *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout"):
		return "timed out"
	case errors.As(err, &dns):
		return "DNS: " + dns.Err
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused"
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg)-i > 8 {
		return msg[i+2:]
	}
	return msg
}

// Recorder is what the prober stores results in (the store in production).
type Recorder interface {
	ListApps(ctx context.Context) ([]*store.App, error)
	RecordProbes(ctx context.Context, probes []store.Probe) error
	OpenIncident(ctx context.Context, in store.Incident) (int64, error)
	CloseIncident(ctx context.Context, id int64, ended time.Time) error
	OpenIncidents(ctx context.Context) ([]store.Incident, error)
	RollupProbes(ctx context.Context, since time.Time) error
	PruneProbes(ctx context.Context, now time.Time) error
}

// FailuresToOpen is how many failed checks in a row make an outage: one
// failure is often a blip (a pod restarting, a slow response).
const FailuresToOpen = 2

// Prober checks every target once per Interval and records the results.
type Prober struct {
	Store    Recorder
	Checker  *Checker
	Interval time.Duration
	Log      *slog.Logger
	// Parallel caps concurrent checks (default 8).
	Parallel int
	// Notify emails outages and recoveries (nil: no email); BaseURL links them.
	Notify  *notify.Notifier
	BaseURL string

	mu      sync.Mutex
	state   map[string]*checkState // target key → its streak
	changes []change               // outages opened or closed this round
}

type checkState struct {
	fails     int
	firstFail store.Probe
	incident  int64     // open incident ID, or 0
	since     time.Time // when the open incident started
}

// change is an outage that started or ended, for the round's emails.
type change struct {
	target Target
	down   bool
	since  time.Time
	err    string
}

func key(appID int64, service, kind string) string {
	return fmt.Sprintf("%d/%s/%s", appID, service, kind)
}

// Run checks until ctx is done.
func (p *Prober) Run(ctx context.Context) {
	if p.Parallel < 1 {
		p.Parallel = 8
	}
	p.state = map[string]*checkState{}
	if open, err := p.Store.OpenIncidents(ctx); err == nil {
		for _, in := range open {
			p.state[key(in.AppID, in.Service, in.Kind)] = &checkState{fails: FailuresToOpen, incident: in.ID, since: in.StartedAt}
		}
	}
	p.Log.Info("uptime checks started", "interval", p.Interval)
	tick := time.NewTicker(p.Interval)
	defer tick.Stop()
	lastRollup := time.Time{}
	for {
		p.Round(ctx)
		if time.Since(lastRollup) >= 5*time.Minute {
			now := time.Now()
			// The last 3 hours: the current hour stays fresh, late checks are counted.
			if err := p.Store.RollupProbes(ctx, now.Add(-3*time.Hour)); err != nil {
				p.Log.Warn("uptime rollup failed", "err", err)
			}
			if err := p.Store.PruneProbes(ctx, now); err != nil {
				p.Log.Warn("uptime prune failed", "err", err)
			}
			lastRollup = now
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Round checks every target once, records the results and updates outages.
func (p *Prober) Round(ctx context.Context) {
	apps, err := p.Store.ListApps(ctx)
	if err != nil {
		p.Log.Warn("uptime: could not list apps", "err", err)
		return
	}
	targets := Targets(apps)
	results := make([]store.Probe, len(targets))
	sem := make(chan struct{}, max(p.Parallel, 1))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p.Checker.Check(ctx, t)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	if err := p.Store.RecordProbes(ctx, results); err != nil {
		p.Log.Warn("uptime: could not record checks", "err", err)
	}
	for i, r := range results {
		p.observe(ctx, targets[i], r)
	}
	p.notifyChanges()
}

// notifyChanges sends one email per app for the outages that started, and
// one for those that ended, this round.
func (p *Prober) notifyChanges() {
	p.mu.Lock()
	changes := p.changes
	p.changes = nil
	p.mu.Unlock()
	type group struct{ down, up []change }
	byApp := map[string]*group{}
	var order []string
	for _, c := range changes {
		g := byApp[c.target.App]
		if g == nil {
			g = &group{}
			byApp[c.target.App] = g
			order = append(order, c.target.App)
		}
		if c.down {
			g.down = append(g.down, c)
		} else {
			g.up = append(g.up, c)
		}
	}
	for _, app := range order {
		g := byApp[app]
		if len(g.down) > 0 {
			p.Notify.Notify(outageMessage(app, g.down, p.BaseURL))
		}
		if len(g.up) > 0 {
			p.Notify.Notify(recoveryMessage(app, g.up, p.BaseURL))
		}
	}
}

func checkName(t Target) string {
	if t.Kind == KindPublic {
		return M("%s (public URL)", t.Service)
	}
	return M("%s (inside the cluster)", t.Service)
}

// joinAnd lists names as "a and b and c", one translatable pair at a time.
func joinAnd(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := names[len(names)-1]
	for i := len(names) - 2; i >= 0; i-- {
		out = M("%s and %s", names[i], out)
	}
	return out
}

func outageMessage(app string, down []change, baseURL string) notify.Message {
	var facts []notify.Fact
	var names, details []string
	for _, c := range down {
		names = append(names, checkName(c.target))
		facts = append(facts, notify.Fact{Label: checkName(c.target), Value: M("down since %s", c.since.Local().Format("15:04 MST"))})
		where := c.target.URL
		if where == "" {
			where = c.target.Addr
		}
		details = append(details, fmt.Sprintf("%s → %s", where, c.err))
	}
	subject := M("🔴 %s is down", app)
	if len(down) == 1 {
		subject = M("🔴 %s: %s is down", app, names[0])
	}
	return notify.Message{
		Tone: notify.Critical, Subject: subject, Key: "down:" + app,
		Title:   M("%s is down", app),
		Summary: M("%s failed two checks in a row. rendimiento keeps checking every minute and will email you when it recovers.", joinAnd(names)),
		Facts:   facts, Details: strings.Join(details, "\n"),
		ActionURL: baseURL + "/apps/" + app + "/reliability", ActionLabel: M("See reliability"),
	}
}

func recoveryMessage(app string, up []change, baseURL string) notify.Message {
	var facts []notify.Fact
	var names []string
	for _, c := range up {
		names = append(names, checkName(c.target))
		value := M("back up")
		if !c.since.IsZero() {
			value = M("back up after %s", humanDuration(time.Since(c.since)))
		}
		facts = append(facts, notify.Fact{Label: checkName(c.target), Value: value})
	}
	return notify.Message{
		Tone: notify.Good, Subject: M("✅ %s recovered", app), Key: "up:" + app,
		Title:     M("%s recovered", app),
		Summary:   M("%s answered normally again.", joinAnd(names)),
		Facts:     facts,
		ActionURL: baseURL + "/apps/" + app + "/reliability", ActionLabel: M("See the outage"),
	}
}

func humanDuration(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m < 1:
		return M("under a minute")
	case m < 60:
		return M("%d min", m)
	}
	return M("%d h %d min", m/60, m%60)
}

// observe updates a target's streak, its metrics, and opens or closes its outage.
func (p *Prober) observe(ctx context.Context, t Target, r store.Probe) {
	labels := prometheus.Labels{"app": t.App, "service": t.Service, "kind": t.Kind}
	up := 0.0
	if r.OK {
		up = 1
	}
	upGauge.With(labels).Set(up)
	latencyGauge.With(labels).Set(float64(r.LatencyMS) / 1000)
	checks.With(prometheus.Labels{"app": t.App, "service": t.Service, "kind": t.Kind, "result": map[bool]string{true: "ok", false: "fail"}[r.OK]}).Inc()

	p.mu.Lock()
	defer p.mu.Unlock()
	k := key(t.AppID, t.Service, t.Kind)
	st := p.state[k]
	if st == nil {
		st = &checkState{}
		p.state[k] = st
	}
	if r.OK {
		if st.incident != 0 {
			if err := p.Store.CloseIncident(ctx, st.incident, r.At); err != nil {
				p.Log.Warn("uptime: could not close incident", "err", err)
				return // try again on the next success
			}
			p.Log.Info("service recovered", "app", t.App, "service", t.Service, "check", t.Kind)
			p.changes = append(p.changes, change{target: t, since: st.since})
		}
		*st = checkState{}
		return
	}
	if st.fails == 0 {
		st.firstFail = r
	}
	st.fails++
	if st.fails >= FailuresToOpen && st.incident == 0 {
		id, err := p.Store.OpenIncident(ctx, store.Incident{AppID: t.AppID, Service: t.Service, Kind: t.Kind,
			StartedAt: st.firstFail.At, Error: st.firstFail.Error})
		if err != nil {
			p.Log.Warn("uptime: could not open incident", "err", err)
			return
		}
		st.incident, st.since = id, st.firstFail.At
		p.Log.Warn("service down", "app", t.App, "service", t.Service, "check", t.Kind, "error", st.firstFail.Error)
		p.changes = append(p.changes, change{target: t, down: true, since: st.firstFail.At, err: st.firstFail.Error})
	}
}

// Prometheus metrics, registered by Register.
var (
	upGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rendimiento_uptime_up", Help: "1 if the latest check of the service succeeded, else 0.",
	}, []string{"app", "service", "kind"})
	latencyGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rendimiento_uptime_latency_seconds", Help: "Response time of the latest check.",
	}, []string{"app", "service", "kind"})
	checks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rendimiento_uptime_checks_total", Help: "Uptime checks run, by result.",
	}, []string{"app", "service", "kind", "result"})
)

// Register adds the uptime metrics to a Prometheus registry (the
// controller-runtime one, served on METRICS_ADDR).
func Register(r prometheus.Registerer) {
	r.MustRegister(upGauge, latencyGauge, checks)
}
