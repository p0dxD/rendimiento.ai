# Settings

rendimiento is configured only through **environment variables**, read once at startup in `cmd/rendimiento/main.go`. On the cluster, they come from the `rendimiento` ConfigMap and three Secrets (`deploy/rendimiento.yaml`). To change a setting, edit the ConfigMap and run `kubectl -n rendimiento-system rollout restart deploy/rendimiento`.

## Core

| Variable | Default | This cluster | Meaning |
|---|---|---|---|
| `DATABASE_URL` | *(required)* | from Secret `rendimiento-db` | Postgres connection string |
| `BASE_URL` | `http://localhost:8080` | `https://rendimiento.joserod.space` | public URL; used for OAuth callbacks, webhooks and links in GitHub checks |
| `ALLOWED_USERS` | *(empty: nobody)* | `p0dxD` | GitHub logins allowed to sign in (comma- or space-separated) |
| `SETUP_TOKEN` | — | from Secret `rendimiento-setup` | protects `/api/setup/github` until the GitHub App exists |
| `NAMESPACE` | `rendimiento-system` | | where the platform and its Secrets live |
| `LISTEN` | `:8080` | | HTTP address (API, UI, webhooks) |
| `METRICS_ADDR` | `:9090` | | Prometheus metrics (controller-runtime) |
| `LEADER_ELECTION` | `true` | `false` | one active replica; off because one Recreate replica never overlaps |

## GitHub

| Variable | Default | Meaning |
|---|---|---|
| `GITHUB_APP_NAME` | `rendimiento` | the App's name, used when creating it with the manifest flow (`rendimiento-joserod` here) |
| `GITHUB_SECRET` | `rendimiento-github` | the Secret the App's credentials are saved in after setup |

## Builds

| Variable | Default | This cluster | Meaning |
|---|---|---|---|
| `REGISTRY` | `registry.cube.local:5000` | | where images and build caches are pushed |
| `REGISTRY_INSECURE` | `true` | | registry over plain HTTP |
| `BUILD_NAMESPACE` | `rendimiento-builds` | | where build and test pods run |
| `BUILDKIT_POOL` | *(empty)* | `buildkitd-pool.devops-tools.svc.cluster.local` | headless Service of the daemon pool; images are spread across it by rendezvous hashing |
| `BUILDKIT_ADDR` | `tcp://buildkitd.devops-tools.svc.cluster.local:1234` | | the single daemon, used when there is no pool or the pool can't be resolved |
| `BUILDKIT_IMAGE` | `moby/buildkit:v0.18.2` | | image of the `buildctl` client in build pods |
| `MAX_PARALLEL_STEPS` | `2` | `4` | steps running at once, across all runs |
| `STEP_TIMEOUT` | `45m` | | longest one step may run (≥ 1m) |
| `BUILD_EXCLUDE_NODES` | *(empty)* | `podoi-ai` | nodes build pods must avoid |
| `RAILPACK_IMAGE` | *(empty: Railpack off)* | `registry.cube.local:5000/rendimiento-railpack:0.40.0` | the Railpack CLI image, for services without a Dockerfile |
| `RAILPACK_FRONTEND` | *(empty)* | `ghcr.io/railwayapp/railpack-frontend:v0.40.0` | the BuildKit frontend matching that version |

## Rendering apps

| Variable | Default | Meaning |
|---|---|---|
| `INGRESS_CLASS` | `nginx` | `ingressClassName` of app Ingresses |
| `CLUSTER_ISSUER` | `letsencrypt-prod` | cert-manager issuer for app certificates |
| `STORAGE_CLASS` | `longhorn` | storage class of `volume:` claims and `needs:` databases |
| `GPU_RESOURCE` | *(empty)* | resource name requested by `gpu:` services (`nvidia.com/gpu`) |
| `GPU_RUNTIME_CLASS` | *(empty)* | runtime class for GPU pods (`nvidia`) |
| `GPU_HOST_PATHS` | *(empty)* | host folders mounted read-only into GPU pods (driver libraries) |
| `GPU_ENV` | *(empty)* | `KEY=value;KEY=value` added to GPU containers |
| `GPU_SHARED_MEMORY` | *(empty)* | size of `/dev/shm` for GPU pods |

## DNS

Taken from the optional Secret `rendimiento-dns`. Without a token, DNS automation is off (`dns.Noop`).

| Variable | Default | Meaning |
|---|---|---|
| `CLOUDFLARE_API_TOKEN` | — | token with *Zone:DNS:Edit* |
| `DNS_TARGET` | — | where app hostnames point; an IP turns on dynamic DNS |
| `DNS_ZONE` | — | the default zone offered in the wizard (`joserod.space`) |
| `DNS_PROXIED` | `false` | create records behind Cloudflare's proxy (`true` here) |
| `DDNS_INTERVAL` | `5m` | how often dynamic DNS checks the public IP (≥ 1m) |

## Uptime checks

| Variable | Default | Meaning |
|---|---|---|
| `UPTIME_INTERVAL` | `1m` | How often every service is checked ([Reliability](../guide/reliability.md)); `0` turns checks off. At least `10s`. |
| `VERIFY_WINDOW` | `5m` | How long each new release is watched before it counts as verified ([verifying releases](../guide/reliability.md#verifying-each-release)); `0` turns verification and automatic rollback off. At least `1m`. |

## Notifications

| Variable | Default | This cluster | Meaning |
|---|---|---|---|
| `NOTIFY_EMAIL_TO` | *(empty: off)* | `jose0797@gmail.com` | who gets [notification emails](../guide/notifications.md), comma-separated |
| `NOTIFY_EMAIL_FROM` | `rendimiento <alerts@joserod.space>` | | the sender; must be on a Resend-verified domain |
| `RESEND_API_KEY` | — | from Secret `rendimiento-notify` | the Resend API key |

## Add-ons

| Variable | Default | Meaning |
|---|---|---|
| `ADDONS_REPO` | `p0dxD/gitops` | the git repository add-ons are declared in (`addons/*.yaml`) |
| `ADDONS_IDENTITY` | `system:serviceaccount:<NAMESPACE>:rendimiento-addons` | the identity add-on objects are applied as (impersonation) |

## Secrets the platform reads

| Secret (in `rendimiento-system`) | Keys | Written by |
|---|---|---|
| `rendimiento-db` | `password` | you, once |
| `rendimiento-setup` | `token` | you, once |
| `rendimiento-github` | App ID, private key, webhook secret, OAuth client | the setup flow |
| `rendimiento-dns` | `CLOUDFLARE_API_TOKEN`, `DNS_TARGET` | you |
| `rendimiento-notify` | `RESEND_API_KEY` | you |
