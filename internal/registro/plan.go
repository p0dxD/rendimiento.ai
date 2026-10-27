package registro

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tag is one tag of a repository in the registry and the manifest it
// points to, with when it was last pushed.
type Tag struct {
	Repo, Name, Digest string
	At                 time.Time
}

// Listing is what the registry holds, read from its storage: every tag,
// every manifest revision, what each index points to, revision links left
// empty (a push cut short by a full disk), and the disk's use.
type Listing struct {
	Tags      []Tag
	Revisions map[string]map[string]bool // repo → manifest digests
	Children  map[string][]string        // index digest → the digests it lists
	Broken    map[string][]string        // repo → revisions whose link is empty
	Used      int64                      // bytes
	Free      int64
}

// InUse is what must stay: the tags and digests any workload in the
// cluster, or a recent release, refers to.
type InUse struct {
	Tags    map[string]map[string]bool // repo → tags
	Digests map[string]bool
}

// Plan is what a cleanup removes: tags, and manifest revisions nothing
// keeps any more (their layers are then freed by the garbage collector).
type Plan struct {
	Tags      []Tag
	Revisions [][2]string // repo, digest
}

// buildTag is how rendimiento tags the images it builds: a commit's
// abbreviated hash, maybe with a suffix. Other tags (latest, buildcache, a
// version) are someone's choice, and stay.
var buildTag = regexp.MustCompile(`^[0-9a-f]{7,40}(-[\w.-]+)?$`)

// Decide plans a cleanup. Per repository it keeps the newest keep tags,
// every tag that is not a build tag, and everything in use; it removes the
// other tags, and every revision that no kept tag, workload or index
// still points to: old images, and the build caches each push replaced.
func Decide(l Listing, use InUse, keep int) Plan {
	byRepo := map[string][]Tag{}
	for _, t := range l.Tags {
		byRepo[t.Repo] = append(byRepo[t.Repo], t)
	}
	var p Plan
	repos := map[string]bool{}
	for r := range byRepo {
		repos[r] = true
	}
	for r := range l.Revisions {
		repos[r] = true
	}
	names := make([]string, 0, len(repos))
	for r := range repos {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, repo := range names {
		tags := byRepo[repo]
		sort.SliceStable(tags, func(i, j int) bool { return tags[i].At.After(tags[j].At) })
		kept := map[string]bool{}
		for d := range use.Digests {
			kept[d] = true
		}
		for i, t := range tags {
			if i < keep || !buildTag.MatchString(t.Name) || use.Tags[repo][t.Name] || use.Digests[t.Digest] {
				kept[t.Digest] = true
			} else {
				p.Tags = append(p.Tags, t)
			}
		}
		// A tag that goes may share its manifest with one that stays: the
		// revision is kept by the latter. What a kept index lists stays too,
		// all the way down.
		todo := make([]string, 0, len(kept))
		for d := range kept {
			todo = append(todo, d)
		}
		for len(todo) > 0 {
			d := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			for _, c := range l.Children[d] {
				if !kept[c] {
					kept[c] = true
					todo = append(todo, c)
				}
			}
		}
		revs := make([]string, 0, len(l.Revisions[repo]))
		for d := range l.Revisions[repo] {
			if !kept[d] {
				revs = append(revs, d)
			}
		}
		sort.Strings(revs)
		for _, d := range revs {
			p.Revisions = append(p.Revisions, [2]string{repo, d})
		}
	}
	return p
}

// safeName is what a repository or tag name may hold before it is written
// into a shell command.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Script is the shell that carries the plan out under root (the
// registry's repositories folder): it first repairs the empty revision
// links (each holds its own digest), then removes the planned tags and
// revisions. Names that could escape the folder are refused.
func Script(root string, p Plan, broken map[string][]string) (string, error) {
	var b strings.Builder
	b.WriteString("set -e\n")
	ok := func(s string) bool { return safeName.MatchString(s) && !strings.Contains(s, "..") }
	for repo, ds := range broken {
		for _, d := range ds {
			if !ok(repo) || !digestRe.MatchString(d) {
				return "", fmt.Errorf("unsafe name %q or digest %q", repo, d)
			}
			fmt.Fprintf(&b, "printf '%%s' '%s' > '%s/%s/_manifests/revisions/sha256/%s/link'\n", d, root, repo, d[7:])
		}
	}
	for _, t := range p.Tags {
		if !ok(t.Repo) || !ok(t.Name) {
			return "", fmt.Errorf("unsafe name %q:%q", t.Repo, t.Name)
		}
		fmt.Fprintf(&b, "rm -rf '%s/%s/_manifests/tags/%s'\n", root, t.Repo, t.Name)
	}
	for _, r := range p.Revisions {
		if !ok(r[0]) || !digestRe.MatchString(r[1]) {
			return "", fmt.Errorf("unsafe name %q or digest %q", r[0], r[1])
		}
		fmt.Fprintf(&b, "rm -rf '%s/%s/_manifests/revisions/sha256/%s'\n", root, r[0], r[1][7:])
	}
	return b.String(), nil
}

// Parse reads the listing script's output: "T repo tag digest unixtime",
// "R repo hex", "E repo hex" (an empty revision link), "C hex digest…" and
// "D used free".
func Parse(out string) Listing {
	l := Listing{Revisions: map[string]map[string]bool{}, Children: map[string][]string{}, Broken: map[string][]string{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "T" && len(f) == 5 && digestRe.MatchString(f[3]):
			sec, _ := strconv.ParseInt(f[4], 10, 64)
			l.Tags = append(l.Tags, Tag{Repo: f[1], Name: f[2], Digest: f[3], At: time.Unix(sec, 0)})
		case f[0] == "R" && len(f) == 3:
			if l.Revisions[f[1]] == nil {
				l.Revisions[f[1]] = map[string]bool{}
			}
			l.Revisions[f[1]]["sha256:"+f[2]] = true
		case f[0] == "E" && len(f) == 3:
			l.Broken[f[1]] = append(l.Broken[f[1]], "sha256:"+f[2])
		case f[0] == "C" && len(f) >= 2:
			for _, c := range f[2:] {
				if digestRe.MatchString(c) {
					l.Children["sha256:"+f[1]] = append(l.Children["sha256:"+f[1]], c)
				}
			}
		case f[0] == "D" && len(f) == 3:
			l.Used, _ = strconv.ParseInt(f[1], 10, 64)
			l.Free, _ = strconv.ParseInt(f[2], 10, 64)
		}
	}
	return l
}

// Ref splits an image reference in registry host's into its repository,
// tag and digest; ok is false for an image from elsewhere.
func Ref(image, host string) (repo, tag, digest string, ok bool) {
	rest, found := strings.CutPrefix(image, host+"/")
	if !found {
		return "", "", "", false
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		rest, digest = rest[:i], rest[i+1:]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && !strings.Contains(rest[i:], "/") {
		rest, tag = rest[:i], rest[i+1:]
	}
	return rest, tag, digest, rest != ""
}
