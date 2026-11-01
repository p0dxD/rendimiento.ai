package pipeline

import (
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// The Environment page lowers what a step asks for, never what it may use,
// and adds nodes that take no runs to BUILD_EXCLUDE_NODES.
func TestCapacityLimits(t *testing.T) {
	k := &KubeExecutor{ExcludeNodes: []string{"podoi-ai"}, Limits: func() ([]string, int64) { return []string{"colorpi", "podoi-ai"}, 1500 }}
	res, _ := taskResources(spec.Resources{CPURequest: "2", CPULimit: "4"})
	k.capCPU(&res)
	if got := res.Requests.Cpu().MilliValue(); got != 1500 {
		t.Fatalf("request %dm, want 1500m", got)
	}
	if got := res.Limits.Cpu().MilliValue(); got != 4000 {
		t.Fatalf("limit %dm, want it left at 4000m", got)
	}
	small, _ := taskResources(spec.Resources{CPURequest: "500m"})
	k.capCPU(&small)
	if got := small.Requests.Cpu().MilliValue(); got != 500 {
		t.Fatalf("a small request went to %dm", got)
	}
	off := k.affinity().NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values
	if len(off) != 2 || off[0] != "podoi-ai" || off[1] != "colorpi" {
		t.Fatalf("nodes off %v", off)
	}
	if (&KubeExecutor{}).affinity() != nil {
		t.Fatal("no nodes off, yet an affinity")
	}
}
