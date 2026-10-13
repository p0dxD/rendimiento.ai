package ai.rendimiento.observa;

import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.common.AttributesBuilder;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.context.Context;
import io.opentelemetry.context.Scope;
import java.lang.management.ManagementFactory;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;
import java.util.function.Function;
import java.util.regex.Pattern;

/**
 * rendimiento for Java: a thin layer over OpenTelemetry so a service shows up on the Universe map
 * with its own moments, not just its HTTP calls.
 *
 * <p>With {@code observe: true} in rendimiento.yaml the platform already loads the OpenTelemetry
 * Java agent and sets the OTEL_* variables; these helpers then add spans and events to the traces
 * the agent makes. Outside the cluster (or without the agent) {@link #iniciar()} starts the SDK
 * itself when {@code opentelemetry-sdk-extension-autoconfigure} and an exporter are on the
 * classpath and OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise every helper still works and simply
 * records nothing.
 */
public final class Observa {
  static final String NAME = "ai.rendimiento.observa";
  static final String VERSION = "0.1.0";

  /** Where flags and settings are read from; tests replace it. */
  static Function<String, String> entorno = System::getenv;

  private static volatile String iniciado;

  private Observa() {}

  /** Work done inside a span; it may return a value and throw. */
  @FunctionalInterface
  public interface Trabajo<T, E extends Exception> {
    T hacer() throws E;
  }

  private static Tracer tracer() {
    return GlobalOpenTelemetry.getTracer(NAME, VERSION);
  }

  /**
   * Starts tracing once and says how: "agent" (the OpenTelemetry agent already traces), "sdk"
   * (started here), "off" (no endpoint) or "missing" (the SDK is not on the classpath).
   */
  public static synchronized String iniciar() {
    if (iniciado != null) {
      return iniciado;
    }
    if (agentLoaded()) {
      return iniciado = "agent";
    }
    String endpoint = entorno.apply("OTEL_EXPORTER_OTLP_ENDPOINT");
    if (endpoint == null || endpoint.isBlank()) {
      return iniciado = "off";
    }
    try {
      Class<?> auto = Class.forName("io.opentelemetry.sdk.autoconfigure.AutoConfiguredOpenTelemetrySdk");
      Object builder = auto.getMethod("builder").invoke(null);
      builder = builder.getClass().getMethod("setResultAsGlobal").invoke(builder);
      builder.getClass().getMethod("build").invoke(builder);
      return iniciado = "sdk";
    } catch (ReflectiveOperationException e) {
      System.err.println(NAME + ": to trace without rendimiento's agent, add opentelemetry-sdk-extension-autoconfigure and opentelemetry-exporter-otlp");
      return iniciado = "missing";
    }
  }

  /** True when the OpenTelemetry Java agent is loaded (-javaagent:…opentelemetry…). */
  static boolean agentLoaded() {
    String opts = String.valueOf(entorno.apply("JAVA_TOOL_OPTIONS"));
    if (opts.contains("otel-auto-instrumentation") || opts.contains("opentelemetry-javaagent")) {
      return true;
    }
    for (String arg : ManagementFactory.getRuntimeMXBean().getInputArguments()) {
      if (arg.startsWith("-javaagent:") && arg.contains("opentelemetry")) {
        return true;
      }
    }
    return false;
  }

  /**
   * Runs work inside a span named after a moment of the business ("cobrar", "generar-reporte"),
   * which becomes the current span, so calls made inside nest under it. An exception marks the span
   * failed and is thrown again.
   */
  public static <T, E extends Exception> T tramo(String nombre, Trabajo<T, E> trabajo) throws E {
    return tramo(nombre, Map.of(), trabajo);
  }

  /** {@link #tramo(String, Trabajo)} with attributes on the span. */
  public static <T, E extends Exception> T tramo(String nombre, Map<String, ?> atributos, Trabajo<T, E> trabajo) throws E {
    try (Tramo t = abrir(nombre, atributos)) {
      try {
        return trabajo.hacer();
      } catch (Exception | Error e) {
        t.fallo(e);
        throw e;
      }
    }
  }

  /**
   * Opens a span that is current until closed, for try-with-resources:
   *
   * <pre>{@code
   * try (var t = Observa.abrir("cobrar", Map.of("pedido.id", id))) { ... }
   * }</pre>
   */
  public static Tramo abrir(String nombre, Map<String, ?> atributos) {
    Span span = tracer().spanBuilder(nombre).setAllAttributes(attributes(atributos)).startSpan();
    return new Tramo(span, span.makeCurrent());
  }

  /** {@link #abrir(String, Map)} without attributes. */
  public static Tramo abrir(String nombre) {
    return abrir(nombre, Map.of());
  }

  /** An open span; closing it ends the span and restores the one before. */
  public static final class Tramo implements AutoCloseable {
    private final Span span;
    private final Scope scope;

    Tramo(Span span, Scope scope) {
      this.span = span;
      this.scope = scope;
    }

    /** The OpenTelemetry span, for anything the helpers do not cover. */
    public Span span() {
      return span;
    }

    /** Marks the span failed with the error. */
    public void fallo(Throwable e) {
      span.recordException(e);
      span.setStatus(StatusCode.ERROR, String.valueOf(e.getMessage()));
    }

    @Override
    public void close() {
      scope.close();
      span.end();
    }
  }

  /**
   * Records that something happened (an order paid, a cache miss) on the current span; with no span
   * going, as a short span of its own so it is not lost.
   */
  public static void evento(String nombre, Map<String, ?> atributos) {
    Span current = Span.current();
    if (current.isRecording()) {
      current.addEvent(nombre, attributes(atributos));
    } else {
      tracer().spanBuilder(nombre).setAllAttributes(attributes(atributos)).startSpan().end();
    }
  }

  /** {@link #evento(String, Map)} without attributes. */
  public static void evento(String nombre) {
    evento(nombre, Map.of());
  }

  /**
   * Headers that carry the current trace to another service (traceparent), for an HTTP client the
   * agent does not instrument.
   */
  public static Map<String, String> cabeceras() {
    Map<String, String> out = new HashMap<>();
    GlobalOpenTelemetry.getPropagators().getTextMapPropagator().inject(Context.current(), out, Map::put);
    return out;
  }

  private static final Pattern TRUTHY = Pattern.compile("^(1|true|on|yes|si|sí)$", Pattern.CASE_INSENSITIVE);
  private static final Pattern NON_ENV = Pattern.compile("[^A-Z0-9]+");

  /**
   * A feature flag read from RENDIMIENTO_FLAG_&lt;NAME&gt; ("1", "true", "on", "yes" turn it on),
   * recorded on the current span as rendimiento.flag.&lt;name&gt; so a trace says which side it
   * took. The hook future A/B and canary releases will set.
   */
  public static boolean bandera(String nombre, boolean porDefecto) {
    String raw = entorno.apply("RENDIMIENTO_FLAG_" + NON_ENV.matcher(nombre.toUpperCase(Locale.ROOT)).replaceAll("_"));
    boolean on = raw == null ? porDefecto : TRUTHY.matcher(raw.trim()).matches();
    Span current = Span.current();
    if (current.isRecording()) {
      current.setAttribute("rendimiento.flag." + nombre, on);
    }
    return on;
  }

  /** {@link #bandera(String, boolean)}, off by default. */
  public static boolean bandera(String nombre) {
    return bandera(nombre, false);
  }

  // The same in English.

  public static String start() {
    return iniciar();
  }

  public static <T, E extends Exception> T span(String name, Trabajo<T, E> work) throws E {
    return tramo(name, work);
  }

  public static void event(String name, Map<String, ?> attributes) {
    evento(name, attributes);
  }

  public static Map<String, String> headers() {
    return cabeceras();
  }

  public static boolean flag(String name, boolean byDefault) {
    return bandera(name, byDefault);
  }

  /** Attributes from a map of strings, numbers and booleans; anything else becomes its text. */
  static Attributes attributes(Map<String, ?> m) {
    AttributesBuilder b = Attributes.builder();
    if (m == null) {
      return b.build();
    }
    m.forEach((k, v) -> {
      if (v instanceof Boolean x) {
        b.put(k, x);
      } else if (v instanceof Integer || v instanceof Long || v instanceof Short) {
        b.put(k, ((Number) v).longValue());
      } else if (v instanceof Number x) {
        b.put(k, x.doubleValue());
      } else if (v != null) {
        b.put(k, v.toString());
      }
    });
    return b.build();
  }

  /** For tests: forget that tracing was started. */
  static void reset() {
    iniciado = null;
  }
}
