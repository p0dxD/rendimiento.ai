package platform

import (
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

func TestReusableBuilds(t *testing.T) {
	sp := spec.Spec{Services: []spec.Service{{Name: "docs", Path: "docs"}},
		Builds: []spec.ImageBuild{{Name: "platform", Path: "cmd"}, {Name: "tool", Path: "tool"}}}
	prev := &store.Release{Number: 3, Images: map[string]string{
		"docs": "r/docs@sha256:1", "build:platform": "r/platform@sha256:2", "build:tool": "r/tool@sha256:3"}}
	reuse, _ := reusable(sp, []string{"cmd/main.go"}, prev)
	if reuse["build:platform"] != "" || reuse["build:tool"] != "r/tool@sha256:3" || reuse["docs"] != "r/docs@sha256:1" {
		t.Fatalf("reuse = %v", reuse)
	}
}
