// Package pipeline plans and executes CI runs. Every step is a Kubernetes
// pod: an init container clones the commit, then the step runs its tests or
// drives the cluster's BuildKit daemon to build and push an image.
package pipeline

import (
	"fmt"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// Kind is what a step does: test, build, or run a task.
type Kind string

const (
	KindTest  Kind = "test"
	KindBuild Kind = "build"
	KindTask  Kind = "task"
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

	// Task steps: the app (whose namespace holds the secrets they read),
	// environment, secrets, resources and an optional shorter timeout.
	App       string            `json:"app,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Secrets   []string          `json:"secrets,omitempty"`
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
	Resources spec.Resources    `json:"resources,omitempty"`
	Timeout   time.Duration     `json:"timeout,omitempty"`
	// Optional steps may fail without failing the run.
	Optional bool `json:"optional,omitempty"`

	// Test steps: a throwaway Postgres next to the tests (DATABASE_URL),
	// and a /cache kept between runs (one volume per app and step).
	Postgres bool `json:"postgres,omitempty"`
	Cache    bool `json:"cache,omitempty"`
}

// TaskStepID is the ID of a task's step.
func TaskStepID(name string) string { return name + ":task" }

// TaskKey is the Service field of a task's step: what change detection and
// the run page group it by.
func TaskKey(name string) string { return "task:" + name }

// Source identifies the commit being built.
type Source struct {
	Repo   string // owner/name
	SHA    string
	Branch string
	// Deploy is true for pushes to the default branch; PR runs only verify.
	Deploy bool
}

// Plan builds the DAG for a spec: per service, its test (if configured) and
// its build, side by side; job images; the builds (test and build); then
// tasks, after the builds (and their tests) and tasks they name. A release
// needs every step to succeed, so an image is only released once tested;
// building while testing saves the wait. Services are independent of each
// other and run in parallel.
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
			steps = append(steps, testStep(app, svc.Name, svc.Name, svc.Path, *svc.Test))
		}
		steps = append(steps, build)
	}
	for _, b := range s.Builds {
		build := Step{
			ID: b.Name + ":build", Service: b.ImageKey(), Kind: KindBuild, Path: b.Path,
			Target: fmt.Sprintf("%s/%s-%s", registry, app, b.Name), Dockerfile: b.Build.Dockerfile,
			Builder: b.Build.Builder, Start: b.Build.Start, BuildArgs: b.Build.Args,
		}
		if b.Test != nil {
			steps = append(steps, testStep(app, b.Name, b.ImageKey(), b.Path, *b.Test))
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
	for _, t := range s.Tasks {
		if t.Deploys() {
			continue // runs around the deploy, in the app's namespace
		}
		step := Step{
			ID: TaskStepID(t.Name), Service: TaskKey(t.Name), Kind: KindTask, Path: t.Path,
			Image: t.Image, Command: t.Command, App: app, Env: t.Env, Secrets: t.Secrets, SecretEnv: t.SecretEnv,
			Resources: spec.ResourcesFor(t.Size, t.Resources), Timeout: time.Duration(t.Timeout) * time.Second,
			Optional: t.Optional,
		}
		for _, a := range t.After {
			if isTask(s, a) {
				step.DependsOn = append(step.DependsOn, TaskStepID(a))
			} else {
				// A tested image: its build and, when it has one, its test.
				step.DependsOn = append(step.DependsOn, a+":build")
				if hasTest(s, a) {
					step.DependsOn = append(step.DependsOn, a+":test")
				}
			}
		}
		steps = append(steps, step)
	}
	return steps
}

// testStep is the test of a service or build named name, whose image key is key.
func testStep(app, name, key, dir string, t spec.Test) Step {
	return Step{
		ID: name + ":test", Service: key, Kind: KindTest, Path: dir, Image: t.Image, Command: t.Command,
		App: app, Env: t.Env, Resources: t.Requests(), Timeout: time.Duration(t.Timeout) * time.Second,
		Postgres: t.Postgres, Cache: t.Cache,
	}
}

// isTask reports whether name is a task of the spec.
func isTask(s spec.Spec, name string) bool {
	for _, t := range s.Tasks {
		if t.Name == name {
			return true
		}
	}
	return false
}

// hasTest reports whether the service or build named name has a test step.
func hasTest(s spec.Spec, name string) bool {
	for _, svc := range s.Services {
		if svc.Name == name {
			return svc.Test != nil && svc.Image == ""
		}
	}
	for _, b := range s.Builds {
		if b.Name == name {
			return b.Test != nil
		}
	}
	return false
}
