# HTTP API

All routes are registered in `Server.Handler` (`internal/api/server.go`). The UI uses this API and nothing else, so anything the UI does can be scripted.

- **Auth:** a session cookie set by GitHub login (`/api/auth/login`). Routes marked 🔒 return `401` without one or for people who are not [members](../guide/members.md), and `403` when the person's role is below the route's.
- **Format:** JSON in and out. Errors are `{"error": "message"}` with a matching status code.
- **Live updates:** `…/events` routes are **Server-Sent Events** streams (`text/event-stream`). The ingress disables buffering for them.

## Public

| Method | Path | Does |
|---|---|---|
| GET | `/healthz` | `ok`; readiness and liveness probes |
| GET | `/llms.txt` | for AI agents: what the platform is, how to connect over MCP, roles and rules ([Built for agents](../guide/agents.md)) |
| GET | `/api/public/stats` | aggregate numbers for a public page; only with `PUBLIC_STATS=true`, and only on the internal `STATS_LISTEN` port when that is set ([what it contains](../guide/reliability.md#public-stats)) |
| GET | `/api/public/activity` | the welcome page's *Right now*: runs queued or running and the 8 latest releases, by app name and time only (no repositories, commits, branches or messages); only with `PUBLIC_ACTIVITY=true` |
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
| GET | `/api/platform/version` | which release of rendimiento runs and whether a newer one is ready (`SELF_APP`) |
| POST | `/api/platform/update` | update rendimiento to a release's image: `{release}`; it restarts a few seconds later |
| GET | `/api/services` | the Services catalog (apps, add-ons, cluster services, connection info) |
| GET | `/api/universe` | every app from above: barrios, systems, nodes (with their latest uptime checks) and the edges between them, with calls per minute, errors and latency (with their trends) from traces over `?window=` (`15m`, `1h`, `24h`); `?refresh=1` rebuilds the catalog first |
| GET | `/api/universe/traces` | requests the traces service kept, newest first; `?service=app/service`, `?errors=1` for the failed ones, `?before=` (RFC 3339) to page back |
| GET | `/api/universe/traces/{id}` | one kept request and its spans, for the waterfall |
| GET | `/api/universe/spans` | what one observed service does, from its traces: routes served, its own moments, calls made, with calls per minute, errors, latency and trends; `?service=app/service`, `?window=` |
| GET | `/api/stats/visits` | the dashboard's visitors panel: the last 7 days of every app Umami counts, and the 7 before ([Visits](../guide/visits.md)) |
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
| POST | `/api/agent-keys` | create an agent key (`name`, `role`: `reader` or `collaborator`, `apps`: empty for all, `days`); the token is in this answer only |
| DELETE | `/api/agent-keys/{id}` | revoke an agent key |
| GET | `/api/orgs` | the organizations the person belongs to, with their role |
| POST | `/api/orgs` | create an organization (`slug`, `name`); the platform's operators only; the creator is its first administrator |
| POST | `/api/orgs/{id}/switch` | work in another of the person's organizations |
| GET | `/api/ia` | the organization's AI: provider, model, cap, the end of its own key, this month's spending, the latest calls |
| PUT | `/api/ia` | change it (`provider`: `local`, `own`, `platform`; `kind`, `baseUrl`, `model`, `capDollars`, `key`, `removeKey`); administrators |
| POST | `/api/ia/test` | ask the organization's model one question (`prompt`), measured; collaborators |
| GET | `/api/ia/orgs` | every organization's allowance of the platform's AI and spending, and the platform's bill; operators |
| PUT | `/api/ia/orgs/{id}/allowance` | an organization's monthly allowance of the platform's AI (`dollars`); operators |
| GET | `/api/members` | the organization's people and their roles |
| PUT | `/api/members/{login}` | add a person by GitHub login, or change their role (`role`: `guest`, `collaborator`, `admin`); administrators only |
| DELETE | `/api/members/{login}` | remove a person; the last administrator stays |
| GET | `/api/encargos` | open encargos (`?all=1`: also reviewed and dropped) |
| POST | `/api/encargos` | ask agents for something (`title`, `body`, `app`, `runId`) |
| GET | `/api/encargos/{id}` | one encargo with its history |
| POST | `/api/encargos/{id}/review` | accept a delivered encargo (`accept: true`) or send it back with a `comment` |
| POST | `/api/encargos/{id}/cancel` | drop an encargo |
| POST | `/api/notifications/test` | send a sample notification email now (the Environment page's button) |
| POST | `/api/dns/sync` | re-create every app's DNS records now |
| POST | `/api/propose` | detect a repo and return a proposed `rendimiento.yaml` + Dockerfile |
| GET | `/api/namespaces/{name}/migration` | what already exists in a namespace (for adopting existing workloads) |

## Apps 🔒

| Method | Path | Does |
|---|---|---|
| GET | `/api/apps` | list apps with status |
| POST | `/api/apps` | onboard a repo (opens the setup pull request, or adopts directly) |
| GET | `/api/mercado` | the [Mercado](../guide/mercado.md): whether it is on, and every page with its app's state |
| POST | `/api/mercado` | open a page: `{pagina, dominio}`; creates its repository and app (photos as `data:` URLs, up to 24 MB in all) |
| POST | `/api/mercado/preview` | the page's HTML for the form's preview, as `{html}` |
| GET | `/api/mercado/{app}` | a page's form answers (`pagina.json`), address and repository |
| PUT | `/api/mercado/{app}` | save a page: one commit, which publishes it |
| GET | `/api/apps/{app}` | one app: spec, status, latest run and release |
| DELETE | `/api/apps/{app}` | delete the app and everything it created |
| GET | `/api/apps/{app}/impact` | what a delete would remove (shown before confirming) |
| POST | `/api/apps/{app}/disconnect` | stop managing the app but leave its workloads running |
| GET | `/api/apps/{app}/runs` | CI runs |
| GET | `/api/apps/{app}/caminos` | the last 14 days of runs by branch, with where each branch started and was merged ([Roads](../guide/caminos.md)) |
| POST | `/api/apps/{app}/runs` | start a run of the default branch (the **Run** button) |
| GET | `/api/apps/{app}/releases` | release history (digest per service) |
| GET | `/api/apps/{app}/reliability?range=24h\|7d\|30d` | uptime checks: per check, uptime, p50/p95, chart buckets and the latest check; outages; releases in the range |
| GET | `/api/apps/{app}/releases/{number}/tasks/{task}/log` | the log of a release's post-deploy task |
| GET | `/api/apps/{app}/visits?range=24h\|7d\|30d` | visitors from Umami: per matching website, totals and the period before, a chart series, top pages, referrers and countries |
| POST | `/api/apps/{app}/rollback` | roll back to an earlier release (`{"release": N}`) |
| GET | `/api/apps/{app}/resources` | the live Kubernetes objects and their health (the resource tree) |
| GET | `/api/apps/{app}/secrets/{secret}` | the keys a secret holds (`{"keys": [...]}`), never their values |
| PUT | `/api/apps/{app}/secrets/{secret}` | add or replace a secret's keys (`{"KEY": "value"}`), keeping the others; `null` removes a key. Write-only: values are never returned |
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
