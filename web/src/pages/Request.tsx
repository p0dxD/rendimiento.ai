import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { universeApi, type KeptTrace, type TraceSpan } from "../api";
import { ms } from "../components/maqueta";
import { ErrorBox } from "../components/ui";
import { locale, t, tn } from "../i18n";
import { reasonLabel } from "./Universe";

interface Row {
  span: TraceSpan;
  depth: number;
  offsetUs: number;
}

/** The spans in call order: each one under its parent, siblings by start. */
function rows(spans: TraceSpan[], start: number): Row[] {
  const ids = new Set(spans.map((s) => s.id));
  const children = new Map<string, TraceSpan[]>();
  const roots: TraceSpan[] = [];
  for (const s of spans) {
    if (s.parentId && ids.has(s.parentId)) {
      const list = children.get(s.parentId) ?? [];
      list.push(s);
      children.set(s.parentId, list);
    } else {
      roots.push(s); // no parent, or one that was not kept
    }
  }
  const byStart = (a: TraceSpan, b: TraceSpan) => new Date(a.startedAt).getTime() - new Date(b.startedAt).getTime();
  const out: Row[] = [];
  const walk = (s: TraceSpan, depth: number) => {
    out.push({ span: s, depth, offsetUs: (new Date(s.startedAt).getTime() - start) * 1000 });
    for (const c of (children.get(s.id) ?? []).sort(byStart)) walk(c, depth + 1);
  };
  for (const r of roots.sort(byStart)) walk(r, 0);
  return out;
}

/** A steady color per service, so a request's hops between services show. */
function hue(service: string) {
  let h = 0;
  for (const c of service) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

/**
 * One kept request as a waterfall: every span in call order, with a bar for
 * when it ran within the request. A row opens its attributes and events.
 */
export function RequestPage() {
  const { id = "" } = useParams();
  const [data, setData] = useState<{ trace: KeptTrace; spans: TraceSpan[] }>();
  const [error, setError] = useState<unknown>();
  const [open, setOpen] = useState<string>();
  useEffect(() => {
    universeApi.trace(id).then((d) => {
      setData(d);
      setOpen(d.spans.find((s) => s.error)?.id);
    }, setError);
  }, [id]);
  const list = useMemo(() => (data ? rows(data.spans, new Date(data.trace.startedAt).getTime()) : []), [data]);
  if (error) return <ErrorBox error={error} />;
  if (!data) return <p className="muted">{t("Loading…")}</p>;
  const { trace } = data;
  const total = Math.max(trace.durationUs, 1);
  return (
    <div className="wf-page">
      <p className="small"><Link to="/universe">{t("Universe")}</Link> / {t("Request")}</p>
      <h1 className="wf-title">{trace.name}</h1>
      <p className="muted wf-meta">
        <span className={`badge ${trace.error ? "bad" : trace.reason === "slow" ? "warn" : "ok"}`}>{t(reasonLabel[trace.reason])}</span>
        <span>{ms(trace.durationUs / 1000)}</span>
        <span>{new Date(trace.startedAt).toLocaleString(locale)}</span>
        <span>{tn(trace.spans, "{n} span", "{n} spans")}</span>
        <span>{tn(trace.services.length, "through {n} service", "through {n} services")}</span>
      </p>
      <div className="wf" role="table">
        <div className="wf-head" role="row">
          <span>{t("Step")}</span>
          <span className="wf-scale">
            {[0, 0.25, 0.5, 0.75, 1].map((f) => <span key={f} style={{ left: `${f * 100}%` }}>{ms((total * f) / 1000)}</span>)}
          </span>
        </div>
        {list.map(({ span: s, depth, offsetUs }) => {
          const left = (offsetUs / total) * 100, width = Math.max((s.durationUs / total) * 100, 0.3);
          const color = s.error ? "var(--bad)" : `hsl(${hue(s.service)} 55% 45%)`;
          return (
            <div key={s.id} className={`wf-item${open === s.id ? " open" : ""}`}>
              <button className="wf-row" role="row" onClick={() => setOpen(open === s.id ? undefined : s.id)} aria-expanded={open === s.id}>
                <span className="wf-name" style={{ paddingLeft: depth * 14 }}>
                  <span className="wf-dot" style={{ background: color }} />
                  <span className="wf-label">{s.name}</span>
                  <span className="small muted">{s.service}</span>
                </span>
                <span className="wf-track">
                  <span className="wf-bar" style={{ left: `${Math.min(left, 99.7)}%`, width: `${Math.min(width, 100 - Math.min(left, 99.7))}%`, background: color }} />
                  <span className="wf-dur small" style={{ left: `${Math.min(left + width, 88)}%` }}>{ms(s.durationUs / 1000)}</span>
                </span>
              </button>
              {open === s.id && <SpanDetail s={s} offsetUs={offsetUs} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}

const kindLabel: Record<TraceSpan["kind"], string> = {
  server: "served a request", client: "made a call", internal: "its own moment", producer: "sent a message", consumer: "received a message",
};

function SpanDetail({ s, offsetUs }: { s: TraceSpan; offsetUs: number }) {
  const attrs = Object.entries(s.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <div className="wf-detail small">
      <p>
        {t(kindLabel[s.kind])} · {t("started {at} into the request, took {d}", { at: ms(offsetUs / 1000), d: ms(s.durationUs / 1000) })}
        {s.error && <> · <strong className="wf-bad">{s.statusMessage || t("failed")}</strong></>}
      </p>
      {attrs.length > 0 && (
        <table className="wf-attrs">
          <tbody>
            {attrs.map(([k, v]) => <tr key={k}><th>{k}</th><td>{typeof v === "string" ? v : JSON.stringify(v)}</td></tr>)}
          </tbody>
        </table>
      )}
      {(s.events ?? []).map((e, i) => (
        <div key={i} className="wf-event">
          <strong>{e.name}</strong> <span className="muted">{t("at {at}", { at: ms((new Date(e.at).getTime() - new Date(s.startedAt).getTime())) })}</span>
          {e.attributes && Object.entries(e.attributes).map(([k, v]) => (
            <pre key={k}><span className="muted">{k}: </span>{typeof v === "string" ? v : JSON.stringify(v)}</pre>
          ))}
        </div>
      ))}
    </div>
  );
}
