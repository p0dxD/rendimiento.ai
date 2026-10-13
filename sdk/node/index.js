// rendimiento for Node: a thin layer over OpenTelemetry so a service shows
// up on the Universe map with its own moments, not just its HTTP calls.
//
// With `observe: true` in rendimiento.yaml the platform already loads the
// OpenTelemetry agent and sets the OTEL_* variables; these helpers then add
// spans and events to the traces the agent makes. Outside the cluster (or
// without the agent) iniciar() starts the OpenTelemetry SDK itself when it
// is installed and OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise every
// helper still works and simply records nothing.
"use strict";

const api = require("@opentelemetry/api");

const NAME = "@rendimiento/observa";
const VERSION = "0.1.0";

/** The tracer the helpers use. */
function tracer() {
  return api.trace.getTracer(NAME, VERSION);
}

/** True when the platform's agent is loaded (NODE_OPTIONS --require …autoinstrumentation). */
function agentLoaded() {
  return /autoinstrumentation/.test(process.env.NODE_OPTIONS || "");
}

let started;

/**
 * Starts tracing once. With the platform's agent loaded it does nothing
 * (the agent already traces). Otherwise, when OTEL_EXPORTER_OTLP_ENDPOINT
 * (or opciones.endpoint) is set and @opentelemetry/sdk-node and
 * @opentelemetry/auto-instrumentations-node are installed, it starts the
 * SDK with every automatic instrumentation.
 *
 * @returns {{ como: "agent" | "sdk" | "off" | "missing", sdk?: unknown }}
 */
function iniciar(opciones = {}) {
  if (started) return started;
  if (agentLoaded()) return (started = { como: "agent" });
  if (opciones.endpoint) process.env.OTEL_EXPORTER_OTLP_ENDPOINT = opciones.endpoint;
  if (opciones.servicio) process.env.OTEL_SERVICE_NAME = opciones.servicio;
  if (!process.env.OTEL_EXPORTER_OTLP_ENDPOINT) return (started = { como: "off" });
  let NodeSDK, getNodeAutoInstrumentations;
  try {
    ({ NodeSDK } = require("@opentelemetry/sdk-node"));
    ({ getNodeAutoInstrumentations } = require("@opentelemetry/auto-instrumentations-node"));
  } catch {
    console.warn(`${NAME}: to trace without rendimiento's agent, install @opentelemetry/sdk-node and @opentelemetry/auto-instrumentations-node`);
    return (started = { como: "missing" });
  }
  const sdk = new NodeSDK({ instrumentations: [getNodeAutoInstrumentations()] });
  sdk.start();
  return (started = { como: "sdk", sdk });
}

/** Marks a span failed with the error. */
function fail(span, err) {
  span.recordException(err instanceof Error ? err : String(err));
  span.setStatus({ code: api.SpanStatusCode.ERROR, message: String((err && err.message) || err) });
}

/**
 * Runs fn inside a span named nombre (a moment of the business: "cobrar",
 * "generar-reporte"), which becomes the current span, so calls made inside
 * nest under it. Works with sync and async functions; an error marks the
 * span failed and is thrown again.
 *
 * @template T
 * @param {string} nombre
 * @param {(span: import("@opentelemetry/api").Span) => T} fn
 * @param {import("@opentelemetry/api").Attributes} [atributos]
 * @returns {T}
 */
function tramo(nombre, fn, atributos) {
  return tracer().startActiveSpan(nombre, { attributes: atributos }, (span) => {
    let result;
    try {
      result = fn(span);
    } catch (err) {
      fail(span, err);
      span.end();
      throw err;
    }
    if (result && typeof result.then === "function") {
      return result.then(
        (v) => {
          span.end();
          return v;
        },
        (err) => {
          fail(span, err);
          span.end();
          throw err;
        },
      );
    }
    span.end();
    return result;
  });
}

/**
 * Records that something happened (an order paid, a cache miss) on the
 * current span; with no span going, as a short span of its own so it is
 * not lost.
 *
 * @param {string} nombre
 * @param {import("@opentelemetry/api").Attributes} [atributos]
 */
function evento(nombre, atributos) {
  const span = api.trace.getActiveSpan();
  if (span) span.addEvent(nombre, atributos);
  else tracer().startSpan(nombre, { attributes: atributos }).end();
}

/**
 * fetch() that carries the current trace to the service it calls (the
 * traceparent header), so the call joins the caller's trace even where
 * fetch is not instrumented.
 *
 * @param {string | URL} url
 * @param {RequestInit} [init]
 * @returns {Promise<Response>}
 */
function llamar(url, init = {}) {
  const headers = new Headers(init.headers);
  api.propagation.inject(api.context.active(), headers, { set: (h, k, v) => h.set(k, v) });
  return fetch(url, { ...init, headers });
}

/**
 * A feature flag read from RENDIMIENTO_FLAG_<NAME> ("1", "true", "on",
 * "yes" turn it on), recorded on the current span as rendimiento.flag.<name>
 * so a trace says which side of the flag it took. The hook future A/B and
 * canary releases will set.
 *
 * @param {string} nombre
 * @param {boolean} [porDefecto]
 * @returns {boolean}
 */
function bandera(nombre, porDefecto = false) {
  const raw = process.env["RENDIMIENTO_FLAG_" + nombre.toUpperCase().replace(/[^A-Z0-9]+/g, "_")];
  const on = raw === undefined ? porDefecto : /^(1|true|on|yes|si|sí)$/i.test(raw.trim());
  const span = api.trace.getActiveSpan();
  if (span) span.setAttribute("rendimiento.flag." + nombre, on);
  return on;
}

module.exports = {
  iniciar, tramo, evento, llamar, bandera,
  // The same in English.
  start: iniciar, span: tramo, event: evento, call: llamar, flag: bandera,
  /** @internal for tests */
  _reset() {
    started = undefined;
  },
};
