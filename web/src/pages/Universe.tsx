// The Universe page: every app from above, as a model village. Apps are
// pueblos grouped in barrios (rendimiento.yaml's barrio:, or found from
// what they call); from afar only the barrios and the big roads between
// them show, a barrio shows its pueblos and what goes in and out of them,
// and inside a pueblo every service is a house. The panel beside the map
// reads like a market board: calls per minute, a small trend, latency.
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { timeAgo, universeApi, type KeptTrace, type Universe as UniverseData, type UniverseBarrio, type UniverseEdge, type UniverseNode, type UniverseSpan, type UniverseSystem, type UniverseWindow } from "../api";
import { Maqueta, ms, rate, worst, type Engine, type MapModel, type MapRoad, type MapView, type Pick, type Status } from "../components/maqueta";
import { ErrorBox, usePoll } from "../components/ui";
import { t, tn } from "../i18n";

const windows: { id: UniverseWindow; label: string }[] = [
  { id: "15m", label: "Last 15 minutes" },
  { id: "1h", label: "Last hour" },
  { id: "24h", label: "Last 24 hours" },
];

const kindLabel: Record<UniverseNode["kind"], string> = {
  service: "Service", workload: "Job or worker", postgres: "PostgreSQL", redis: "Redis", external: "Outside the cluster", internet: "Internet",
};
const sourceLabel: Record<UniverseEdge["sources"][number], string> = {
  declared: "declared in rendimiento.yaml", env: "found in its environment", ingress: "public address", traced: "seen in traces",
};
/** The way the map is drawn, remembered on this device: flat at an angle (2.5D) or in 3D. */
type Mode = "2.5d" | "3d";
const MODE_KEY = "rendimiento.universe.mode";
function savedMode(): Mode {
  try { return localStorage.getItem(MODE_KEY) === "3d" ? "3d" : "2.5d"; } catch { return "2.5d"; }
}

const statusLabel: Record<Status, string> = { none: "Not measured", ok: "Normal", slow: "Slow", bad: "Failing" };

/** Numbers for anything on the map: its calls, how they went, and their trends. */
interface Numbers { perMinute: number; errors: number; p50?: number; p95?: number; trend?: number[]; p50Trend?: number[]; p95Trend?: number[]; status: Status }
/** A road between pueblos or barrios: the calls it is made of. */
interface Road extends Numbers { id: string; from: string; to: string; parts: UniverseEdge[] }

const edgeId = (e: UniverseEdge) => `${e.from}>${e.to}`;

/** How a measured call is doing: red with errors, amber when slow. */
function edgeStatus(e: UniverseEdge): Status {
  const m = e.metrics;
  if (!m) return "none";
  if (m.errors >= 0.05) return "bad";
  if ((m.p95Ms ?? 0) >= 500) return "slow";
  return "ok";
}

/** The numbers of several calls together: summed calls, the worst latency at each point. */
function combine(edges: UniverseEdge[]): Numbers {
  const measured = edges.filter((e) => e.metrics);
  const perMinute = measured.reduce((s, e) => s + e.metrics!.perMinute, 0);
  const sum = (k: "trend") => {
    const lists = measured.map((e) => e.metrics![k]).filter((l): l is number[] => !!l);
    return lists.length ? lists[0].map((_, i) => lists.reduce((s, l) => s + (l[i] ?? 0), 0)) : undefined;
  };
  const top = (k: "p50Trend" | "p95Trend") => {
    const lists = measured.map((e) => e.metrics![k]).filter((l): l is number[] => !!l);
    return lists.length ? lists[0].map((_, i) => Math.max(...lists.map((l) => l[i] ?? 0))) : undefined;
  };
  const max = (k: "p50Ms" | "p95Ms") => (measured.some((e) => e.metrics![k]) ? Math.max(...measured.map((e) => e.metrics![k] ?? 0)) : undefined);
  return {
    perMinute,
    errors: perMinute ? measured.reduce((s, e) => s + e.metrics!.errors * e.metrics!.perMinute, 0) / perMinute : 0,
    p50: max("p50Ms"), p95: max("p95Ms"),
    trend: sum("trend"), p50Trend: top("p50Trend"), p95Trend: top("p95Trend"),
    status: worst(edges.map(edgeStatus)),
  };
}

/** An app (or a namespace) with its numbers and the calls that touch it. */
type SystemInfo = UniverseSystem & Numbers & { nodes: UniverseNode[]; inside: UniverseEdge[]; ins: UniverseEdge[]; outs: UniverseEdge[]; slowest?: number };
/** A barrio with its apps, and the numbers of what comes into it. */
type BarrioInfo = UniverseBarrio & Numbers & { systems: SystemInfo[]; tone: number };

/** Everything the map and the panel need, made once per answer and filter. */
interface World {
  model: MapModel;
  barrios: Map<string, BarrioInfo>;
  systems: Map<string, SystemInfo>;
  nodes: Map<string, UniverseNode & Numbers>;
  edges: Map<string, UniverseEdge>;
  roads: Map<string, Road>;
  systemOf: Map<string, string>;
  fromInternet: UniverseEdge[];
}

function build(u: UniverseData, showOther: boolean, showInfra: boolean): World {
  const keep = new Set(u.systems.filter((s) => s.kind === "app" || (showOther && (s.kind === "other" || s.kind === "addon")) || (showInfra && s.kind === "infrastructure")).map((s) => s.id));
  const nodesIn = u.nodes.filter((n) => n.kind !== "internet" && n.system && keep.has(n.system));
  const systemOf = new Map(nodesIn.map((n) => [n.id, n.system!]));
  const visible = (id: string) => id === "internet" || systemOf.has(id);
  const edgeList = u.edges.filter((e) => visible(e.from) && visible(e.to));
  const edges = new Map(edgeList.map((e) => [edgeId(e), e]));

  // A service is down when its checks fail; otherwise it is as good as the calls it answers.
  const nodes = new Map<string, UniverseNode & Numbers>();
  for (const n of nodesIn) {
    const ins = edgeList.filter((e) => e.to === n.id);
    const nums = combine(ins);
    let status = nums.status;
    if (n.health && !n.health.up) status = "bad";
    else if (status === "none" && n.health?.up) status = "ok";
    nodes.set(n.id, { ...n, ...nums, status });
  }

  const systems: World["systems"] = new Map();
  for (const s of u.systems) {
    if (!keep.has(s.id)) continue;
    const members = nodesIn.filter((n) => n.system === s.id);
    if (!members.length) continue;
    const ids = new Set(members.map((n) => n.id));
    const ins = edgeList.filter((e) => ids.has(e.to) && !ids.has(e.from));
    const outs = edgeList.filter((e) => ids.has(e.from) && !ids.has(e.to));
    const inside = edgeList.filter((e) => ids.has(e.from) && ids.has(e.to));
    // An app's calls are the ones that come from outside it; an app nobody
    // calls shows what its services do.
    let nums = combine(ins);
    if (!nums.perMinute) nums = { ...combine(inside.filter((e) => nodes.get(e.to)?.kind === "service")), status: nums.status };
    const status = worst([nums.status, ...members.map((n) => nodes.get(n.id)!.status)]);
    // The slowest call that touches it, for "needs attention".
    const slowest = Math.max(0, ...[...ins, ...inside].map((e) => e.metrics?.p95Ms ?? 0)) || undefined;
    systems.set(s.id, { ...s, ...nums, status, nodes: members, inside, ins, outs, slowest });
  }

  // Roads between pueblos, and between barrios.
  const roads = new Map<string, Road>();
  const add = (from: string, to: string, e: UniverseEdge) => {
    const id = `${from}>${to}`;
    const r = roads.get(id) ?? { id, from, to, parts: [], ...combine([]) };
    r.parts.push(e);
    roads.set(id, r);
  };
  const barrioOf = new Map([...systems.values()].map((s) => [s.id, s.barrio]));
  for (const e of edgeList) {
    const fs = e.from === "internet" ? "internet" : systemOf.get(e.from)!, ts = systemOf.get(e.to)!;
    if (fs === ts) continue;
    add(fs, ts, e);
    const fb = fs === "internet" ? "internet" : barrioOf.get(fs)!, tb = barrioOf.get(ts)!;
    if (fb !== tb) add("b:" + fb, "b:" + tb, e);
  }
  for (const r of roads.values()) Object.assign(r, combine(r.parts));
  const puebloRoads: MapRoad[] = [], barrioRoads: MapRoad[] = [];
  for (const r of roads.values()) {
    const mr: MapRoad = { id: r.id, from: r.from.replace(/^b:/, ""), to: r.to.replace(/^b:/, ""), perMinute: r.perMinute, p50: r.p50, status: r.status };
    (r.id.startsWith("b:") ? barrioRoads : puebloRoads).push(mr);
  }

  const barrios: World["barrios"] = new Map();
  let tone = 0;
  for (const b of u.barrios ?? []) {
    const list = [...systems.values()].filter((s) => s.barrio === b.id).sort((a, z) => z.perMinute - a.perMinute || z.nodes.length - a.nodes.length || a.id.localeCompare(z.id));
    if (!list.length) continue;
    const ins = edgeList.filter((e) => barrioOf.get(systemOf.get(e.to)!) === b.id && (e.from === "internet" || barrioOf.get(systemOf.get(e.from)!) !== b.id));
    const nums = combine(ins);
    barrios.set(b.id, { ...b, ...nums, perMinute: nums.perMinute || list.reduce((s, x) => s + x.perMinute, 0), status: worst(list.map((s) => s.status)), systems: list, tone: b.kind === "outskirts" ? -1 : b.kind === "centre" ? 4 : tone++ });
  }
  const model: MapModel = {
    barrios: [...barrios.values()].map((b) => ({
      id: b.id, title: barrioTitle(b), tone: b.tone, status: b.status, perMinute: b.perMinute,
      pueblos: b.systems.map((s) => ({
        id: s.id, title: systemTitle(s), status: s.status, perMinute: s.perMinute, p95: s.p95,
        nodes: s.nodes.map((n) => ({ id: n.id, name: n.name, kind: n.kind, lang: n.language, status: nodes.get(n.id)!.status })),
      })),
    })),
    barrioRoads, puebloRoads,
    nodeRoads: edgeList.map((e) => ({ id: edgeId(e), from: e.from, to: e.to, perMinute: e.metrics?.perMinute ?? 0, p50: e.metrics?.p50Ms, status: edgeStatus(e) })),
    puebloOf: systemOf,
  };
  return { model, barrios, systems, nodes, edges, roads, systemOf, fromInternet: edgeList.filter((e) => e.from === "internet") };
}

/** A barrio's name: its own, or what it is. */
function barrioTitle(b: UniverseBarrio) {
  if (b.kind === "centre") return t("Town centre");
  if (b.kind === "outskirts") return t("Outskirts");
  if (b.kind === "auto") return t("{name}'s barrio", { name: b.title });
  return b.title;
}
const systemTitle = (s: UniverseSystem) => (s.id === "outside" ? t("Outside the cluster") : s.title);

/** The Universe page. */
export function Universe() {
  const [win, setWin] = useState<UniverseWindow>("1h");
  const { data, error, setData } = usePoll(() => universeApi.get(win), [win], 60000);
  const [showOther, setShowOther] = useState(true);
  const [showInfra, setShowInfra] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [sel, setSel] = useState<Pick>();
  const [search, setSearch] = useState("");
  // Where the map is (?barrio=…&pueblo=…), so a view can be shared.
  const [params, setParams] = useSearchParams();
  const barrio = params.get("barrio") ?? undefined, pueblo = params.get("pueblo") ?? undefined;
  const level = pueblo ? "pueblo" : barrio ? "barrio" : "region";

  const world = useMemo(() => (data ? build(data, showOther, showInfra) : undefined), [data, showOther, showInfra]);

  const go = (to: { barrio?: string; pueblo?: string; sel?: Pick }) => {
    const next = new URLSearchParams(params);
    for (const k of ["barrio", "pueblo"] as const) {
      if (to[k]) next.set(k, to[k]!);
      else next.delete(k);
    }
    setParams(next, { replace: false });
    setSel(to.sel);
  };
  const goApp = (id: string) => {
    const s = world?.systems.get(id);
    if (s) go({ barrio: s.barrio, sel: { kind: "pueblo", id } });
  };
  const goRef = useRef(go);
  goRef.current = go;

  // The canvas: the 2.5D maqueta, or the 3D one (loaded only when chosen, with three.js).
  const [mode, setModeState] = useState<Mode>(savedMode);
  const [no3d, setNo3d] = useState(false);
  const [loading3d, setLoading3d] = useState(false);
  const setMode = (m: Mode) => {
    setModeState(m);
    try { localStorage.setItem(MODE_KEY, m); } catch { /* kept for this visit only */ }
  };
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const maqueta = useRef<Engine>(null);
  const shown = useRef<{ model: MapModel; view: MapView }>(null);
  const onPick = useRef<(p: Pick | null, twice: boolean) => void>(() => {});
  onPick.current = (p, twice) => {
    if (!p) return setSel(undefined);
    if (level === "region") {
      if (p.kind === "barrio") return go({ barrio: p.id });
      if (p.kind === "pueblo") return goApp(p.id);
      return setSel(p);
    }
    if (level === "barrio") {
      if (p.kind === "pueblo") return twice ? go({ barrio, pueblo: p.id }) : setSel(p);
      if (p.kind === "barrio") return p.id === barrio ? setSel(undefined) : go({ barrio: p.id });
      return setSel(p);
    }
    setSel(p);
  };
  useEffect(() => {
    let gone = false, engine: Engine | undefined;
    const pick = (p: Pick | null, twice: boolean) => onPick.current(p, twice);
    const ready = (e: Engine) => {
      if (gone) return e.destroy();
      engine = e;
      maqueta.current = e;
      if (shown.current) e.set(shown.current.model, shown.current.view);
    };
    const flat = () => { setNo3d(true); setMode("2.5d"); };
    if (mode === "3d" && hostRef.current) {
      const host = hostRef.current;
      setLoading3d(true);
      import("../components/maqueta3d")
        .then(({ Maqueta3D }) => { if (!gone) ready(new Maqueta3D(host, pick)); })
        .catch(flat) // no WebGL here, or the file did not load
        .finally(() => setLoading3d(false));
    } else if (mode === "2.5d" && canvasRef.current) ready(new Maqueta(canvasRef.current, pick));
    return () => { gone = true; engine?.destroy(); maqueta.current = null; };
  }, [world !== undefined, mode]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!world) return;
    shown.current = { model: world.model, view: { level, barrio, pueblo, sel } };
    maqueta.current?.set(world.model, shown.current.view);
  }, [world, level, barrio, pueblo, sel]);

  // Esc goes up a level.
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || (e.target as HTMLElement).matches?.("input, select, textarea")) return;
      if (sel) return setSel(undefined);
      if (pueblo) return goRef.current({ barrio, sel: { kind: "pueblo", id: pueblo } });
      if (barrio) return goRef.current({});
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [sel, barrio, pueblo]);

  if (error && !data) return <ErrorBox error={error} />;
  if (!data || !world) return <div className="empty">{t("Loading…")}</div>;

  const apps = data.systems.filter((s) => s.kind === "app").length;
  const here = barrio ? world.barrios.get(barrio) : undefined;
  const town = pueblo ? world.systems.get(pueblo) : undefined;
  const hint = level === "region" ? t("Tap a barrio to come closer.")
    : level === "barrio" ? t("Tap a pueblo to see what comes in and goes out; tap it twice to go in.")
    : t("Tap a house, the well or a road. Esc to leave the pueblo.");
  const label = t("Map of every app as a village");

  return (
    <div className="stack">
      <div className="row between u-head">
        <div>
          <h1>{t("Universe")}</h1>
          <p className="muted" style={{ margin: 0 }}>
            {t("{apps} apps in {barrios} barrios, seen from the hill.", { apps, barrios: world.barrios.size })}
          </p>
        </div>
        <div className="row u-controls">
          <label className="row small"><input type="checkbox" checked={showOther} onChange={(e) => setShowOther(e.target.checked)} /> {t("Deployed another way")}</label>
          <label className="row small"><input type="checkbox" checked={showInfra} onChange={(e) => setShowInfra(e.target.checked)} /> {t("Cluster internals")}</label>
          {data.traces && (
            <label className="u-field">
              <span>{t("Period")}</span>
              <select value={win} onChange={(e) => setWin(e.target.value as UniverseWindow)}>
                {windows.map((w) => <option key={w.id} value={w.id}>{t(w.label)}</option>)}
              </select>
            </label>
          )}
          <label className="u-field">
            <span>{t("Go to an app")}</span>
            <input list="u-apps" value={search} placeholder={t("Name")} onChange={(e) => {
              const v = e.target.value;
              setSearch(v);
              const s = [...world.systems.values()].find((x) => x.id === v.trim().toLowerCase());
              if (s) { goApp(s.id); setSearch(""); }
            }} />
            <datalist id="u-apps">{[...world.systems.keys()].sort().map((id) => <option key={id} value={id} />)}</datalist>
          </label>
          <button disabled={refreshing} onClick={() => { setRefreshing(true); universeApi.get(win, true).then(setData).finally(() => setRefreshing(false)); }}>
            {refreshing ? t("Refreshing…") : t("Refresh")}
          </button>
        </div>
      </div>

      {data.metricsError && <div className="alert small">{t("Latency could not be read from the metrics store: {error}", { error: data.metricsError })}</div>}

      <nav className="u-crumbs" aria-label={t("Where you are")}>
        {level === "region" ? <span>{t("Region")}</span> : <button className="link" onClick={() => go({})}>{t("Region")}</button>}
        {here && <><span className="muted">›</span>{level === "barrio" && !sel ? <span>{barrioTitle(here)}</span> : <button className="link" onClick={() => go({ barrio })}>{barrioTitle(here)}</button>}</>}
        {town && <><span className="muted">›</span>{sel ? <button className="link" onClick={() => setSel(undefined)}>{systemTitle(town)}</button> : <span>{systemTitle(town)}</span>}</>}
      </nav>

      <div className="universe">
        <div className="u-stage">
          {mode === "3d"
            ? <div ref={hostRef} key="3d" className="u-canvas u-host" role="img" aria-label={label} />
            : <canvas ref={canvasRef} key="2.5d" className="u-canvas" role="img" aria-label={label} />}
          <div className="u-mode" role="group" aria-label={t("How the map is drawn")}>
            <button aria-pressed={mode === "2.5d"} onClick={() => setMode("2.5d")} title={t("Flat, seen at an angle")}>2.5D</button>
            <button aria-pressed={mode === "3d"} onClick={() => { setNo3d(false); setMode("3d"); }} title={t("In 3D: turn it and tilt it")}>3D</button>
          </div>
          <div className="u-zoom" role="group" aria-label={t("Zoom")}>
            <button onClick={() => maqueta.current?.zoom(1.3)} title={t("Zoom in")} aria-label={t("Zoom in")}>+</button>
            <button onClick={() => maqueta.current?.zoom(1 / 1.3)} title={t("Zoom out")} aria-label={t("Zoom out")}>−</button>
            <button onClick={() => maqueta.current?.fit()} title={t("Fit")} aria-label={t("Fit")}>⤢</button>
            {mode === "3d" && <>
              <button onClick={() => maqueta.current?.turn?.(-45)} title={t("Turn left")} aria-label={t("Turn left")}>⟲</button>
              <button onClick={() => maqueta.current?.turn?.(45)} title={t("Turn right")} aria-label={t("Turn right")}>⟳</button>
            </>}
          </div>
          {loading3d && <div className="u-loading small">{t("Building the village in 3D…")}</div>}
          <div className="u-hint small"><span>{no3d ? t("3D needs WebGL, which this browser does not have; the map stays in 2.5D.") : hint}</span></div>
        </div>
        <aside className="universe-side card stack" aria-live="polite">
          <Panel world={world} data={data} win={win} level={level} barrio={here} town={town} sel={sel} setSel={setSel} go={go} goApp={goApp} />
        </aside>
      </div>

      <details className="card u-legend">
        <summary><strong>{t("How to read it")}</strong></summary>
        <ul className="small">
          <li>{t("Barrio: the apps of one team or domain (barrio: in rendimiento.yaml), or grouped by what they call. From afar you see the barrios and the big roads between them.")}</li>
          <li>{t("Pueblo: an app. More houses, more services; the ring on the ground is its health (green up, red down or failing, amber slow, gray not checked).")}</li>
          <li>{t("Road: a connection. Wider, more calls. Fireflies go from the caller to the callee; the faster they move, the lower the latency. A dashed footpath is a connection nobody measured.")}</li>
          <li>{t("Red: 5% or more of the calls fail; amber: the slowest 5% take half a second or more.")}</li>
          <li>{t("Inside a pueblo: houses are services, the well is the database and the jar the cache. Calls from outside come in through the arch; calls to other apps leave by the signposts.")}</li>
          <li>{t("The laurels, benches, fountain, flower beds, the presidencia with its clock (it tells the real time) and the people strolling round the plaza are there for life: they mean nothing.")}</li>
          <li>{t("Scroll or pinch to zoom, drag to move, Esc to go up a level.")}</li>
          <li>{t("2.5D or 3D, at the map's top left: the same village. In 3D, drag with the right button or two fingers to turn it and tilt it, or use ⟲ ⟳; the choice is kept on this device.")}</li>
        </ul>
        {!data.traces && <p className="small muted">{t("For latency on each road, the platform needs METRICS_URL and the OpenTelemetry collector add-on.")}</p>}
        <p className="small muted" style={{ margin: 0 }}>{t("Updated {when}", { when: timeAgo(data.generated) })} · <Link to="/services">{t("Services")}</Link></p>
      </details>
    </div>
  );
}

// ---- the panel ----

type Go = (to: { barrio?: string; pueblo?: string; sel?: Pick }) => void;

function Panel({ world, data, win, level, barrio, town, sel, setSel, go, goApp }: {
  world: World; data: UniverseData; win: UniverseWindow; level: string; barrio?: BarrioInfo; town?: SystemInfo;
  sel?: Pick; setSel: (p?: Pick) => void; go: Go; goApp: (id: string) => void;
}) {
  if (sel?.kind === "pueblo" && world.systems.has(sel.id)) return <SystemPanel s={world.systems.get(sel.id)!} world={world} enter={() => go({ barrio: world.systems.get(sel.id)!.barrio, pueblo: sel.id })} goApp={goApp} />;
  if (sel?.kind === "node" && world.nodes.has(sel.id)) return <NodePanel n={world.nodes.get(sel.id)!} world={world} setSel={setSel} traces={data.traces} win={win} />;
  if (sel?.kind === "road" && world.edges.has(sel.id)) return <EdgePanel e={world.edges.get(sel.id)!} world={world} goApp={goApp} />;
  if (sel?.kind === "road" && world.roads.has(sel.id)) return <RoadPanel r={world.roads.get(sel.id)!} world={world} setSel={setSel} goApp={goApp} />;
  if (sel?.kind === "sign" && town) {
    const outs = town.outs.filter((e) => (e.to === "internet" ? "internet" : world.systemOf.get(e.to)) === sel.id);
    return <EdgeList title={t("Calls to {name}", { name: sel.id })} edges={outs} world={world} side="out" setSel={setSel} after={sel.id !== "internet" && <button onClick={() => goApp(sel.id)}>{t("Go to {name}", { name: sel.id })}</button>} />;
  }
  if (sel?.kind === "arch" && town) return <EdgeList title={t("What comes into {name}", { name: systemTitle(town) })} edges={town.ins} world={world} side="in" setSel={setSel} empty={t("Nobody calls it: it only works outward.")} />;
  if (sel?.kind === "internet") return <EdgeList title={t("Internet")} lead={t("What comes through the public addresses.")} edges={world.fromInternet} world={world} side="out" setSel={setSel} />;
  if (sel?.kind === "barrio" && world.barrios.has(sel.id)) return <BarrioPanel b={world.barrios.get(sel.id)!} goApp={goApp} />;
  if (level === "pueblo" && town) return <SystemPanel s={town} world={world} inside goApp={goApp} setSel={setSel} />;
  if (level === "barrio" && barrio) return <BarrioPanel b={barrio} goApp={goApp} />;
  return <RegionPanel world={world} data={data} go={go} goApp={goApp} />;
}

function Figures({ list }: { list: [string, string, Status?][] }) {
  return <div className="u-figures">{list.map(([k, v, s]) => <div key={k}><small>{k}</small><b className={s && s !== "none" ? `u-${s}` : ""}>{v}</b></div>)}</div>;
}
const pct = (f: number) => `${Math.round(f * 1000) / 10}%`;
const figures = (n: Numbers): [string, string, Status?][] => [
  [t("Requests"), n.perMinute ? rate(n.perMinute) : "—"],
  [t("Failing"), n.perMinute ? pct(n.errors) : "—", n.errors >= 0.05 ? "bad" : undefined],
  ["p50", n.p50 ? ms(n.p50) : "—"],
  ["p95", n.p95 ? ms(n.p95) : "—", n.p95 && n.p95 >= 500 ? "slow" : undefined],
];
const Badge = ({ s }: { s: Status }) => <span className={`u-status u-${s}`}>{t(statusLabel[s])}</span>;

/** A tiny chart with an area, for the ticker rows. */
function Spark({ values, status }: { values?: number[]; status: Status }) {
  if (!values || values.length < 2 || !values.some((v) => v > 0)) return <span className="u-spark" />;
  const w = 76, h = 26, max = Math.max(...values) * 1.1 || 1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, h - 2 - (v / max) * (h - 4)]);
  const d = pts.map((p, i) => `${i ? "L" : "M"}${p[0].toFixed(1)} ${p[1].toFixed(1)}`).join("");
  const last = pts[pts.length - 1];
  return (
    <svg className={`u-spark u-${status}`} viewBox={`0 0 ${w} ${h}`} width={w} height={h} aria-hidden="true">
      <path d={`${d}L${w} ${h}L0 ${h}Z`} className="area" />
      <path d={d} className="line" />
      <circle cx={last[0]} cy={last[1]} r="2.4" />
    </svg>
  );
}

/** The change across the period: the last quarter against the first. */
function Change({ values }: { values?: number[] }) {
  if (!values || values.length < 8) return null;
  const q = Math.floor(values.length / 4), avg = (l: number[]) => l.reduce((s, v) => s + v, 0) / l.length;
  const a = avg(values.slice(0, q)), b = avg(values.slice(-q));
  if (!a) return null;
  const d = ((b - a) / a) * 100;
  return <small className={d >= 0 ? "u-up" : "u-down"}>{d >= 0 ? "▲" : "▼"} {Math.abs(Math.round(d * 10) / 10)}%</small>;
}

function Tick({ name, sub, n, onClick }: { name: string; sub: string; n: Numbers; onClick: () => void }) {
  return (
    <button className="u-tick" type="button" onClick={onClick}>
      <span className={`u-dot u-${n.status}`} />
      <span className="u-tn">{name}<small>{sub}</small></span>
      <Spark values={n.trend} status={n.status} />
      <span className="u-tv">{n.perMinute ? rate(n.perMinute) : "—"}<Change values={n.trend} /></span>
    </button>
  );
}

/** Latency over the period: p95 solid, p50 dashed. */
function Latency({ n }: { n: Numbers }) {
  const p95 = n.p95Trend, p50 = n.p50Trend;
  if (!p95 || !p95.some((v) => v > 0)) return <p className="small muted" style={{ margin: 0 }}>{t("No measured calls in this period.")}</p>;
  const w = 320, h = 140, l = 48, r = 8, top = 10, b = 20, max = Math.max(...p95) * 1.15 || 1;
  const X = (i: number) => l + (i / (p95.length - 1)) * (w - l - r), Y = (v: number) => top + (1 - v / max) * (h - top - b);
  const line = (vals: number[]) => vals.map((v, i) => `${i ? "L" : "M"}${X(i).toFixed(1)} ${Y(v).toFixed(1)}`).join("");
  return (
    <>
      <svg className={`u-chart u-${n.status}`} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={t("Latency")}>
        {[0, 0.5, 1].map((f) => (
          <g key={f}><line x1={l} x2={w - r} y1={Y(max * f)} y2={Y(max * f)} className="grid" /><text x={l - 6} y={Y(max * f) + 4} textAnchor="end">{ms(max * f)}</text></g>
        ))}
        <path d={`${line(p95)}L${X(p95.length - 1)} ${Y(0)}L${X(0)} ${Y(0)}Z`} className="area" />
        <path d={line(p95)} className="p95" />
        {p50 && <path d={line(p50)} className="p50" />}
        <text x={l} y={h - 5}>{t("start of the period")}</text>
        <text x={w - r} y={h - 5} textAnchor="end">{t("now")}</text>
      </svg>
      <p className="small muted" style={{ margin: 0 }}>{t("Solid line: p95. Dashed: p50.")}</p>
    </>
  );
}

/** What comes into an app on the left and what leaves on the right, as ribbons. */
function Sankey({ s, world, goApp }: { s: SystemInfo; world: World; goApp: (id: string) => void }) {
  const group = (edges: UniverseEdge[], other: (e: UniverseEdge) => string) => {
    const m = new Map<string, UniverseEdge[]>();
    for (const e of edges) m.set(other(e), [...(m.get(other(e)) ?? []), e]);
    return [...m.entries()].map(([id, es]) => ({ id, ...combine(es) })).sort((a, b) => b.perMinute - a.perMinute || a.id.localeCompare(b.id));
  };
  const sys = (id: string) => (id === "internet" ? "internet" : world.systemOf.get(id) ?? id);
  const ins = group(s.ins, (e) => sys(e.from)), outs = group(s.outs, (e) => sys(e.to));
  if (!ins.length && !outs.length) return <p className="small muted" style={{ margin: 0 }}>{t("No known connections.")}</p>;
  const w = 320, rows = Math.max(ins.length, outs.length, 1), H = Math.max(110, rows * 34 + 10);
  // Unmeasured connections still get a thin ribbon.
  const weight = (x: Numbers) => x.perMinute || 0;
  const total = Math.max(ins.reduce((a, x) => a + weight(x), 0), outs.reduce((a, x) => a + weight(x), 0), 1);
  const k = (H * 0.55) / total, thick = (x: Numbers) => Math.max(3, weight(x) * k);
  const xL = 96, xC = 156, xR = 216, cw = 8;
  const cIn = ins.reduce((a, x) => a + thick(x), 0), cOut = outs.reduce((a, x) => a + thick(x), 0), cH = Math.max(cIn, cOut, 20), cy0 = (H - cH) / 2;
  const band = (x1: number, y1: number, x2: number, y2: number, th: number) => {
    const mx = (x1 + x2) / 2;
    return `M${x1} ${y1}C${mx} ${y1} ${mx} ${y2} ${x2} ${y2}L${x2} ${y2 + th}C${mx} ${y2 + th} ${mx} ${y1 + th} ${x1} ${y1 + th}Z`;
  };
  const name = (id: string) => (id === "internet" ? t("Internet") : id);
  const side = (list: typeof ins, left: boolean) => {
    const step = (H - 12) / Math.max(1, list.length);
    let y = 6, cy = cy0 + (cH - (left ? cIn : cOut)) / 2;
    return list.map((x) => {
      const th = thick(x), ny = y + (step - th) / 2, c = cy;
      y += step; cy += th;
      const go = () => x.id !== "internet" && goApp(x.id);
      return (
        <g key={(left ? "in:" : "out:") + x.id} className={`u-band u-${x.status}`} onClick={go}>
          <path d={left ? band(xL + 6, ny, xC, c, th) : band(xC + cw, c, xR, ny, th)} />
          <rect x={left ? xL : xR} y={ny} width={6} height={th} rx={2} />
          <text x={left ? xL - 6 : xR + 12} y={ny + th / 2} textAnchor={left ? "end" : "start"} dominantBaseline="middle">
            {name(x.id)}
            <tspan x={left ? xL - 6 : xR + 12} dy="12" className="num">{x.perMinute ? rate(x.perMinute) : t("not measured")}</tspan>
          </text>
        </g>
      );
    });
  };
  return (
    <svg className="u-sankey" viewBox={`0 0 ${w} ${H}`} role="img" aria-label={t("What comes in and what goes out")}>
      {side(ins, true)}
      {side(outs, false)}
      <rect x={xC} y={cy0} width={cw} height={cH} rx={3} className="center" />
      {!ins.length && <text x={xL} y={H / 2} textAnchor="end" className="num">{t("nobody comes in")}</text>}
      {!outs.length && <text x={xR} y={H / 2} className="num">{t("calls no other app")}</text>}
    </svg>
  );
}

function RegionPanel({ world, data, go, goApp }: { world: World; data: UniverseData; go: Go; goApp: (id: string) => void }) {
  const fromNet = combine(world.fromInternet);
  const attention = [...world.systems.values()].filter((s) => s.status === "slow" || s.status === "bad").sort((a, b) => (a.status === "bad" ? -1 : 1) - (b.status === "bad" ? -1 : 1));
  return (
    <>
      <div><h2 className="u-title">{t("The whole region")}</h2><p className="small muted" style={{ margin: 0 }}>{t("Tap a barrio to come closer, or a road to see what goes between them.")}</p></div>
      <Figures list={[[t("From the internet"), fromNet.perMinute ? rate(fromNet.perMinute) : "—"], [t("Failing"), fromNet.perMinute ? pct(fromNet.errors) : "—", fromNet.errors >= 0.05 ? "bad" : undefined], [t("Pueblos"), String(world.systems.size)], [t("Connections"), String(data.edges.length)]]} />
      <Attention list={attention} goApp={goApp} />
      <div>
        <h3 className="u-h">{t("Barrios")}</h3>
        <div className="u-ticks">
          {[...world.barrios.values()].map((b) => <Tick key={b.id} name={barrioTitle(b)} sub={barrioKind(b)} n={b} onClick={() => go({ barrio: b.id })} />)}
        </div>
      </div>
      <p className="small muted" style={{ margin: 0 }}>{t("The change (▲▼) compares the end of the period with its start.")}</p>
    </>
  );
}

function barrioKind(b: BarrioInfo) {
  const n = t("{n} apps", { n: b.systems.length });
  if (b.kind === "centre") return `${n} · ${t("call no other app")}`;
  if (b.kind === "outskirts") return `${n} · ${t("not deployed by rendimiento")}`;
  if (b.kind === "auto") return `${n} · ${t("grouped by what they call")}`;
  return n;
}

function Attention({ list, goApp }: { list: SystemInfo[]; goApp: (id: string) => void }) {
  return (
    <div>
      <h3 className="u-h">{t("Needs attention")}</h3>
      {list.length ? (
        <div className="row u-chips">
          {list.map((s) => (
            <button key={s.id} className={`u-chip u-${s.status}`} onClick={() => goApp(s.id)}>
              {systemTitle(s)} · {s.status === "bad" ? (s.errors >= 0.05 ? t("{pct} failing", { pct: pct(s.errors) }) : t("Down")) : `p95 ${ms(s.slowest ?? s.p95 ?? 0)}`}
            </button>
          ))}
        </div>
      ) : <p className="small muted" style={{ margin: 0 }}>{t("Nothing needs attention in this period.")}</p>}
    </div>
  );
}

function BarrioPanel({ b, goApp }: { b: BarrioInfo; goApp: (id: string) => void }) {
  return (
    <>
      <div><h2 className="u-title">{barrioTitle(b)}</h2><p className="small muted" style={{ margin: 0 }}>{barrioKind(b)}. {t("Tap a pueblo to see what comes in and goes out; tap it twice to go in.")}</p></div>
      <Figures list={[[t("Coming in from outside"), b.perMinute ? rate(b.perMinute) : "—"], [t("Failing"), b.perMinute ? pct(b.errors) : "—", b.errors >= 0.05 ? "bad" : undefined], ["p50", b.p50 ? ms(b.p50) : "—"], [t("Slowest p95"), b.p95 ? ms(b.p95) : "—", b.p95 && b.p95 >= 500 ? "slow" : undefined]]} />
      <Attention list={b.systems.filter((s) => s.status === "slow" || s.status === "bad")} goApp={goApp} />
      <div>
        <h3 className="u-h">{t("Pueblos")}</h3>
        <div className="u-ticks">
          {b.systems.map((s) => <Tick key={s.id} name={systemTitle(s)} sub={t("{n} services", { n: s.nodes.length })} n={s} onClick={() => goApp(s.id)} />)}
        </div>
      </div>
    </>
  );
}

function SystemPanel({ s, world, inside, enter, goApp, setSel }: {
  s: SystemInfo; world: World; inside?: boolean; enter?: () => void; goApp: (id: string) => void; setSel?: (p?: Pick) => void;
}) {
  const page = s.kind === "app" && s.app ? `/apps/${s.app}` : undefined;
  return (
    <>
      <div>
        <h3 className="u-h">{t(s.kind === "app" ? "App" : "Deployed another way")}</h3>
        <h2 className="u-title">{systemTitle(s)}</h2>
        <Badge s={s.status} />
      </div>
      <Figures list={figures(s)} />
      <div className="row">
        {!inside && enter && <button className="primary" onClick={enter}>{t("Go into the pueblo")}</button>}
        {page && <Link className="button" to={page}>{t("Open app")}</Link>}
      </div>
      <div><h3 className="u-h">{t("What comes in and what goes out")}</h3><Sankey s={s} world={world} goApp={goApp} /></div>
      {inside && setSel && (
        <div>
          <h3 className="u-h">{t("Services")}</h3>
          <div className="u-ticks">
            {s.nodes.map((n) => { const nn = world.nodes.get(n.id)!; return <Tick key={n.id} name={n.name} sub={t(kindLabel[n.kind])} n={nn} onClick={() => setSel({ kind: "node", id: n.id })} />; })}
          </div>
        </div>
      )}
      <div><h3 className="u-h">{t("Latency of what comes in")}</h3><Latency n={s} /></div>
    </>
  );
}

function NodePanel({ n, world, setSel, traces, win }: { n: UniverseNode & Numbers; world: World; setSel: (p?: Pick) => void; traces: boolean; win: UniverseWindow }) {
  const calls = [...world.edges.values()].filter((e) => e.from === n.id), by = [...world.edges.values()].filter((e) => e.to === n.id);
  return (
    <>
      <div>
        <h3 className="u-h">{t(kindLabel[n.kind])}{n.system ? ` · ${n.system}` : ""}{n.language ? ` · ${n.language}` : ""}</h3>
        <h2 className="u-title">{n.name}</h2>
        <Badge s={n.status} />
      </div>
      {n.health && (
        <div className="row small">
          <span className={`badge ${n.health.up ? "ok" : "bad"}`}>{n.health.up ? t("Up") : t("Down")}</span>
          {!!n.health.insideMs && <span>{t("inside {ms} ms", { ms: n.health.insideMs })}</span>}
          {!!n.health.publicMs && <span>{t("public {ms} ms", { ms: n.health.publicMs })}</span>}
          <span className="muted">{timeAgo(n.health.at)}</span>
        </div>
      )}
      {n.health?.error && <div className="small" style={{ color: "var(--bad)" }}>{n.health.error}</div>}
      <Figures list={figures(n)} />
      {n.catalog && <div className="small muted">{t("{ready} of {pods} pods ready", { ready: n.ready, pods: n.pods })}</div>}
      {n.public?.map((u) => <a key={u} className="small" href={u} target="_blank" rel="noreferrer">{u}</a>)}
      <div><h3 className="u-h">{t("Latency")}</h3><Latency n={n} /></div>
      {traces && (n.kind === "service" || n.kind === "workload") && <Spans service={n.id} win={win} />}
      {traces && (n.kind === "service" || n.kind === "workload") && <Requests service={n.id} />}
      {by.length > 0 && <EdgeRows title={t("Called by")} edges={by} world={world} side="in" setSel={setSel} />}
      {calls.length > 0 && <EdgeRows title={t("Calls")} edges={calls} world={world} side="out" setSel={setSel} />}
      {n.catalog && <Link className="small" to={`/services/${n.id}`}>{t("Open in Services")}</Link>}
    </>
  );
}

const spanStatus = (s: UniverseSpan): Status => (s.errors >= 0.05 ? "bad" : (s.p95Ms ?? 0) >= 500 ? "slow" : "ok");
const spanGroups: { kinds: UniverseSpan["kind"][]; title: string; empty?: string }[] = [
  { kinds: ["internal"], title: "Its own moments", empty: "No moments of its own yet: mark them with tramo() from the observa libraries." },
  { kinds: ["server"], title: "Routes it serves" },
  { kinds: ["client", "producer", "consumer"], title: "Calls and queries it makes" },
];

/**
 * What a service does, from its traces: its own moments (the observa
 * libraries' tramos), the routes it serves and the calls it makes. A row
 * opens its latency over the period.
 */
function Spans({ service, win }: { service: string; win: UniverseWindow }) {
  const [spans, setSpans] = useState<UniverseSpan[]>();
  const [failed, setFailed] = useState<string>();
  const [open, setOpen] = useState<string>();
  const [all, setAll] = useState<Record<string, boolean>>({});
  useEffect(() => {
    let live = true;
    setSpans(undefined); setFailed(undefined); setOpen(undefined); setAll({});
    universeApi.spans(service, win)
      .then((r) => { if (!live) return; setSpans(r.spans); if (r.metricsError) setFailed(r.metricsError); })
      .catch((e: Error) => live && setFailed(e.message));
    return () => { live = false; };
  }, [service, win]);
  if (failed && !spans?.length) return <p className="small muted" style={{ margin: 0 }}>{t("Latency could not be read from the metrics store: {error}", { error: failed })}</p>;
  if (!spans) return <p className="small muted" style={{ margin: 0 }}>{t("Loading…")}</p>;
  return (
    <>
      {spanGroups.map((g) => {
        const list = spans.filter((s) => g.kinds.includes(s.kind));
        if (!list.length && !g.empty) return null;
        const shown = all[g.title] ? list : list.slice(0, 6);
        return (
          <div key={g.title}>
            <h3 className="u-h">{t(g.title)}</h3>
            {!list.length && <p className="small muted" style={{ margin: 0 }}>{g.empty && t(g.empty)}</p>}
            <div className="u-ticks">
              {shown.map((s) => {
                const k = s.kind + ":" + s.name, nums: Numbers = { perMinute: s.perMinute, errors: s.errors, p50: s.p50Ms, p95: s.p95Ms, trend: s.trend, p50Trend: s.p50Trend, p95Trend: s.p95Trend, status: spanStatus(s) };
                return (
                  <div key={k}>
                    <Tick name={s.name} sub={[s.p95Ms ? `p95 ${ms(s.p95Ms)}` : "", s.errors > 0 ? t("{pct} failing", { pct: pct(s.errors) }) : ""].filter(Boolean).join(" · ")} n={nums} onClick={() => setOpen(open === k ? undefined : k)} />
                    {open === k && <div className="u-open"><Latency n={nums} /></div>}
                  </div>
                );
              })}
            </div>
            {list.length > shown.length && <button className="link small" onClick={() => setAll({ ...all, [g.title]: true })}>{t("Show all {n}", { n: list.length })}</button>}
          </div>
        );
      })}
    </>
  );
}

export const reasonLabel: Record<KeptTrace["reason"], string> = { error: "it failed", slow: "it was slow", sample: "in the sample" };

/**
 * The requests the traces service kept that went through a service, newest
 * first; each opens its waterfall.
 */
function Requests({ service }: { service: string }) {
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [list, setList] = useState<KeptTrace[]>();
  const [failed, setFailed] = useState<string>();
  useEffect(() => {
    let live = true;
    setList(undefined); setFailed(undefined);
    universeApi.traces(service, errorsOnly)
      .then((r) => live && setList(r.traces))
      .catch((e: Error) => live && setFailed(e.message));
    return () => { live = false; };
  }, [service, errorsOnly]);
  return (
    <div>
      <h3 className="u-h">{t("Recent requests")}</h3>
      <label className="small muted"><input type="checkbox" checked={errorsOnly} onChange={(e) => setErrorsOnly(e.target.checked)} /> {t("Only the failed ones")}</label>
      {failed && <p className="small muted" style={{ margin: 0 }}>{failed}</p>}
      {!list && !failed && <p className="small muted" style={{ margin: 0 }}>{t("Loading…")}</p>}
      {list && !list.length && <p className="small muted" style={{ margin: 0 }}>{t("None kept yet. The traces service keeps every request that fails or is slow, and one in ten of the rest, for a week.")}</p>}
      <div className="u-reqs">
        {list?.slice(0, 12).map((r) => (
          <Link key={r.id} to={`/universe/requests/${r.id}`} className="u-req">
            <span className={`badge ${r.error ? "bad" : r.reason === "slow" ? "warn" : "ok"}`}>{t(reasonLabel[r.reason])}</span>
            <span className="u-req-name">{r.name}</span>
            <span className="small muted">{ms(r.durationUs / 1000)} · {timeAgo(r.startedAt)}</span>
          </Link>
        ))}
      </div>
    </div>
  );
}

function nodeName(id: string, world: World) {
  if (id === "internet") return t("Internet");
  const n = world.nodes.get(id);
  return n?.system ? `${n.system}/${n.name}` : n?.name ?? id;
}

function EdgeRows({ title, edges, world, side, setSel }: { title: string; edges: UniverseEdge[]; world: World; side: "in" | "out" | "both"; setSel: (p?: Pick) => void }) {
  return (
    <div>
      <h3 className="u-h">{title}</h3>
      <div className="u-ticks">
        {edges.map((e) => {
          const nums = combine([e]);
          const name = side === "both" ? `${nodeName(e.from, world)} → ${nodeName(e.to, world)}` : nodeName(side === "in" ? e.from : e.to, world);
          return <Tick key={edgeId(e)} name={name} sub={nums.p95 ? `p95 ${ms(nums.p95)}` : e.sources.map((s) => t(sourceLabel[s])).join(", ")} n={nums} onClick={() => setSel({ kind: "road", id: edgeId(e) })} />;
        })}
      </div>
    </div>
  );
}

function EdgeList({ title, lead, edges, world, side, setSel, empty, after }: {
  title: string; lead?: string; edges: UniverseEdge[]; world: World; side: "in" | "out"; setSel: (p?: Pick) => void; empty?: string; after?: React.ReactNode;
}) {
  const nums = combine(edges);
  return (
    <>
      <div><h2 className="u-title">{title}</h2>{lead && <p className="small muted" style={{ margin: 0 }}>{lead}</p>}</div>
      {edges.length ? <><Figures list={figures(nums)} /><EdgeRows title={t("Connections")} edges={edges} world={world} side={side} setSel={setSel} /></> : <p className="small muted">{empty}</p>}
      {after && <div className="row">{after}</div>}
    </>
  );
}

function EdgePanel({ e, world, goApp }: { e: UniverseEdge; world: World; goApp: (id: string) => void }) {
  const nums = combine([e]);
  const fs = e.from === "internet" ? undefined : world.systemOf.get(e.from), ts = world.systemOf.get(e.to);
  return (
    <>
      <div>
        <h3 className="u-h">{t("Road")}</h3>
        <h2 className="u-title">{nodeName(e.from, world)} → {nodeName(e.to, world)}</h2>
        <Badge s={nums.status} />
      </div>
      <Figures list={figures(nums)} />
      <div className="small">
        <div><span className="muted">{t("How it is known")}:</span> {e.sources.map((s) => t(sourceLabel[s])).join(", ")}</div>
        {e.env && <div><span className="muted">{t("Address in")}</span> <code>{e.env}</code></div>}
        {e.hosts?.map((h) => <div key={h}><a href={h} target="_blank" rel="noreferrer">{h}</a></div>)}
      </div>
      <div><h3 className="u-h">{t("Latency")}</h3><Latency n={nums} /></div>
      <div className="row">
        {fs && fs !== ts && <button onClick={() => goApp(fs)}>{t("Go to {name}", { name: fs })}</button>}
        {ts && <button onClick={() => goApp(ts)}>{t("Go to {name}", { name: ts })}</button>}
      </div>
    </>
  );
}

function RoadPanel({ r, world, setSel, goApp }: { r: Road; world: World; setSel: (p?: Pick) => void; goApp: (id: string) => void }) {
  const between = r.id.startsWith("b:");
  const name = (id: string) => {
    const k = id.replace(/^b:/, "");
    if (k === "internet") return t("Internet");
    const b = world.barrios.get(k);
    return between && b ? barrioTitle(b) : k;
  };
  return (
    <>
      <div>
        <h3 className="u-h">{between ? t("Road between barrios") : t("Road")}</h3>
        <h2 className="u-title">{name(r.from)} → {name(r.to)}</h2>
        <Badge s={r.status} />
      </div>
      <Figures list={figures(r)} />
      <div><h3 className="u-h">{t("Latency")}</h3><Latency n={r} /></div>
      <EdgeRows title={tn(r.parts.length, "Made of {n} connection", "Made of {n} connections")} edges={r.parts} world={world} side="both" setSel={setSel} />
      {!between && (
        <div className="row">
          {r.from !== "internet" && <button onClick={() => goApp(r.from)}>{t("Go to {name}", { name: r.from })}</button>}
          <button onClick={() => goApp(r.to)}>{t("Go to {name}", { name: r.to })}</button>
        </div>
      )}
    </>
  );
}
