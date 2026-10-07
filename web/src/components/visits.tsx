// Visitors, from Umami: the Visits tab of an app and the dashboard summary.
// Stat tiles with the change against the period before (in words, never
// color alone), a two-line chart of visitors and page views with a
// crosshair tooltip, and the top pages, referrers and countries as tables
// with in-row bars. Plain SVG and HTML, like the Reliability tab.
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, type App, type VisitMetric, type VisitRange, type VisitReport, type VisitStats, type Visits } from "../api";
import { ErrorBox, usePoll } from "./ui";
import { locale, t, tn } from "../i18n";

const ranges: { id: VisitRange; label: string }[] = [
  { id: "24h", label: "Last 24 hours" }, // labels are translated where shown
  { id: "7d", label: "Last 7 days" },
  { id: "30d", label: "Last 30 days" },
];

const fmtN = (n: number) => n.toLocaleString(locale);

function bounceRate(s: VisitStats): number | undefined {
  return s.visits > 0 ? (Math.min(s.bounces, s.visits) / s.visits) * 100 : undefined;
}

function avgVisit(s: VisitStats): number | undefined {
  return s.visits > 0 ? s.totalTimeSec / s.visits : undefined;
}

function fmtVisitTime(sec: number): string {
  const s = Math.round(sec);
  if (s < 60) return t("{n} s", { n: s });
  return t("{m} min {s} s", { m: Math.floor(s / 60), s: s % 60 });
}

/** "↑ 40% vs the previous period, better": direction in words. */
function change(cur: number | undefined, prev: number | undefined, higherIsBetter = true): string {
  if (cur === undefined || prev === undefined) return t("No earlier data to compare");
  if (Math.round(cur) === Math.round(prev)) return t("Same as the period before");
  if (prev === 0) return t("↑ from none in the period before");
  const pct = Math.round((Math.abs(cur - prev) / prev) * 100);
  const up = cur > prev;
  return t(up === higherIsBetter ? "{arrow} {pct}% vs the period before, better" : "{arrow} {pct}% vs the period before, worse", { arrow: up ? "↑" : "↓", pct });
}

function Tile({ label, value, note, title }: { label: string; value: string; note: string; title?: string }) {
  return (
    <div className="rel-tile" title={title}>
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      <div className="note">{note}</div>
    </div>
  );
}

function niceMax(v: number): number {
  if (v <= 0) return 10;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

/** Visitors and page views per hour or day, with a crosshair tooltip. */
function VisitChart({ report, range, tz }: { report: VisitReport; range: VisitRange; tz: string }) {
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

  const pv = report.pageviews, vs = report.visitors;
  const n = pv.length;
  if (n === 0) return null;
  const when = (at: string) => {
    const d = new Date(at);
    return range === "24h"
      ? d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", timeZone: tz })
      : d.toLocaleDateString(locale, { weekday: range === "7d" ? "short" : undefined, month: "short", day: "numeric", timeZone: tz });
  };
  const H = 180, L = 44, R = 12, T = 12, B = 22;
  const x = (i: number) => L + (n === 1 ? 0 : (i / (n - 1)) * (width - L - R));
  const yMax = niceMax(Math.max(...pv.map((p) => p.n), ...vs.map((p) => p.n)));
  const y = (v: number) => T + (1 - v / yMax) * (H - T - B);
  const line = (pts: { n: number }[]) => pts.map((p, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(p.n).toFixed(1)}`).join("");
  const nTicks = Math.max(2, Math.min(6, Math.floor((width - L - R) / 110) + 1));
  const xTicks = Array.from({ length: nTicks }, (_, k) => Math.round(((n - 1) * k) / (nTicks - 1)));
  const h = hover;
  return (
    <div className="rel-chart" ref={wrap}>
      <div className="rel-legend">
        <span><i className="line" style={{ background: "var(--viz-series-1)" }} />{t("Visitors")}</span>
        <span><i className="line" style={{ background: "var(--viz-series-2)" }} />{t("Page views")}</span>
      </div>
      <svg viewBox={`0 0 ${width} ${H}`} height={H} role="img"
        aria-label={t("{visitors} visitors and {views} page views over the range", { visitors: fmtN(report.current.visitors), views: fmtN(report.current.pageviews) })}
        onPointerMove={(e) => {
          const box = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
          const px = ((e.clientX - box.left) / box.width) * width;
          setHover(Math.max(0, Math.min(n - 1, Math.round(((px - L) / Math.max(width - L - R, 1)) * (n - 1)))));
        }}
        onPointerLeave={() => setHover(null)}>
        {[0, yMax / 2, yMax].map((v) => (
          <g key={v}>
            <line x1={L} x2={width - R} y1={y(v)} y2={y(v)} stroke={v === 0 ? "var(--viz-axis)" : "var(--viz-grid)"} strokeWidth={1} />
            <text x={L - 6} y={y(v) + 4} textAnchor="end">{fmtN(Math.round(v))}</text>
          </g>
        ))}
        {xTicks.map((i, k) => (
          <text key={k} x={x(i)} y={H - 6} textAnchor={k === 0 ? "start" : k === nTicks - 1 ? "end" : "middle"}>{when(pv[i].at)}</text>
        ))}
        <path d={line(pv)} fill="none" stroke="var(--viz-series-2)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        <path d={line(vs)} fill="none" stroke="var(--viz-series-1)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        {h !== null && (
          <g pointerEvents="none">
            <line x1={x(h)} x2={x(h)} y1={T} y2={H - B} stroke="var(--muted)" strokeWidth={1} />
            <circle cx={x(h)} cy={y(pv[h].n)} r={4} fill="var(--viz-series-2)" stroke="var(--surface)" strokeWidth={2} />
            {vs[h] && <circle cx={x(h)} cy={y(vs[h].n)} r={4} fill="var(--viz-series-1)" stroke="var(--surface)" strokeWidth={2} />}
          </g>
        )}
        <rect x={L} y={0} width={Math.max(width - L - R, 0)} height={H} fill="transparent" />
      </svg>
      {h !== null && (
        <div className="rel-tip" style={{ left: Math.min(x(h) + 12, width - 170), top: 8 }}>
          <div className="when">{when(pv[h].at)}</div>
          <div className="row"><i style={{ background: "var(--viz-series-1)" }} /><strong>{fmtN(vs[h]?.n ?? 0)}</strong> <span className="muted">{t("visitors")}</span></div>
          <div className="row"><i style={{ background: "var(--viz-series-2)" }} /><strong>{fmtN(pv[h].n)}</strong> <span className="muted">{t("page views")}</span></div>
        </div>
      )}
    </div>
  );
}

let regionNames: Intl.DisplayNames | undefined;
function countryName(code: string): string {
  try {
    regionNames ??= new Intl.DisplayNames([locale], { type: "region" });
    return regionNames.of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

/** A top list: the value, a bar for its share, the count. */
function Top({ title, rows, label, empty }: { title: string; rows: VisitMetric[]; label: (v: string) => string; empty: string }) {
  const max = Math.max(...rows.map((r) => r.n), 1);
  return (
    <section className="card vis-top">
      <h3>{title}</h3>
      {rows.length === 0 ? <p className="small muted">{empty}</p> : (
        <table>
          <tbody>
            {rows.map((r) => (
              <tr key={r.value}>
                <td>
                  <span className="vis-bar" style={{ width: `${(r.n / max) * 100}%` }} aria-hidden="true" />
                  <span className="vis-label">{label(r.value)}</span>
                </td>
                <td className="num">{fmtN(r.n)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function ReportView({ report, data, multi }: { report: VisitReport; data: Visits; multi: boolean }) {
  const c = report.current, p = report.previous;
  const br = bounceRate(c), av = avgVisit(c);
  const open = data.umamiURL ? `${data.umamiURL}/websites/${report.website.id}` : undefined;
  return (
    <div className="stack">
      {(multi || open) && (
        <div className="row between">
          <h3 style={{ margin: 0 }}>{report.website.name} <span className="small muted mono">{report.website.domain}</span></h3>
          {open && <a href={open} target="_blank" rel="noreferrer" className="small">{t("Open in Umami →")}</a>}
        </div>
      )}
      <section className="card">
        <div className="rel-tiles">
          <Tile label={t("Visitors")} value={fmtN(c.visitors)} note={change(c.visitors, p.visitors)} title={t("Different people (browsers) who visited")} />
          <Tile label={t("Page views")} value={fmtN(c.pageviews)} note={change(c.pageviews, p.pageviews)} />
          <Tile label={t("Visits")} value={fmtN(c.visits)} note={change(c.visits, p.visits)} title={t("A visit ends after 30 minutes without activity")} />
          <Tile label={t("Left after one page")} value={br !== undefined ? `${Math.round(br)}%` : "—"} note={change(br, bounceRate(p), false)}
            title={t("Share of visits that saw only one page (the bounce rate)")} />
          <Tile label={t("Average visit")} value={av !== undefined ? fmtVisitTime(av) : "—"} note={change(av, avgVisit(p))} />
          <Tile label={t("Right now")} value={fmtN(report.active)} note={t("visitors in the last 5 minutes")} />
        </div>
        <VisitChart report={report} range={data.range} tz={data.timeZone} />
      </section>
      <div className="vis-tops">
        <Top title={t("Pages")} rows={report.paths} label={(v) => v} empty={t("No page views in this range.")} />
        <Top title={t("Where they come from")} rows={report.referrers} label={(v) => v || t("Direct or unknown")} empty={t("Every visit came directly, or the referrer was hidden.")} />
        <Top title={t("Countries")} rows={report.countries} label={(v) => (v ? countryName(v) : t("Unknown"))} empty={t("No countries yet.")} />
      </div>
    </div>
  );
}

export function VisitsTab({ app }: { app: App }) {
  const [range, setRange] = useState<VisitRange>("7d");
  const { data, error } = usePoll(() => api.visits(app.name, range), [app.name, range], 120000);
  const loading = !!data && data.range !== range;
  return (
    <>
      <div className={`rel-filters${loading ? " loading" : ""}`} role="group" aria-label={t("Time range")}>
        {ranges.map((r) => (
          <button key={r.id} aria-pressed={range === r.id} onClick={() => setRange(r.id)}>{t(r.label)}</button>
        ))}
        <span className="small muted">{t("Counted by Umami, without cookies; the numbers refresh every few minutes.")}</span>
      </div>
      <ErrorBox error={error} />
      {!data && !error && <p className="muted">{t("Loading…")}</p>}
      {data && !data.configured && (
        <div className="card">
          <p style={{ marginTop: 0 }}>{t("Visitor numbers are off: rendimiento has no Umami account to read them with.")}</p>
          <p className="small muted" style={{ marginBottom: 0 }}>{t("Set UMAMI_URL, UMAMI_USERNAME and UMAMI_PASSWORD (a view-only Umami user); the book's Visits page shows how.")}</p>
        </div>
      )}
      {data && data.configured && data.reports.length === 0 && (
        <div className="card">
          {data.hosts.length === 0 ? (
            <p style={{ margin: 0 }}>{t("This app has no public domain, so there are no visits to count.")}</p>
          ) : (
            <>
              <p style={{ marginTop: 0 }}>{t("Umami does not count {hosts} yet.", { hosts: data.hosts.join(", ") })}</p>
              <ol className="small" style={{ marginBottom: 0 }}>
                <li>{t("In Umami, add a website with the domain {host}, in the team rendimiento reads.", { host: data.hosts[0] })}</li>
                <li>{t("Put its tracking script in the app's pages, inside <head>:")}
                  <pre className="mono small">{`<script defer src="${data.umamiURL || "https://umami.example.com"}/script.js" data-website-id="…"></script>`}</pre>
                </li>
                <li>{t("Visits appear here a few minutes after the first one.")}</li>
              </ol>
            </>
          )}
        </div>
      )}
      {data && data.reports.length > 0 && (
        <div className="stack">
          {data.reports.map((r) => <ReportView key={r.website.id} report={r} data={data} multi={data.reports.length > 1} />)}
        </div>
      )}
    </>
  );
}

/** The dashboard's visitors panel: the last 7 days, all apps Umami counts. */
export function VisitsPanel() {
  const { data } = usePoll(api.visitsSummary, [], 300000);
  if (!data || !data.configured || data.apps.length === 0) return null;
  const c = data.current, p = data.previous;
  const max = Math.max(...data.apps.map((a) => a.current.visitors), 1);
  return (
    <section className="card dlv vis-panel" aria-labelledby="vis-h">
      <div className="row between">
        <h2 id="vis-h" style={{ margin: 0, fontSize: 16 }}>{t("Visitors, last {n} days", { n: data.days })}</h2>
        <span className="small muted">{t("from Umami")}</span>
      </div>
      <div className="rel-tiles dlv-tiles">
        <Tile label={t("Visitors")} value={fmtN(c.visitors)} note={change(c.visitors, p.visitors)} />
        <Tile label={t("Page views")} value={fmtN(c.pageviews)} note={change(c.pageviews, p.pageviews)} />
        <Tile label={t("Visits")} value={fmtN(c.visits)} note={tn(data.apps.length, "across {n} app", "across {n} apps")} />
      </div>
      <table className="vis-apps">
        <tbody>
          {data.apps.map((a) => (
            <tr key={a.app}>
              <td><Link to={`/apps/${a.app}/visits`}>{a.app}</Link></td>
              <td className="vis-cell">
                <span className="vis-bar" style={{ width: `${(a.current.visitors / max) * 100}%` }} aria-hidden="true" />
                <span className="vis-label">{tn(a.current.visitors, "{n} visitor", "{n} visitors", { n: fmtN(a.current.visitors) })}</span>
              </td>
              <td className="small muted">{change(a.current.visitors, a.previous.visitors)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
