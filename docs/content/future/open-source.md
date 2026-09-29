# Open source

The repository [p0dxD/rendimiento.ai](https://github.com/p0dxD/rendimiento.ai) is already public. Turning it into a project that other people install and contribute to is mostly *packaging and process*, not new features. This page is the checklist.

## Make it installable anywhere

Today several settings assume this cluster (`registry.cube.local:5000`, `joserod.space`, `letsencrypt-prod`, the `devops-tools` namespace). Every one of them is already an environment variable (see [Settings](../reference/config.md)). What's missing:

- **A Helm chart** (`charts/rendimiento`) that renders `deploy/` with values for those settings, Postgres (bundled or external) and the BuildKit pool. It lets people run `helm install rendimiento oci://ghcr.io/…`.
- **A first-run experience that works everywhere.** The Environment page already checks for ingress, cert-manager, storage, a registry and BuildKit, and says how to fix what's missing. Add **one-click installs** of missing pieces through the add-on catalog (a registry, BuildKit, cert-manager).
- **DNS providers besides Cloudflare** (Route 53, and a "manual" provider that just shows the record to create). See the [recipe](../develop/recipes.md#add-a-dns-provider).
- **A tested quickstart** on k3d or kind on a laptop, the first thing a newcomer tries.

## Releases and multi-arch images

Images are built today for arm64 only, by the cluster's own BuildKit. For others:

- **GitHub Actions** on tags `v*`: run the tests, then `docker buildx build --platform linux/amd64,linux/arm64` and push to **ghcr.io/p0dxD/rendimiento:vX.Y.Z**. The Go stage cross-compiles natively: add `--platform=$BUILDPLATFORM` to the builder stage and pass `GOARCH=$TARGETARCH`, so no emulation is needed. Only the UI stage is platform-independent.
- **Semantic versioning.** Bump the CRD version (`v1alpha1` → `v1beta1`) only with a conversion plan.
- **A changelog** generated from Conventional Commits, or written by hand in `CHANGELOG.md`.
- **Signed images** (cosign keyless) and an SBOM (`buildx --sbom`), both cheap to add in Actions.
- The Railpack image the same way (`make railpack-image` → an Actions job).

## Contribution flow

| File | Contents |
|---|---|
| `LICENSE` | **Apache-2.0** is recommended: Kubernetes-ecosystem norm, patent grant, business-friendly. |
| `CONTRIBUTING.md` | setup (link to [Setting up](../develop/setup.md)), tests, the "book in the same PR" rule, commit style |
| `CODE_OF_CONDUCT.md` | Contributor Covenant |
| `SECURITY.md` | how to report vulnerabilities privately (GitHub private advisories) |
| `.github/ISSUE_TEMPLATE/` | bug report (version, cluster, logs), feature request |
| `.github/pull_request_template.md` | what, why, how tested, book updated? |
| `.github/workflows/ci.yml` | `go vet`, `go test` with envtest and a Postgres service, UI typecheck, `mkdocs build --strict` on every PR |

Label a handful of issues **good first issue**. The refactors in [Refactoring](../develop/refactoring.md) (splitting `server.go`, tygo) are ideal: well-scoped, mechanical, and well tested.

## Documentation

This book is the documentation. For a public audience:

- Publish it with **GitHub Pages** (`mkdocs gh-deploy` in Actions) in addition to the LAN copy.
- Split it into *user* (Using it, Reference) and *contributor* (Architecture, Developing it) paths. The navigation already does this.
- Move the pages about this cluster (Your environment) behind an "example deployment" heading.

## Governance and sustainability

- Start as a **BDFL** (you decide). Write down the decision process in `GOVERNANCE.md` once there are regular contributors.
- **Say what's in scope** in the README: "Heroku-like deploys for your own Kubernetes; opinionated 80%, not Jenkins parity".
- A public **roadmap** (GitHub Projects) mirrored from [Roadmap](roadmap.md).
- **Security posture** matters for a tool with cluster-admin-like powers. Least-privilege RBAC and impersonation for add-ons (already there), an audit log, and a documented threat model ([Security](../architecture/security.md)).

## Checklist

- [ ] LICENSE, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY
- [ ] CI workflow on pull requests
- [ ] Release workflow: multi-arch images on ghcr.io, changelog
- [ ] Helm chart
- [ ] Quickstart on k3d, tested in CI
- [ ] A DNS provider besides Cloudflare
- [ ] Book on GitHub Pages
- [ ] Good first issues
