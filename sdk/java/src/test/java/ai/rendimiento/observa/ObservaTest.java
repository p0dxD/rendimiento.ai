package ai.rendimiento.observa;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.opentelemetry.api.common.AttributeKey;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.sdk.testing.junit5.OpenTelemetryExtension;
import io.opentelemetry.sdk.trace.data.SpanData;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.RegisterExtension;

class ObservaTest {
  @RegisterExtension static final OpenTelemetryExtension otel = OpenTelemetryExtension.create();

  private Map<String, String> env;

  @BeforeEach
  void setUp() {
    env = new java.util.HashMap<>();
    Observa.entorno = env::get;
    Observa.reset();
  }

  @AfterEach
  void tearDown() {
    Observa.entorno = System::getenv;
  }

  private List<SpanData> spans() {
    return otel.getSpans();
  }

  @Test
  void tramoNestsWhatRunsInside() {
    int out = Observa.tramo("cobrar", Map.of("pedido.id", "7", "monto", 120), () -> Observa.tramo("validar", () -> 42));
    assertEquals(42, out);
    SpanData inner = spans().get(0), outer = spans().get(1);
    assertEquals("cobrar", outer.getName());
    assertEquals("7", outer.getAttributes().get(AttributeKey.stringKey("pedido.id")));
    assertEquals(120L, outer.getAttributes().get(AttributeKey.longKey("monto")));
    assertEquals(outer.getSpanId(), inner.getParentSpanId());
  }

  @Test
  void failuresMarkTheSpanAndGoOn() {
    Exception e = assertThrows(java.io.IOException.class, () -> Observa.tramo("falla", () -> {
      throw new java.io.IOException("sin saldo");
    }));
    assertEquals("sin saldo", e.getMessage());
    SpanData s = spans().get(0);
    assertEquals(StatusCode.ERROR, s.getStatus().getStatusCode());
    assertEquals("exception", s.getEvents().get(0).getName());
  }

  @Test
  void abrirWorksWithTryWithResources() {
    try (var t = Observa.abrir("reporte")) {
      assertTrue(Span.current().getSpanContext().equals(t.span().getSpanContext()));
    }
    assertFalse(Span.current().isRecording());
    assertEquals("reporte", spans().get(0).getName());
  }

  @Test
  void eventoOnTheCurrentSpanOrItsOwn() {
    Observa.tramo("pedido", () -> {
      Observa.evento("pagado", Map.of("monto", 120));
      return null;
    });
    Observa.event("suelto", Map.of());
    SpanData pedido = spans().get(0), suelto = spans().get(1);
    assertEquals("pagado", pedido.getEvents().get(0).getName());
    assertEquals("suelto", suelto.getName());
  }

  @Test
  void banderaReadsTheEnvironmentAndRecordsTheSide() {
    env.put("RENDIMIENTO_FLAG_NUEVO_COBRO", "sí");
    Observa.tramo("t", () -> {
      assertTrue(Observa.bandera("nuevo-cobro"));
      assertFalse(Observa.bandera("otra"));
      assertTrue(Observa.flag("otra", true));
      return null;
    });
    assertEquals(true, spans().get(0).getAttributes().get(AttributeKey.booleanKey("rendimiento.flag.nuevo-cobro")));
  }

  @Test
  void cabecerasCarryTheTrace() {
    Observa.tramo("llamada", () -> {
      String traceId = Span.current().getSpanContext().getTraceId();
      String tp = Observa.cabeceras().get("traceparent");
      assertTrue(tp != null && tp.matches("^00-" + traceId + "-[0-9a-f]{16}-[0-9a-f]{2}$"), tp);
      return null;
    });
  }

  @Test
  void iniciarWithTheAgentOrWithoutAnEndpoint() {
    env.put("JAVA_TOOL_OPTIONS", "-javaagent:/otel-auto-instrumentation/javaagent.jar");
    assertEquals("agent", Observa.iniciar());
    Observa.reset();
    env.clear();
    assertEquals("off", Observa.start());
    assertEquals("off", Observa.iniciar());
    Observa.reset();
    env.put("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318");
    assertEquals("missing", Observa.iniciar());
  }
}
