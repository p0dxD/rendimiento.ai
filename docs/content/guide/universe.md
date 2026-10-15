# The Universe: every app from above

The **Universe** page shows your whole world of apps on one map: every app, the services it runs, the databases and caches it uses, what calls what, and what the internet reaches. Where the Services page lists each service, the Universe shows how they fit together.

No setup is needed: the map is made from what rendimiento already knows.

## What is on the map

The map is a model village, seen from a hill at an angle:

- **Barrios** are the raised plates: groups of apps (see [Barrios](#barrios) below). From afar you see only the barrios and the big roads between them, so a cluster with many apps stays readable.
- **Pueblos** are the apps inside a barrio. Each one is a cluster of whitewashed houses with a band (*guardapolvo*) in its barrio's color: more houses for more services, a **well** when it runs a database, a **clay jar** when it runs a cache. With **Deployed another way** on, apps from ArgoCD, Helm or `kubectl`, add-ons and calls outside the cluster show up too, in the *Outskirts*. **Cluster internals** (ingress, cert-manager, monitoring…) are hidden unless an app calls them or you turn them on.
- **The ring** on the ground around a pueblo or a house is its health: green when its latest [uptime checks](reliability.md) passed and its calls are fine, **red** when a check failed or 5% or more of its calls fail, **amber** when it is slow, gray when nothing was checked or measured.
- **Roads** are connections: from the **Internet**'s arch to every service with a public address, and between apps. A chevron on each road points from the caller to the callee.

Inside a pueblo, each **house** is a service, with its language on a badge (`JS`, `Py`, `Jv`, `Go`); a tall house is a job or worker that only calls others. Calls from other apps and from the internet come in through the **arch**, and calls to other apps leave by **signposts** named after them.

## Where the roads come from

| Road | Source | Example |
|---|---|---|
| declared | a `needs:` entry in the caller's `rendimiento.yaml` | `needs: [postgres]`, `needs: [{service: api}]` |
| found in the environment | an environment variable of the caller that points at the service (the same detection as the [services catalog](needs.md)) | `API_URL=http://api.wellness.svc.cluster.local` |
| public address | an Ingress routed to the service, from the Internet's arch | `https://wellness.example.com` |
| seen in traces | a call measured by the [collector](#latency-on-every-line) that nobody declared | a call to `api.openweathermap.org` |

A road nobody has measured is a **dashed footpath**; a measured one is paved, with traffic on it (see below). The panel says how each road is known. Values read from secrets are not seen, so a connection whose address lives only in a secret shows up only when it is declared with `needs:` or measured. Declaring needs makes the map complete.

## Barrios

Every app lands in one barrio:

1. **Its own**, when its `rendimiento.yaml` says so: `barrio: Pagos` (letters, digits, spaces and dashes, up to 40; accents are fine). Use it for a team or a domain.
2. Otherwise, **the barrio of the apps it talks to most**: apps that call each other end up together, named after the biggest of them (*stockpulse's barrio*). Measured traffic counts more than a declared connection, so the busiest neighbours win.
3. Apps that call no other app go to **the centre**.
4. What rendimiento does not deploy (add-ons, what was deployed another way, calls outside the cluster) is in **the outskirts**.

## Using it

- **Three levels.** The **region** shows the barrios; tap one to come closer. In a **barrio**, tap a pueblo to see what comes in and what goes out in the panel; tap it twice, or **Go into the pueblo**, to go inside. Inside the **pueblo**, tap a house, the well or a road. **Esc** goes up a level, and the path above the map (*Region › Town centre › wellness*) jumps back to any of them. The place is kept in the address (`?barrio=centre&pueblo=wellness`), so a view can be bookmarked or shared.
- **Tap a road** to see what goes along it: calls per minute, failures, p50 and p95 with a chart over the period, how it is known, and buttons to go to either end. A road between barrios lists the calls it is made of. Roads take a tap a finger's width away.
- **Zoom** with the mouse wheel or a pinch, and **drag** to move around; the **+**, **−** and **⤢** buttons zoom in, out, and back to the whole level.
- **Go to an app** jumps straight to an app by name.
- **The panel** reads like a market board: every barrio, pueblo and service with its calls per minute, a small trend over the period and the change (▲▼) from its start to its end; **Needs attention** lists what is failing or slow. **Open app** goes to the app's page; **Open in Services** goes to the service's entry in the catalog.
- **Refresh** rebuilds the catalog from the cluster right away (otherwise it is at most 30 seconds old, and the page reloads every minute).

## Latency on every line

With tracing on, each road also carries what was measured over the **Period** chosen at the top (15 minutes, an hour, 24 hours): **calls per minute**, the share that **failed**, and how long they took (**p50**, half were faster; **p95**, the slowest 5%), each with its trend across the period. Roads get **wider** with more calls, turn **red** when 5% or more fail and **amber** when the slowest 5% take half a second or more. Fireflies (by night) or confetti (by day) run along measured roads from the caller to the callee: more of them for more calls, faster for quicker calls. Turn movement off in the top bar and they hold still.

Calls **seen only in traces** (nobody declared them) get roads of their own: a call to a service no `needs:` mentions, a database, or an API **outside the cluster**, grouped under *Outside the cluster* in the outskirts.

### Turning it on

1. Install the **OpenTelemetry Collector** add-on (Add-ons). It receives traces and writes only the numbers, the service graph, to the metrics store; traces themselves are not kept. Set its remote-write URL to your VictoriaMetrics or Prometheus. Health checks (Kubernetes' probes, `kube-probe/…`, and rendimiento's uptime checks, `rendimiento-uptime/…`) are dropped first, so they do not draw a line from Internet to every service.
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
