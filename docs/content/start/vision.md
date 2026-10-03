# Why rendimiento exists

## The problem it replaced

Before rendimiento, getting one app from a GitHub repository to a public HTTPS address on this cluster took about eleven manual steps, spread over four systems:

1. Write a `Jenkinsfile` and a `jenkinsconfig.yaml` for the shared Jenkins library.
2. Write a `Dockerfile`.
3. Write an `argocd/` folder: namespace, deployment, service, ingress, kustomization and an ArgoCD `Application`.
4. Add a job for the repo to the Ansible role that configures Jenkins, and run Ansible.
5. Add a GitHub webhook pointing at Jenkins.
6. Give ArgoCD credentials for the repo.
7. `kubectl apply` the ArgoCD Application.
8. Create the DNS record in Cloudflare by hand.
9. `kubectl create secret` for anything sensitive.
10. Push.
11. Watch CI commit a `[skip ci] bump image tags` back to the repo so ArgoCD would notice the new image, and hope the two systems agreed.

Every one of those steps was glue between tools, and each tool had its own credentials, its own idea of state and its own failure modes. Personal access tokens expired (Renovate's did, silently, for over a month). Deployments were only as good as the last hand-edited YAML.

## The goal

> Connect GitHub, pick a repo, press **Deploy**, get a live HTTPS URL. Then every push builds, tests and ships, with no manifests to write, and room to grow: visualizations, add-ons, and later chaos, performance and A/B testing.

## The decisions that shaped it

These choices were made at the start, and most of the rest of the book follows from them.

| Decision | Instead of | Why |
|---|---|---|
| **Build our own control plane** (API, CI engine, controllers) and reuse the cluster's building blocks (BuildKit, the registry, cert-manager, ingress-nginx, Longhorn, MetalLB) | Wrapping Jenkins and ArgoCD, or adopting a big PaaS | The pain was the *glue* between tools. One system that owns the whole path can make it one step, and the building blocks are already solid. |
| **Go** for the backend, **React + TypeScript** for the UI | Node, Python, Java | Go is the language of Kubernetes: its client libraries, controller-runtime, Helm and kustomize are Go libraries rendimiento imports directly. It compiles to one static binary that runs anywhere. See [Go for this codebase](../develop/go.md). |
| **One binary** that is API, UI, CI worker and controller at once | Microservices | Simpler to run and reason about on a small cluster. The parts are separate packages behind interfaces, so they can be split later ([Scaling](../future/scaling.md)). |
| A **GitHub App** | Personal access tokens, per-repo webhooks and deploy keys | One installation covers every repo: webhooks for all of them, and short-lived (1 hour) tokens minted on demand. Nothing long-lived to leak or expire. |
| **The release pointer lives in a Kubernetes object** (`App`), not in git | CI committing image tags back to the repo | No `[skip ci]` bump commits, no race between CI and GitOps, and rollback is re-pointing to an earlier release (images are pinned by digest). The *configuration* is still in git (`rendimiento.yaml`). |
| **Detection with templates**, behind a `Generator` interface | Asking the user for everything, or AI from day one | Rules and templates are predictable and testable; the interface leaves room for an AI generator later. |
| **Cloudflare DNS per app**, managed by the platform | DNS by hand | A domain in `rendimiento.yaml` should just work, including following the home network's changing public IP. |
| **Postgres** for platform state | Only Kubernetes objects | CI runs, logs and history are relational data with a queue; Kubernetes stores desired state. See [Data and state](../architecture/data.md). |

## How your apps moved onto it

rendimiento was built alongside the existing Jenkins and ArgoCD, and every app was migrated one at a time, **in place and without downtime**: the new workloads start next to the old ones, traffic moves only when they are ready, and the old objects are taken over or removed. Databases kept their volumes. Each migration was watched second by second.

| When (2026) | What |
|---|---|
| Sep 23–24 | First version: GitHub App setup, detection, CI on BuildKit, the App controller, DNS and TLS. Deployed at `rendimiento.joserod.space`. |
| Sep 24–26 | Migrated **secplus**, **simplerfc** (SQLite volume moved), **podoi** (with its Postgres), **stockpulse** (web, API, Postgres, scraper job), **wellness** (UI, API, Postgres, serverless functions). |
| Sep 27 | Migrated **jobsentry** (gate, ui, intel, whois in a namespace shared with ArgoCD), then **linguistic-ai + ollama** on the GPU node. Added Railpack builds, the Services catalog, categories. **Jenkins** switched off. **Renovate** became an add-on authenticated as the GitHub App. |
| Sep 28 | The **add-on engine**; took over **umami**, **hajimari**, **longhorn-backups** and **Longhorn** from ArgoCD with zero changes; **ArgoCD** switched off. Added `needs:`, Helm hooks, the BuildKit pool, remote tests and this book. |

## Lessons learned the hard way

Every one of these is now handled in the code, and most have a test. They are worth knowing because they will come up again in other forms.

??? failure "A Service's `targetPort` by name broke traffic during a takeover"
    Pods created by ArgoCD did not name their port `http`. When rendimiento took over the Service with `targetPort: http`, the old pods stopped receiving traffic for three minutes. Services now target the port **number**. ([controller](../architecture/controller.md))

??? failure "Server-side apply kept fields owned by the previous tool"
    After adopting an object, ArgoCD's field ownership stayed, so removing a field from `rendimiento.yaml` did not remove it from the object. Takeover now replaces the object once and converts rendimiento's ownership into the only apply owner. ([controller](../architecture/controller.md#adoption-taking-over-what-is-already-running))

??? failure "A stale CRD silently dropped new fields"
    Adding a field to the Go types without regenerating the CRD makes the API server prune it without an error. `make test` (and `make test-remote`) now always regenerate first.

??? failure "Leader election killed the platform mid-build"
    On a busy control plane, the leader lease timed out and the process exited, taking running builds with it. With one replica, leader election is off; interrupted runs are retried on restart.

??? failure "An unpinned Python dependency crashed an app"
    SQLAlchemy 2.1 made psycopg 3 the default Postgres driver, and wellness (on psycopg2) crash-looped. Dependencies are pinned, and Renovate keeps SQLAlchemy minor updates for manual review.

??? failure "A 'healthy' app during a stuck rollout"
    An app was reported Healthy while its new pods crash-looped behind the old ones. Health now requires the rollout to be complete and reports crash loops.

??? failure "The control plane's disk filled up"
    Old local `docker build`s left 24.7 GB of dangling images on `main`, which is also the k3s control plane. Builds now happen only on the BuildKit pool, and tests run on a worker with [`make test-remote`](../develop/testing.md#remote-tests).

??? failure "The GPU could not allocate memory after a reboot"
    CUDA on that board allocates from system RAM, and fragmented memory made 256 MB allocations fail with gigabytes free. The update playbook now drops caches and compacts memory before the node rejoins.

??? failure "Renovate's token expired for five weeks, silently"
    A personal token behind a CronJob stopped working and nobody noticed. Renovate now runs as a rendimiento add-on with a fresh GitHub App token per run, and its runs are visible in the UI.

## Where it is going

Short term: post-deploy tasks (migrations, smoke tests of the new version), and moving the apps' hand-written databases onto `needs:`. Jenkins and ArgoCD are already retired. Longer term: multiple clusters and cloud targets from the same `rendimiento.yaml`, and an open-source project others can run and contribute to. See [Roadmap](../future/roadmap.md) and [Becoming an open-source project](../future/open-source.md).
