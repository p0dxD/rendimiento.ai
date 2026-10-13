import asyncio
import http.server
import re
import threading

import pytest
from opentelemetry.trace import StatusCode

import rendimiento_observa as observa


def test_tramo_nests_what_runs_inside(spans):
    with observa.tramo("cobrar", {"pedido.id": "7"}):
        with observa.tramo("validar"):
            pass
    inner, outer = spans()
    assert outer.name == "cobrar" and outer.attributes["pedido.id"] == "7"
    assert inner.parent.span_id == outer.context.span_id


def test_tramo_as_decorator_sync_async_and_failures(spans):
    @observa.tramo("reporte")
    def reporte():
        return 42

    @observa.span("lento")
    async def lento():
        await asyncio.sleep(0)
        return "listo"

    @observa.tramo("falla")
    def falla():
        raise ValueError("sin saldo")

    assert reporte() == 42
    assert asyncio.run(lento()) == "listo"
    assert reporte.__name__ == "reporte"
    with pytest.raises(ValueError):
        falla()
    ok, slow, bad = spans()
    assert (ok.name, slow.name) == ("reporte", "lento")
    assert bad.status.status_code == StatusCode.ERROR
    assert bad.events[0].name == "exception"


def test_evento_on_the_current_span_or_its_own(spans):
    with observa.tramo("pedido"):
        observa.evento("pagado", {"monto": 120})
    observa.event("suelto")
    pedido, suelto = spans()
    assert pedido.events[0].name == "pagado" and pedido.events[0].attributes["monto"] == 120
    assert suelto.name == "suelto"


def test_bandera_reads_the_environment_and_records_the_side(spans, monkeypatch):
    monkeypatch.setenv("RENDIMIENTO_FLAG_NUEVO_COBRO", "sí")
    with observa.tramo("t"):
        assert observa.bandera("nuevo-cobro") is True
        assert observa.bandera("otra") is False
        assert observa.flag("otra", True) is True
    assert spans()[0].attributes["rendimiento.flag.nuevo-cobro"] is True


def test_llamar_carries_the_trace(spans):
    seen = {}

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            seen["traceparent"] = self.headers.get("traceparent")
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")

        def log_message(self, *args):
            pass

    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        with observa.tramo("llamada") as s:
            trace_id = format(s.get_span_context().trace_id, "032x")
            assert observa.llamar(f"http://127.0.0.1:{server.server_port}/").text() == "ok"
            assert "traceparent" in observa.cabeceras({"x-otro": "1"})
    finally:
        server.shutdown()
    assert re.match(rf"^00-{trace_id}-[0-9a-f]{{16}}-[0-9a-f]{{2}}$", seen["traceparent"])


def test_iniciar_with_the_agent_or_without_an_endpoint(spans, monkeypatch):
    monkeypatch.setenv("PYTHONPATH", "/otel-auto-instrumentation/opentelemetry/instrumentation/auto_instrumentation:/otel-auto-instrumentation")
    assert observa.iniciar() == "agent"
    observa._reset()
    monkeypatch.delenv("PYTHONPATH")
    monkeypatch.delenv("OTEL_EXPORTER_OTLP_ENDPOINT", raising=False)
    assert observa.start() == "off"
    assert observa.iniciar() == "off"  # once
