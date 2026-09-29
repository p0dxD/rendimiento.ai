# Design patterns

The patterns below are the load-bearing ideas of the codebase. Each has a *why*, and a *where* so you can see it in the code. When you add something, reach for these first: code that follows the existing shapes is easier for the next person (including future you) to read.

## Architecture-level patterns

### Modular monolith

One binary, many packages with clear boundaries (`spec`, `render`, `pipeline`, `controller`…). Packages talk through small interfaces, never through shared globals.

- **Why:** one Deployment is easy to run on a Pi cluster, one version to reason about, no network calls between components, no distributed transactions. The package boundaries keep it *splittable*: the worker, controllers and API could become separate Deployments by changing only `cmd/rendimiento/main.go` (see [Scaling](../future/scaling.md)).
- **Where:** `cmd/rendimiento/main.go` wires everything.

### Composition root / dependency injection by hand

`main.go` reads settings, builds every concrete object (store, GitHub holder, DNS provider, executor, runner, platform, controllers, API server) and passes each one what it needs through struct fields.

```go
--8<-- "cmd/rendimiento/main.go:startup"
```

- **Why:** no DI framework, no hidden magic. Reading `main.go` top to bottom tells you exactly what runs and with what. Tests build the same structs with fakes.

### Reconciliation loop (level-triggered control)

Controllers don't react to *what happened*; they compare *what should be* (the `App` or `Addon` spec) with *what is* (the cluster) and act to close the gap. Every event just means "look again".

```go
--8<-- "internal/controller/app_controller.go:reconcile"
```

- **Why:** it's self-healing. Missed events, crashes, a person running `kubectl delete`: the next reconcile fixes it. It's also idempotent: running twice does no harm.
- **Where:** `AppReconciler.Reconcile`, `AddonReconciler.Reconcile`, and in spirit the add-on git syncer and the DDNS loop (periodic "make it so").

### Declarative desired state + server-side apply

The controller computes the complete desired objects (`render.Render`) and hands them to Kubernetes with **server-side apply** under the field manager `rendimiento`. Kubernetes merges, tracks field ownership, and reports conflicts.

- **Why:** no read-modify-write races, no diffing by hand; fields other controllers own (HPA replicas, cert-manager annotations) are left alone.

### Pure core, imperative shell

`spec`, `detect`, `generate`, `render`, `pipeline.Plan`, `addon.Render` are **pure**: input in, output out, no cluster, no network. The *shell* (controllers, executor, API handlers) does I/O and calls into them.

- **Why:** the pure parts hold the tricky logic and are tested with fast unit and golden tests. The shell is thin and tested with envtest.

### Work queue in the database (transactional outbox-ish)

Runs are rows. Workers claim one with `SELECT … FOR UPDATE SKIP LOCKED`:

```go
--8<-- "internal/store/store.go:claim"
```

- **Why:** Postgres is already there; no Redis/RabbitMQ to run. `SKIP LOCKED` lets many workers claim different rows without blocking each other, and a crash leaves the row claimable again (`RequeueOrphans` at startup).

### GitOps for configuration, database for releases

The *configuration* of an app lives in its repo (`rendimiento.yaml`), add-ons in `p0dxD/gitops`. The *release pointer* (which image digest runs) lives in the `App` object and the `releases` table.

- **Why:** config gets code review and history; releases don't create `[skip ci]` commit noise, and rollback is instant (repoint to an older digest).

## Code-level patterns

### Small interfaces, defined by the consumer

`platform.GitHub`, `pipeline.Executor`, `dns.Provider`, `addon.GitFetcher`, `generate.Generator`. Each declares only the methods its user calls. Real implementations and test fakes both satisfy them. See [Go for this codebase](go.md#interfaces-the-most-important-idea).

### Strategy

A family of interchangeable algorithms behind one interface, chosen at startup or per call:

| Decision | Strategies |
|---|---|
| How to build an image | Dockerfile vs **Railpack** (`build.builder`) |
| Which DNS provider | `Cloudflare` vs `Noop` |
| How an add-on renders | Helm chart vs kustomize/plain manifests |
| Which BuildKit daemon | pool (rendezvous hash) vs a single address |

### Adapter

`github.Holder` adapts the GitHub REST API to `platform.GitHub`; `GitHubFetcher` adapts a repo tree to `fs.FS` so Helm and kustomize can read it like a local folder; the log recorder adapts Kubernetes log streams to `io.Writer` + the store + SSE.

### Holder (atomic swap)

The GitHub App credentials don't exist until setup finishes. `github.Holder` wraps an `atomic.Pointer` so the running server can swap in a configured App without a restart, and every caller sees either "not configured" or a complete App, never half of one.

### Observer / publish-subscribe

`events.Hub` fans out change notifications (run updated, app status changed) to every browser connected over **Server-Sent Events**. Producers call `Publish` and don't know who listens.

### Builder-free options with defaults

Specs and settings are plain structs; a `Default()` method fills gaps and `Validate()` checks the result, reporting **every** problem at once (`errors.Join`). No builder chains, no functional options: the YAML *is* the options.

### Template method (pipelines)

`pipeline.Plan` turns a spec into a DAG of steps (clone → test → build per service). The runner walks any DAG the same way; only the steps differ. New step kinds plug in without touching the runner (see [Recipes](recipes.md#add-a-new-kind-of-ci-step)).

### Rendezvous hashing

`buildkitFor` chooses a BuildKit daemon for an image by hashing *(image, daemon)* and taking the highest score:

```go
--8<-- "internal/pipeline/kube.go:buildkitFor"
```

- **Why:** the same image always lands on the same daemon (warm cache), and adding/removing a daemon only moves the images that hashed to it. No coordinator needed.

### Ownership and labels as the index

Everything rendimiento creates carries `app.kubernetes.io/managed-by: rendimiento` and `rendimiento.ai/app` (or `rendimiento.ai/addon`) labels, and owner references where possible. Pruning is "list by label, delete what isn't desired"; adoption is "take over objects that match but lack our labels, only after a human agrees".

### Safety gates

Destructive or surprising changes stop and ask: adoption of existing objects, add-ons with `manualSync`, `neverDelete` kinds (CRDs, Namespaces, PVCs, PVs, StorageClasses), the pause annotation, pre-delete hooks. The controller returns `errBlocked` and shows *why* in status, rather than guessing.

### Sentinel errors and wrapped errors

`store.ErrNotFound`, `errBlocked`, and `fmt.Errorf("…: %w", err)` everywhere. Callers branch on *kinds* of failure with `errors.Is`.

### Embed everything

UI, templates and migrations are `//go:embed`-ed. The image is one static binary; a deploy can never mix a new binary with old assets.

## Patterns deliberately *not* used

| Not used | Why not (yet) |
|---|---|
| ORM | Plain SQL with `pgx` is shorter, faster, and every query is visible. |
| Message broker | The Postgres queue is enough at this scale; see [Scaling](../future/scaling.md). |
| Microservices | One person, one cluster: the operational cost would dwarf the benefit. The boundaries exist to split later. |
| Plugin loading (`plugin` package, WASM) | Extension points today are interfaces compiled in; the plugin contract (containers, JSON in/out) is on the [roadmap](../future/roadmap.md). |
| Global loggers/config | Everything is passed explicitly, so tests can run in parallel. |
