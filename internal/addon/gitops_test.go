package addon

import (
	"strings"
	"testing"
)

func TestDefinition(t *testing.T) {
	d, err := ParseDefinition([]byte(`
name: hajimari
namespace: hajimari
createNamespace: true
helm: {repo: https://hajimari.io, chart: hajimari, version: 2.0.2}
values:
  hajimari: {title: Home Lab}
adopt: true
prune: true
`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := d.Spec("p0dxD/gitops", "abc")
	if err != nil || s.Source.Helm.Chart != "hajimari" || !strings.Contains(s.Values, "title: Home Lab") || !s.Adopt || !s.CreateNamespace {
		t.Fatalf("spec = %+v, %v", s, err)
	}

	g, err := ParseDefinition([]byte("name: umami\nnamespace: umami\ngit: {path: umami}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s, _ = g.Spec("p0dxD/gitops", "abc")
	if s.Source.Git.Repo != "p0dxD/gitops" || s.Source.Git.Revision != "abc" {
		t.Errorf("a folder of the gitops repo renders the definition's commit: %+v", s.Source.Git)
	}
	other, _ := ParseDefinition([]byte("name: x\nnamespace: x\ngit: {repo: o/other, path: deploy}\n"))
	if s, _ := other.Spec("p0dxD/gitops", "abc"); s.Source.Git.Revision != "" {
		t.Error("another repo follows its default branch")
	}

	for _, bad := range []string{
		"name: Bad\nnamespace: x\ngit: {path: a}\n",
		"name: x\nnamespace: x\n",
		"name: x\nnamespace: x\ngit: {path: a}\nhelm: {repo: r, chart: c, version: v}\n",
		"name: x\nnamespace: x\ngit: {path: ../etc}\n",
		"name: x\nnamespace: x\ngit: {path: a}\nvalues: {a: 1}\n",
		"name: x\nnamespace: x\ngit: {path: a}\nunknownField: 1\n",
	} {
		if _, err := ParseDefinition([]byte(bad)); err == nil {
			t.Errorf("should be invalid:\n%s", bad)
		}
	}
	out, _ := d.Marshal()
	back, err := ParseDefinition(out)
	if err != nil || back.Helm.Version != "2.0.2" {
		t.Errorf("round trip: %v", err)
	}
}
