# HTTP API

All routes are registered in `Server.Handler` (`internal/api/server.go`). The UI uses this API and nothing else, so anything the UI does can be scripted.

- **Auth:** a session cookie set by GitHub login (`/api/auth/login`). Routes marked 🔒 return `401` without one, and `403` for users not in `ALLOWED_USERS`.
- **Format:** JSON in and out. Errors are `{"error": "message"}` with a matching status code.
- **Live updates:** `…/events` routes are **Server-Sent Events** streams (`text/event-stream`). The ingress disables buffering for them.

## Public

| Method | Path | Does |
|---|---|---|
| GET | `/healthz` | `ok`; readiness and liveness probes |
| GET | `/api/public/stats` | aggregate numbers for a public page; only with `PUBLIC_STATS=true`, and only on the internal `STATS_LISTEN` port when that is set ([what it contains](../guide/reliability.md#public-stats)) |
| POST | `/api/webhooks/github` | GitHub App webhooks (push, pull request, installation), verified by HMAC |
| GET | `/api/auth/login` | redirects to GitHub OAuth |
| GET | `/api/auth/callback` | OAuth callback; creates the session |
| POST | `/api/auth/logout` | ends the session |
| GET | `/api/setup/status` | whether the GitHub App is configured |
| GET | `/api/setup/github` | starts the App manifest flow (needs `?token=SETUP_TOKEN`) |
| POST | `/mcp` | the MCP endpoint for agents ([la Ruta](../guide/ruta.md)); needs an agent key as a bearer token, not a session |
| GET | `/api/setup/github/callback` | GitHub returns the new App's credentials; they are saved in a Secret |

## Account and environment 🔒

| Method | Path | Does |
|---|---|---|
| GET | `/api/me` | the signed-in login |
| GET | `/api/installations` | GitHub App installations |
| GET | `/api/installations/{id}/repos` | repositories of an installation |
| GET | `/api/zones` | DNS zones the token can edit |
| GET | `/api/environment` | the Environment page's checks |
| GET | `/api/services` | the Services catalog (apps, add-ons, cluster services, connection info) |
| GET | `/api/stats/delivery` | the dashboard's delivery stats: the last 30 days, the 30 before, and twelve weekly points |
| GET | `/api/problems` | the platform's own warnings and errors, grouped ([Problems](../guide/problems.md)) |
| GET | `/api/problems/count` | how many problems are open (the top bar's badge) |
| POST | `/api/problems/{id}/dismiss` | dismiss a problem |
| GET | `/api/ruta?kind=&q=` | [la Ruta](../guide/ruta.md)'s entries |
| POST | `/api/ruta` | add an entry (written by the signed-in person) |
| PUT | `/api/ruta/{id}` | change an entry (the previous version is kept) |
| POST | `/api/ruta/{id}/archive` | hide an entry from lists |
| GET | `/api/ruta/activity` | agents' cargos and their latest tool calls |
| GET | `/api/agent-keys` | agent keys (never the tokens) |
| POST | `/api/agent-keys` | create an agent key; the token is in this answer only |
| DELETE | `/api/agent-keys/{id}` | revoke an agent key |
| POST | `/api/notifications/test` | send a sample notification email now (the Environment page's button) |
| POST | `/api/dns/sync` | re-create every app's DNS records now |
| POST | `/api/propose` | detect a repo and return a proposed `rendimiento.yaml` + Dockerfile |
| GET | `/api/namespaces/{name}/migration` | what already exists in a namespace (for adopting existing workloads) |

## Apps 🔒

| Method | Path | Does |
|---|---|---|
| GET | `/api/apps` | list apps with status |
| POST | `/api/apps` | onboard a repo (opens the setup pull request, or adopts directly) |
| GET | `/api/apps/{app}` | one app: spec, status, latest run and release |
| DELETE | `/api/apps/{app}` | delete the app and everything it created |
| GET | `/api/apps/{app}/impact` | what a delete would remove (shown before confirming) |
| POST | `/api/apps/{app}/disconnect` | stop managing the app but leave its workloads running |
| GET | `/api/apps/{app}/runs` | CI runs |
| POST | `/api/apps/{app}/runs` | start a run of the default branch (the **Run** button) |
| GET | `/api/apps/{app}/releases` | release history (digest per service) |
| GET | `/api/apps/{app}/reliability?range=24h\|7d\|30d` | uptime checks: per check, uptime, p50/p95, chart buckets and the latest check; outages; releases in the range |
| GET | `/api/apps/{app}/releases/{number}/tasks/{task}/log` | the log of a release's post-deploy task |
| POST | `/api/apps/{app}/rollback` | roll back to an earlier release (`{"release": N}`) |
| GET | `/api/apps/{app}/resources` | the live Kubernetes objects and their health (the resource tree) |
| PUT | `/api/apps/{app}/secrets/{secret}` | set a secret's values (write-only; values are never returned) |
| GET | `/api/apps/{app}/events` | SSE: app status changes |
| GET | `/api/apps/{app}/addons` | per-app add-ons (e.g. Renovate) and their state |
| PUT | `/api/apps/{app}/addons/{addon}` | turn a per-app add-on on or off, or configure it |

## Runs 🔒

| Method | Path | Does |
|---|---|---|
| GET | `/api/runs/{id}` | a run with its steps (the graph) |
| POST | `/api/runs/{id}/cancel` | cancel it; its build pods are deleted |
| GET | `/api/runs/{id}/steps/{step}/log` | a step's log (stored, or live while running) |
| GET | `/api/runs/{id}/events` | SSE: step state and log lines |

## Per-app add-ons (Renovate) 🔒

| Method | Path | Does |
|---|---|---|
| GET | `/api/addons` | per-app add-on kinds |
| GET | `/api/addons/{addon}` | one, with its settings |
| PUT | `/api/addons/{addon}` | change its settings |
| GET | `/api/addons/{addon}/runs` | its runs |
| POST | `/api/addons/{addon}/runs` | run it now |
| GET | `/api/addons/{addon}/runs/{id}` | one run with its log |

## Installed add-ons (cluster add-ons) 🔒

| Method | Path | Does |
|---|---|---|
| GET | `/api/addon-catalog` | the one-click catalog |
| GET | `/api/installed` | installed add-ons (`Addon` objects) with status |
| GET | `/api/installed/{name}` | one, with preview, hooks and inventory |
| PUT | `/api/installed/{name}` | install or change one (writes `addons/<name>.yaml` in the gitops repo) |
| PATCH | `/api/installed/{name}` | toggles: suspend, manual sync, prune, adopt |
| POST | `/api/installed/{name}/sync` | approve and apply pending changes (manual sync) |
| DELETE | `/api/installed/{name}` | uninstall (pre-delete hooks first; `neverDelete` kinds stay) |

## Example

```bash
# with a session cookie copied from the browser
curl -s -b "session=…" https://rendimiento.joserod.space/api/apps | jq '.[].name'
curl -s -b "session=…" -X POST https://rendimiento.joserod.space/api/apps/jobsentry/runs
```
