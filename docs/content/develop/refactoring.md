# Refactoring and code health

rendimiento grew quickly, one feature at a time, with tests at every step. The code is healthy. It has no global state, it is small (about 21,000 lines of Go and TypeScript) and every exported name is documented. Some parts are now doing more than one job. This chapter lists what to improve and how to do it safely.

## How to refactor safely here

1. **Start green.** Run `make test-remote` before touching anything.
2. **Move, then change.** First move code without changing behaviour (a file split, a rename) and commit it on its own. Change behaviour in a separate commit. A reviewer can then check the move mechanically.
3. **Lean on the golden files.** A refactor of `render` must leave every `testdata/*.golden.yaml` untouched. If a golden file changes, the refactor changed behaviour.
4. **Keep packages pure.** Don't let `spec`, `render`, `detect`, `generate` or `pipeline.Plan` start doing I/O. They are fast to test because they don't.
5. **Leave exported names documented.** Run `go run ./hack/undoc` to see any that are missing a doc comment, then `make docs-codemap`.

## The largest files

| File | Lines | Doing |
|---|---|---|
| `internal/api/server.go` | ~1,070 | routing, auth, sessions, apps, runs, releases, secrets, events, setup |
| `internal/spec/spec.go` | ~970 | every type of `rendimiento.yaml`, defaults, validation |
| `internal/platform/platform.go` | ~850 | webhooks, runs, releases, onboarding PRs, App objects |
| `internal/renovate/renovate.go` | ~700 | scheduler, job rendering, config, status |
| `internal/catalog/catalog.go` | ~680 | the Services page: discovery, connection info, LAN |
| `internal/controller/app_controller.go` | ~620 | reconcile, apply, prune, takeover, needs, DNS, status |

Large files are not a problem in themselves. Go is often written in long, flat files. These files are worth splitting because each one mixes several concerns.

## Suggested refactors, in order of value

### Split `api/server.go` by area

`addons.go`, `installed.go` and `credentials.go` already show the pattern. Move the handlers into `apps.go`, `runs.go`, `auth.go`, `setup.go` and `events.go`. Keep `server.go` for the `Server` struct, `Handler()` (all routes in one place is useful) and the shared helpers (`writeJSON`, `httpError`, `auth`). This is a pure move with no behaviour change, and it is a good first contribution.

### Split `spec.go` into types, defaults and validation

Split it into `types.go` (structs and their `+kubebuilder` markers), `defaults.go` and `validate.go`. Both controller-gen and the CRD read the types, so keeping them together helps when you review a schema change.

### Give the controller's steps names

`AppReconciler.Reconcile` reads as a list of stages: render, needs, apply, prune, DNS and status. Turn each stage into a method that takes the same `appState` struct and returns an error. The reconcile function then fits on one screen, and each stage can get focused envtest cases.

### Typed SQL with sqlc

Queries are hand-written strings scanned by hand. [sqlc](https://sqlc.dev) generates type-safe Go from `.sql` files. That catches column typos at build time and removes the scanning boilerplate. Adopt it one store file at a time, and keep the same method signatures so callers don't change.

### Share types between Go and TypeScript

`web/src/api.ts` repeats the JSON shapes that the Go handlers return, by hand. When a Go field changes, nothing fails until a page breaks. Options, from simplest to most complete:

- **[tygo](https://github.com/gzuidhof/tygo)**: generates TypeScript interfaces from Go structs. Add it to `make generate`, and let the UI typecheck catch drift.
- **OpenAPI**: describe the API (hand-written or generated), then generate a typed client with `openapi-typescript`. This also gives outside users documentation for the API. It is worth doing before [open-sourcing](../future/open-source.md).

### Structured, levelled logging everywhere

`log/slog` is already used in most packages, and controller-runtime logs through `logr` (bridged in `cmd/rendimiento/logr.go`). Give every log line an `app`, `run` or `addon` attribute so logs can be filtered, and add a `LOG_LEVEL` setting.

### Metrics of our own

`METRICS_ADDR` (:9090) exposes controller-runtime's metrics: reconcile counts, errors and queue depth. Add platform metrics too: runs by outcome, build duration per service, queue wait time, BuildKit daemon choice and releases. These metrics make it possible to answer questions like "are builds getting slower?". See [Scaling](../future/scaling.md).

### UI structure

The pages hold their own data-loading and form logic. As they grow, move shared pieces (a status badge, log viewer or diff view) into `components/`. Consider a data library (TanStack Query) instead of `usePoll`, and a component test setup (Vitest and Testing Library) so the UI gets tests.

### Configuration as a struct

`main.go` reads about 40 environment variables with `env("NAME", default)`. Gather them into one `Config` struct with a `Load()` function that validates everything at once. The code can then print the effective configuration at startup (with secrets redacted) and generate [the settings reference](../reference/config.md).

## Known rough edges

- **Single platform replica.** Leader election exists, but `LEADER_ELECTION=false` in the deploy, and live updates are in-memory (see [Scaling](../future/scaling.md#live-updates-across-replicas)).
- **Logs in Postgres.** Build logs are stored as rows. This works at this scale, but they belong in object storage (MinIO or S3) over time.
- **Registry over plain HTTP.** `registry.example.lan:5000` is insecure. This is fine on a home LAN, but it needs TLS for anything else.
- **arm64 only.** Images are built for the cluster's architecture. Multi-arch is covered in [Open source](../future/open-source.md#releases-and-multi-arch-images).
- **Retries.** Transient build failures are retried once (`TestTransientRetry`). Other steps are not retried.
