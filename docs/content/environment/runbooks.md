# Runbooks

Each runbook is: how you notice, what is going on, and what to do. Commands assume `kubectl` on `main`.

## Everyday checks

```bash
kubectl get apps.rendimiento.ai          # every app and its phase
kubectl get radd                         # every add-on (radd = rendimiento add-on; k3s has its own "addons")
kubectl -n rendimiento-system logs deploy/rendimiento --since=10m
```

The **Environment** page shows the same, plus every dependency, node and failing pod.

## A build failed

1. Open the run in the UI: each step's log is kept (the last 1 MB), with the BuildKit daemon that built it.
2. Typical causes:
    - **The code or its dependencies**: fix and push; the run's check on GitHub links to the log.
    - **`deadline exceeded`**: a step ran longer than `STEP_TIMEOUT` (45 minutes). Cold caches plus native Python wheels are the usual reason; the next build is faster.
    - **`OOMKilled` in a test step**: test pods are limited to 2 GiB.
    - **`database is locked` / 5xx from the API server**: the control plane was overloaded. Pod and secret creation already retry these; if it keeps happening, see [the control plane is slow](#the-control-plane-is-slow-or-its-disk-is-full).
    - **Railpack could not detect a start command**: add `build.start`, or a Dockerfile.
3. Re-run from the app's **Runs** tab (**Run**), or push again.

## An app is Degraded

The app page shows each service's message, for example `pod api-…: CrashLoopBackOff (6 restarts)` or a stuck rollout.

```bash
kubectl -n <app> get pods
kubectl -n <app> logs deploy/<service> --previous    # the crashed container's last output
```

The previous pods keep serving during a failed rolling update, so there is usually no outage. To go back immediately: **Releases → Roll back** on the last good release. Then fix forward.

## Roll back an app

UI: **Releases → Roll back** on any release. This creates a new release with that release's images (by digest) and spec, and the controller rolls it out. Nothing is rebuilt.

## A certificate is not issued

The service card shows *TLS certificate* in yellow.

```bash
kubectl -n <app> get certificate,certificaterequest,order,challenge
kubectl -n cert-manager logs deploy/cert-manager --since=30m | grep -i <domain>
```

cert-manager solves DNS-01 challenges through Cloudflare with its own token (secret `cloudflare-api-key-secret`). If that token expired or lost permission, every new certificate fails: create a new token with *Zone:DNS:Edit* and update the secret.

## A domain points at the wrong IP

The home network's public IP changes. With `DNS_TARGET=auto`, rendimiento checks it every `DDNS_INTERVAL` (5 minutes) and repoints every record it manages, plus any record whose Cloudflare comment contains `rendimiento-ddns`. To force it: **Environment → DNS → Update now**. Records created by hand without that marker are never changed.

## The control plane is slow or its disk is full

Signs: `kubectl` is slow; Longhorn's CSI pods restart with `failed to renew lease … context deadline exceeded`; builds fail with `database is locked`; `No space left on device`.

`main` is both a Raspberry Pi and the k3s control plane (with SQLite). Heavy compiles or tests on it slow the API server for the whole cluster.

```bash
uptime; df -h /
docker system df                  # dangling images from old local builds
docker image prune -f             # removes only untagged (dangling) images
go clean -cache                   # the Go build cache (rebuilt on demand)
```

Prevention: build images with `make image` (runs on the BuildKit pool), run tests with `make test-remote` (runs on a worker), never `docker build` on `main`.

## The AI models fail with "cudaMalloc … out of memory"

Signs: ollama answers `/api/tags` but every generation fails, and jobsentry's `/analyze` returns `SYSTEM_FAILURE`. `tegrastats` on the Jetson shows a low `lfb` (largest free block), e.g. `lfb 18x4MB`.

The Jetson's GPU takes its memory from system RAM, and after a reboot, memory can be too fragmented to hand CUDA a large block, even with gigabytes free. Fix:

```bash
ssh -i ~/.ssh/ansible_automation podoi@podoi-ai \
  'sudo sysctl vm.drop_caches=3 && sudo sysctl vm.compact_memory=1'
kubectl -n jobsentry rollout restart deploy/ollama-internal
```

The Sunday update playbook does this automatically before the Jetson rejoins the cluster (`gpu_memory_compact: true` in its host vars). ollama also runs with `OLLAMA_NUM_PARALLEL=1` to keep its context cache small.

## A storm is coming

`scripts/storm_watch.py` (every 10 minutes) watches the weather service and runs `storm_shutdown.yml` for severe warnings:

- apps listed in `rendimiento_apps` get `spec.suspend: true`;
- add-ons listed in `rendimiento_addons` get the annotation `rendimiento.ai/paused=true` (an annotation, so the git sync does not undo it);
- the listed databases are scaled to zero.

`storm_startup.yml` reverses it. To pause an add-on by hand:

```bash
kubectl annotate radd umami rendimiento.ai/paused=true --overwrite   # pause
kubectl annotate radd umami rendimiento.ai/paused-                  # resume
```

## An add-on says "Review needed" (Blocked)

Taking over something already running would change it. Open the add-on's page: the diff shows exactly which fields differ between what runs and what the definition renders. Either fix the definition (usually a chart value or version) so the diff disappears, or, if the change is intended, press **Allow these changes and take over**.

## Upgrade Longhorn (or any add-on)

1. Edit the version (and values) on the add-on's page, or in `p0dxD/gitops/addons/longhorn.yaml`, and commit.
2. Longhorn uses **manual sync**, so nothing happens yet: the page shows every object that would change, with diffs, and the hooks that would run (Longhorn's `post-upgrade` job).
3. Read Longhorn's upgrade notes for the version, make sure all volumes are healthy, then press **Sync**. Pre-upgrade hooks run first, the objects are applied, and then the post-upgrade hooks; each hook's result is listed on the page.

## Renovate does nothing

On the Add-ons page:

- A red box about **permissions** means the GitHub App lacks *Issues* or *Commit statuses*. Grant them, then accept on the installation.
- A run that succeeds with `0 PRs opened` usually means old `renovate/*` branches that the new bot did not create and will not touch, filling its branch limit. Delete those branches, or see [Renovate](../guide/renovate.md#branches-left-by-an-earlier-renovate).
- Set **Log level** to *debug*, press **Run now**, and read the log for the reason.

## Backups

- **App databases** on Longhorn: the `longhorn-backups` add-on configures MinIO as the backup target and a nightly `backup-postgres` recurring job. Volumes join it through the label `recurring-job-group.longhorn.io/backup-postgres: enabled`.
- **The platform database** (`rendimiento-db`) holds history (runs, logs, releases) but not what is running: apps keep running without it, and releases can be recreated from git. Adding it to the backup group is on the [roadmap](../future/roadmap.md).
- **Git** holds each app's `rendimiento.yaml` and the add-on definitions.

## Rotate secrets

| Secret | How |
|---|---|
| Cloudflare token (platform) | New token → `kubectl -n rendimiento-system edit secret rendimiento-dns` (or recreate it) → restart the platform |
| Cloudflare token (cert-manager) | New token → update `cloudflare-api-key-secret` in `cert-manager` |
| GitHub App private key | Generate a new key in the App's settings → update `rendimiento-github` → restart; delete the old key |
| An app's secret | App page → **Settings → Secrets** (or a sealed secret in the repo) |

## Retire ArgoCD and Jenkins for good

Both are scaled to zero and manage nothing. When you are ready:

```bash
helm uninstall argocd -n argocd          # frees 192.168.50.74
helm uninstall jenkinsci -n devops-tools # frees 192.168.50.89; the jenkins-home volume stays
kubectl delete namespace argocd
```

Then remove their Ansible roles from `~/main_configs`.
