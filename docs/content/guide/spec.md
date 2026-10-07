# `rendimiento.yaml` reference

`rendimiento.yaml`, at the root of an app's repository, is the only file rendimiento needs. The wizard writes a first version; after that it is ordinary code: edit it in a PR, and the change deploys on merge. It is parsed **strictly** (an unknown or misspelt field is an error), defaults are filled in, and every problem is reported at once with its path (`services[1].port …`). The Go type is `spec.Spec` in [`internal/spec/spec.go`](https://github.com/p0dxD/rendimiento.ai/blob/main/internal/spec/spec.go).

## A complete example

```yaml
# Everything is optional except services[].name.
postgres: { version: "17", size: 10Gi }       # settings for needs: [postgres]
redis: { maxMemory: 128mb }                   # settings for needs: [redis]

services:
  - name: web                                 # a DNS label: lowercase, digits, dashes
    path: web                                 # folder to build (default ".")
    port: 3000                                # the port the app listens on (default 8080)
    domain: shop.joserod.space                # public HTTPS address (DNS + certificate)
    aliases: [www.shop.joserod.space]         # more hosts, same certificate
    size: medium                              # small | medium | large
    replicas: 2                               # >1 = zero-downtime rollouts
    health: { path: /api/health }             # readiness + liveness
    env: { NODE_ENV: production }
    secrets: [shop-web]                       # whole secrets as env vars
    needs: [redis, { service: api }]          # REDIS_URL, API_URL injected
    test: { image: "node:20", command: "npm ci && npm test" }

  - name: api
    path: api
    port: 8000
    routes: [shop.joserod.space/api]          # a path on web's host
    needs: [postgres]                         # DATABASE_URL + PG* injected
    secretEnv: { STRIPE_KEY: shop-api/stripe } # one variable from a secret key
    resources: { memory: 512Mi, memoryLimit: 1Gi }
    build: { builder: railpack, start: "uvicorn main:app --host 0.0.0.0 --port 8000" }

  - name: models
    image: dustynv/ollama:r36.4.0             # a ready-made image: nothing is built
    port: 11434
    gpu: 1
    volume: { size: 20Gi, mount: /root/.ollama }
    lan: { ip: 192.168.1.50 }                 # also reachable on the home network
    catalog:
      title: Ollama
      env: OLLAMA_HOST

jobs:
  - name: nightly-report
    schedule: "0 6 * * *"
    timeZone: America/New_York
    service: api                              # run with the api's image
    command: [python, report.py]

tasks:
  - name: mobile                              # a command run as a CI step
    image: node:20-bookworm
    path: mobile                              # runs only when mobile/ changes
    command: npx --yes eas-cli build --platform all --non-interactive --no-wait
    secretEnv: { EXPO_TOKEN: expo/token }
```

## Top level

| Field | Type | Default | Meaning |
|---|---|---|---|
| `services` | list | **required** | The app's services (at least one). |
| `jobs` | list | none | Scheduled jobs (CronJobs). |
| `builds` | list | none | Images built and tested but not deployed ([Builds](#builds)). |
| `tasks` | list | none | Commands run as CI steps ([Tasks](tasks.md)). |
| `verify` | object | on, 5 min, rollback | How each release is verified after it goes live: `window` (seconds, 60–3600), `rollback` (`false` only reports), `disabled` ([Reliability](reliability.md#verifying-each-release)). |
| `sharedNamespace` | bool | `false` | The app's namespace is owned by something else (e.g. ArgoCD): it must exist, and rendimiento never creates, labels, owns or deletes it. |
| `postgres` | object | see [needs](#postgres-and-redis) | Settings for `needs: [postgres]`. |
| `redis` | object | see [needs](#postgres-and-redis) | Settings for `needs: [redis]`. |

## Services

### Identity and source

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | **required** | DNS label; unique in the app. Also the Deployment's and Service's name. |
| `path` | string | `.` | Folder of the repository to build, relative to the root. |
| `image` | string | none | Run this ready-made image instead of building (e.g. `postgres:15-alpine`). No build or test step. |
| `watch` | list of paths | none | Other folders whose changes should also rebuild this service (shared code). |
| `language` | string | detected | Informational (shown in the UI). |

### Build and test

| Field | Type | Default | Meaning |
|---|---|---|---|
| `build.builder` | `dockerfile` \| `railpack` | automatic | Automatic: the Dockerfile if the folder has one, Railpack otherwise. |
| `build.dockerfile` | string | `Dockerfile` | Path of the Dockerfile, relative to `path`. |
| `build.args` | map | none | Build arguments (Dockerfile `ARG`s), or build environment for Railpack. `GIT_SHA` is always passed to Dockerfiles. |
| `build.start` | string | detected | Railpack only: the start command. |
| `test.image` | string | — | Image the test runs in. |
| `test.command` | string | — | Shell command, run in the service's folder; a non-zero exit fails the run and skips the build. |
| `test.size`, `test.resources` | as for the service | 250m / 256Mi, limit 2Gi | The test pod's requests and limits. |
| `test.timeout` | seconds | `STEP_TIMEOUT` | Only shorter than the platform's limit. |
| `test.env` | map | none | Environment variables for the tests. |
| `test.postgres` | bool | `false` | A throwaway PostgreSQL next to the tests, in `DATABASE_URL` ([Tests](builds.md#tests)). |
| `test.cache` | bool | `false` | Keep `/cache` between runs; Go, npm and pip use it ([Tests](builds.md#tests)). |

### Running

| Field | Type | Default | Meaning |
|---|---|---|---|
| `port` | int | `8080` | The port the app listens on. `PORT` is set to it. |
| `replicas` | int 0–10 | `1` | Pods. More than one gives zero-downtime rollouts. |
| `size` | `small` \| `medium` \| `large` | `small` | Resource preset (below). |
| `resources` | object | none | Override any of `cpu`, `memory` (requests), `cpuLimit`, `memoryLimit`; a limit may be `none`. |
| `health.path` | string | none | HTTP GET check for readiness and liveness. |
| `health.tcp` | bool | `false` | Check that the port accepts connections (databases, non-HTTP). |
| `health.timeout` | int 1–60 | 1 | Seconds per check. |
| `command`, `args` | lists | the image's | Override the entrypoint and arguments. |
| `gpu` | int 0–8 | `0` | GPUs to request (one replica only). The cluster's GPU profile adds the runtime class, drivers and shared memory. |

Without `health`, a TCP readiness check on the port is used, so traffic only moves to a pod once it listens.

| Size | CPU request | Memory request | CPU limit | Memory limit |
|---|---|---|---|---|
| `small` | 50m | 64Mi | 500m | 256Mi |
| `medium` | 100m | 256Mi | 1 | 512Mi |
| `large` | 250m | 512Mi | 2 | 1Gi |

### Configuration and secrets

| Field | Type | Meaning |
|---|---|---|
| `env` | map | Plain environment variables. |
| `secrets` | list of names | Load whole Secrets as environment variables (`envFrom`). Missing secrets do not block the pod. Values are set on the app's **Settings** page or come from a sealed secret. |
| `secretEnv` | map `VAR: secret/key` | One variable from one key of a Secret. |
| `secretFiles` | list of `{secret, mount}` | Mount a Secret as read-only files. |
| `configFiles` | list of `{configMap, mount}` | Mount an existing ConfigMap as read-only files. |

### Storage

| Field | Type | Default | Meaning |
|---|---|---|---|
| `volume.size` | quantity | — | A new Longhorn volume (`<service>-data`) of this size. |
| `volume.mount` | path | **required** | Where to mount it. |
| `volume.existingClaim` | name | none | Mount an existing PersistentVolumeClaim instead (keeps its data; used when migrating). |
| `volume.fsGroup` | int | 1001 for built images, none for ready-made ones | Group that may write the volume; `-1` disables it. |

A service with a volume uses the **Recreate** strategy (a volume attaches to one pod at a time), so its updates have a short gap. Volumes are **never deleted** by removing them from the file.

### Exposure

| Field | Type | Meaning |
|---|---|---|
| `domain` | hostname | Public HTTPS address: an Ingress, a certificate and a DNS record. |
| `aliases` | hostnames | More hosts on the same certificate (needs `domain`). |
| `routes` | `host/path` list | Send a path on a host owned by another service of this app to this service, e.g. `shop.joserod.space/api`. |
| `tlsSecret` | name | Certificate secret name (default `<name>-tls`); keep an existing one when migrating. |
| `ingress.name` | name | Keep an existing Ingress's name when migrating. |
| `ingress.annotations` | map | Extra `nginx.ingress.kubernetes.io/*` settings (rate limits, body size, CORS…). Snippets are refused. |
| `ingress.tlsSecrets` | map `host: secret` | Put some hosts on their own certificate. |
| `streaming` | bool | Unbuffered, long-lived responses (server-sent events, streamed AI answers, websockets). |
| <a id="lan"></a>`lan.ip` | private IPv4 | Also expose on the home network through MetalLB, at this address (from its pool; empty lets it choose). |
| `lan.port` | int | Port on that address (default 80). The app page and dashboard link to the address once MetalLB assigns it. |

### Needs

`needs` lists what the service depends on. See [Needs and the services catalog](needs.md).

| Form | Provides | Injected |
|---|---|---|
| `postgres` | the app's Postgres | `DATABASE_URL`, `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` |
| `redis` | the app's Redis cache | `REDIS_URL` |
| `{service: namespace/name}` or `{service: name}` | another service's address | `<NAME>_URL` |
| any of the above with `env: VAR` (`{postgres: {env: DB_URL}}`) | the same | under `VAR` |

Variables you set yourself in `env` or `secretEnv` always win.

### Catalog

How the service is described on the Services page, for other apps:

| Field | Meaning |
|---|---|
| `catalog.title`, `catalog.description` | Shown instead of the name. |
| `catalog.category` | `database`, `messaging`, `ai`, `storage`, `monitoring`, `web`, `devtools` or `platform` (otherwise guessed). |
| `catalog.env` | The variable callers usually put the address in (e.g. `OLLAMA_HOST`). |
| `catalog.path` | Appended to the address in the suggested value (e.g. `/analyze`). |
| `catalog.docs` | Link to API documentation. |
| `catalog.endpoints` | Short lines such as `POST /analyze: classify a message`. |

## Postgres and Redis

| Field | Default | Meaning |
|---|---|---|
| `postgres.version` | `17` | Major version of `postgres:<v>-alpine`. |
| `postgres.size` | `5Gi` | Volume size. |
| `postgres.resources` | 100m / 256Mi, limit 512Mi | Overrides. |
| `redis.version` | `7` | Version of `redis:<v>-alpine`. |
| `redis.maxMemory` | `64mb` | Cache size; least recently used keys are evicted. |
| `redis.resources` | 50m / 64Mi, limit 256Mi | Overrides. |

## Jobs

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string ≤ 52 | **required** | Unique among jobs and services. |
| `schedule` | cron | **required** | Standard 5-field cron or `@daily` style. |
| `timeZone` | IANA name | cluster time (UTC) | e.g. `America/New_York`. |
| `service` / `path` / `image` | exactly one | — | Use a service's released image, build this folder, or run a ready-made image. |
| `watch`, `build` | as for services | | For `path` jobs. |
| `command`, `args` | lists | | What to run. |
| `size`, `resources` | as for services | `small` | |
| `timeout` | seconds | none | Stop a run after this long. |
| `env`, `secretEnv`, `secrets` | as for services | | |

## Builds

Images built on every run like a service's, but not deployed; their digests are kept with each release ([Images that are not services](builds.md#images-that-are-not-services)).

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string ≤ 40 | **required** | Unique among services, jobs, builds and tasks. The steps are `<name>:test` and `<name>:build`; the image is `<registry>/<app>-<name>`. |
| `path`, `watch` | as for services | `.` | What is built, and what counts as a change. |
| `build` | as for services | `Dockerfile` | |
| `test` | as for services | none | Run before the build. |

## Tasks

Commands run as CI steps; see [Tasks](tasks.md) for when they run and how secrets work.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string ≤ 40 | **required** | Unique among services, jobs, builds and tasks. The step is `<name>:task`. |
| `stage` | `build` \| `pre-deploy` \| `post-deploy` | `build` | `build`: a CI step. `pre-deploy`: after the build, before the rollout; a failure stops the release ([pre-deploy](tasks.md#pre-deploy-tasks)). `post-deploy`: against the live release, as part of verification; a failure rolls it back ([post-deploy](tasks.md#post-deploy-tasks)). |
| `service` | service name | — | Pre- and post-deploy: run in this service's new image with its environment (instead of `image`). |
| `image` | image | **required** (build) | What the command runs in. Post-deploy tasks give `image` or `service`. |
| `command` | string | **required** | Run with `sh -c`. |
| `path` | string | `.` | Working directory, relative to the repo root; also what counts as a change. |
| `watch` | list of paths | none | More paths whose changes run the task. |
| `after` | list | none | Services and builds (their build) and tasks to wait for. |
| `when` | `deploy` \| `always` | `deploy` | `deploy`: pushes to the default branch only. `always`: every run, including branches and pull requests. (Not `on:`, which YAML reads as `true`.) |
| `optional` | bool | `false` | A failure doesn't fail the run. |
| `size`, `resources` | as for services | `medium` | |
| `timeout` | seconds | `STEP_TIMEOUT` | Only shorter than the platform's limit. |
| `env`, `secrets`, `secretEnv` | as for services | | Secrets are read from the app's namespace when the step starts. |
