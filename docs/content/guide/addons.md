# Add-ons

Add-ons are the software your cluster needs that is not one of your apps: storage, dashboards, operators, the build pool. rendimiento installs them from **Helm charts** or **folders of manifests in git**, previews every change before applying it, and can take over what is already running without touching it. How it works inside: [the add-on engine](../architecture/addons.md).

## What is installed today

| Add-on | Source | Mode |
|---|---|---|
| `longhorn` | Helm chart `longhorn` v1.10.2 | manual sync, no pruning |
| `longhorn-backups` | `p0dxD/gitops/longhorn-backups` | automatic |
| `umami` | `p0dxD/gitops/umami` | automatic |
| `hajimari` | Helm chart `hajimari` 2.0.2 | automatic |
| `buildkit` | `p0dxD/gitops/buildkit` (the BuildKit pool) | automatic |

`kubectl get radd` shows them with their phase.

An add-on whose services have a MetalLB address shows it on its card and its page. Web UIs, such as Longhorn's at 192.168.1.73:8082 and Hajimari's at 192.168.1.78:3000, are links you can open. Other services, such as a database port, show the address as plain text.

## Install one from the catalog

**Add-ons → Add from the catalog → Install**, fill in the name, namespace, version and the few settings shown, optionally paste chart values in YAML (merged over the settings), choose the options, and **Install**. That commits `addons/<name>.yaml` to `p0dxD/gitops`; within seconds the add-on appears under *Installed*, is previewed, and is applied.

The options:

- **Take over what is already running**: adopt objects that exist (from ArgoCD, Helm or `kubectl`). The first sync only proceeds if it would change nothing.
- **Manual sync**: changes wait for you to review and press Sync.
- **Delete objects removed from the chart**: pruning (never CRDs, namespaces, volumes or storage classes).

*Any Helm chart* takes a repository URL, chart name and version. *Manifests from a git folder* takes a folder of the gitops repository (or `owner/repo` and a folder).

## Write one by hand

Commit a file to `p0dxD/gitops/addons/`:

```yaml
name: uptime-kuma
title: Uptime Kuma
category: monitoring
description: Uptime monitoring and a status page.
namespace: uptime-kuma
createNamespace: true
helm:
  repo: https://dirsigler.github.io/uptime-kuma-helm
  chart: uptime-kuma
  version: 4.2.0
values:
  volume:
    size: 2Gi
    storageClassName: longhorn
prune: true
```

| Field | Meaning |
|---|---|
| `name` | DNS label; also the default Helm release name. |
| `title`, `category`, `description` | How it is shown. |
| `namespace`, `createNamespace` | Where namespaced objects go; create it if missing. |
| `helm: {repo, chart, version}` | A chart from a classic Helm repository (`index.yaml`). |
| `git: {repo, path}` | A folder (kustomization or plain YAML); `repo` defaults to the gitops repository. |
| `releaseName` | Keep an existing Helm release's name when adopting (labels depend on it). |
| `values` | Chart values (Helm only). |
| `adopt`, `allowAdoptChanges` | Take over what exists; allow the takeover to change it. |
| `manualSync` | Changes wait for **Sync**. |
| `prune` | Delete objects that leave the source. |
| `suspend` | Stop syncing. |
| `skipHooks`, `hookTimeout` | Never run Helm hooks; seconds a hook Job may run (default 600). |

Unknown fields are errors; an invalid file is reported on the Add-ons page and ignored.

## Change, review, sync

Edit the definition on the add-on's page (**Edit definition**, which commits) or in git. The add-on's page shows the **preview**: how many objects would be created, updated or deleted, each with a diff, and which Helm hooks would run. In manual mode, press **Sync** when satisfied.

## When an add-on says…

| Phase | Meaning | What to do |
|---|---|---|
| **In sync** | Everything applied and unchanged since. | Nothing. |
| **Changes waiting** | Manual sync, and the source changed. | Review, then **Sync**. |
| **Review needed** | Taking over would change something that is running. | Read the diff; fix the definition, or **Allow these changes and take over**. |
| **Error** | Rendering, applying or a hook failed; the message says which. | Fix the source; it retries every 5 minutes. |
| **Suspended** | `suspend: true` or the `rendimiento.ai/paused` annotation. | **Resume**, or remove the annotation. |

## Remove an add-on

- **Stop managing (keep running)** deletes the definition; everything it installed keeps running, unmanaged.
- **Uninstall** first runs the chart's `pre-delete` hooks (if one fails, the uninstall stops), deletes what the add-on installed except CRDs, namespaces, volumes and storage classes, then runs `post-delete` hooks.

## Adopting something from ArgoCD, step by step

This is how umami, Hajimari, longhorn-backups and Longhorn were moved:

1. Write the definition so it renders **exactly** what runs: the same chart, version, release name and values (copy them from the ArgoCD Application), or the same git folder. Set `adopt: true`.
2. Commit it. The add-on previews against the live objects: **0 to update** means a perfect match, and it takes over. Anything else stops as *Review needed*, with the diffs.
3. Once it shows *In sync*, delete the ArgoCD Application **without cascading** (no finalizer), so ArgoCD lets go and nothing is deleted.
