# Open source

The repository [rendimiento-ai/rendimiento](https://github.com/rendimiento-ai/rendimiento) is public, under the **Apache-2.0** license. Anyone can install it from a release: on one computer with the installer, or on a Kubernetes cluster with the Helm chart. What is left to make it a project that other people install and contribute to is mostly *packaging and process*, not new features. This page says what is there and what is left.

## Make it installable anywhere

Every setting that assumed this cluster (`registry.example.lan:5000`, the operator's domain, `letsencrypt-prod`, the `devops-tools` namespace) is an environment variable (see [Settings](../reference/config.md)). There are three ways to install it:

- **On one computer, without Kubernetes:** `curl -fsSL https://rendimiento.ai/instalar | sh` downloads the program from the latest release and runs it as a user service with containerd (or Docker). See [On one computer](../environment/deploy.md#on-one-computer).
- **On a cluster, with Helm:** `chart/rendimiento` installs the platform from the public image, with values for those settings, its Postgres (bundled or external) and the BuildKit pool. See [Installing with Helm](../environment/deploy.md#installing-with-helm).
- **From this repository's `deploy/`**, with a private overlay, as this cluster does.

Still to do:

- **A first-run experience that works everywhere.** The Environment page already checks for ingress, cert-manager, storage, a registry and BuildKit, and says how to fix what's missing. Add **one-click installs** of missing pieces through the add-on catalog (a registry, BuildKit, cert-manager).
- **DNS providers besides Cloudflare** (Route 53, and a "manual" provider that just shows the record to create). See the [recipe](../develop/recipes.md#add-a-dns-provider).
- **A tested quickstart**, the first thing a newcomer tries: from nothing to a deployed app in under ten minutes, on a clean amd64 machine and on k3d or kind.

## Releases and multi-arch images

Every change is tested by rendimiento itself before it reaches `main` ([on every push](../develop/testing.md#on-every-push)). GitHub Actions only build and publish, with no secret of their own:

- **Images** (`.github/workflows/imagen.yml`): `ghcr.io/rendimiento-ai/rendimiento` for **amd64 and arm64**, as `:main` and `:sha-<commit>` for each change to `main`, and `:1.2.3`, `:1.2` and `:latest` for a `v1.2.3` tag. The Dockerfile cross-compiles, so there is no emulation.
- **Programs and the chart** (`.github/workflows/binarios.yml`), on each `v*` tag: a GitHub release with `rendimiento-linux-amd64`, `rendimiento-linux-arm64` and their `SHA256SUMS` (what the installer downloads), and the Helm chart (from `v0.1.3` on) with that version as both its `version` and its `appVersion`, attached to the release and pushed to `oci://ghcr.io/rendimiento-ai/charts/rendimiento`. In the repository, `chart/rendimiento` keeps `appVersion: main`, the image of the code it sits beside.
- The first releases, `v0.1.0` to `v0.1.2`, came out on 2026-11-05.

Still to do:

- **Semantic versioning.** Bump the CRD version (`v1alpha1` → `v1beta1`) only with a conversion plan.
- **Signed images** (cosign keyless) and an SBOM (`buildx --sbom`), both cheap to add in Actions.
- The Railpack image the same way (`make railpack-image` → an Actions job).

## Contribution flow

| File | State |
|---|---|
| `LICENSE` | **Apache-2.0**: the Kubernetes-ecosystem norm, a patent grant, friendly to businesses |
| `CONTRIBUTING.md` | there: setup ([Setting up](../develop/setup.md)), tests, the "book in the same change" rule, commit style |
| `CODE_OF_CONDUCT.md` | there: the Contributor Covenant 2.1, English or Spanish welcome |
| `SECURITY.md` | there: report vulnerabilities privately (GitHub's private vulnerability reporting) |
| `.github/ISSUE_TEMPLATE/` | there: something doesn't work (version, where it runs, logs), an idea |
| `.github/pull_request_template.md` | there: what, why, how tested, book updated? |
| `CHANGELOG.md` | there: what changed in each release, for people who run it |
| Checks on pull requests | rendimiento runs its steps (Go tests with envtest and Postgres, the browser tests, the book) on every branch of the repository, and reports them on the pull request. A pull request **from a fork** is never run by rendimiento (its code would run on the cluster with the installation's token): `.github/workflows/forks.yml` runs the same steps on GitHub's machines, with no secret, and GitHub asks a maintainer to approve a first-time contributor's first run. |

Label a handful of issues **good first issue**. The refactors in [Refactoring](../develop/refactoring.md) (splitting `server.go`, tygo) are ideal: well-scoped, mechanical, and well tested.

## Documentation

This book is the documentation, published in English at [learn.rendimiento.ai](https://learn.rendimiento.ai) and in Spanish at [aprende.rendimiento.ai](https://aprende.rendimiento.ai), by rendimiento itself (the `docs` service). Still to do for a public audience:

- Split it into *user* (Using it, Reference) and *contributor* (Architecture, Developing it) paths. The navigation already does this.
- Move the pages about this cluster (Your environment) behind an "example deployment" heading.

## Governance and sustainability

- Start as a **BDFL**: the founders decide. Write the decision process down in `GOVERNANCE.md` once there are regular contributors.
- **Say what's in scope** in the README: let coding agents deploy to infrastructure you own, safely, with roles, an audit trail and people approving production. That means opinionated deploys, not Jenkins parity.
- A public **roadmap** (GitHub Projects) mirrored from [Roadmap](roadmap.md).
- **Security posture** matters for a tool with cluster-admin-like powers: least-privilege RBAC and impersonation for add-ons (already there), an audit log, and a documented threat model ([Security](../architecture/security.md)).
- **Stay Apache-2.0.** Moving to a source-available license later (BSL, SSPL) splits the community, as it did for Terraform (OpenTofu) and Redis (Valkey). Revenue comes from a hosted control plane, a paid governance tier and metered AI, not from closing the code.

## Checklist

- [x] LICENSE (Apache-2.0) and CONTRIBUTING
- [x] CODE_OF_CONDUCT and SECURITY, issue and pull request templates
- [x] Checks on the repository's own pull requests (rendimiento tests every branch)
- [x] Checks on pull requests from forks (GitHub Actions)
- [x] Release workflow: multi-arch images on ghcr.io, Linux programs with checksums
- [x] Changelog
- [ ] Signed images, SBOM
- [x] Helm chart, published with each release
- [x] Install on one computer without Kubernetes (`/instalar`)
- [ ] Quickstart on a clean machine and on k3d, tested
- [ ] A DNS provider besides Cloudflare
- [x] The book, public in English and Spanish
- [ ] Good first issues
