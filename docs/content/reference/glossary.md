# Glossary

**Add-on**
: A piece of cluster software (a Helm chart or git manifests) that rendimiento installs and keeps in sync. It's declared in the gitops repo and represented by an `Addon` object. A *per-app add-on* (Renovate) is different: it's a feature you turn on for one app.

**Adoption**
: Taking over objects that already exist, so rendimiento manages them from now on. Adoption never happens silently. It needs `adopt`, and it stops at **Blocked** if it would change live objects.

**App**
: One onboarded repository and everything deployed from it. Also the `App` custom resource.

**BuildKit**
: The build engine behind `docker build`. rendimiento runs a pool of BuildKit daemons (`buildkitd`) and sends each image to one of them.

**Composition root**
: The single place where every object is created and connected: `cmd/rendimiento/main.go`.

**Controller**
: A loop that makes the cluster match a desired state (see *reconcile*). rendimiento has the App and Addon controllers.

**CRD**
: Custom Resource Definition. It teaches Kubernetes a new kind of object (`App`, `Addon`).

**Digest**
: The content hash of an image (`sha256:…`). Releases pin digests, not tags, so what runs is exactly what was built.

**envtest**
: A real Kubernetes API server and etcd started by tests, with no nodes.

**Golden file**
: A saved expected output (`testdata/*.golden.yaml`) that a test compares against.

**Helm hook**
: A chart object annotated to run at a moment of the lifecycle (pre-install, post-upgrade, pre-delete…). rendimiento runs hooks the way Helm does.

**Impersonation**
: Acting as another identity in Kubernetes. Add-ons are applied as `rendimiento-addons`, not as the platform itself.

**Leader election**
: Ensures only one replica runs the controllers at a time.

**MetalLB**
: Gives `LoadBalancer` Services an IP on the home network (pool 192.168.50.70–99).

**Needs**
: `needs:` in `rendimiento.yaml`. It declares a database, a cache or another service, and rendimiento provides it and wires in its connection details.

**Pool (BuildKit)**
: Several BuildKit daemons behind a headless Service, discovered through DNS SRV records.

**Prune**
: Deleting objects that are no longer desired.

**Railpack**
: Builds an image from source without a Dockerfile, by detecting the language.

**Reconcile**
: One pass of a controller: read desired state, read actual state, act on the difference. It's safe to repeat.

**Release**
: A numbered set of image digests, one per service, produced by a successful run on the default branch. Rollback means switching to an older release.

**Rendezvous hashing**
: Picks the daemon with the highest `hash(image, daemon)`. It is stable, and it moves little work when the pool changes.

**Run**
: One CI execution (clone → test → build) for a commit. Its **steps** form a graph.

**Server-side apply (SSA)**
: Kubernetes merges a desired object on the server and tracks which *field manager* owns each field.

**Server-Sent Events (SSE)**
: A one-way HTTP stream from server to browser. It's how the UI updates live.

**`SKIP LOCKED`**
: A Postgres clause that lets many workers claim different queued runs without waiting on each other.

**Spec**
: `rendimiento.yaml`: an app's services, builds, tests, domains, needs and so on.
