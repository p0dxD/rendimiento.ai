package dns

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestParseIPResponse(t *testing.T) {
	good := map[string]string{
		"203.0.113.7\n": "203.0.113.7",
		"fl=123\nh=1.1.1.1\nip=203.0.113.7\nts=1\n": "203.0.113.7",
	}
	for in, want := range good {
		if got, err := ParseIPResponse(in); err != nil || got != want {
			t.Errorf("ParseIPResponse(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "hello", "ip=2001:db8::1", "192.168.50.75", "10.0.0.1", "127.0.0.1", "0.0.0.0", "169.254.1.1"} {
		if got, err := ParseIPResponse(bad); err == nil {
			t.Errorf("ParseIPResponse(%q) accepted %q", bad, got)
		}
	}
}

func TestSyncFollowsPublicIP(t *testing.T) {
	cf, f := setup(t)
	ip := "203.0.113.7"
	cf.Target = "auto"
	cf.DetectIP = func(context.Context) (string, error) { return ip, nil }
	ctx := context.Background()

	// New apps point at the detected IP.
	if err := cf.Ensure(ctx, "hello.joserod.space", "hello"); err != nil {
		t.Fatal(err)
	}
	// A hand-made record opted in via its comment, one that is not, and a CNAME.
	f.records["tagged"] = record{ID: "tagged", Type: "A", Name: "secplus.joserod.space", Content: ip, Comment: "site rendimiento-ddns"}
	f.records["untagged"] = record{ID: "untagged", Type: "A", Name: "nas.joserod.space", Content: ip, Comment: "my nas"}
	f.records["cname"] = record{ID: "cname", Type: "CNAME", Name: "www.joserod.space", Content: "joserod.space", Comment: "rendimiento-ddns"}

	// Same IP: nothing to do.
	res, err := cf.Sync(ctx)
	if err != nil || res.Changed || len(res.Updated) != 0 {
		t.Fatalf("steady state: %+v %v", res, err)
	}

	// The ISP hands out a new address.
	ip = "203.0.113.9"
	res, err = cf.Sync(ctx)
	if err != nil || !res.Changed {
		t.Fatalf("after change: %+v %v", res, err)
	}
	sort.Strings(res.Updated)
	if strings.Join(res.Updated, ",") != "hello.joserod.space,secplus.joserod.space" {
		t.Fatalf("updated = %v", res.Updated)
	}
	for id, want := range map[string]string{"tagged": "203.0.113.9", "untagged": "203.0.113.7", "cname": "joserod.space"} {
		if got := f.records[id].Content; got != want {
			t.Errorf("%s content = %s, want %s", id, got, want)
		}
	}
	if f.records["tagged"].Comment != "site rendimiento-ddns" {
		t.Error("comment of a hand-made record was changed")
	}
	st := cf.DDNSStatus()
	if !st.Enabled || st.IP != "203.0.113.9" || st.ChangedAt.IsZero() || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	if d := cf.Describe(ctx); !d.Healthy || !strings.Contains(d.Detail, "203.0.113.9 (auto-detected)") {
		t.Fatalf("describe = %+v", d)
	}

	// Detection failing keeps the last good IP and reports the error.
	cf.DetectIP = func(context.Context) (string, error) { return "", errors.New("offline") }
	if _, err := cf.Sync(ctx); err == nil {
		t.Fatal("expected error")
	}
	if st := cf.DDNSStatus(); st.IP != "203.0.113.9" || !strings.Contains(st.Error, "offline") {
		t.Fatalf("status after failure = %+v", st)
	}
	if d := cf.Describe(ctx); d.Healthy {
		t.Fatal("describe should report detection failure")
	}
}

func TestFixedTargetSyncIsNoop(t *testing.T) {
	cf, f := setup(t)
	f.records["x"] = record{ID: "x", Type: "A", Name: "a.joserod.space", Content: "1.2.3.4", Comment: "managed-by=rendimiento app=a"}
	res, err := cf.Sync(context.Background())
	if err != nil || res.IP != "203.0.113.7" || f.records["x"].Content != "1.2.3.4" {
		t.Fatalf("fixed mode touched records: %+v %v %+v", res, err, f.records["x"])
	}
}
