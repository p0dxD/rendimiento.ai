package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeUmami answers like Umami v3 for one view-only user in one team.
func fakeUmami(t *testing.T, logins *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["username"] != "rendimiento" || in["password"] != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n := logins.Add(1)
		reply(w, map[string]any{"token": "tok" + string(rune('0'+n))})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// The first token "expires" so the client must sign in again.
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok") || r.Header.Get("Authorization") == "Bearer tok1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/me/websites", authed(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"data": []any{}, "count": 0})
	}))
	mux.HandleFunc("GET /api/me/teams", authed(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"data": []any{map[string]any{"id": "team1", "name": "rendimiento"}}})
	}))
	mux.HandleFunc("GET /api/teams/team1/websites", authed(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"data": []any{
			map[string]any{"id": "w2", "name": "Shop", "domain": "https://www.shop.example.com/"},
			map[string]any{"id": "w1", "name": "Blog", "domain": "blog.example.com"},
		}})
	}))
	mux.HandleFunc("GET /api/websites/w1/stats", authed(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("startAt") == "" || r.URL.Query().Get("timezone") != "America/Mexico_City" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// bigint columns may come as strings
		reply(w, map[string]any{"pageviews": "120", "visitors": 40, "visits": 50, "bounces": 20, "totaltime": 3000,
			"comparison": map[string]any{"pageviews": 60, "visitors": 30, "visits": 35, "bounces": 10, "totaltime": 1000}})
	}))
	mux.HandleFunc("GET /api/websites/w1/pageviews", authed(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("unit") != "day" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reply(w, map[string]any{
			"pageviews": []any{map[string]any{"x": "2026-10-05 00:00:00", "y": 70}, map[string]any{"x": "2026-10-06 00:00:00", "y": 50}},
			"sessions":  []any{map[string]any{"x": "2026-10-06 00:00:00", "y": 40}},
		})
	}))
	mux.HandleFunc("GET /api/websites/w1/metrics", authed(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("type") {
		case "path":
			reply(w, []any{map[string]any{"x": "/", "y": 90}, map[string]any{"x": "/about", "y": 30}})
		case "referrer":
			reply(w, []any{map[string]any{"x": "google.com", "y": 12}})
		case "country":
			reply(w, []any{map[string]any{"x": "MX", "y": 30}})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	mux.HandleFunc("GET /api/websites/w1/active", authed(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"visitors": 3})
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUmamiReport(t *testing.T) {
	var logins atomic.Int32
	srv := fakeUmami(t, &logins)
	u := &Umami{URL: srv.URL, Username: "rendimiento", Password: "pw"}
	ctx := context.Background()

	sites, err := u.Websites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].Name != "Blog" || logins.Load() != 2 {
		t.Fatalf("websites %+v after %d logins; want Blog first and a second login after the expired token", sites, logins.Load())
	}
	m := Match(sites, []string{"shop.example.com", "Blog.Example.com", "other.example.com"})
	if len(m) != 2 {
		t.Fatalf("match = %+v, want both sites (www., scheme and case ignored)", m)
	}

	tz, _ := time.LoadLocation("America/Mexico_City")
	to := time.Date(2026, 10, 6, 15, 0, 0, 0, tz)
	r, err := u.Report(ctx, sites[0], Period{From: to.AddDate(0, 0, -2), To: to, Unit: "day", TZ: tz})
	if err != nil {
		t.Fatal(err)
	}
	if r.Current.Pageviews != 120 || r.Previous.Visitors != 30 || r.Active != 3 {
		t.Errorf("stats %+v / %+v active %d", r.Current, r.Previous, r.Active)
	}
	if len(r.Pageviews) != 3 || r.Pageviews[0].N != 0 || r.Pageviews[1].N != 70 || r.Pageviews[2].N != 50 {
		t.Errorf("pageviews %+v, want 3 days with the gap filled", r.Pageviews)
	}
	if len(r.Visitors) != 3 || r.Visitors[2].N != 40 {
		t.Errorf("visitors %+v", r.Visitors)
	}
	if len(r.Paths) != 2 || r.Paths[0].Value != "/" || r.Referrers[0].N != 12 || r.Countries[0].Value != "MX" {
		t.Errorf("tops %+v %+v %+v", r.Paths, r.Referrers, r.Countries)
	}

	bad := &Umami{URL: srv.URL, Username: "rendimiento", Password: "wrong"}
	if _, err := bad.Websites(ctx); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong password: err = %v, want ErrAuth", err)
	}
}

func TestHost(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.Example.com:443/path": "example.com",
		"example.com.":                     "example.com",
		"  shop.example.com ":              "shop.example.com",
	} {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}
