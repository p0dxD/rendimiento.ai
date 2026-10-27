package registro

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func dig(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

// The plan keeps the newest build tags, other tags, what is in use and
// what a kept index lists; it removes the rest, and the manifests (old
// caches) no tag points to.
func TestDecide(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 10, n, 12, 0, 0, 0, time.UTC) }
	l := Parse(strings.Join([]string{
		fmt.Sprintf("T shop/api aaaaaaa %s %d", dig('1'), day(1).Unix()), // old: goes
		fmt.Sprintf("T shop/api bbbbbbb %s %d", dig('2'), day(2).Unix()), // old, but in use by a workload
		fmt.Sprintf("T shop/api ccccccc %s %d", dig('3'), day(3).Unix()), // newest two: stay
		fmt.Sprintf("T shop/api ddddddd %s %d", dig('4'), day(4).Unix()),
		fmt.Sprintf("T shop/api eeeeeee %s %d", dig('4'), day(1).Unix()),    // old, same manifest as a kept one: the tag goes, the manifest stays
		fmt.Sprintf("T shop/api buildcache %s %d", dig('5'), day(1).Unix()), // not a build tag: stays
		fmt.Sprintf("T shop/api fffffff %s %d", dig('6'), day(1).Unix()),    // old index, its child is kept by a pod's digest
		"R shop/api " + strings.Repeat("1", 64), "R shop/api " + strings.Repeat("2", 64), "R shop/api " + strings.Repeat("3", 64),
		"R shop/api " + strings.Repeat("4", 64), "R shop/api " + strings.Repeat("5", 64), "R shop/api " + strings.Repeat("6", 64),
		"R shop/api " + strings.Repeat("7", 64), // an old cache, untagged: goes
		"R shop/api " + strings.Repeat("8", 64), // listed by the kept cache index: stays
		"R shop/api " + strings.Repeat("9", 64), // listed by an index that goes, and in use by digest: stays
		"C " + strings.Repeat("5", 64) + " " + dig('8'),
		"C " + strings.Repeat("6", 64) + " " + dig('9'),
		"E shop/api " + strings.Repeat("4", 64),
		"D 800 200",
	}, "\n"))
	use := InUse{Tags: map[string]map[string]bool{"shop/api": {"bbbbbbb": true}}, Digests: map[string]bool{dig('9'): true}}
	p := Decide(l, use, 2)

	var tags []string
	for _, x := range p.Tags {
		tags = append(tags, x.Name)
	}
	if got := strings.Join(tags, " "); got != "eeeeeee aaaaaaa fffffff" && got != "aaaaaaa eeeeeee fffffff" {
		t.Errorf("tags removed: %s; want aaaaaaa, eeeeeee and fffffff", got)
	}
	var revs []string
	for _, r := range p.Revisions {
		revs = append(revs, r[1][7:8])
	}
	if got := strings.Join(revs, ""); got != "167" {
		t.Errorf("manifests removed: %s; want 1, 6 and 7 (4 is shared, 8 and 9 are kept by an index or a pod)", got)
	}
	if share(l) != 0.8 || len(l.Broken["shop/api"]) != 1 {
		t.Errorf("disk %v, broken %v", share(l), l.Broken)
	}
	sh, err := Script("/r", p, l.Broken)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"printf '%s' '" + dig('4') + "' > '/r/shop/api/_manifests/revisions/sha256/" + strings.Repeat("4", 64) + "/link'",
		"rm -rf '/r/shop/api/_manifests/tags/aaaaaaa'",
		"rm -rf '/r/shop/api/_manifests/revisions/sha256/" + strings.Repeat("7", 64) + "'",
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("script lacks %q:\n%s", want, sh)
		}
	}
	if strings.Contains(sh, "tags/ccccccc") || strings.Contains(sh, "tags/buildcache") || strings.Contains(sh, "tags/bbbbbbb") {
		t.Errorf("script removes a kept tag:\n%s", sh)
	}
}

// Nothing that could leave the registry's folder gets into the script.
func TestScriptRefusesOddNames(t *testing.T) {
	for _, p := range []Plan{
		{Tags: []Tag{{Repo: "shop/../../etc", Name: "x"}}},
		{Tags: []Tag{{Repo: "shop", Name: "a'; rm -rf /; '"}}},
		{Revisions: [][2]string{{"shop", "sha256:nothex"}}},
	} {
		if _, err := Script("/r", p, nil); err == nil {
			t.Errorf("%+v: accepted", p)
		}
	}
}

func TestRef(t *testing.T) {
	h := "registry.example.lan:5000"
	cases := []struct{ in, repo, tag, digest string }{
		{h + "/shop-api:4f2c1a9", "shop-api", "4f2c1a9", ""},
		{h + "/team/shop:4f2c1a9@" + dig('a'), "team/shop", "4f2c1a9", dig('a')},
		{h + "/shop@" + dig('b'), "shop", "", dig('b')},
	}
	for _, c := range cases {
		repo, tag, d, ok := Ref(c.in, h)
		if !ok || repo != c.repo || tag != c.tag || d != c.digest {
			t.Errorf("%s: %q %q %q %v", c.in, repo, tag, d, ok)
		}
	}
	if _, _, _, ok := Ref("docker.io/library/postgres:17", h); ok {
		t.Error("an image from elsewhere counted as the registry's")
	}
}

// The cleanup runs at its time of day, today or else tomorrow.
func TestNext(t *testing.T) {
	c := &Cleaner{At: "03:30"}
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	if got := c.next(now); !got.Equal(time.Date(2026, 10, 27, 3, 30, 0, 0, time.UTC)) {
		t.Errorf("before: %v", got)
	}
	if got := c.next(now.Add(2 * time.Hour)); !got.Equal(time.Date(2026, 10, 28, 3, 30, 0, 0, time.UTC)) {
		t.Errorf("after: %v", got)
	}
	mx, _ := time.LoadLocation("America/Mexico_City")
	c.TZ = mx
	if got := c.next(now); !got.Equal(time.Date(2026, 10, 27, 3, 30, 0, 0, mx)) {
		t.Errorf("in its time zone: %v", got) // 02:00 UTC is still the evening before there
	}
}
