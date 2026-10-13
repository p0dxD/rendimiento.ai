// The Universe page: every app from above. Each system (an app, an add-on,
// a namespace deployed another way) is a circle holding its services,
// databases and jobs; lines are what calls what, and the internet sits in
// the middle with lines to every public address.
import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { timeAgo, universeApi, type Universe as UniverseData, type UniverseEdge, type UniverseNode, type UniverseSystem, type UniverseWindow } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t } from "../i18n";

const NODE_R = 15;
const INTERNET_R = 26;

// Colors for systems, from the Cotija palette; each system keeps one by its name.
const palette = ["var(--cempasuchil)", "var(--anil)", "var(--nopal)", "var(--rosa)", "var(--teja)", "var(--maiz)", "var(--terciopelo)", "var(--grana)"];

/** A stable palette color for a system name. */
function colorOf(id: string) {
  let h = 0;
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return palette[h % palette.length];
}

const kindOrder: Record<UniverseNode["kind"], number> = { service: 0, workload: 1, postgres: 2, redis: 3, external: 4, internet: 5 };
const systemOrder: Record<UniverseSystem["kind"], number> = { app: 0, addon: 1, other: 2, infrastructure: 3 };

interface Circle { x: number; y: number; r: number }

interface Layout {
  systems: Map<string, Circle>;
  nodes: Map<string, { x: number; y: number }>;
  box: { x: number; y: number; w: number; h: number };
}

/**
 * Places each system as a circle with its nodes on a ring inside, and the
 * systems around the internet node on a golden-angle spiral, the biggest
 * nearest the middle. The same universe always gets the same layout.
 */
function layout(systems: UniverseSystem[], nodes: UniverseNode[], hasInternet: boolean): Layout {
  const members = new Map<string, UniverseNode[]>();
  for (const n of nodes) {
    if (n.kind === "internet" || !n.system) continue;
    members.set(n.system, [...(members.get(n.system) ?? []), n]);
  }
  const sized = systems
    .filter((s) => members.has(s.id))
    .map((s) => {
      const list = members.get(s.id)!.sort((a, b) => kindOrder[a.kind] - kindOrder[b.kind] || a.name.localeCompare(b.name));
      const ring = list.length <= 1 ? 0 : Math.max(38, (list.length * 58) / (2 * Math.PI));
      return { s, list, ring, r: ring + NODE_R + 30 };
    })
    .sort((a, b) => systemOrder[a.s.kind] - systemOrder[b.s.kind] || b.list.length - a.list.length || a.s.id.localeCompare(b.s.id));

  const placed: Circle[] = hasInternet ? [{ x: 0, y: 0, r: INTERNET_R + 40 }] : [];
  const out: Layout = { systems: new Map(), nodes: new Map(), box: { x: 0, y: 0, w: 0, h: 0 } };
  const golden = Math.PI * (3 - Math.sqrt(5));
  let k = 0;
  for (const { s, list, ring, r } of sized) {
    let c: Circle = { x: 0, y: 0, r };
    // Walk out along the spiral until the circle fits without touching another.
    for (let step = 0; ; step++, k++) {
      const dist = placed.length === 0 ? 0 : 30 * Math.sqrt(k + 1) + step * 4;
      c = { x: Math.cos(k * golden) * dist, y: Math.sin(k * golden) * dist, r };
      if (placed.every((p) => Math.hypot(p.x - c.x, p.y - c.y) >= p.r + c.r + 18)) break;
    }
    placed.push(c);
    out.systems.set(s.id, c);
    list.forEach((n, i) => {
      const a = (2 * Math.PI * i) / list.length - Math.PI / 2;
      out.nodes.set(n.id, { x: c.x + Math.cos(a) * ring, y: c.y + Math.sin(a) * ring });
    });
  }
  if (hasInternet) out.nodes.set("internet", { x: 0, y: 0 });

  let [x0, y0, x1, y1] = [-INTERNET_R, -INTERNET_R, INTERNET_R, INTERNET_R];
  for (const c of placed) {
    x0 = Math.min(x0, c.x - c.r); y0 = Math.min(y0, c.y - c.r - 22);
    x1 = Math.max(x1, c.x + c.r); y1 = Math.max(y1, c.y + c.r);
  }
  const pad = 24;
  out.box = { x: x0 - pad, y: y0 - pad, w: x1 - x0 + 2 * pad, h: y1 - y0 + 2 * pad };
  return out;
}

/** A line from a to b, curved when it crosses between systems, stopping at the node's edge. */
function edgePath(a: { x: number; y: number }, b: { x: number; y: number }, ra: number, rb: number, curve: boolean) {
  const dx = b.x - a.x, dy = b.y - a.y;
  const len = Math.hypot(dx, dy) || 1;
  const ux = dx / len, uy = dy / len;
  const sx = a.x + ux * ra, sy = a.y + uy * ra;
  const ex = b.x - ux * (rb + 3), ey = b.y - uy * (rb + 3);
  if (!curve) return `M${sx},${sy} L${ex},${ey}`;
  const bend = Math.min(60, len * 0.15);
  const cx = (sx + ex) / 2 - uy * bend, cy = (sy + ey) / 2 + ux * bend;
  return `M${sx},${sy} Q${cx},${cy} ${ex},${ey}`;
}

/** The color a node's ring takes from its latest checks: up, down, or not checked. */
function healthColor(n: UniverseNode) {
  if (!n.health) return "var(--muted)";
  return n.health.up ? "var(--ok)" : "var(--bad)";
}

const kindLabel: Record<UniverseNode["kind"], string> = {
  service: "Service",
  workload: "Job or worker",
  postgres: "PostgreSQL",
  redis: "Redis",
  external: "Outside the cluster",
  internet: "Internet",
};

const sourceLabel: Record<UniverseEdge["sources"][number], string> = {
  declared: "declared in rendimiento.yaml",
  env: "found in its environment",
  ingress: "public address",
  traced: "seen in traces",
};

const windows: { id: UniverseWindow; label: string }[] = [
  { id: "15m", label: "Last 15 minutes" },
  { id: "1h", label: "Last hour" },
  { id: "24h", label: "Last 24 hours" },
];

/** A system's title; the one for calls outside the cluster is translated. */
const systemTitle = (s: UniverseSystem) => (s.id === "outside" ? t("Outside the cluster") : s.title);

/** "2.5/min · p50 12 ms · p95 250 ms · 5% errors", or "" without numbers. */
function metricsText(e: UniverseEdge) {
  const m = e.metrics;
  if (!m) return "";
  const rate = m.perMinute >= 10 ? Math.round(m.perMinute) : Math.round(m.perMinute * 10) / 10;
  const parts = [t("{n}/min", { n: rate })];
  if (m.p50Ms) parts.push(t("p50 {ms} ms", { ms: Math.round(m.p50Ms) }));
  if (m.p95Ms) parts.push(t("p95 {ms} ms", { ms: Math.round(m.p95Ms) }));
  if (m.errors > 0) parts.push(t("{pct}% errors", { pct: Math.round(m.errors * 1000) / 10 }));
  return parts.join(" · ");
}

/** How a measured line looks: thicker with more calls, red with errors, amber when slow. */
function edgeStyle(e: UniverseEdge): { width: number; color?: string } {
  const m = e.metrics;
  if (!m) return { width: 1.6 };
  const width = 1.6 + Math.min(4.4, Math.log2(1 + m.perMinute));
  if (m.errors >= 0.05) return { width, color: "var(--bad)" };
  if ((m.p95Ms ?? 0) >= 500) return { width, color: "var(--warn)" };
  return { width };
}

/** The Universe page. */
export function Universe() {
  const [win, setWin] = useState<UniverseWindow>("1h");
  const { data, error, setData } = usePoll(() => universeApi.get(win), [win], 60000);
  const [showOther, setShowOther] = useState(true);
  const [showInfra, setShowInfra] = useState(false);
  const [selected, setSelected] = useState<string>();
  const [hover, setHover] = useState<string>();
  const [refreshing, setRefreshing] = useState(false);

  const view = useMemo(() => (data ? visible(data, showOther, showInfra) : undefined), [data, showOther, showInfra]);
  const geo = useMemo(() => view && layout(view.systems, view.nodes, view.nodes.some((n) => n.kind === "internet")), [view]);

  const focus = hover ?? selected;
  const near = useMemo(() => {
    const s = new Set<string>();
    if (!focus || !view) return s;
    s.add(focus);
    for (const e of view.edges) {
      if (e.from === focus) s.add(e.to);
      if (e.to === focus) s.add(e.from);
    }
    return s;
  }, [focus, view]);

  if (error && !data) return <ErrorBox error={error} />;
  if (!data || !view || !geo) return <div className="empty">{t("Loading…")}</div>;

  const byId = new Map(view.nodes.map((n) => [n.id, n]));
  const sel = selected ? byId.get(selected) : undefined;
  const apps = data.systems.filter((s) => s.kind === "app").length;

  return (
    <div className="stack">
      <div className="row between">
        <div>
          <h1>{t("Universe")}</h1>
          <p className="muted" style={{ margin: 0 }}>
            {t("{apps} apps, {nodes} services and {edges} connections, seen from above.", { apps, nodes: data.nodes.filter((n) => n.kind !== "internet").length, edges: data.edges.length })}
          </p>
        </div>
        <div className="row">
          <label className="row small"><input type="checkbox" checked={showOther} onChange={(e) => setShowOther(e.target.checked)} /> {t("Deployed another way")}</label>
          <label className="row small"><input type="checkbox" checked={showInfra} onChange={(e) => setShowInfra(e.target.checked)} /> {t("Cluster internals")}</label>
          {data.traces && (
            <select value={win} onChange={(e) => setWin(e.target.value as UniverseWindow)} aria-label={t("Window for the measured numbers")}>
              {windows.map((w) => <option key={w.id} value={w.id}>{t(w.label)}</option>)}
            </select>
          )}
          <button disabled={refreshing} onClick={() => { setRefreshing(true); universeApi.get(win, true).then(setData).finally(() => setRefreshing(false)); }}>
            {refreshing ? t("Refreshing…") : t("Refresh")}
          </button>
        </div>
      </div>

      {data.metricsError && <div className="alert small">{t("Latency could not be read from the metrics store: {error}", { error: data.metricsError })}</div>}

      <div className="universe">
        <svg className="universe-map card" viewBox={`${geo.box.x} ${geo.box.y} ${geo.box.w} ${geo.box.h}`} role="img" aria-label={t("Map of every app and its connections")}
          onClick={() => setSelected(undefined)}>
          <defs>
            <marker id="u-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" fill="context-stroke" />
            </marker>
          </defs>
          {view.systems.map((s) => {
            const c = geo.systems.get(s.id);
            if (!c) return null;
            const color = colorOf(s.id);
            return (
              <g key={s.id} className="u-system">
                <circle cx={c.x} cy={c.y} r={c.r} fill={color} fillOpacity={0.07} stroke={color} strokeOpacity={0.45} strokeDasharray={s.kind === "app" ? undefined : "5 5"} />
                <text x={c.x} y={c.y - c.r - 8} textAnchor="middle" className="u-system-title" fill={color}>{systemTitle(s)}</text>
              </g>
            );
          })}
          {view.edges.map((e) => {
            const a = geo.nodes.get(e.from), b = geo.nodes.get(e.to);
            if (!a || !b) return null;
            const from = byId.get(e.from), to = byId.get(e.to);
            const crosses = from?.system !== to?.system;
            const dim = focus && !(e.from === focus || e.to === focus);
            const cls = e.sources.includes("ingress") ? "u-edge ingress" : e.sources.includes("declared") ? "u-edge declared"
              : e.sources.includes("env") ? "u-edge env" : "u-edge traced";
            const st = edgeStyle(e);
            const d = edgePath(a, b, e.from === "internet" ? INTERNET_R : NODE_R, NODE_R, crosses);
            const lit = focus && (e.from === focus || e.to === focus);
            return (
              <g key={`${e.from}>${e.to}`} opacity={dim ? 0.1 : 1}>
                <path className={cls} d={d} markerEnd="url(#u-arrow)" style={{ strokeWidth: st.width, ...(st.color ? { stroke: st.color, strokeOpacity: 0.85 } : {}) }}>
                  <title>{edgeTitle(e, byId)}</title>
                </path>
                {lit && e.metrics?.p50Ms ? (
                  <text className="u-edge-label" x={(a.x + b.x) / 2} y={(a.y + b.y) / 2} textAnchor="middle">{Math.round(e.metrics.p50Ms)} ms</text>
                ) : null}
              </g>
            );
          })}
          {view.nodes.map((n) => {
            const p = geo.nodes.get(n.id);
            if (!p) return null;
            const dim = focus && !near.has(n.id);
            const r = n.kind === "internet" ? INTERNET_R : NODE_R;
            return (
              <g key={n.id} className={`u-node ${n.kind}${n.id === selected ? " selected" : ""}`} transform={`translate(${p.x},${p.y})`} opacity={dim ? 0.25 : 1}
                onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(undefined)}
                onClick={(ev) => { ev.stopPropagation(); setSelected(n.id === selected ? undefined : n.id); }}
                tabIndex={0} role="button" aria-label={n.name}
                onKeyDown={(ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); setSelected(n.id); } }}>
                {n.kind === "postgres" || n.kind === "redis"
                  ? <rect x={-r} y={-r} width={2 * r} height={2 * r} rx={5} stroke={healthColor(n)} />
                  : <circle r={r} stroke={n.kind === "internet" ? "var(--anil)" : healthColor(n)} strokeDasharray={n.kind === "workload" ? "3 3" : undefined} />}
                <text className="u-glyph" textAnchor="middle" dy="0.35em">{glyph(n)}</text>
                {n.kind !== "internet" && <text className="u-label" textAnchor="middle" y={r + 13}>{n.name}</text>}
              </g>
            );
          })}
        </svg>

        <aside className="universe-side card stack">
          {sel ? <NodeDetail n={sel} view={view} byId={byId} onPick={setSelected} /> : <Legend generated={data.generated} traces={data.traces} />}
        </aside>
      </div>
    </div>
  );
}

/** The systems, nodes and edges the filters leave on the map. */
function visible(u: UniverseData, showOther: boolean, showInfra: boolean) {
  const keep = new Set(u.systems.filter((s) => s.kind === "app" || (showOther && (s.kind === "other" || s.kind === "addon")) || (showInfra && s.kind === "infrastructure")).map((s) => s.id));
  let nodes = u.nodes.filter((n) => n.kind === "internet" || (n.system && keep.has(n.system)));
  const ids = new Set(nodes.map((n) => n.id));
  const edges = u.edges.filter((e) => ids.has(e.from) && ids.has(e.to));
  if (!edges.some((e) => e.from === "internet")) nodes = nodes.filter((n) => n.kind !== "internet");
  return { systems: u.systems.filter((s) => keep.has(s.id)), nodes, edges };
}

/** The letter or sign inside a node: the language, or what it is. */
function glyph(n: UniverseNode) {
  switch (n.kind) {
    case "internet": return "🌐";
    case "postgres": return "PG";
    case "redis": return "R";
    case "workload": return "⏱";
    case "external": return "↗";
  }
  const lang: Record<string, string> = { node: "JS", python: "Py", java: "J", go: "Go", static: "◇" };
  return (n.language && lang[n.language]) || "•";
}

/** "wellness/ui → wellness/api (API_URL; declared in rendimiento.yaml)" */
function edgeTitle(e: UniverseEdge, byId: Map<string, UniverseNode>) {
  const name = (id: string) => (id === "internet" ? t("Internet") : byId.get(id)?.name ?? id);
  const how = e.sources.map((s) => t(sourceLabel[s])).join(", ");
  const extra = [e.env ?? e.hosts?.join(", "), metricsText(e)].filter(Boolean).join(" · ");
  return `${name(e.from)} → ${name(e.to)}${extra ? ` · ${extra}` : ""} (${how})`;
}

/** The side panel for a picked node: what it is, its health, what it calls and what calls it. */
function NodeDetail({ n, view, byId, onPick }: { n: UniverseNode; view: ReturnType<typeof visible>; byId: Map<string, UniverseNode>; onPick: (id: string) => void }) {
  const navigate = useNavigate();
  const system = view.systems.find((s) => s.id === n.system);
  const calls = view.edges.filter((e) => e.from === n.id);
  const calledBy = view.edges.filter((e) => e.to === n.id);
  const page = system?.kind === "app" && system.app ? `/apps/${system.app}` : n.catalog ? `/services/${n.id}` : undefined;
  const list = (edges: UniverseEdge[], other: (e: UniverseEdge) => string) => (
    <ul className="u-list">
      {edges.map((e) => {
        const id = other(e);
        return (
          <li key={id}>
            <button className="link" onClick={() => onPick(id)}>{id === "internet" ? t("Internet") : byId.get(id)?.name ?? id}</button>
            <span className="small muted"> {[e.env ?? e.hosts?.join(", "), e.sources.map((s) => t(sourceLabel[s])).join(", ")].filter(Boolean).join(" · ")}</span>
            {e.metrics && <div className="small">{metricsText(e)}</div>}
          </li>
        );
      })}
    </ul>
  );
  return (
    <>
      <div>
        <strong className="mono">{n.name}</strong>
        <div className="small muted">{t(kindLabel[n.kind])}{system ? ` · ${systemTitle(system)}` : ""}{n.language ? ` · ${n.language}` : ""}</div>
      </div>
      {n.health && (
        <div className="row">
          <span className={`badge ${n.health.up ? "ok" : "bad"}`}>{n.health.up ? t("Up") : t("Down")}</span>
          {!!n.health.insideMs && <span className="small">{t("inside {ms} ms", { ms: n.health.insideMs })}</span>}
          {!!n.health.publicMs && <span className="small">{t("public {ms} ms", { ms: n.health.publicMs })}</span>}
          <span className="small muted">{timeAgo(n.health.at)}</span>
        </div>
      )}
      {n.health?.error && <div className="small" style={{ color: "var(--bad)" }}>{n.health.error}</div>}
      {n.kind !== "internet" && n.catalog && <div className="small muted">{t("{ready} of {pods} pods ready", { ready: n.ready, pods: n.pods })}</div>}
      {n.public?.map((u) => <a key={u} className="small" href={u} target="_blank" rel="noreferrer">{u}</a>)}
      {calls.length > 0 && <div><strong className="small">{t("Calls")}</strong>{list(calls, (e) => e.to)}</div>}
      {calledBy.length > 0 && <div><strong className="small">{t("Called by")}</strong>{list(calledBy, (e) => e.from)}</div>}
      {calls.length === 0 && calledBy.length === 0 && <div className="small muted">{t("No known connections.")}</div>}
      {page && <button onClick={() => navigate(page)}>{system?.kind === "app" ? t("Open app") : t("Open in Services")}</button>}
    </>
  );
}

/** What the shapes and lines mean, shown while nothing is picked. */
function Legend({ generated, traces }: { generated: string; traces: boolean }) {
  return (
    <>
      <strong>{t("How to read it")}</strong>
      <ul className="u-legend small">
        <li><svg width="22" height="16"><circle cx="11" cy="8" r="6" className="u-key" stroke="var(--ok)" /></svg>{t("A service; the ring is its latest uptime check (green up, red down, gray not checked)")}</li>
        <li><svg width="22" height="16"><rect x="5" y="2" width="12" height="12" rx="3" className="u-key" stroke="var(--muted)" /></svg>{t("A database or cache the app runs")}</li>
        <li><svg width="22" height="16"><circle cx="11" cy="8" r="6" className="u-key" stroke="var(--muted)" strokeDasharray="3 3" /></svg>{t("A job or worker that only calls others")}</li>
        <li><svg width="22" height="16"><path d="M2,8 H20" className="u-edge declared" /></svg>{t("Declared in rendimiento.yaml (needs)")}</li>
        <li><svg width="22" height="16"><path d="M2,8 H20" className="u-edge env" /></svg>{t("Found in its environment variables")}</li>
        <li><svg width="22" height="16"><path d="M2,8 H20" className="u-edge ingress" /></svg>{t("From the internet, through a public address")}</li>
        <li><svg width="22" height="16"><path d="M2,8 H20" className="u-edge traced" /></svg>{t("Seen only in traces: nobody declared it")}</li>
        <li><svg width="22" height="16"><path d="M2,8 H20" className="u-edge declared" style={{ strokeWidth: 5 }} /></svg>{t("Thicker: more calls; red: 5% or more fail; amber: the slowest 5% take half a second or more")}</li>
      </ul>
      <p className="small muted" style={{ margin: 0 }}>
        {traces
          ? t("Click a service to see what it calls and what calls it, with calls per minute, latency and errors for apps with observe.")
          : t("Click a service to see what it calls and what calls it. For latency on each line, the platform needs METRICS_URL and the OpenTelemetry collector add-on.")}
      </p>
      <p className="small muted" style={{ margin: 0 }}>{t("Updated {when}", { when: timeAgo(generated) })} · <Link to="/services">{t("Services")}</Link></p>
    </>
  );
}
