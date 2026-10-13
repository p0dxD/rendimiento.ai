import type { Attributes, Span } from "@opentelemetry/api";

export interface Opciones {
  /** OTLP/HTTP endpoint; defaults to OTEL_EXPORTER_OTLP_ENDPOINT. */
  endpoint?: string;
  /** Service name; defaults to OTEL_SERVICE_NAME (app/service on rendimiento). */
  servicio?: string;
}

export interface Inicio {
  /** agent: rendimiento's agent traces; sdk: started here; off: no endpoint; missing: SDK not installed. */
  como: "agent" | "sdk" | "off" | "missing";
  sdk?: unknown;
}

export function iniciar(opciones?: Opciones): Inicio;
export function tramo<T>(nombre: string, fn: (span: Span) => T, atributos?: Attributes): T;
export function evento(nombre: string, atributos?: Attributes): void;
export function llamar(url: string | URL, init?: RequestInit): Promise<Response>;
export function bandera(nombre: string, porDefecto?: boolean): boolean;

export { iniciar as start, tramo as span, evento as event, llamar as call, bandera as flag };
