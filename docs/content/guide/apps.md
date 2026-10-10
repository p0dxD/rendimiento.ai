# Apps day to day

## Deploy a new repository

1. **New app** → pick the repository.
2. Check the proposal: one form per detected service. Set the public URL (a subdomain under one of your Cloudflare zones), size, health check, storage and needs. Choose **from source** (Railpack) or **add a Dockerfile** if the folder has none.
3. **Open PR & deploy**. rendimiento opens a PR adding `rendimiento.yaml` (and the Dockerfile if chosen). The PR's branch is built and checked right away.
4. **Merge the PR.** That push to the default branch builds, releases and deploys. The DNS record and certificate follow within a minute or two.

A repository that already contains `rendimiento.yaml` skips the PR: its first build starts immediately.

!!! tip "Monorepos"
    Each folder with its own manifest (`package.json`, `go.mod`…) becomes a service. The first UI-like service gets the bare domain; the others get `<service>-<app>.<zone>`. Adjust freely.

## Migrate an app that is already running

When the namespace the app would use already exists (deployed by ArgoCD, Helm or `kubectl`), the wizard shows what is running there (deployments, hosts, secrets, the ArgoCD app) and offers to **take it over**. Typical steps for an app coming from ArgoCD:

1. In `rendimiento.yaml`, keep the existing names where they matter: `tlsSecret` (reuse the certificate), `ingress.name`, `volume.existingClaim` (reuse the data), `secrets` (reuse the Secrets).
2. Detach the ArgoCD Application **without cascading** (it has no finalizer, or remove it first) so ArgoCD stops managing but nothing is deleted.
3. Onboard with **take over**. New pods start next to the old ones; traffic moves only when they are ready; old objects are replaced in place or removed. See [adoption](../architecture/controller.md#adoption-taking-over-what-is-already-running).
4. If ArgoCD also owns the namespace (other things still live there), use `sharedNamespace: true`.

For a volume with a database, the switch has a short gap (the volume moves from the old pod to the new one): 15 to 50 seconds in the migrations done so far.

## Domains, aliases and routes

- `domain` gives a service `https://<domain>`: an Ingress, a Let's Encrypt certificate and a Cloudflare record that follows the network's public IP.
- `aliases` adds hosts on the same certificate (e.g. `www.`).
- `routes` sends a path on another service's host to this service: a UI at `wellness.jobsentry.net` and its API at `wellness.jobsentry.net/api`, each deployed on its own.

The app's **Overview** lists every address of each service: its domain, its aliases and its routes.

## Secrets

Declare names in `rendimiento.yaml` (`secrets`, `secretEnv`, `secretFiles`); set the values on the app's **Settings** page, or commit **sealed secrets** (encrypted with the cluster's public key, only the cluster can decrypt them). Values never go to git in plain text or to the platform database.

On the **Settings** page each secret lists the keys it holds (names only; values are never shown again). Saving `KEY=value` lines adds or replaces those keys and keeps the others; **×** on a key removes it. Either way the app restarts to pick up the change.

## Volumes

`volume: { size: 5Gi, mount: /data }` creates a Longhorn volume that survives restarts, rollouts and even removing the service from the file. Use `existingClaim` to keep an existing volume. Add a Longhorn backup label to include it in the nightly backups.

## Scheduled jobs

`jobs:` run on a cron schedule, with a service's image, their own folder or a ready-made image; resources, timeouts and secrets work like services. Watch runs with `kubectl -n <app> get cronjobs,jobs`.

## GPUs

`gpu: 1` requests a GPU. The cluster's GPU profile (settings `GPU_*`) adds the NVIDIA runtime class, the host's driver libraries (read-only), `NVIDIA_*` variables and a larger shared-memory mount. A GPU service runs one replica with the Recreate strategy.

## Expose a service on the LAN

`lan: { ip: 192.168.1.50 }` adds a LoadBalancer Service with that address from MetalLB's pool, on port 80 (or `lan.port`). Useful for dashboards and tools that should not be public. This book is served that way.

## Rollback

**Releases → Roll back** on any release. A new release with that release's images and spec is created and rolled out. Nothing is rebuilt; it takes as long as a rollout.

## Suspend

`kubectl patch apps.rendimiento.ai <app> --type merge -p '{"spec":{"suspend":true}}'` stops reconciliation: manual changes (like scaling a database to zero during a storm) are left alone until you resume with `"suspend":false`.

## Delete or disconnect

On **Settings → Danger zone**:

- **Delete** first shows exactly what would be removed (namespace, volumes and their sizes, secrets), then removes the app, its namespace and its DNS records. For a shared namespace, only the app's own objects are removed.
- **Disconnect** stops managing the app and leaves everything running (DNS records included), for when you want to hand it back to another tool.
