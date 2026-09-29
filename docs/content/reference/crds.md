# Custom resources

rendimiento adds two **cluster-scoped** resources in the `rendimiento.ai/v1alpha1` API group. They are generated from `api/v1alpha1/*_types.go` into `deploy/crds/`.

```bash
kubectl get apps.rendimiento.ai          # or: kubectl get app
kubectl get addons.rendimiento.ai        # or: kubectl get radd
```

!!! note "Why `radd`?"
    k3s already has a resource named `addons` (`addons.k3s.cattle.io`). Plain `kubectl get addons` would be ambiguous, so the short name is `radd`.

## App

One `App` per onboarded repository. The **platform** writes it (from `rendimiento.yaml` and the latest release). The **App controller** turns it into workloads. You rarely edit it by hand.

| Field | Meaning |
|---|---|
| `spec.repo` | `owner/name` on GitHub |
| `spec.services[]` | the services from `rendimiento.yaml` ([spec](../guide/spec.md)) |
| `spec.jobs[]` | scheduled or one-off jobs |
| `spec.sharedNamespace` | deploy into an existing namespace shared with other things |
| `spec.postgres`, `spec.redis` | options for `needs:` databases |
| `spec.images` | service → image reference, pinned by digest |
| `spec.release` | the release number these images belong to |
| `spec.suspend` | stop reconciling (manual changes are left alone) |
| `spec.adopt` | allow taking over existing objects with matching names |
| `status.phase` | `WaitingForBuild`, `Progressing`, `Healthy`, `Degraded`, `Suspended`, `Error` |
| `status.message` | why, in one line |
| `status.release` | the release actually applied |
| `status.services[]` | replicas, ready replicas, image, URL, LAN URL (`lanURL`), certificate and DNS readiness per service |
| `status.conditions` | standard Kubernetes conditions |

`kubectl get app` shows **Phase**, **Release**, **Repo** and **Age**.

## Addon

One `Addon` per installed cluster add-on. They come from `addons/*.yaml` in the gitops repo (`ADDONS_REPO`), which the syncer applies. The **Addon controller** renders and applies them.

| Field | Meaning |
|---|---|
| `spec.namespace` | where the objects go |
| `spec.source.helm` | `repo`, `chart`, `version` of a Helm chart |
| `spec.source.git` | `repo`, `path`, `revision` of plain manifests or a kustomization |
| `spec.values` | Helm values (YAML text) |
| `spec.releaseName` | Helm release name (defaults to the add-on's name); matters for adopting an existing release |
| `spec.createNamespace` | create `spec.namespace` if it's missing |
| `spec.adopt` | allow taking over objects that already exist |
| `spec.allowAdoptChanges` | allow adoption even when it would change live objects (otherwise **Blocked**) |
| `spec.prune` | delete objects that are no longer rendered |
| `spec.manualSync` | changes wait in **OutOfSync** until approved |
| `spec.syncRequest` | bump to approve (the **Sync** button) |
| `spec.suspend` | stop reconciling |
| `spec.skipHooks` | don't run Helm hooks |
| `spec.hookTimeout` | seconds a hook may take |
| `spec.title`, `spec.category`, `spec.description` | how it appears in the UI |
| `status.phase` | `Pending`, `Synced`, `OutOfSync`, `Blocked`, `Error`, `Suspended` |
| `status.revision` | chart version or git commit applied |
| `status.objects[]` | inventory: every object it owns (used for pruning and uninstall) |
| `status.preview` | create/update/unchanged/prune counts with per-object diffs |
| `status.hooks[]`, `status.hookRuns[]` | hooks found in the chart and their recent runs |
| `status.appliedHash` | hash of what was applied; distinguishes install from upgrade for hooks |
| `status.lastSynced`, `status.appliedSyncRequest`, `status.adopted` | bookkeeping |

The annotation **`rendimiento.ai/paused: "true"`** pauses an add-on without changing its spec. Scripts and automation use it.

`kubectl get radd` shows **Phase**, **Namespace**, **Revision** and **Age**.

## Labels and annotations

| Key | On | Meaning |
|---|---|---|
| `app.kubernetes.io/managed-by: rendimiento` | everything created | ownership |
| `rendimiento.ai/app` | app objects | which app |
| `rendimiento.ai/addon` | add-on objects | which add-on |
| `rendimiento.ai/lan` | `<service>-lan` Services | a LAN LoadBalancer made by `lan:` |
| `rendimiento.ai/paused` | Addon | pause reconciling |
| `metallb.io/loadBalancerIPs` | LAN Services | the fixed IP requested from MetalLB |
