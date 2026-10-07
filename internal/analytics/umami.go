// Package analytics reads visitor statistics from Umami, so each app's page
// shows who visits it without leaving rendimiento. It only reads: the Umami
// account it signs in with should be a view-only one.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// cacheFor keeps Umami's answers this long: the numbers move slowly and
// every report is several database queries on the Umami side.
const cacheFor = 2 * time.Minute

// Umami is a client for the Umami API (v2 and v3), signed in as one user.
type Umami struct {
	URL      string // inside the cluster, e.g. http://umami.umami.svc:3000
	Username string
	Password string
	HTTP     *http.Client

	mu    sync.Mutex
	token string
	cache map[string]cached
}

type cached struct {
	at  time.Time
	val any
}

// Website is a site Umami counts visits for.
type Website struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

// Stats are the totals of a period.
type Stats struct {
	Pageviews int64 `json:"pageviews"`
	Visitors  int64 `json:"visitors"`
	Visits    int64 `json:"visits"`
	Bounces   int64 `json:"bounces"`
	TotalTime int64 `json:"totalTimeSec"` // summed visit time
}

// Point is one bucket of a series: its start and its count.
type Point struct {
	At time.Time `json:"at"`
	N  int64     `json:"n"`
}

// Metric is one row of a top list (a path, a referrer, a country code).
type Metric struct {
	Value string `json:"value"`
	N     int64  `json:"n"`
}

// Report is everything the Visits tab shows for one website.
type Report struct {
	Website   Website  `json:"website"`
	Current   Stats    `json:"current"`
	Previous  Stats    `json:"previous"` // the period just before, as long
	Active    int64    `json:"active"`   // visitors in the last 5 minutes
	Pageviews []Point  `json:"pageviews"`
	Visitors  []Point  `json:"visitors"`
	Paths     []Metric `json:"paths"`
	Referrers []Metric `json:"referrers"`
	Countries []Metric `json:"countries"`
}

// ErrAuth means Umami refused the user name or password.
var ErrAuth = errors.New("umami refused the user name or password")

func (u *Umami) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (u *Umami) login(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": u.Username, "password": u.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(u.URL, "/")+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("umami login: %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		return "", fmt.Errorf("umami login: no token in the answer")
	}
	return out.Token, nil
}

// get calls a read-only endpoint, signing in first and again when the
// token has expired.
func (u *Umami) get(ctx context.Context, path string, q url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		u.mu.Lock()
		tok := u.token
		u.mu.Unlock()
		if tok == "" {
			var err error
			if tok, err = u.login(ctx); err != nil {
				return err
			}
			u.mu.Lock()
			u.token = tok
			u.mu.Unlock()
		}
		target := strings.TrimRight(u.URL, "/") + path
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		resp, err := u.client().Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			u.mu.Lock()
			u.token = ""
			u.mu.Unlock()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("umami %s: %s", path, resp.Status)
		}
		return json.Unmarshal(data, out)
	}
}

// remember returns a cached answer for key, or computes and keeps it.
func remember[T any](u *Umami, key string, f func() (T, error)) (T, error) {
	u.mu.Lock()
	if c, ok := u.cache[key]; ok && time.Since(c.at) < cacheFor {
		u.mu.Unlock()
		return c.val.(T), nil
	}
	u.mu.Unlock()
	v, err := f()
	if err != nil {
		return v, err
	}
	u.mu.Lock()
	if u.cache == nil {
		u.cache = map[string]cached{}
	}
	for k, c := range u.cache { // keep it small
		if time.Since(c.at) >= cacheFor {
			delete(u.cache, k)
		}
	}
	u.cache[key] = cached{at: time.Now(), val: v}
	u.mu.Unlock()
	return v, nil
}

type page[T any] struct {
	Data []T `json:"data"`
}

// Websites lists the sites the user can see: its own and its teams'.
func (u *Umami) Websites(ctx context.Context) ([]Website, error) {
	return remember(u, "websites", func() ([]Website, error) {
		q := url.Values{"pageSize": {"200"}}
		var own page[Website]
		if err := u.get(ctx, "/api/me/websites", q, &own); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		out := []Website{}
		add := func(ws []Website) {
			for _, w := range ws {
				if !seen[w.ID] {
					seen[w.ID] = true
					out = append(out, w)
				}
			}
		}
		add(own.Data)
		var teams page[struct {
			ID string `json:"id"`
		}]
		if err := u.get(ctx, "/api/me/teams", q, &teams); err != nil {
			return nil, err
		}
		for _, t := range teams.Data {
			var ws page[Website]
			if err := u.get(ctx, "/api/teams/"+url.PathEscape(t.ID)+"/websites", q, &ws); err != nil {
				return nil, err
			}
			add(ws.Data)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	})
}

// num reads a JSON number that may come as a string (bigint columns) or null.
type num int64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = num(f)
	return nil
}

type rawStats struct {
	Pageviews num `json:"pageviews"`
	Visitors  num `json:"visitors"`
	Visits    num `json:"visits"`
	Bounces   num `json:"bounces"`
	TotalTime num `json:"totaltime"`
}

func (r rawStats) stats() Stats {
	return Stats{Pageviews: int64(r.Pageviews), Visitors: int64(r.Visitors), Visits: int64(r.Visits),
		Bounces: int64(r.Bounces), TotalTime: int64(r.TotalTime)}
}

type xy struct {
	X string `json:"x"`
	Y num    `json:"y"`
}

// Period is the window a report covers, bucketed by hour or by day.
type Period struct {
	From, To time.Time
	Unit     string // hour | day
	TZ       *time.Location
}

// Report gathers a website's numbers for the period, in parallel.
func (u *Umami) Report(ctx context.Context, w Website, p Period) (*Report, error) {
	key := fmt.Sprintf("report/%s/%d/%s", w.ID, p.To.Sub(p.From)/time.Minute, p.Unit)
	return remember(u, key, func() (*Report, error) {
		base := url.Values{
			"startAt":  {strconv.FormatInt(p.From.UnixMilli(), 10)},
			"endAt":    {strconv.FormatInt(p.To.UnixMilli(), 10)},
			"timezone": {p.TZ.String()},
		}
		with := func(kv ...string) url.Values {
			q := url.Values{}
			for k, v := range base {
				q[k] = v
			}
			for i := 0; i+1 < len(kv); i += 2 {
				q.Set(kv[i], kv[i+1])
			}
			return q
		}
		site := "/api/websites/" + url.PathEscape(w.ID)
		r := &Report{Website: w}
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			var s struct {
				rawStats
				Comparison *rawStats `json:"comparison"`
			}
			if err := u.get(gctx, site+"/stats", base, &s); err != nil {
				return err
			}
			r.Current = s.stats()
			if s.Comparison != nil {
				r.Previous = s.Comparison.stats()
			}
			return nil
		})
		g.Go(func() error {
			var s struct {
				Pageviews []xy `json:"pageviews"`
				Sessions  []xy `json:"sessions"`
			}
			if err := u.get(gctx, site+"/pageviews", with("unit", p.Unit), &s); err != nil {
				return err
			}
			r.Pageviews, r.Visitors = series(s.Pageviews, p), series(s.Sessions, p)
			return nil
		})
		top := func(kind string, dst *[]Metric) func() error {
			return func() error {
				var rows []xy
				if err := u.get(gctx, site+"/metrics", with("type", kind, "limit", "10"), &rows); err != nil {
					return err
				}
				out := make([]Metric, 0, len(rows))
				for _, x := range rows {
					out = append(out, Metric{Value: x.X, N: int64(x.Y)})
				}
				*dst = out
				return nil
			}
		}
		g.Go(top("path", &r.Paths))
		g.Go(top("referrer", &r.Referrers))
		g.Go(top("country", &r.Countries))
		g.Go(func() error {
			var a struct {
				Visitors num `json:"visitors"`
				X        num `json:"x"` // Umami v2
			}
			if err := u.get(gctx, site+"/active", nil, &a); err != nil {
				return err
			}
			r.Active = int64(max(a.Visitors, a.X))
			return nil
		})
		if err := g.Wait(); err != nil {
			return nil, err
		}
		return r, nil
	})
}

// Totals are a website's numbers for a period and for the one before it:
// the dashboard's summary, one call per site.
func (u *Umami) Totals(ctx context.Context, w Website, from, to time.Time) (cur, prev Stats, err error) {
	key := fmt.Sprintf("totals/%s/%d", w.ID, to.Sub(from)/time.Minute)
	pair, err := remember(u, key, func() ([2]Stats, error) {
		var s struct {
			rawStats
			Comparison *rawStats `json:"comparison"`
		}
		q := url.Values{"startAt": {strconv.FormatInt(from.UnixMilli(), 10)}, "endAt": {strconv.FormatInt(to.UnixMilli(), 10)}}
		if err := u.get(ctx, "/api/websites/"+url.PathEscape(w.ID)+"/stats", q, &s); err != nil {
			return [2]Stats{}, err
		}
		out := [2]Stats{s.stats(), {}}
		if s.Comparison != nil {
			out[1] = s.Comparison.stats()
		}
		return out, nil
	})
	return pair[0], pair[1], err
}

// series turns Umami's sparse buckets into one point per hour or day of the
// period, zeros included, so a chart has no holes.
func series(rows []xy, p Period) []Point {
	counts := map[time.Time]int64{}
	for _, r := range rows {
		if at, ok := parseBucket(r.X, p.TZ); ok {
			counts[truncate(at, p)] += int64(r.Y)
		}
	}
	out := []Point{}
	for at := truncate(p.From.In(p.TZ), p); !at.After(p.To); at = next(at, p) {
		out = append(out, Point{At: at, N: counts[at]})
	}
	return out
}

func truncate(t time.Time, p Period) time.Time {
	t = t.In(p.TZ)
	if p.Unit == "day" {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, p.TZ)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, p.TZ)
}

func next(t time.Time, p Period) time.Time {
	if p.Unit == "day" {
		return t.AddDate(0, 0, 1)
	}
	return t.Add(time.Hour)
}

// parseBucket reads "2026-10-06 13:00:00" (local to the time zone asked
// for) or "2026-10-06T13:00:00Z" (UTC).
func parseBucket(s string, tz *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, tz); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// Host normalizes a website domain or an app host for matching:
// lower case, no scheme, port, path or leading "www.".
func Host(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(strings.TrimSuffix(s, "."), "www.")
}

// Match returns the websites whose domain is one of hosts.
func Match(sites []Website, hosts []string) []Website {
	want := map[string]bool{}
	for _, h := range hosts {
		want[Host(h)] = true
	}
	out := []Website{}
	for _, w := range sites {
		if h := Host(w.Domain); h != "" && want[h] {
			out = append(out, w)
		}
	}
	return out
}
