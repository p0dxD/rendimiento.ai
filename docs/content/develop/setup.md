# Setting up and building

## Tools

| Tool | Version | Where it is on `main` | Used for |
|---|---|---|---|
| Go | 1.27 | `~/.local/go/bin` | building, tests, generators |
| Node.js + npm | 20 | system | the UI |
| controller-gen | v0.22.0 | `~/go/bin` | deepcopy code and CRDs from Go types |
| setup-envtest | v0.25.1 | `~/go/bin` | downloads a test Kubernetes API server + etcd |
| kubectl | matches the cluster | system | everything cluster-side |
| docker | any | system | only to run the `buildctl` client for `make image` |

The Makefile puts `~/.local/go/bin` and `~/go/bin` on the `PATH` for its targets. For a new machine:

```bash
# Go
curl -sL https://go.dev/dl/go1.27.1.linux-arm64.tar.gz | tar -C ~/.local -xz
export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.1
```

## The repository

```text
rendimiento.ai/
├── cmd/rendimiento/        main.go: settings, wiring, startup (the composition root)
├── api/v1alpha1/           App and Addon custom resources (Go types → CRDs)
├── internal/
│   ├── spec/               rendimiento.yaml: types, defaults, validation
│   ├── detect/             what a repository contains (language, port, tests, needs)
│   ├── generate/           proposals from detection (+ templates/ Dockerfiles)
│   ├── github/             the GitHub App: JWT, tokens, API client, repo trees, OAuth
│   ├── platform/           orchestration: webhooks → runs → releases → App objects
│   ├── pipeline/           CI: plan, runner, Kubernetes executor (build pods)
│   ├── store/              Postgres: apps, runs, logs, releases, sessions, add-ons
│   ├── render/             spec + images → Kubernetes objects (pure)
│   ├── controller/         App and Addon controllers, hooks, needs
│   ├── addon/              add-on rendering (Helm, kustomize), git sync, catalog
│   ├── renovate/           the Renovate add-on
│   ├── catalog/            the Services page
│   ├── environment/        the Environment page
│   ├── dns/                Cloudflare records, dynamic DNS
│   ├── events/             live-update hub (SSE)
│   └── api/                HTTP API, auth, webhooks, setup, the UI server
├── web/                    the React UI (src/) and its embed (embed.go)
├── templates/dockerfiles/  Dockerfile templates for detected stacks
├── deploy/                 manifests for the platform itself (kubectl apply -k)
├── docs/                   this book (MkDocs Material)
├── hack/                   developer tools: test-remote.sh, codemap, undoc
├── Dockerfile              the platform image (UI + Go, multi-stage)
├── Makefile                build, test, image, deploy
└── rendimiento.yaml        how rendimiento deploys this book
```

## Make targets

```makefile
--8<-- "Makefile"
```

| Target | Does | Where it runs |
|---|---|---|
| `make generate` | deepcopy code and CRDs from `api/v1alpha1` and `internal/spec` | here (light) |
| `make test-remote` | generate, vet, all Go tests, UI typecheck | **a worker node** |
| `make test` | the same | here: avoid on `main` |
| `make test-db` | a disposable Postgres for local tests | here |
| `make itest` | real builds on the BuildKit pool (Dockerfile and Railpack) | the cluster |
| `make image` | build and push `rendimiento:latest` | the BuildKit pool |
| `make railpack-image` | the Railpack CLI image for build pods | the BuildKit pool |
| `make deploy` | `kubectl apply -k deploy` | — |
| `make docs-codemap` | regenerate `docs/content/reference/code-map.md` | here |
| `make ui`, `make build` | UI and a local binary | here (heavy) |

## The change loop

```bash
# 1. edit code (and the book)
# 2. fast feedback on the package you touched (light enough for main)
go vet ./internal/render && go test ./internal/render
# 3. everything, on the cluster: push a branch, and rendimiento tests and
#    builds it like any app's (or make test-remote, on a worker)
git push origin HEAD:my-change
# 4. merge it; when main's run passes, the book redeploys itself and the
#    Environment page offers the new platform: Update
```

## Running rendimiento

rendimiento is a controller: **never run a second copy against the production cluster.** It would reconcile the same `App` objects as the real one, and the two would fight.

To run it outside the cluster, use a separate cluster (for example [k3d](https://k3d.io) or [kind](https://kind.sigs.k8s.io), on a laptop) with its own Postgres:

```bash
make test-db                                     # Postgres on 127.0.0.1:55432
kubectl apply -f deploy/crds/                    # against the test cluster
export DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/rendimiento?sslmode=disable
export ALLOWED_USERS=<your-github-login> BASE_URL=http://localhost:8080 SETUP_TOKEN=dev LEADER_ELECTION=false
go run ./cmd/rendimiento                         # uses your current kubeconfig
```

Then open `http://localhost:8080/api/setup/github?token=dev` to create a development GitHub App. Webhooks need a public URL (a tunnel such as `cloudflared`), or use the **Run** button instead of pushes.

## Working on the UI

```bash
cd web && npm ci
npm run dev          # Vite on :5173 with hot reload; /api is proxied to localhost:8080
npm run typecheck    # what the tests run
npm run build        # web/dist, embedded by the Go build
```

The dev server needs a rendimiento API on `localhost:8080`, the local one above. The session cookie is set by that server's login, so sign in through `http://localhost:8080` first.

## Generating code

`make generate` runs **controller-gen** twice:

- `object` writes `zz_generated.deepcopy.go`: the `DeepCopy` methods every Kubernetes type needs (the cache hands out copies);
- `crd` writes `deploy/crds/*.yaml` from the Go types and their `// +kubebuilder:` markers (validation, printer columns, scope, short names).

Both outputs are committed. After changing `api/v1alpha1` or `internal/spec`, regenerate, apply the CRD, and restart the platform.
