# Roadmap

Items are ordered by value per effort for this cluster first, and for other users second. Each lists where the work goes, so any item can be picked up as a project.

## Near term

| # | Item | Why | Where |
|---|---|---|---|
| 1 | **Post-deploy tasks**: run a task in the app's namespace after the release is healthy (migrations, smoke tests of the new version) | tasks today run before the release, in the isolated build namespace | `spec.Task` (`stage: post-deploy`), the App controller or platform after `pointApp` |
| 2 | **Move apps' databases onto `needs:`** | one way to get Postgres, with credentials generated and wired in | per app: `rendimiento.yaml`, data migration runbook |
| 3 | **Platform metrics** + a Grafana dashboard | see builds slowing or queues growing before users do | `pipeline`, `platform`, `METRICS_ADDR` |
| 4 | **Log archive** to MinIO | keep Postgres small | `store`, `platform/recorder.go` |
| 5 | **Notifications**: run failed or release rolled back, sent to email, Discord or ntfy | know without watching the UI | new `internal/notify`, `platform` |
| 6 | **Preview environments** per pull request (`pr-42.app.joserod.space`) | try changes before merging | `platform` (PR webhooks), `render` (name suffix), cleanup on close |

## Medium term

| Item | Why |
|---|---|
| **Canary and A/B releases** with ingress-nginx canary weights | ship to 10% first; automatic rollback on errors |
| **Plugin contract**: a container that takes JSON in and returns a JSON verdict, at hooks *pre-deploy*, *post-deploy* and *analysis* | k6 performance gates, chaos experiments (Chaos Mesh), AI checks, all without changing the core |
| **AI generator** behind `generate.Generator` | better `rendimiento.yaml` and Dockerfiles for unusual repos |
| **Secrets from an external store** (SOPS in git, Vault, 1Password) | secrets become reproducible and auditable |
| **Roles and audit log** | more than one person can use it safely |
| **HA platform** (leader election, `LISTEN/NOTIFY` events) | no downtime on node loss; see [Scaling](scaling.md#live-updates-across-replicas) |
| **Scheduled jobs** (`cron:` in the spec, rendered as CronJobs) | replaces the last hand-written CronJobs |

## Long term

| Item | Why |
|---|---|
| **Multi-cluster agents** and cloud targets (EKS, GKE, bare metal) | deploy the same app to the Pis and to the cloud; see [Scaling](scaling.md#many-clusters) |
| **Environments and promotion** (dev → staging → prod by digest) | the same image, promoted once it's proven |
| **Cluster provisioning** as a plugin (Terraform or Crossplane) | a new cluster in one click |
| **Installable by anyone** (Helm chart, multi-arch images, docs site) | see [Open source](open-source.md) |

## Done

- Repo → live HTTPS URL; GitHub App, onboarding pull requests, detection and templates
- CI on a BuildKit pool across the workers with a registry cache; Railpack Dockerfile-less builds
- The App controller: adoption, pruning, self-healing, rollback, DNS, GPU workloads, LAN IPs
- `needs:` for Postgres, Redis and existing services
- An add-on engine (Helm and kustomize from git), with previews, safety gates and Helm hooks, which replaced ArgoCD
- Renovate as an add-on, which replaced the CronJob
- ArgoCD and Jenkins uninstalled (2026-09-28)
- Tasks: commands as CI steps with secrets, for Expo/EAS builds and smoke tests (2026-09-29)
- The Services catalog, Environment checks, dynamic DNS
- Remote tests (`make test-remote`), and this book
