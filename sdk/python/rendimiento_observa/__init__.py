"""rendimiento for Python: a thin layer over OpenTelemetry so a service shows
up on the Universe map with its own moments, not just its HTTP calls.

With ``observe: true`` in rendimiento.yaml the platform already loads the
OpenTelemetry agent and sets the OTEL_* variables; these helpers then add
spans and events to the traces the agent makes. Outside the cluster (or
without the agent) ``iniciar()`` starts the OpenTelemetry SDK itself when
it is installed (``pip install rendimiento-observa[sdk]``) and
OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise every helper still works and
simply records nothing.
"""

from __future__ import annotations

import functools
import inspect
import os
import re
import sys
import urllib.request
import warnings
from dataclasses import dataclass
from typing import Any, Mapping, Optional

from opentelemetry import propagate, trace

__all__ = [
    "iniciar", "tramo", "evento", "cabeceras", "llamar", "bandera",
    "start", "span", "event", "headers", "call", "flag",
]

NAME = "rendimiento-observa"
VERSION = "0.1.0"

_started: Optional[str] = None


def _tracer() -> trace.Tracer:
    return trace.get_tracer(NAME, VERSION)


def _agent_loaded() -> bool:
    """True when the platform's agent is loaded (on PYTHONPATH, or already imported)."""
    return "otel-auto-instrumentation" in os.environ.get("PYTHONPATH", "") or any(
        m.startswith("opentelemetry.instrumentation.auto_instrumentation") for m in sys.modules
    )


def iniciar(endpoint: Optional[str] = None, servicio: Optional[str] = None) -> str:
    """Starts tracing once and says how: "agent" (rendimiento's agent already
    traces), "sdk" (started here), "off" (no endpoint) or "missing" (the SDK
    is not installed)."""
    global _started
    if _started:
        return _started
    if _agent_loaded():
        _started = "agent"
        return _started
    if endpoint:
        os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"] = endpoint
    if servicio:
        os.environ["OTEL_SERVICE_NAME"] = servicio
    if not os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT"):
        _started = "off"
        return _started
    try:
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
    except ImportError:
        warnings.warn(f"{NAME}: to trace without rendimiento's agent, pip install rendimiento-observa[sdk]")
        _started = "missing"
        return _started
    provider = TracerProvider(resource=Resource.create())  # reads OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)
    _started = "sdk"
    return _started


class tramo:
    """A span named after a moment of the business ("cobrar",
    "generar-reporte") that becomes the current span, so calls made inside
    nest under it. Use it with ``with``::

        with tramo("cobrar", {"pedido.id": pedido.id}) as span:
            ...

    or as a decorator, on plain and async functions::

        @tramo("generar-reporte")
        async def generar(): ...

    An exception marks the span failed and goes on.
    """

    def __init__(self, nombre: str, atributos: Optional[Mapping[str, Any]] = None):
        self.nombre = nombre
        self.atributos = dict(atributos or {})
        self._cm = None

    def __enter__(self) -> trace.Span:
        self._cm = _tracer().start_as_current_span(self.nombre, attributes=self.atributos)
        return self._cm.__enter__()

    def __exit__(self, *exc) -> Optional[bool]:
        return self._cm.__exit__(*exc)

    def __call__(self, fn):
        if inspect.iscoroutinefunction(fn):
            @functools.wraps(fn)
            async def async_wrapper(*args, **kwargs):
                with tramo(self.nombre, self.atributos):
                    return await fn(*args, **kwargs)
            return async_wrapper

        @functools.wraps(fn)
        def wrapper(*args, **kwargs):
            with tramo(self.nombre, self.atributos):
                return fn(*args, **kwargs)
        return wrapper


def evento(nombre: str, atributos: Optional[Mapping[str, Any]] = None) -> None:
    """Records that something happened (an order paid, a cache miss) on the
    current span; with no span going, as a short span of its own so it is
    not lost."""
    current = trace.get_current_span()
    if current.is_recording():
        current.add_event(nombre, dict(atributos or {}))
    else:
        _tracer().start_span(nombre, attributes=dict(atributos or {})).end()


def cabeceras(extra: Optional[Mapping[str, str]] = None) -> dict:
    """Headers that carry the current trace to another service
    (traceparent), merged over extra: for requests, httpx or any client
    the agent does not instrument."""
    out = dict(extra or {})
    propagate.inject(out)
    return out


@dataclass
class Respuesta:
    """What llamar() got back."""
    status: int
    headers: Mapping[str, str]
    body: bytes

    def text(self) -> str:
        return self.body.decode()


def llamar(url: str, metodo: str = "GET", datos: Optional[bytes] = None,
           headers: Optional[Mapping[str, str]] = None, timeout: float = 10) -> Respuesta:
    """A small HTTP call (urllib) that carries the current trace."""
    req = urllib.request.Request(url, data=datos, method=metodo, headers=cabeceras(headers))
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return Respuesta(resp.status, dict(resp.headers), resp.read())


_truthy = re.compile(r"^(1|true|on|yes|si|sí)$", re.IGNORECASE)


def bandera(nombre: str, por_defecto: bool = False) -> bool:
    """A feature flag read from RENDIMIENTO_FLAG_<NAME> ("1", "true", "on",
    "yes" turn it on), recorded on the current span as
    rendimiento.flag.<name> so a trace says which side it took. The hook
    future A/B and canary releases will set."""
    raw = os.environ.get("RENDIMIENTO_FLAG_" + re.sub(r"[^A-Z0-9]+", "_", nombre.upper()))
    on = por_defecto if raw is None else bool(_truthy.match(raw.strip()))
    current = trace.get_current_span()
    if current.is_recording():
        current.set_attribute("rendimiento.flag." + nombre, on)
    return on


# The same in English.
start, span, event, headers, call, flag = iniciar, tramo, evento, cabeceras, llamar, bandera


def _reset() -> None:
    """For tests: forget that tracing was started."""
    global _started
    _started = None
