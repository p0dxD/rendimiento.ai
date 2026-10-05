package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	html, text, err := Render(Message{
		Tone: Critical, Subject: "s", Title: "Release #9 was rolled back", Summary: "It broke a service.",
		Facts:     []Fact{{"App", "hello-rendimiento"}, {"Now running", "release #10"}},
		Details:   `web (public): 8 of 16 checks failed (<script>alert(1)</script>)`,
		ActionURL: "https://rendimiento.example/apps/x/releases", ActionLabel: "See releases",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Release #9 was rolled back", "hello-rendimiento", "#d03b3b", "Needs attention",
		`href="https://rendimiento.example/apps/x/releases"`, "&lt;script&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Error("details are not escaped: error text could inject HTML")
	}
	for _, want := range []string{"NEEDS ATTENTION", "App: hello-rendimiento", "See releases: https://rendimiento.example/apps/x/releases"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeSender) Send(_ context.Context, _ []string, subject, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, subject)
	return nil
}

func TestNotifierDedupAndCap(t *testing.T) {
	var off *Notifier
	if off.Notify(Message{Subject: "x"}) || off.Enabled() {
		t.Fatal("a nil notifier must do nothing")
	}
	f := &fakeSender{}
	n := &Notifier{Sender: f, To: []string{"me@example.com"}, PerHour: 3}
	if !n.Notify(Message{Subject: "down", Key: "down:shop"}) {
		t.Fatal("first message dropped")
	}
	if n.Notify(Message{Subject: "down again", Key: "down:shop"}) {
		t.Fatal("a duplicate within the cooldown was sent")
	}
	n.Notify(Message{Subject: "a"})
	n.Notify(Message{Subject: "b"})
	if n.Notify(Message{Subject: "c"}) {
		t.Fatal("the hourly cap was not applied")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.sent)
		f.mu.Unlock()
		if n == 3 || time.Now().After(deadline) {
			if n != 3 {
				t.Fatalf("sent %d emails, want 3", n)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResend(t *testing.T) {
	var got map[string]any
	var auth, ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ua = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["subject"] == "reject" {
			w.WriteHeader(422)
			w.Write([]byte(`{"message":"domain not verified"}`))
			return
		}
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()
	r := &Resend{APIKey: "key", From: "rendimiento <alerts@example.com>", URL: srv.URL}
	if err := r.Send(context.Background(), []string{"me@example.com"}, "hi", "<p>x</p>", "x"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer key" || !strings.HasPrefix(ua, "rendimiento/") || got["from"] != "rendimiento <alerts@example.com>" || got["html"] != "<p>x</p>" {
		t.Fatalf("request: auth=%q ua=%q body=%v", auth, ua, got)
	}
	if err := r.Send(context.Background(), []string{"me@example.com"}, "reject", "", ""); err == nil || !strings.Contains(err.Error(), "domain not verified") {
		t.Fatalf("a rejected email: %v", err)
	}
}

// OnSent runs after an email went out, not after a failed one.
type failingSender struct{}

func (failingSender) Send(context.Context, []string, string, string, string) error {
	return errors.New("no route to host")
}

func TestOnSent(t *testing.T) {
	calls := 0
	n := &Notifier{Sender: &fakeSender{}, To: []string{"me@example.com"}, OnSent: func() { calls++ }}
	if err := n.Send(context.Background(), Message{Subject: "x"}); err != nil || calls != 1 {
		t.Fatalf("sent: err %v, OnSent calls %d", err, calls)
	}
	n.Sender = failingSender{}
	if err := n.Send(context.Background(), Message{Subject: "y"}); err == nil || calls != 1 {
		t.Fatalf("failed: err %v, OnSent calls %d", err, calls)
	}
}
