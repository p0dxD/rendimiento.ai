# rendimiento

**Let your coding agents ship to infrastructure you own, safely.**

Coding agents now write more apps than a platform team can check by hand. rendimiento is the deploy platform that agents and people share. Connect a GitHub repo, press **Deploy**, and get a live HTTPS URL, on your own Kubernetes cluster or on one computer.

- **Agents work on it, people decide what ships.** Each agent connects over **MCP** with a key that has a role (reader or collaborator) and, if you want, a set of apps. It can read apps, runs, logs and request traces, test any branch in CI, and pick up work people asked for. It cannot deploy: a push to the main branch, by a person, does. Every call an agent makes is recorded (the Ruta's Activity tab), and what each agent did and handed over stays in **la Ruta**, the platform's shared memory.
- **What breaks explains itself.** A failed run gets an AI diagnosis and a proposed fix. A release that doesn't answer says why ("nothing listens on port 3000; inside its container it listens on 4000"), and it is rolled back.
- **One binary instead of Jenkins + Argo CD + hand-made DNS and permissions.** CI on BuildKit, GitOps delivery, certificates, DNS, databases on demand (`needs: [postgres]`) and add-ons, in one Go program with its UI. Apache-2.0.

```sh
# On one computer (Linux, with containerd or Docker):
curl -fsSL https://rendimiento.ai/instalar | sh

# On a Kubernetes cluster:
helm install rendimiento oci://ghcr.io/rendimiento-ai/charts/rendimiento --version 0.1.3 \
  -n rendimiento-system --create-namespace \
  --set baseURL=https://rendimiento.example.com --set firstAdmins=your-github-login \
  --set registry.host=registry.example.com
```

Read the book in [English](https://learn.rendimiento.ai) or [Spanish](https://aprende.rendimiento.ai), and see [Built for agents](docs/content/guide/agents.md) and `/llms.txt`.

## How it fits together

rendimiento runs a small home k3s cluster and deploys every app on it, including its own book, and it tests and builds itself on every push.

```mermaid
flowchart LR
    gh[GitHub App] -- push webhook --> api[API + UI]
    api --> pg[(Postgres<br/>apps, runs, releases, queue)]
    pg --> worker[CI worker]
    worker -- build pods --> bk[BuildKit pool] --> reg[(registry)]
    worker -- release --> app[App object]
    app --> ctrl[App controller] --> k8s[(workloads, ingress,<br/>TLS, DNS)]
    git[gitops repo] --> addons[Addon controller] --> k8s
```

## What it does

- **Onboarding.** It detects Go, Node, Python, Java, static sites and Dockerfiles (including monorepos), then opens a pull request that adds `rendimiento.yaml`.
- **CI.** Clone → test → build, run as pods on a BuildKit pool spread across the workers, with a registry cache. Repos with no Dockerfile are built with **Railpack**.
- **Delivery.** Each release is a set of image digests. The App controller renders Deployments, Services, Ingresses, certificates, volumes, DNS records, GPU workloads and LAN IPs. It prunes, self-heals, and rolls back in one click.
- **`needs:`.** Declare `postgres`, `redis` or another service, and it is provided and wired in.
- **Add-ons.** Helm charts and kustomizations are declared in a gitops repo. You get previews and diffs, adoption of existing releases, safety gates and Helm hooks. It replaced ArgoCD.
- **Renovate** can be turned on per app. The **Services** catalog lists every service with its connection details. The **Environment** page checks what the cluster is missing and explains how to fix it.

## The book

Everything is explained in **[the rendimiento book](docs/content/index.md)**: concepts, the architecture with diagrams, how the platform is deployed, the `rendimiento.yaml` reference, Go for this codebase, recipes for common changes, design patterns, scaling and the roadmap. It deploys itself from this repo (`rendimiento.yaml`, `docs/Dockerfile`) on the home network.

Preview it locally:

```sh
python3 -m venv .venv && . .venv/bin/activate && pip install -r docs/requirements.txt
cd docs && mkdocs serve
```

## `rendimiento.yaml`

This is the only file an app needs:

```yaml
services:
  - name: web
    path: .
    port: 3000
    size: small              # small | medium | large
    replicas: 2
    domain: myapp.example.com
    health: { path: /api/health }
    test: { image: node:20-bookworm, command: npm ci && npm test }
    needs: [postgres]        # DATABASE_URL is injected
    secrets: [myapp-env]     # values are set in the UI, never in git
    volume: { size: 5Gi, mount: /data }
```

## Develop

```sh
make test-remote   # generate, vet, all Go tests (unit, envtest, Postgres), UI typecheck, on a worker
make image         # build + push the platform image on the BuildKit pool
make deploy && kubectl -n rendimiento-system rollout restart deploy
```

Start with [Developing it](docs/content/develop/index.md). Don't compile or run tests on the control-plane node.

## Layout

| Path | Role |
|---|---|
| `cmd/rendimiento` | settings and wiring: the composition root |
| `api/v1alpha1` | `App` and `Addon` custom resources |
| `internal/spec`, `detect`, `generate` | `rendimiento.yaml`, stack detection, proposals |
| `internal/platform`, `pipeline`, `store` | webhooks → runs → releases, the CI runner, Postgres |
| `internal/render`, `controller` | spec → Kubernetes objects, and the controllers |
| `internal/addon`, `renovate`, `catalog`, `environment`, `dns`, `events`, `github`, `api` | add-ons, Renovate, Services page, Environment page, DNS, live updates, GitHub App, HTTP API |
| `web/` | React UI (embedded in the binary) |
| `deploy/` | the platform's own manifests |
| `docs/` | the book |
| `hack/` | test-remote, code map, doc-comment checker |

A generated [code map](docs/content/reference/code-map.md) lists every package, type and function.

## License

[Apache License 2.0](LICENSE), like the observa libraries ([observa-node](https://github.com/rendimiento-ai/observa-node), [-python](https://github.com/rendimiento-ai/observa-python), [-java](https://github.com/rendimiento-ai/observa-java)) and OpenTelemetry they build on.
