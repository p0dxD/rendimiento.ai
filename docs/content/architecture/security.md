# Security model

A deployment platform can run anyone's code and change anything in the cluster, so its security model deserves a chapter. This one says what is protected, how, and what is not yet.

## Identities

| Identity | Can | Used by |
|---|---|---|
| **You** (a GitHub login in `ALLOWED_USERS`) | everything in the UI and API | the dashboard |
| **The GitHub App** (installation tokens, 1 hour) | read code, push branches, open PRs, write checks, manage issues (Renovate), in installed repos only | the platform, build pods (clone), Renovate |
| **`rendimiento`** service account | manage App/Addon objects; namespaces, Deployments, Services, Ingresses, CronJobs, PVCs, Secrets cluster-wide; build pods in `rendimiento-builds`; read nodes, metrics, CRDs; **impersonate `rendimiento-addons` only** | the platform process |
| **`rendimiento-addons`** service account | `cluster-admin`; no pod runs as it | the add-on controller, by impersonation |
| **Build pods** | nothing in Kubernetes (no token mounted) | CI steps, Renovate |

## Isolation of untrusted code

Code from repositories runs in CI: tests and Dockerfile `RUN` lines. It is contained by layers:

1. **No credentials**: build pods get no Kubernetes service-account token; the only secret is a clone token for that one run.
2. **Network**: the `isolate-builds` NetworkPolicy allows DNS, the BuildKit daemons and the public internet only. Other apps, databases, the Kubernetes API, the node network and the home LAN (`192.168.0.0/16`) are unreachable. This was verified from test pods on every node.
3. **Nodes**: builds never run on the control plane or on nodes listed in `BUILD_EXCLUDE_NODES` (for example, nodes that cannot enforce NetworkPolicy).
4. **Limits**: a deadline per step, memory limits per test pod.
5. **Forks**: pull requests from forks are never built.

BuildKit daemons themselves run **privileged** (they create containers); build pods talk to them over TCP. A malicious `RUN` executes inside BuildKit's sandbox, not in the daemon's own container, but a BuildKit escape would reach that node. Rootless BuildKit is on the [roadmap](../future/roadmap.md).

## Secrets

- **Never in git in plain text.** Apps reference secrets by name in `rendimiento.yaml`; values are set in the UI (**Settings → Secrets**) or committed as **sealed secrets** (encrypted to the cluster's key).
- **Never in the platform database.** Postgres holds session hashes, not tokens.
- **Generated when possible.** `needs:` credentials are generated once, stored in a Secret, and injected by reference (`secretKeyRef`), so the password never appears in a spec or a log.
- **Short-lived** where possible: GitHub installation tokens last an hour and are minted per build and per Renovate run.
- **Not logged.** Renovate's log parser, build logs and error messages never include tokens.

## The web surface

- GitHub OAuth login; sessions are random tokens stored hashed, valid 7 days, checked against the allowlist on every request.
- Cookies are `HttpOnly`, `Secure` (on HTTPS) and `SameSite=Lax`; state-changing requests from another origin are refused.
- Webhooks are verified by HMAC signature with a constant-time comparison.
- The GitHub App setup page needs a one-time setup token.
- Security headers: no MIME sniffing, no framing, same-origin referrers.

## Validation at the edges

`rendimiento.yaml` is validated before anything runs: names are DNS labels, paths cannot escape the repository, ingress annotations are limited to `nginx.ingress.kubernetes.io/*` and **snippets are refused** (they allow arbitrary nginx configuration), LAN addresses must be private, secret references are checked. The controller refuses namespaces and hostnames it does not own.

## Known gaps

These are the honest weak points, in rough order of importance for a multi-user or public deployment:

1. **Single tenant.** Every allowed user can do everything to every app. Per-app permissions (owners, viewers) and per-team namespaces are needed before strangers share a rendimiento. See [Becoming an open-source project](../future/open-source.md).
2. **Cluster-wide permissions** for the platform (to create namespaces for any app). A more restricted mode would give each app a namespace that an administrator pre-creates.
3. **Privileged BuildKit** daemons.
4. **Plain-HTTP registry** on the LAN.
5. **Backups stay in the cluster.** Volumes are backed up nightly to Garage, which runs on the same machines, so the backups would not survive losing the whole cluster. An off-site copy is on the roadmap.
6. **No audit trail in the app** of who changed what (the Kubernetes audit log and git history cover part of it).
