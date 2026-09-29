// Package pipeline plans and executes CI runs. Every step is a Kubernetes
// pod: an init container clones the commit, then the step runs its tests or
// drives the cluster's BuildKit daemon to build and push an image.
package pipeline

import (
	"fmt"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// Kind is what a step does: test or build.
type Kind string

const (
	KindTest  Kind = "test"
	KindBuild Kind = "build"
)

// Status is a step's state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped" // a dependency failed
	StatusReused    Status = "reused"  // unchanged since the last release; its image is reused
)

// Done reports whether the step has finished (in any way).
func (s Status) Done() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusSkipped || s == StatusReused
}

// Step is one node of the run's DAG.
type Step struct {
	ID        string   `json:"id"` // "<service>:<kind>"
	Service   string   `json:"service"`
	Kind      Kind     `json:"kind"`
	Path      string   `json:"path"`
	Image     string   `json:"image,omitempty"`   // test image
	Command   string   `json:"command,omitempty"` // test command
	DependsOn []string `json:"dependsOn,omitempty"`
	// Target is the image name pushed by a build step (without tag).
	Target     string `json:"target,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
	// Builder is dockerfile, railpack or empty: decided in the build pod
	// (the Dockerfile if the folder has one, Railpack otherwise).
	Builder   string            `json:"builder,omitempty"`
	Start     string            `json:"start,omitempty"`
	BuildArgs map[string]string `json:"buildArgs,omitempty"`
}

// Source identifies the commit being built.
type Source struct {
	Repo   string // owner/name
	SHA    string
	Branch string
	// Deploy is true for pushes to the default branch; PR runs only verify.
	Deploy bool
}

// Plan builds the DAG for a spec: per service, test (if configured) then build.
// Services are independent of each other and run in parallel.
func Plan(app, registry string, s spec.Spec) []Step {
	var steps []Step
	for _, svc := range s.Services {
		if svc.Image != "" {
			continue // ready-made image: nothing to test or build
		}
		build := Step{
			ID: svc.Name + ":build", Service: svc.Name, Kind: KindBuild, Path: svc.Path,
			Target: fmt.Sprintf("%s/%s-%s", registry, app, svc.Name), Dockerfile: svc.Build.Dockerfile,
			Builder: svc.Build.Builder, Start: svc.Build.Start, BuildArgs: svc.Build.Args,
		}
		if svc.Test != nil {
			test := Step{ID: svc.Name + ":test", Service: svc.Name, Kind: KindTest, Path: svc.Path, Image: svc.Test.Image, Command: svc.Test.Command}
			steps = append(steps, test)
			build.DependsOn = []string{test.ID}
		}
		steps = append(steps, build)
	}
	for _, j := range s.Jobs {
		if j.Path == "" {
			continue
		}
		steps = append(steps, Step{
			ID: "job-" + j.Name + ":build", Service: j.ImageKey(), Kind: KindBuild, Path: j.Path,
			Target: fmt.Sprintf("%s/%s-%s", registry, app, j.Name), Dockerfile: j.Build.Dockerfile,
			Builder: j.Build.Builder, Start: j.Build.Start, BuildArgs: j.Build.Args,
		})
	}
	return steps
}
