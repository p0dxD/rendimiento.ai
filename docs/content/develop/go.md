# Go for this codebase

This chapter teaches the Go you need to read and change rendimiento, using its own code as the examples. If you know another language (Python, JavaScript, Java), you will recognise most ideas; the differences are what this chapter is about.

## Why Go here

- **It is the language of Kubernetes.** The Kubernetes client (`client-go`), controller-runtime, Helm and kustomize are Go libraries. rendimiento *imports* them: it renders Helm charts with Helm's own code and kustomizations with kustomize's, instead of shelling out to tools.
- **One static binary.** `go build` produces a single executable with no runtime to install (no JVM, no Node, no Python). The container image is that file on an otherwise empty base.
- **Easy concurrency.** Goroutines and channels make "run these steps in parallel, at most four at a time" a few lines.
- **Simple, explicit, fast to compile.** Few features, strict formatting (`gofmt`), and errors as values make code easy to read months later, and easy for newcomers to contribute to.

## Modules and packages

A **module** is a versioned project: `go.mod` at the root names it (`github.com/p0dxD/rendimiento.ai`) and lists its dependencies with exact versions (`go.sum` holds their checksums). `go mod tidy` keeps both in order.

A **package** is a folder of `.go` files that share a `package` name. Each file begins with it and its imports:

```go
package controller

import (
	"context"                                   // standard library
	appsv1 "k8s.io/api/apps/v1"                 // a dependency, renamed
	"github.com/p0dxD/rendimiento.ai/internal/render" // another package of ours
)
```

- **`internal/`** is special: packages under it can only be imported from within this module. Everything except `api/v1alpha1` lives there, so the code can change freely without breaking outsiders.
- **`cmd/rendimiento`** has `package main` and `func main()`: the program's entry point.
- **Capitalisation is visibility.** `Render` (capital) is exported and usable from other packages; `render` (lowercase) is private to its package. There are no `public`/`private` keywords.

## Types: structs and methods

A **struct** groups fields. JSON and YAML names come from **struct tags**:

```go
type Health struct {
	Path    string `json:"path,omitempty"`    // HTTP check
	TCP     bool   `json:"tcp,omitempty"`     // TCP check
	Timeout int    `json:"timeout,omitempty"` // seconds
}
```

**Methods** are functions with a *receiver*. A pointer receiver (`*Spec`) can modify the value; a value receiver works on a copy:

```go
func (s *Spec) Default()        { /* fills in defaults in place */ }
func (s Size) Resources() Resources { return sizes[s] }
```

There are no classes and no inheritance. Behaviour is attached to types with methods, and shared through **interfaces** and **composition** (embedding one struct in another, as `AddonReconciler` embeds `client.Client` and so gains its `Get`, `List`, `Update` methods).

## Interfaces: the most important idea

An interface is a set of method signatures. **Any** type that has those methods satisfies it, without declaring so. That lets code depend on *what something does* rather than *what it is*:

```go
--8<-- "internal/dns/dns.go:provider"
```

The controller holds a `dns.Provider`. In production it is `*dns.Cloudflare`; without a token it is `dns.Noop{}`; in tests it is a fake that records calls in a map. The controller's code is the same in all three cases. This one idea is behind most of rendimiento's testability:

| Interface | Real implementation | Test implementation |
|---|---|---|
| `dns.Provider` | `Cloudflare` | `fakeDNS` |
| `pipeline.Executor` | `KubeExecutor` (pods) | `fakeExec` (sleeps, records order) |
| `platform.GitHub` | `github.Holder` (GitHub API) | `fakeGitHub` (in memory) |
| `addon.GitFetcher` | `GitHubFetcher` | `fakeGit` (`fstest.MapFS`) |
| `generate.Generator` | `Templates` | — (future: an AI generator) |
| `io.Writer`, `fs.FS` | log recorders, a GitHub tree | buffers, in-memory file trees |

Go convention: **accept interfaces, return concrete types**, and keep interfaces small and defined where they are *used* (the platform declares the `GitHub` interface with exactly the methods it calls).

## Errors are values

Functions return an `error` as their last result; callers check it right away. There are no exceptions:

```go
raw, err := p.GitHub.FileAt(ctx, app.InstallationID, app.Repo, spec.FileName, ref)
if err != nil {
	return nil, fmt.Errorf("read %s: %w", spec.FileName, err)   // add context, keep the cause
}
```

- **`%w`** wraps the cause, so callers can still ask about it with **`errors.Is`** (a specific value) or **`errors.As`** (a specific type).
- **Sentinel errors** name a condition: `store.ErrNotFound`, or the controller's `errBlocked` ("needs a human; retry slowly"):

```go
var errBlocked = errors.New("blocked")
...
if err != nil && !errors.Is(err, errBlocked) {
	return ctrl.Result{}, err // retried with backoff
}
```

- **`errors.Join`** combines many errors into one: `spec.Validate` reports every problem in a file at once.

## `context.Context`: cancellation and deadlines

Almost every function that does I/O takes a `ctx context.Context` first. It carries a **deadline** and a **cancellation signal** down the call chain: when a run is cancelled or a step times out, its context is cancelled and every HTTP call, Kubernetes request and wait loop beneath it stops.

```go
ctx, cancel := context.WithTimeout(ctx, k.Timeout)   // a step may run at most STEP_TIMEOUT
defer cancel()                                        // always release the timer
```

`defer` runs a call when the surrounding function returns, however it returns: the idiom for cleanup (closing files, deleting a build pod, releasing a lock).

## Concurrency: goroutines, channels, mutexes

A **goroutine** is a lightweight thread: `go f()` runs `f` concurrently. Thousands are cheap. The CI runner starts one per step.

A **channel** passes values between goroutines, and can be used as a signal or a semaphore:

```go
sem := make(chan struct{}, maxParallel) // a buffered channel with maxParallel slots
sem <- struct{}{}                        // take a slot (blocks when all are taken)
defer func() { <-sem }()                 // give it back
```

The runner uses exactly this to cap concurrent steps, and a closed channel per step (`close(done[s.ID])`) to tell dependents a step finished. **`select`** waits on several channels at once (a result, a timer, or cancellation).

A **`sync.Mutex`** protects shared data (the events hub's subscriber map, the renovate runner's "running" flag). **`sync.WaitGroup`** waits for a group of goroutines to finish. **`sync/atomic`** holds the GitHub App in a `Holder` that can be swapped at runtime after setup.

Rules of thumb used in the code: never share a map between goroutines without a lock; never block while holding a lock; always give goroutines a way to stop (a context).

## Embedding files: `//go:embed`

Files can be compiled *into* the binary:

```go
--8<-- "web/embed.go"
```

The UI (`web/dist`), the Dockerfile templates (`templates/`) and the SQL migrations (`internal/store/migrations`) are embedded this way, which is why the platform is one file.

## Generics

Since Go 1.18, functions can take type parameters. rendimiento uses them sparingly, for tiny helpers:

```go
func ptr[T any](v T) *T { return &v }   // ptr(false), ptr(60 * time.Second)
```

## Testing

Tests live next to the code in `*_test.go` files and run with `go test ./...`. A test is a function `func TestX(t *testing.T)`; `t.Errorf` records a failure and continues, `t.Fatalf` stops the test. The common shape is the **table-driven test**:

```go
for _, tc := range []struct{ yaml, want string }{
	{"services: [{name: a, needs: [mysql]}]", "not something rendimiento provides"},
	{"services: [{name: postgres}, {name: b, needs: [postgres]}]", "rename the service"},
} {
	if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
		t.Errorf("%s: got %v, want %q", tc.yaml, err, tc.want)
	}
}
```

Standard helpers used throughout: `httptest.NewServer` (a fake HTTP server, e.g. a Helm repository), `fstest.MapFS` (an in-memory file tree), `t.TempDir()`. More in [Testing](testing.md).

## Tooling

| Command | Does |
|---|---|
| `gofmt -w .` | Formats code the one standard way (run before every commit). |
| `go vet ./...` | Finds suspicious code (wrong format verbs, unreachable code…). |
| `go build ./...` | Compiles everything. |
| `go test ./pkg/...` | Runs tests. |
| `go mod tidy` | Syncs `go.mod`/`go.sum` with the imports. |
| `go doc pkg.Name` | Shows documentation from comments. |

**Doc comments** start with the name they describe (`// Render returns every object…`). Every exported name in this repository has one; the [code map](../reference/code-map.md) is generated from them.

## Building a static binary

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o rendimiento ./cmd/rendimiento
```

`CGO_ENABLED=0` avoids linking any C code, so the binary needs no system libraries. `GOOS`/`GOARCH` cross-compile: `GOOS=linux GOARCH=amd64 go build …` on an arm64 Pi produces an x86 binary, no emulation needed.

## Where to learn more

- *[A Tour of Go](https://go.dev/tour/)* (an afternoon) and *[Effective Go](https://go.dev/doc/effective_go)*.
- *[Go by Example](https://gobyexample.com/)* for quick recipes.
- The [controller-runtime book](https://book.kubebuilder.io/) (Kubebuilder) for controllers and CRDs.
