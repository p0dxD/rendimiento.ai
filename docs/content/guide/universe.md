# The Universe: every app from above

The **Universe** page shows your whole world of apps on one map: every app, the services it runs, the databases and caches it uses, what calls what, and what the internet reaches. Where the Services page lists each service, the Universe shows how they fit together.

No setup is needed: the map is made from what rendimiento already knows.

## What is on the map

- **Systems** are the big circles: one per rendimiento app (solid border). With **Deployed another way** on, apps from ArgoCD, Helm or `kubectl` and add-ons appear too (dashed border), one per namespace. **Cluster internals** (ingress, cert-manager, monitoring…) are hidden unless an app calls them or you turn them on.
- **Nodes** are what runs inside each system:
    - a **circle** is a service; the letters inside are its language (`JS`, `Py`, `J`, `Go`);
    - a **square** is a database or cache the app runs for its `needs` (`PG`, `R`);
    - a **dashed circle** is a job or worker that only calls others (a cron job).
- **The ring** of each service is its latest [uptime check](reliability.md): green when every check passed in the last 15 minutes, red when one failed, gray when it has not been checked.
- **Internet** sits in the middle, with a line to every service that has a public address.

## Where the lines come from

| Line | Source | Example |
|---|---|---|
| solid | a `needs:` entry in the caller's `rendimiento.yaml` | `needs: [postgres]`, `needs: [{service: api}]` |
| dashed | an environment variable of the caller that points at the service (the same detection as the [services catalog](needs.md)) | `API_URL=http://api.wellness.svc.cluster.local` |
| dotted, from Internet | a public address (an Ingress) routed to the service | `https://wellness.example.com` |

A line found both ways is drawn solid. Values read from secrets are not seen, so a connection whose address lives only in a secret shows up only when it is declared with `needs:`. Declaring needs makes the map complete.

## Using it

- **Hover** a node to light up its connections; everything else fades.
- **Click** a node to pin it. The side panel shows what it is, its health and pods, its public addresses, **Calls** (what it uses) and **Called by** (who uses it), each with the variable or address and where the line comes from. Click a name in those lists to jump to that node.
- **Open app** goes to the app's page; **Open in Services** goes to the service's entry in the catalog.
- **Refresh** rebuilds the catalog from the cluster right away (otherwise it is at most 30 seconds old, and the page reloads every minute).

## What comes next

Latency, requests per minute and errors on every line, measured from traces (OpenTelemetry), for apps that turn tracing on. Java, Node and Python services will be instrumented without code changes; libraries for each language will add custom events.
