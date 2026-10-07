# Visits (Umami)

If your apps are counted by [Umami](https://umami.is) (open-source, cookie-free web analytics), rendimiento shows their visitors where you already look: a **Visits** tab on every app, and a **Visitors** panel on the dashboard. rendimiento only reads Umami; it never changes it.

## What you see {#what-you-see}

On an app's **Visits** tab, for the last 24 hours, 7 days or 30 days:

- **Tiles**: visitors, page views, visits, the share of visits that left after one page (the bounce rate), the average visit, and the visitors right now (last 5 minutes). Each tile says how it changed against the period before, in words.
- **A chart** of visitors and page views per hour (24 hours) or per day, with a tooltip.
- **Top lists**: the most viewed pages, where visitors come from (referrers) and their countries.
- **Open in Umami →**, for everything else (when `UMAMI_PUBLIC_URL` is set).

On the dashboard, **Visitors, last 7 days** adds up every app Umami counts, with one row per app (most visited first) and its change against the 7 days before.

The numbers are kept for two minutes, so the pages stay fast and Umami is not asked the same thing again and again. Days and hours are counted in `PUBLIC_STATS_TZ` (default UTC).

## How apps are matched {#how-apps-are-matched}

Nothing to configure per app: an Umami website belongs to an app when its **domain** is one of the app's hostnames (`domain` and `aliases` in [`rendimiento.yaml`](spec.md)). Case, `https://`, a port, a path and a leading `www.` are ignored. An app with several services on different domains shows one section per website.

If no website matches, the tab says which hostnames are missing and shows the tracking script to add.

## Setting it up {#setting-it-up}

rendimiento signs in to Umami with its own **view-only** user. Even if that password leaked, it could not change or delete anything in Umami. In Umami, as an administrator:

1. **Create the user.** *Settings → Users → Create user*: user name `rendimiento`, a long random password, role **View only**.
2. **Put the websites in a team.** A view-only user sees only the websites of the teams it belongs to. *Teams → Create team* (for example `rendimiento`). Then, on each website you want in rendimiento, *Settings → Transfer* it to that team. You stay the team's owner and keep full control.
3. **Add the user to the team.** On the team, *Add member*: `rendimiento`, role **Team view only**.

Then give rendimiento the address and the credentials:

```yaml
# the rendimiento ConfigMap
UMAMI_URL: http://umami.umami.svc.cluster.local:3000   # Umami inside the cluster
UMAMI_PUBLIC_URL: https://umami.example.com            # optional: for "Open in Umami" links
```

```bash
# the password is typed, never saved in the shell history
read -rsp 'Umami password: ' P && kubectl -n rendimiento-system create secret generic rendimiento-umami \
  --from-literal=UMAMI_USERNAME=rendimiento --from-literal=UMAMI_PASSWORD="$P"; unset P
kubectl -n rendimiento-system rollout restart deployment/rendimiento
```

The **Environment** page shows **Visitor numbers (Umami)** as working, with how many websites rendimiento can read. If it says Umami refused the password, check the secret. If it can read fewer websites than you expect, check that they are in the team.

!!! tip "Umami is reached inside the cluster"
    `UMAMI_URL` should be Umami's Service, not its public address: the numbers then never leave the cluster, and Cloudflare or the public ingress are not involved.

## Security {#security}

- The Umami user is **view-only**: rendimiento can read numbers, nothing more.
- The password lives only in the `rendimiento-umami` Secret. It is never shown in the UI or returned by the API, and the session token Umami returns stays in memory.
- The Visits routes need a signed-in session, like the rest of the API.
