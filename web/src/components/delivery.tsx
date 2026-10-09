// Delivery health on the dashboard: the four DORA numbers for the last 30
// days as stat tiles. Each has its trend against the 30 days before, in
// words, and a twelve-week sparkline (one series, so no legend) with a
// tooltip per week. A table below holds the same weeks as numbers.
import { useState } from "react";
import { api, type DeliveryReport, type DeliveryWeek } from "../api";
import { usePoll } from "./ui";
import { locale, t, tn } from "../i18n";

/** Seconds as the largest sensible unit. */
export function fmtSec(sec: number): string {
  if (sec < 60) return t("{n} s", { n: Math.round(sec) });
  const m = Math.round(sec / 60);
  if (m < 60) return t("{n} min", { n: m });
  const h = Math.floor(m / 60);
  if (h >= 48) return t("{n} days", { n: Math.round(h / 24) });
  return m % 60 ? t("{h} h {m} min", { h, m: m % 60 }) : t("{n} h", { n: h });
}

/** "Week of Oct 6" for a week's first day. */
function fmtWeek(start: string): string {
  const [y, m, d] = start.split("-").map(Number);
  return t("Week of {date}", { date: new Date(y, m - 1, d).toLocaleDateString(locale, { month: "short", day: "numeric" }) });
}

type Tile = {
  label: string;
  value: string;
  note: string;
  trend: string;
  title: string;
  points: (number | undefined)[]; // one per week, undefined = no data
  fmt: (v: number) => string;
};

/** "↓ 40% vs the previous 30 days, better": direction in words, never color alone. */
function trend(cur: number | undefined, prev: number | undefined, higherIsBetter: boolean): string {
  if (cur === undefined || prev === undefined) return t("No earlier data to compare");
  if (cur === prev) return t("Same as the previous 30 days");
  if (prev === 0) return higherIsBetter ? t("↑ from none in the previous 30 days, better") : t("↑ from none in the previous 30 days, worse");
  const pct = Math.round((Math.abs(cur - prev) / prev) * 100);
  const up = cur > prev;
  const better = up === higherIsBetter;
  return t(better ? "{arrow} {pct}% vs the previous 30 days, better" : "{arrow} {pct}% vs the previous 30 days, worse", { arrow: up ? "↑" : "↓", pct });
}

/**
 * The delivery numbers to show (deploys, lead time, change failure, recovery, ...), each with its trend against the previous period.
 */
function tiles(r: DeliveryReport): Tile[] {
  const c = r.current, p = r.previous;
  const judged = (s: typeof c) => s.verified + s.failedVerify;
  const lead = (s: typeof c) => (s.deploys > 0 && s.medianLeadSec > 0 ? s.medianLeadSec : undefined);
  const cfr = (s: typeof c) => (judged(s) > 0 ? s.changeFailurePct : undefined);
  const mttr = (s: typeof c) => (s.incidents > 0 && s.meanRecoverySec > 0 ? s.meanRecoverySec : undefined);
  const w = r.weekly;
  return [
    {
      label: t("Deploys"),
      value: String(c.deploys),
      note: t("about {n} a week", { n: (c.deploys / (r.days / 7)).toLocaleString(locale, { maximumFractionDigits: 1 }) }) + " · " + tn(c.activeApps, "{n} app", "{n} apps"),
      trend: trend(c.deploys, p.deploys, true),
      title: t("Releases from builds in the last 30 days (rollbacks and blocked releases are not counted)"),
      points: w.map((x) => x.deploys),
      fmt: (v) => tn(v, "{n} deploy", "{n} deploys"),
    },
    {
      label: t("Lead time"),
      value: lead(c) !== undefined ? fmtSec(lead(c)!) : "—",
      note: t("push to live, median"),
      trend: trend(lead(c), lead(p), false),
      title: t("Median time from a push to its release going live"),
      points: w.map((x) => (x.deploys > 0 && x.medianLeadSec > 0 ? x.medianLeadSec : undefined)),
      fmt: fmtSec,
    },
    {
      label: t("Change failure rate"),
      value: cfr(c) !== undefined ? `${Math.round(cfr(c)!)}%` : "—",
      note: judged(c) > 0
        ? tn(judged(c), "{failed} of {n} release failed verification", "{failed} of {n} releases failed verification", { failed: c.failedVerify }) +
          " · " + tn(c.autoRollbacks, "{n} was rolled back automatically", "{n} were rolled back automatically")
        : t("no releases verified yet"),
      trend: trend(cfr(c), cfr(p), false),
      title: t("Share of verified releases that broke a service during their 5-minute verification"),
      points: w.map((x: DeliveryWeek) => x.changeFailurePct),
      fmt: (v) => `${Math.round(v)}%`,
    },
    {
      label: t("Time to recover"),
      value: mttr(c) !== undefined ? fmtSec(mttr(c)!) : "—",
      note: c.incidents > 0 ? tn(c.incidents, "mean of {n} outage", "mean of {n} outages") : t("no outages"),
      trend: trend(mttr(c), mttr(p), false),
      title: t("Mean duration of the outages that started in the last 30 days and have ended"),
      points: w.map((x) => x.meanRecoverySec),
      fmt: fmtSec,
    },
  ];
}

const W = 160, H = 40, PAD = 4;

/** Twelve weeks as a line; gaps where a week has no data; a dot on the last. */
function Spark({ points, weeks, fmt, label }: { points: (number | undefined)[]; weeks: DeliveryWeek[]; fmt: (v: number) => string; label: string }) {
  const [hover, setHover] = useState<number | null>(null);
  const vals = points.filter((v): v is number => v !== undefined);
  const max = Math.max(...vals, 0) || 1;
  const step = (W - 2 * PAD) / Math.max(points.length - 1, 1);
  const x = (i: number) => PAD + i * step;
  const y = (v: number) => H - PAD - (v / max) * (H - 2 * PAD);
  const segs: string[] = [];
  let cur = "";
  points.forEach((v, i) => {
    if (v === undefined) { if (cur) segs.push(cur); cur = ""; return; }
    cur += `${cur ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
  });
  if (cur) segs.push(cur);
  if (vals.length < 2) {
    return <div className="dlv-spark dlv-spark-empty small muted">{t("A trend line appears after two weeks with data.")}</div>;
  }
  const lastIdx = points.map((v, i) => (v === undefined ? -1 : i)).filter((i) => i >= 0).pop();
  const h = hover !== null ? hover : null;
  return (
    <div className="rel-chart dlv-spark" onMouseLeave={() => setHover(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={t("{label}, last 12 weeks", { label })} style={{ height: H }}>
        <line x1={PAD} x2={W - PAD} y1={H - PAD} y2={H - PAD} stroke="var(--viz-axis)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
        {segs.map((d) => (
          <path key={d} d={d} fill="none" stroke="var(--viz-series-1)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
        ))}
        {h !== null && <line x1={x(h)} x2={x(h)} y1={PAD} y2={H - PAD} stroke="var(--viz-axis)" strokeWidth={1} vectorEffect="non-scaling-stroke" />}
        {points.map((_, i) => (
          <rect key={i} x={x(i) - step / 2} y={0} width={step} height={H} fill="transparent" onMouseEnter={() => setHover(i)} />
        ))}
      </svg>
      {/* Dots in HTML so they stay round when the SVG stretches. */}
      {[lastIdx, h].filter((i): i is number => i !== undefined && i !== null && points[i] !== undefined).map((i) => (
        <span key={i} className="dlv-dot" style={{ left: `${(x(i) / W) * 100}%`, top: y(points[i]!) }} />
      ))}
      {h !== null && (
        <div className="rel-tip" style={{ left: `min(${(x(h) / W) * 100}%, calc(100% - 150px))`, top: H + 6 }}>
          <div className="when">{fmtWeek(weeks[h].start)}</div>
          <strong>{points[h] !== undefined ? fmt(points[h]!) : t("no data")}</strong>
        </div>
      )}
    </div>
  );
}

/** The dashboard's delivery health: the numbers of the last 30 days and a chart of twelve weeks. */
export function DeliveryPanel() {
  const { data } = usePoll(api.delivery, [], 60000);
  if (!data) return null;
  const ts = tiles(data);
  return (
    <section className="card dlv" aria-labelledby="dlv-h">
      <div className="row between">
        <h2 id="dlv-h" style={{ margin: 0, fontSize: 16 }}>{t("Delivery, last {n} days", { n: data.days })}</h2>
        <span className="small muted">{t("trend lines: the last 12 weeks")}</span>
      </div>
      <div className="rel-tiles dlv-tiles">
        {ts.map((t) => (
          <div key={t.label} className="rel-tile" title={t.title}>
            <div className="label">{t.label}</div>
            <div className="value">{t.value}</div>
            <div className="note">{t.note}</div>
            <Spark points={t.points} weeks={data.weekly} fmt={t.fmt} label={t.label} />
            <div className="note">{t.trend}</div>
          </div>
        ))}
      </div>
      <details>
        <summary className="small muted">{t("Weekly numbers")}</summary>
        <table className="dlv-table">
          <thead>
            <tr><th>{t("Week")}</th><th>{t("Deploys")}</th><th>{t("Lead time")}</th><th>{t("Change failure rate")}</th><th>{t("Outages")}</th><th>{t("Time to recover")}</th></tr>
          </thead>
          <tbody>
            {[...data.weekly].reverse().map((w) => (
              <tr key={w.start}>
                <td>{fmtWeek(w.start)}</td>
                <td>{w.deploys}</td>
                <td>{w.deploys > 0 && w.medianLeadSec > 0 ? fmtSec(w.medianLeadSec) : "—"}</td>
                <td>{w.changeFailurePct !== undefined ? `${Math.round(w.changeFailurePct)}%` : "—"}</td>
                <td>{w.incidents}</td>
                <td>{w.meanRecoverySec !== undefined ? fmtSec(w.meanRecoverySec) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </section>
  );
}
