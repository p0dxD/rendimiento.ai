package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCF is an in-memory Cloudflare API with one zone.
type fakeCF struct {
	mu      sync.Mutex
	records map[string]record
	nextID  int
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(v any) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v})
	}
	switch {
	case r.URL.Path == "/user/tokens/verify":
		reply(map[string]string{"status": "active"})
	case r.URL.Path == "/zones":
		reply([]map[string]string{{"id": "z1", "name": "joserod.space"}})
	case r.Method == http.MethodGet && r.URL.Path == "/zones/z1/dns_records":
		var out []record
		q := r.URL.Query()
		for _, rec := range f.records {
			if (q.Get("name") == "" || rec.Name == q.Get("name")) && (q.Get("type") == "" || rec.Type == q.Get("type")) {
				out = append(out, rec)
			}
		}
		reply(out)
	case r.Method == http.MethodPost:
		var rec record
		json.NewDecoder(r.Body).Decode(&rec)
		f.nextID++
		rec.ID = string(rune('a' + f.nextID))
		f.records[rec.ID] = rec
		reply(rec)
	case r.Method == http.MethodPut:
		var rec record
		json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.records[rec.ID] = rec
		reply(rec)
	case r.Method == http.MethodDelete:
		delete(f.records, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		reply(map[string]string{})
	default:
		http.NotFound(w, r)
	}
}

func setup(t *testing.T) (*Cloudflare, *fakeCF) {
	f := &fakeCF{records: map[string]record{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Cloudflare{Token: "t", Target: "203.0.113.7", BaseURL: srv.URL}, f
}

func TestEnsureCreateUpdateRemove(t *testing.T) {
	cf, f := setup(t)
	ctx := context.Background()
	if err := cf.Ensure(ctx, "hello.joserod.space", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 1 {
		t.Fatalf("records = %v", f.records)
	}
	for _, r := range f.records {
		if r.Type != "A" || r.Content != "203.0.113.7" || r.Comment != "managed-by=rendimiento app=hello" {
			t.Fatalf("bad record %+v", r)
		}
	}
	cf.Target = "home.example.net"
	if err := cf.Ensure(ctx, "hello.joserod.space", "hello"); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.records {
		if r.Type != "CNAME" || r.Content != "home.example.net" {
			t.Fatalf("not updated: %+v", r)
		}
	}
	if err := cf.Remove(ctx, "hello.joserod.space", "other-app"); err != nil || len(f.records) != 1 {
		t.Fatalf("removed another app's record: %v %v", err, f.records)
	}
	if err := cf.Remove(ctx, "hello.joserod.space", "hello"); err != nil || len(f.records) != 0 {
		t.Fatalf("not removed: %v %v", err, f.records)
	}
}

func TestNeverTouchesForeignRecords(t *testing.T) {
	cf, f := setup(t)
	ctx := context.Background()
	f.records["x"] = record{ID: "x", Type: "A", Name: "secplus.joserod.space", Content: "198.51.100.1"}
	if err := cf.Ensure(ctx, "secplus.joserod.space", "secplus"); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	f.records["x"] = record{ID: "x", Type: "A", Name: "secplus.joserod.space", Content: "203.0.113.7"}
	if err := cf.Ensure(ctx, "secplus.joserod.space", "secplus"); err != nil {
		t.Fatalf("matching hand-made record should be accepted: %v", err)
	}
	if err := cf.Remove(ctx, "secplus.joserod.space", "secplus"); err != nil || len(f.records) != 1 {
		t.Fatal("must not delete hand-made record")
	}
	if err := cf.Ensure(ctx, "x.other.com", "x"); err == nil {
		t.Fatal("expected unknown zone error")
	}
}

func TestDescribe(t *testing.T) {
	cf, _ := setup(t)
	d := cf.Describe(context.Background())
	if !d.Healthy || d.ID != "cloudflare" || len(d.Zones) != 1 || !strings.Contains(d.Detail, "A records → 203.0.113.7") {
		t.Fatalf("describe = %+v", d)
	}
	cf.Token = "bad"
	cf.BaseURL = "http://127.0.0.1:1" // unreachable
	if d := cf.Describe(context.Background()); d.Healthy {
		t.Fatal("unreachable API reported healthy")
	}
	if d := (Noop{}).Describe(context.Background()); d.Healthy || d.ID != "manual" {
		t.Fatalf("noop = %+v", d)
	}
}
