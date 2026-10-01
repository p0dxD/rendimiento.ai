// The Reliability tab: uptime checks of an app's services, charted. Forms
// follow the data's job: stat tiles for the headline numbers, a status strip
// (reserved status colors, always labelled) for "was it up", a two-line
// chart for response times with release markers, and a table for outages.
// Plain SVG and HTML, no chart library.
import { useEffect, useMemo, useRef, useState } from "react";
import {
  api, timeAgo,
  type App, type CheckKind, type Reliability, type ReliabilityRange, type Service, type UptimeBucket, type UptimeSeries,
} from "../api";
import { ErrorBox, usePoll } from "./ui";

const ranges: { id: ReliabilityRange; label: string }[] = [
  { id: "24h", label: "Last 24 hours" },
  { id: "7d", label: "Last 7 days" },
  { id: "30d", label: "Last 30 days" },
];

const kindLabel: Record<CheckKind, string> = { internal: "Inside the cluster", public: "Public URL" };

export function fmtPct(ok: number, total: number): string {
  if (total === 0) return "—";
  const p = (ok / total) * 100;
  if (p === 100) return "100%";
  return (p >= 99 ? p.toFixed(2) : p.toFixed(1)) + "%";
}

export function fmtMs(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)} s`;
}

function fmtDuration(ms: number): string {
  const m = Math.round(ms / 60000);
  if (m < 1) return "under a minute";
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  return h < 48 ? `${h} h ${m % 60} min` : `${Math.floor(h / 24)} days`;
}

type BucketState = "good" | "warning" | "critical" | "nodata";
const stateLabel: Record<BucketState, string> = { good: "Up", warning: "Partly down", critical: "Down", nodata: "No checks" };
const stateColor: Record<BucketState, string> = {
  good: "var(--viz-good)", warning: "var(--viz-warning)", critical: "var(--viz-critical)", nodata: "var(--viz-nodata)",
};
function bucketState(b?: UptimeBucket): BucketState {
  if (!b || b.total === 0) return "nodata";
  if (b.ok === b.total) return "good";
  return b.ok / b.total >= 0.5 ? "warning" : "critical";
}

/** Every bucket of the range, including the ones without checks. */
function timeline(data: Reliability, s: UptimeSeries) {
  const size = data.bucketSeconds * 1000;
  const since = new Date(data.since).getTime();
  const first = Math.floor(since / size) * size;
  const byAt = new Map(s.buckets.map((b) => [new Date(b.at).getTime(), b]));
  const out: { at: number; b?: UptimeBucket }[] = [];
  for (let t = first; t <= Date.now(); t += size) out.push({ at: t, b: byAt.get(t) });
  return out;
}

function fmtWhen(t: number, range: ReliabilityRange) {
  const d = new Date(t);
  return range === "24h"
    ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString([], { month: "short", day: "numeric" }) + (range === "7d" ? " " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "");
}

function checksOf(svc: Service): { kind: CheckKind; target: string }[] {
  const out: { kind: CheckKind; target: string }[] = [];
  const path = svc.health?.path;
  out.push({ kind: "internal", target: path ? `GET ${path}` : `TCP port ${svc.port ?? 8080}` });
  if (svc.domain) out.push({ kind: "public", target: `https://${svc.domain}${path ?? "/"}` });
  return out;
}

export function ReliabilityTab({ app }: { app: App }) {
  const [range, setRange] = useState<ReliabilityRange>("24h");
  const { data, error } = usePoll(() => api.reliability(app.name, range), [app.name, range], 60000);
  const loading = !!data && data.range !== range;

  const services = app.spec.services.filter((s) => (s.replicas ?? 1) > 0);
  return (
    <>
      <div className={`rel-filters${loading ? " loading" : ""}`} role="group" aria-label="Time range">
        {ranges.map((r) => (
          <button key={r.id} aria-pressed={range === r.id} onClick={() => setRange(r.id)}>{r.label}</button>
        ))}
        <span className="small muted">Every service is checked once a minute: its health check inside the cluster, and its public URL through Cloudflare.</span>
      </div>
      <ErrorBox error={error} />
      {!data && !error && <p className="muted">Loading…</p>}
      {data && (
        <div className="stack">
          {data.series.length === 0 && (
            <div className="card muted">No checks yet. The first results appear about a minute after the platform starts checking this app.</div>
          )}
          {services.flatMap((svc) => checksOf(svc).map((c) => {
            const s = data.series.find((x) => x.service === svc.name && x.kind === c.kind);
            if (!s) return null;
            return <CheckCard key={svc.name + c.kind} data={data} series={s} target={c.target} />;
          }))}
          <Incidents data={data} />
        </div>
      )}
    </>
  );
}

function CheckCard({ data, series: s, target }: { data: Reliability; series: UptimeSeries; target: string }) {
  const outages = data.incidents.filter((i) => i.service === s.service && i.kind === s.kind).length;
  const last = s.last;
  return (
    <section className="card rel-check" aria-label={`${s.service}, ${kindLabel[s.kind]}`}>
      <div className="row between">
        <div>
          <h3><strong>{s.service}</strong> <span className="muted" style={{ fontWeight: 400 }}>· {kindLabel[s.kind]}</span></h3>
          <div className="small muted mono">{target}</div>
        </div>
        {last && (
          <span className={`badge ${last.ok ? "ok" : "bad"}`} title={last.error}>
            {last.ok ? "Up" : "Down"} · checked {timeAgo(last.at)}
          </span>
        )}
      </div>

      <div className="rel-tiles">
        <Tile label="Uptime" value={fmtPct(s.ok, s.total)} note={`${s.ok.toLocaleString()} of ${s.total.toLocaleString()} checks`} />
        <Tile label="Typical response (p50)" value={s.p50Ms ? fmtMs(s.p50Ms) : "—"} note="half of checks were faster" />
        <Tile label="Slowest 5% (p95)" value={s.p95Ms ? fmtMs(s.p95Ms) : "—"} note="95% of checks were faster" />
        <Tile label="Outages" value={String(outages)} note={outages ? "listed below" : "none in this range"} />
      </div>

      <StatusStrip data={data} series={s} />
      <LatencyChart data={data} series={s} />
      <DataTable data={data} series={s} />
      {last && !last.ok && last.error && <div className="small" style={{ marginTop: 8 }}>Latest failure: <span className="mono">{last.error}</span></div>}
    </section>
  );
}

function Tile({ label, value, note }: { label: string; value: string; note: string }) {
  return (
    <div className="rel-tile">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      <div className="note">{note}</div>
    </div>
  );
}

type Tip = { x: number; y: number; lines: { key?: string; value: string; label: string }[]; when: string } | null;

function TipBox({ tip }: { tip: Tip }) {
  if (!tip) return null;
  return (
    <div className="rel-tip" style={{ left: tip.x, top: tip.y }}>
      <div className="when">{tip.when}</div>
      {tip.lines.map((l) => (
        <div key={l.label} className="row">
          {l.key && <i style={{ background: l.key }} />}
          <strong>{l.value}</strong> <span className="muted">{l.label}</span>
        </div>
      ))}
    </div>
  );
}

/** Was it up? One cell per bucket, in the reserved status colors, with a legend. */
function StatusStrip({ data, series }: { data: Reliability; series: UptimeSeries }) {
  const cells = useMemo(() => timeline(data, series), [data, series]);
  const [tip, setTip] = useState<Tip>(null);
  const ref = useRef<HTMLDivElement>(null);
  const size = data.bucketSeconds * 1000;
  return (
    <div style={{ position: "relative" }}>
      <div className="rel-legend" aria-hidden="true">
        {(["good", "warning", "critical", "nodata"] as BucketState[]).map((st) => (
          <span key={st}><i style={{ background: stateColor[st] }} />{stateLabel[st]}</span>
        ))}
      </div>
      <div className="rel-strip" ref={ref} onPointerLeave={() => setTip(null)}
        role="img" aria-label={`Availability: ${fmtPct(series.ok, series.total)} of checks succeeded`}>
        {cells.map(({ at, b }) => {
          const st = bucketState(b);
          return (
            <span key={at} style={{ background: stateColor[st] }}
              onPointerMove={(e) => {
                const box = ref.current!.getBoundingClientRect();
                setTip({
                  x: Math.min(e.clientX - box.left + 12, box.width - 170), y: 36,
                  when: `${fmtWhen(at, data.range)} – ${fmtWhen(at + size, data.range)}`,
                  lines: [
                    { value: stateLabel[st], label: "" },
                    ...(b ? [{ value: fmtPct(b.ok, b.total), label: `of ${b.total} checks` }] : []),
                  ],
                });
              }} />
          );
        })}
      </div>
      <div className="rel-axis"><span>{fmtWhen(cells[0]?.at ?? Date.now(), data.range)}</span><span>now</span></div>
      <TipBox tip={tip} />
    </div>
  );
}

function niceMax(v: number): number {
  if (v <= 0) return 100;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

/** Response times: p95 and p50 per bucket, release markers, crosshair tooltip. */
function LatencyChart({ data, series }: { data: Reliability; series: UptimeSeries }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);
  const [hover, setHover] = useState<number | null>(null);
  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(240, e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const cells = useMemo(() => timeline(data, series), [data, series]);
  if (cells.length === 0) return null;
  const size = data.bucketSeconds * 1000;
  const H = 170, L = 52, R = 36, T = 18, B = 22;
  const t0 = cells[0].at, t1 = Date.now();
  const x = (t: number) => L + ((t - t0) / Math.max(t1 - t0, 1)) * (width - L - R);
  const yMax = niceMax(Math.max(...cells.map((c) => (c.b && c.b.ok > 0 ? c.b.p95Ms : 0))));
  const y = (v: number) => T + (1 - v / yMax) * (H - T - B);
  const ticks = [0, yMax / 2, yMax];
  const mid = (at: number) => Math.min(at + size / 2, t1);

  // One path per run of buckets with successful checks (gaps break the line).
  const path = (pick: (b: UptimeBucket) => number) => {
    let d = "", pen = false;
    for (const c of cells) {
      if (c.b && c.b.ok > 0) {
        d += `${pen ? "L" : "M"}${x(mid(c.at)).toFixed(1)},${y(pick(c.b)).toFixed(1)}`;
        pen = true;
      } else pen = false;
    }
    return d;
  };
  const lastWith = [...cells].reverse().find((c) => c.b && c.b.ok > 0);
  // About one time label per 130px, so they never collide on a phone.
  const nTicks = Math.max(2, Math.min(5, Math.floor((width - L - R) / 130) + 1));
  const xTicks = Array.from({ length: nTicks }, (_, i) => t0 + ((t1 - t0) * i) / (nTicks - 1));
  const releases = data.releases.filter((r) => new Date(r.at).getTime() >= t0);

  const h = hover !== null ? cells[hover] : null;
  const relsIn = h ? releases.filter((r) => { const t = new Date(r.at).getTime(); return t >= h.at && t < h.at + size; }) : [];
  const tip: Tip = h ? {
    x: Math.min(x(mid(h.at)) + 12, width - 180), y: 8,
    when: `${fmtWhen(h.at, data.range)} – ${fmtWhen(h.at + size, data.range)}`,
    lines: h.b && h.b.ok > 0 ? [
      { key: "var(--viz-series-1)", value: fmtMs(h.b.p95Ms), label: "p95" },
      { key: "var(--viz-series-2)", value: fmtMs(h.b.p50Ms), label: "p50" },
      { value: fmtPct(h.b.ok, h.b.total), label: `up (${h.b.total} checks)` },
      ...relsIn.map((r) => ({ value: `#${r.number}`, label: r.rollbackOf ? `rollback to #${r.rollbackOf}` : "released" })),
    ] : [{ value: h.b ? "All checks failed" : "No checks", label: "" }],
  } : null;

  const endLabels = lastWith?.b && Math.abs(y(lastWith.b.p95Ms) - y(lastWith.b.p50Ms)) >= 12;
  return (
    <div className="rel-chart" ref={wrap}>
      <div className="rel-legend">
        <span><i className="line" style={{ background: "var(--viz-series-1)" }} />Slowest 5% (p95)</span>
        <span><i className="line" style={{ background: "var(--viz-series-2)" }} />Typical (p50)</span>
        {releases.length > 0 && <span><i className="line" style={{ background: "var(--muted)", width: 2, height: 10, verticalAlign: -1 }} />Release</span>}
      </div>
      <svg viewBox={`0 0 ${width} ${H}`} height={H} role="img"
        aria-label={`Response time: p50 ${fmtMs(series.p50Ms)}, p95 ${fmtMs(series.p95Ms)} over the range`}
        onPointerMove={(e) => {
          const box = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
          const px = ((e.clientX - box.left) / box.width) * width;
          let best = 0, dist = Infinity;
          cells.forEach((c, i) => { const d = Math.abs(x(mid(c.at)) - px); if (d < dist) { dist = d; best = i; } });
          setHover(best);
        }}
        onPointerLeave={() => setHover(null)}>
        {ticks.map((v) => (
          <g key={v}>
            <line x1={L} x2={width - R} y1={y(v)} y2={y(v)} stroke={v === 0 ? "var(--viz-axis)" : "var(--viz-grid)"} strokeWidth={1} />
            <text x={L - 6} y={y(v) + 4} textAnchor="end">{fmtMs(v)}</text>
          </g>
        ))}
        {xTicks.map((t, i) => (
          <text key={t} x={x(t)} y={H - 6} textAnchor={i === 0 ? "start" : i === nTicks - 1 ? "end" : "middle"}>{i === nTicks - 1 ? "now" : fmtWhen(t, data.range)}</text>
        ))}
        {releases.map((r) => {
          const rx = x(new Date(r.at).getTime());
          return (
            <g key={r.number}>
              <line x1={rx} x2={rx} y1={T - 4} y2={H - B} stroke="var(--muted)" strokeWidth={1} opacity={0.6} />
              <text x={rx} y={T - 7} textAnchor="middle">#{r.number}</text>
            </g>
          );
        })}
        <path d={path((b) => b.p95Ms)} fill="none" stroke="var(--viz-series-1)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        <path d={path((b) => b.p50Ms)} fill="none" stroke="var(--viz-series-2)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        {endLabels && lastWith?.b && (
          <>
            <text x={x(mid(lastWith.at)) + 6} y={y(lastWith.b.p95Ms) + 4}>p95</text>
            <text x={x(mid(lastWith.at)) + 6} y={y(lastWith.b.p50Ms) + 4}>p50</text>
          </>
        )}
        {h && (
          <g pointerEvents="none">
            <line x1={x(mid(h.at))} x2={x(mid(h.at))} y1={T} y2={H - B} stroke="var(--muted)" strokeWidth={1} />
            {h.b && h.b.ok > 0 && (
              <>
                <circle cx={x(mid(h.at))} cy={y(h.b.p95Ms)} r={4} fill="var(--viz-series-1)" stroke="var(--surface)" strokeWidth={2} />
                <circle cx={x(mid(h.at))} cy={y(h.b.p50Ms)} r={4} fill="var(--viz-series-2)" stroke="var(--surface)" strokeWidth={2} />
              </>
            )}
          </g>
        )}
        <rect x={L} y={0} width={Math.max(width - L - R, 0)} height={H} fill="transparent" />
      </svg>
      <TipBox tip={tip} />
    </div>
  );
}

/** The same numbers without hovering: the table view. */
function DataTable({ data, series }: { data: Reliability; series: UptimeSeries }) {
  const size = data.bucketSeconds * 1000;
  return (
    <details>
      <summary className="small muted">Data table</summary>
      <div style={{ maxHeight: 260, overflow: "auto" }}>
        <table>
          <thead><tr><th>From</th><th>Checks</th><th>Uptime</th><th>p50</th><th>p95</th></tr></thead>
          <tbody>
            {[...series.buckets].reverse().map((b) => {
              const t = new Date(b.at).getTime();
              return (
                <tr key={b.at}>
                  <td>{fmtWhen(t, data.range)} – {fmtWhen(t + size, data.range)}</td>
                  <td>{b.ok}/{b.total}</td>
                  <td>{fmtPct(b.ok, b.total)}</td>
                  <td>{b.ok ? fmtMs(b.p50Ms) : "—"}</td>
                  <td>{b.ok ? fmtMs(b.p95Ms) : "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </details>
  );
}

function Incidents({ data }: { data: Reliability }) {
  return (
    <section className="card">
      <h3 style={{ margin: "0 0 8px", fontSize: 15 }}>Outages</h3>
      {data.incidents.length === 0 ? (
        <p className="small muted" style={{ margin: 0 }}>None in this range. An outage starts after two failed checks in a row and ends at the next successful one.</p>
      ) : (
        <div style={{ overflowX: "auto" }}>
        <table>
          <thead><tr><th>Service</th><th>Check</th><th>Started</th><th>Lasted</th><th>First error</th></tr></thead>
          <tbody>
            {data.incidents.map((i) => {
              const start = new Date(i.startedAt).getTime();
              const end = i.endedAt ? new Date(i.endedAt).getTime() : Date.now();
              return (
                <tr key={i.id}>
                  <td>{i.service}</td>
                  <td>{kindLabel[i.kind]}</td>
                  <td title={new Date(start).toLocaleString()}>{timeAgo(i.startedAt)}</td>
                  <td>{i.endedAt ? fmtDuration(end - start) : <span className="badge bad">Ongoing · {fmtDuration(end - start)}</span>}</td>
                  <td className="mono small">{i.error}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        </div>
      )}
    </section>
  );
}
