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

## Latency on every line

With tracing on, each line also carries what was measured: **calls per minute**, the share that **failed**, and how long they took as the caller saw them (**p50**, half were faster; **p95**, the slowest 5%). Pick the window (15 minutes, an hour, 24 hours) at the top. Lines get **thicker** with more calls, turn **red** when 5% or more fail and **amber** when the slowest 5% take half a second or more. Hovering a service shows the typical time on its lines.

Lines **seen only in traces** (nobody declared them) are drawn in green: a call to a service no `needs:` mentions, a database, or an API **outside the cluster**, grouped under *Outside the cluster*.

### Turning it on

1. Install the **OpenTelemetry Collector** add-on (Add-ons). It receives traces and writes only the numbers, the service graph, to the metrics store; traces themselves are not kept. Set its remote-write URL to your VictoriaMetrics or Prometheus.
2. On the platform, set `TELEMETRY_ENDPOINT` to the collector and `METRICS_URL` to the metrics store ([Settings](../reference/config.md#traces-and-the-universe)).
3. In an app's `rendimiento.yaml`, add `observe: true` (or `observe: true` on one service). The next deploy adds tracing.

What each service gets:

| `language` | What rendimiento adds |
|---|---|
| `java` | the OpenTelemetry Java agent, loaded with `JAVA_TOOL_OPTIONS` |
| `node` | the OpenTelemetry Node agent, loaded with `NODE_OPTIONS` (kept after the app's own options) |
| `python` | the OpenTelemetry Python agent, on `PYTHONPATH` |
| `go`, others, ready-made images | only the `OTEL_*` variables, for an OpenTelemetry SDK in the code |

Every observed service is named `app/service` (`OTEL_SERVICE_NAME`), which is how a measured line finds its nodes. The agent is copied into the pod by a small init container; it adds memory (the Java agent the most, around 100 MB) and some start-up time, so turn it on where you want to see. A quarter of the traces are kept (`TELEMETRY_SAMPLE_RATIO`), which is plenty for averages.

!!! note "Python on Alpine"
    The Python agent is built for glibc. A service on an Alpine (musl) image may fail to load it: use a Debian-based image, or set `observe: false` on that service.

A Go service is still measured from the other side: calls *to* it from an observed service are timed by the caller.

To add the moments only the code knows (a payment, a report) and feature flags to the traces, use the [libraries](observa.md) for Node, Python and Java.
