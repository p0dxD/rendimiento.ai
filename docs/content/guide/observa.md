# Libraries: rendimiento for Node, Python and Java

With `observe: true`, the [Universe](universe.md#latency-on-every-line) already measures every HTTP call, database query and message a Java, Node or Python service makes, without code changes. The **observa** libraries add what only the code knows: the moments of the business ("charge", "build the report"), events inside them, and feature flags. They are thin layers over [OpenTelemetry](https://opentelemetry.io), so everything they record joins the same traces.

The libraries live in this repository under `sdk/` and are not published to npm, PyPI or Maven Central yet; install them from git.

## What each one offers

| | Node | Python | Java |
|---|---|---|---|
| Start tracing (no-op with the platform's agent) | `iniciar()` | `iniciar()` | `Observa.iniciar()` |
| A span around work, nested under the current one | `tramo(name, fn, attrs)` | `with tramo(name, attrs):` or `@tramo(name)` | `Observa.tramo(name, attrs, () -> …)` or `try (var t = Observa.abrir(name))` |
| An event on the current span | `evento(name, attrs)` | `evento(name, attrs)` | `Observa.evento(name, attrs)` |
| Carry the trace to another service | `llamar(url, init)` (fetch) | `cabeceras()`, `llamar(url)` | `Observa.cabeceras()` |
| A feature flag, recorded on the trace | `bandera(name, default)` | `bandera(name, default)` | `Observa.bandera(name, default)` |

Every function also has an English name: `start`, `span`, `event`, `call` / `headers`, `flag`.

- **Spans** become the current span, so the calls made inside nest under them on the trace. An error marks the span failed and is raised again.
- **Events** go on the current span; with none going, they become a short span of their own, so they are not lost.
- **Flags** are read from `RENDIMIENTO_FLAG_<NAME>` (`1`, `true`, `on`, `yes`, `sí`) and recorded as `rendimiento.flag.<name>` on the current span, so a trace says which side it took. This is the hook future A/B tests and canary releases will set.
- **Starting**: inside the cluster with `observe`, the platform's agent already traces and `iniciar()` does nothing. Elsewhere (your laptop, a service without the agent) it starts the OpenTelemetry SDK when it is installed and `OTEL_EXPORTER_OTLP_ENDPOINT` is set; without either, every helper still works and records nothing.

## Where you see what they record

The collector turns every span into numbers (its `spanmetrics`), and the [Universe](universe.md#using-it) shows them: tap a service and its panel lists **its own moments**, one row per `tramo` name, with calls per minute, failures, p50/p95 and a trend over the period; a failed `tramo` counts as a failure.

**Events** and **flags** are recorded in the traces too, but rendimiento keeps only the numbers, not the traces themselves, so today they cannot be seen: they show up once traces are kept and a request can be followed step by step (the *request waterfall* on the [roadmap](../future/roadmap.md)). Name your tramos after the moment (`cobrar`, `generar-reporte`), not after a value (`cobrar-pedido-42`): each different name is a row of its own.

## Node

npm cannot install a folder of a git repository, so until it is published, install it from a copy:

```bash
git clone https://github.com/p0dxD/rendimiento.ai
npm install ./rendimiento.ai/sdk/node   # or copy sdk/node into your repo and install that folder
```

```js
const { tramo, evento, bandera } = require("@rendimiento/observa");

app.post("/pedidos/:id/cobrar", (req, res) =>
  tramo("cobrar", async () => {
    const nuevo = bandera("nuevo-cobro");
    const pago = await (nuevo ? cobrarV2 : cobrar)(req.params.id);
    evento("pagado", { monto: pago.monto });
    res.json(pago);
  }, { "pedido.id": req.params.id }));
```

`@opentelemetry/api` is a peer dependency. To trace without the agent, also install `@opentelemetry/sdk-node` and `@opentelemetry/auto-instrumentations-node`.

## Python

```bash
pip install "rendimiento-observa @ git+https://github.com/p0dxD/rendimiento.ai#subdirectory=sdk/python"
# without the agent: add [sdk] for the OpenTelemetry SDK and the OTLP exporter
```

```python
from rendimiento_observa import tramo, evento, bandera

@tramo("generar-reporte")
def generar(usuario):
    if bandera("reporte-rapido"):
        ...
    evento("reporte-listo", {"filas": 1200})
```

`requests` and `httpx` are instrumented by the agent; for another client, pass `cabeceras()` as its headers.

## Java

Build and install it locally (`mvn -f sdk/java install`), then depend on `ai.rendimiento:observa:0.1.0`:

```java
import ai.rendimiento.observa.Observa;

Pago pago = Observa.tramo("cobrar", Map.of("pedido.id", id), () -> {
  Pago p = Observa.bandera("nuevo-cobro") ? cobrarV2(id) : cobrar(id);
  Observa.evento("pagado", Map.of("monto", p.monto()));
  return p;
});
```

Checked exceptions pass through `tramo`. With the agent, spans join its traces through `GlobalOpenTelemetry`.

Until the library is on Maven Central, OpenTelemetry's own annotation does the same for a method, with nothing but the agent: depend on `io.opentelemetry.instrumentation:opentelemetry-instrumentation-annotations` (the agent's version, `2.32.0`) and mark the method:

```java
@WithSpan("guardar-viaje")
public Result saveTrip(...) { ... }
```

## Go

Go has no agent and no observa library. Use the OpenTelemetry SDK: with `observe: true` the platform sets `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME` and the sampler, so the setup reads them all from the environment. *hello-rendimiento* does it in about forty lines (`tracing.go`):

- `otlptracehttp.New(ctx)` and a `TracerProvider` with `resource.WithFromEnv()`;
- `otelhttp.NewHandler` around the mux, naming each request after its route (`r.Pattern`);
- `tracer.Start(ctx, "render-page")` for a moment of its own.

OpenTelemetry for Go v1.38 and later need Go 1.26; v1.37 (with `otelhttp` v0.62) builds with Go 1.24.

## Tests

Each library is tested as a [task](tasks.md) of rendimiento's own `rendimiento.yaml` (`sdk-node`, `sdk-python`, `sdk-java`) on every run that changes its folder, branches included.

## License

The three libraries are under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0), the same as OpenTelemetry, which they are built on: each folder has its `LICENSE`, and `package.json`, `pyproject.toml` and `pom.xml` declare it.
