"use strict";

const { test, beforeEach } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");
const api = require("@opentelemetry/api");
const { AsyncLocalStorageContextManager } = require("@opentelemetry/context-async-hooks");
const { W3CTraceContextPropagator } = require("@opentelemetry/core");
const { BasicTracerProvider, InMemorySpanExporter, SimpleSpanProcessor } = require("@opentelemetry/sdk-trace-base");

const observa = require("..");

const exporter = new InMemorySpanExporter();
api.context.setGlobalContextManager(new AsyncLocalStorageContextManager().enable());
api.propagation.setGlobalPropagator(new W3CTraceContextPropagator());
api.trace.setGlobalTracerProvider(new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] }));

beforeEach(() => {
  exporter.reset();
  observa._reset();
});

const spans = () => exporter.getFinishedSpans();

test("tramo wraps sync work in a span that nests what runs inside", () => {
  const out = observa.tramo("cobrar", () => {
    observa.tramo("validar", () => {});
    return 42;
  }, { "pedido.id": "7" });
  assert.equal(out, 42);
  const [inner, outer] = spans();
  assert.equal(outer.name, "cobrar");
  assert.equal(outer.attributes["pedido.id"], "7");
  assert.equal(inner.name, "validar");
  assert.equal(inner.parentSpanContext.spanId, outer.spanContext().spanId);
});

test("tramo ends async spans when the promise settles and marks failures", async () => {
  assert.equal(await observa.tramo("lento", async () => "listo"), "listo");
  await assert.rejects(observa.tramo("falla", async () => { throw new Error("sin saldo"); }), /sin saldo/);
  assert.throws(() => observa.tramo("falla-sync", () => { throw new Error("no"); }), /no/);
  const [ok, bad, badSync] = spans();
  assert.equal(ok.status.code, api.SpanStatusCode.UNSET);
  assert.equal(bad.status.code, api.SpanStatusCode.ERROR);
  assert.equal(bad.status.message, "sin saldo");
  assert.equal(bad.events[0].name, "exception");
  assert.equal(badSync.status.code, api.SpanStatusCode.ERROR);
});

test("evento goes on the current span, or on a span of its own", () => {
  observa.tramo("pedido", () => observa.evento("pagado", { monto: 120 }));
  observa.evento("suelto");
  const [pedido, suelto] = spans();
  assert.equal(pedido.events[0].name, "pagado");
  assert.equal(pedido.events[0].attributes.monto, 120);
  assert.equal(suelto.name, "suelto");
});

test("bandera reads RENDIMIENTO_FLAG_ and records the side taken", () => {
  process.env.RENDIMIENTO_FLAG_NUEVO_COBRO = "sí";
  observa.tramo("t", () => {
    assert.equal(observa.bandera("nuevo-cobro"), true);
    assert.equal(observa.bandera("otra"), false);
    assert.equal(observa.flag("otra", true), true);
  });
  assert.equal(spans()[0].attributes["rendimiento.flag.nuevo-cobro"], true);
  delete process.env.RENDIMIENTO_FLAG_NUEVO_COBRO;
});

test("llamar carries the trace to the service it calls", async () => {
  let seen;
  const server = http.createServer((req, res) => {
    seen = req.headers.traceparent;
    res.end("ok");
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  try {
    const url = `http://127.0.0.1:${server.address().port}/`;
    let traceId;
    await observa.tramo("llamada", async (span) => {
      traceId = span.spanContext().traceId;
      assert.equal(await (await observa.llamar(url, { headers: { "x-otro": "1" } })).text(), "ok");
    });
    assert.match(seen, new RegExp(`^00-${traceId}-[0-9a-f]{16}-[0-9a-f]{2}$`));
  } finally {
    server.close();
  }
});

test("iniciar does nothing with the platform's agent or without an endpoint", () => {
  const saved = { ...process.env };
  try {
    process.env.NODE_OPTIONS = "--require /otel-auto-instrumentation/autoinstrumentation.js";
    assert.equal(observa.iniciar().como, "agent");
    observa._reset();
    delete process.env.NODE_OPTIONS;
    delete process.env.OTEL_EXPORTER_OTLP_ENDPOINT;
    assert.equal(observa.start().como, "off");
    assert.equal(observa.iniciar().como, "off", "started once");
    observa._reset();
    // The SDK is not a dependency here: it says what to install.
    assert.equal(observa.iniciar({ endpoint: "http://collector:4318" }).como, "missing");
  } finally {
    process.env = saved;
  }
});
