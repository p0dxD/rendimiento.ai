# rendimiento.ai

Connect a GitHub repo, press **Deploy**, get a live HTTPS URL. Rendimiento combines CI (Jenkins' job) and GitOps CD (ArgoCD's job) in one small Go service for a Kubernetes cluster.

```
GitHub App ──push webhook──► API server ──► Postgres (apps, runs, releases, queue)
                                 │
                                 ├─► CI worker ──► pods in rendimiento-builds
                                 │     clone → test → buildctl (shared buildkitd) → registry
                                 │
                                 └─► App CR (spec + image digests) ──► App controller
                                        renders Namespace/Deployment/Service/Ingress/PVC,
                                        server-side apply, prune, self-heal, health,
                                        Cloudflare DNS; cert-manager issues TLS
```

## How an app flows through it

1. **New app** in the UI → pick a repo. `internal/detect` recognizes Go, Node (Next, Vite, Express, Nest), Python (FastAPI, Flask, Django), Java (Maven, Gradle), static sites and existing Dockerfiles, including monorepos.
2. The form is pre-filled: public URL, size, instances, port, health check, storage, secrets, tests.
3. Rendimiento opens a PR adding `rendimiento.yaml`, plus a Dockerfile if the repo has none. The PR's branch build shows as a GitHub check.
4. Merge → push to the default branch → test + build → **release #N** → deployed. Other branches build but never deploy.
5. Roll back to any release with one click; set secrets in the UI (write-only, straight into Kubernetes).

`rendimiento.yaml` is the only file an app needs:

```yaml
services:
  - name: web
    path: .
    port: 3000
    size: small            # small | medium | large
    replicas: 2
    domain: myapp.joserod.space
    health: { path: /api/health }
    test: { image: node:20-bookworm, command: npm ci && npm test }
    env: { NODE_ENV: production }
    secrets: [myapp-env]   # values set in the UI, never in git
    volume: { size: 5Gi, mount: /data }
```

## Safety rails

- Apps never take over a namespace or hostname they don't own. Apps still deployed through Jenkins + ArgoCD are left alone, and a clash shows as an error on the app.
- DNS records are tagged `managed-by=rendimiento` in their Cloudflare comment. Records made by hand are never changed or deleted.
- Volumes are never pruned automatically.
- Failed or cancelled runs never deploy. A test failure skips that service's build.
- Only GitHub users in `ALLOWED_USERS` can sign in; webhooks are HMAC-verified; fork PRs are never built.

## Deploy

```sh
make image                     # build + push registry.example.lan:5000/rendimiento:latest
kubectl apply -f deploy/namespace.yaml
kubectl create secret generic -n rendimiento-system rendimiento-db --from-literal=password=$(openssl rand -hex 24)
kubectl create secret generic -n rendimiento-system rendimiento-setup --from-literal=token=$(openssl rand -hex 24)
make deploy
```

Then:

1. Add a Cloudflare record for `rendimiento.joserod.space` (the same as for your other apps).
2. Open `https://rendimiento.joserod.space`. Paste the setup token:
   `kubectl get secret rendimiento-setup -n rendimiento-system -o jsonpath='{.data.token}' | base64 -d`
3. Click **Create GitHub App**, then install the app on the repos to deploy.

**Optional: automatic DNS.** Create a Cloudflare API token with *Zone:Read* and *DNS:Edit*, then:

```sh
kubectl create secret generic -n rendimiento-system rendimiento-dns \
  --from-literal=CLOUDFLARE_API_TOKEN=... --from-literal=DNS_TARGET=<public IP or tunnel host>
kubectl rollout restart -n rendimiento-system deploy/rendimiento
```

Settings live in the `rendimiento` ConfigMap (`deploy/rendimiento.yaml`).

## Develop

```sh
make test-db   # disposable Postgres on :55432
make test      # Go (unit, envtest, Postgres) + UI typecheck
make itest     # real build through the cluster's buildkitd
cd web && npm run dev   # UI on :5173, proxies /api to :8080
```

| Package | Role |
|---|---|
| `internal/spec` | `rendimiento.yaml` schema, defaults, validation |
| `internal/detect`, `internal/generate`, `templates/` | stack detection, proposed spec, Dockerfile templates (`Generator` interface, ready for an AI implementation) |
| `internal/render` | spec + images → Kubernetes objects |
| `api/v1alpha1`, `internal/controller` | `App` CRD and its reconciler |
| `internal/pipeline` | DAG runner with a concurrency limit; pod executor using buildkitd |
| `internal/github` | GitHub App auth, manifest setup, webhooks, check runs, onboarding PRs |
| `internal/platform` | webhook → run → release → App orchestration, rollback |
| `internal/store` | Postgres: apps, runs, logs, releases, sessions, job queue |
| `internal/api`, `web/` | REST + SSE API and the React UI |

## Roadmap

- Plugins at pipeline and rollout hooks: k6 performance gates, Chaos Mesh experiments, A/B and canary splits through ingress-nginx canary weights.
- Multiple environments with promotion (dev → staging → prod).
- AI `Generator` that refines Dockerfiles, health checks and sizing.
- Creating new repos from starter templates in the wizard.
- More clusters and clouds through a pull-based agent per cluster.
