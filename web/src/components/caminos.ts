// The Caminos tab's canvas: an app's last two weeks as roads. The default
// branch is the camino real, running left to right through time; every other
// branch is a vereda that leaves it at the commit it started from (or leaves
// its parent vereda, when stacked) and comes back where it was merged. Each
// run is a stop with a huacal of maize: whole if it passed, broken with the
// maize spilled if it failed, grey if cancelled. Releases are market stalls.
// "Contar la historia" walks the two weeks: a burro with its load goes down
// every vereda, stumbles where a run failed, and the caravan reaches the
// market with each release.

import type { Caminos, CaminosBranch, CaminosRun } from "../api";
import { locale, t } from "../i18n";
import { clamp, hash, isNight, palette, still, type Palette } from "./maqueta";

export type CaminosSel = { kind: "branch"; name: string } | { kind: "run"; id: number };
export type PlayState = "stopped" | "playing" | "paused";

/** A run placed on the map. */
export interface Stop {
  r: CaminosRun;
  b?: Road; // undefined on the camino real
  at: number; // seconds
  end: number;
  x: number;
  y: number;
}
/** A branch placed on the map. */
export interface Road {
  b: CaminosBranch;
  parent?: Road;
  start: number; // seconds: where it leaves
  merged?: number; // seconds: where it rejoins
  x0: number;
  x1: number;
  y: number; // its lane
  fromY: number; // the y it leaves from (0, or its parent's lane)
  stops: Stop[];
}

const LANE = 52, FIRST = 70, TOP = 34, BOTTOM = 26;
const EXTRA = {
  day: { ground2: "#E6D2B0", tuft: "#B9A274", path: "#D7BE9A", huacal: "#B98A55", huacalD: "#7E5A33", burro: "#8A7F78", burroD: "#5E5550", off: "#8C7B6B", sky: "#F2E3C8" },
  night: { ground2: "#241E4C", tuft: "#3A3170", path: "#3E3466", huacal: "#C49A63", huacalD: "#8A6640", burro: "#B4A9C4", burroD: "#7D7290", off: "#8E84A3", sky: "#1C1F55" },
};
type Colors = Palette & typeof EXTRA.day;

function rng(seed: number) {
  let a = seed >>> 0;
  return () => { a = (a + 0x6D2B79F5) | 0; let x = Math.imul(a ^ (a >>> 15), 1 | a); x = (x + Math.imul(x ^ (x >>> 7), 61 | x)) ^ x; return ((x ^ (x >>> 14)) >>> 0) / 4294967296; };
}
const sec = (iso?: string) => (iso ? new Date(iso).getTime() / 1000 : 0);

/** The map, laid out from a history: times become x, branches get lanes. */
export class Layout {
  roads: Road[] = [];
  byName = new Map<string, Road>();
  main: Stop[] = [];
  releases: Stop[] = [];
  all: Stop[] = [];
  times: number[] = [];
  xs: number[] = [];
  world = 0;
  maxUp = FIRST;
  maxDown = FIRST;
  tMin = 0;
  tMax = 0;

  constructor(h: Caminos) {
    const runEnd = (r: CaminosRun) => sec(r.end) || sec(r.at);
    this.roads = h.branches.map((b) => ({ b, start: 0, x0: 0, x1: 0, y: 0, fromY: 0, stops: [] }));
    for (const r of this.roads) this.byName.set(r.b.name, r);
    for (const r of this.roads) {
      r.parent = r.b.parent ? this.byName.get(r.b.parent) : undefined;
      r.start = Math.min(r.b.fork ? sec(r.b.fork.at) : Infinity, sec(r.b.runs[0].at));
      r.merged = r.b.mergedAt ? sec(r.b.mergedAt) : undefined;
    }
    const allRuns = [...h.main, ...h.branches.flatMap((b) => b.runs)];
    const stamps = [sec(h.from), sec(h.to), ...allRuns.flatMap((r) => [sec(r.at), runEnd(r)]), ...this.roads.flatMap((r) => [r.start, r.merged ?? 0])].filter((x) => x > 0 && Number.isFinite(x));
    this.tMin = Math.min(...stamps) - 3600;
    this.tMax = Math.max(...stamps) + 3600;
    // Every moment gets room; long quiet stretches are shortened.
    this.times = [...new Set([this.tMin, this.tMax, ...stamps])].sort((a, b) => a - b);
    this.xs = [0];
    for (let i = 1; i < this.times.length; i++) {
      const gap = this.times[i] - this.times[i - 1];
      this.xs.push(this.xs[i - 1] + (gap <= 0 ? 0 : 7 + 58 * (1 - Math.exp(-gap / (4 * 3600)))));
    }
    this.world = this.xs[this.xs.length - 1] + 140;

    // Lanes alternate above and below the camino real; a lane is reused once free.
    const order = [...this.roads].sort((a, b) => a.start - b.start);
    const laneEnd: number[] = [], lane = new Map<Road, number>();
    for (const r of order) {
      r.x0 = this.xOf(r.start);
      const last = r.b.runs[r.b.runs.length - 1];
      r.x1 = r.merged ? this.xOf(r.merged) : this.xOf(runEnd(last)) + 46;
      const pl = r.parent ? lane.get(r.parent) : undefined;
      let l = -1;
      for (let i = 0; i < 60 && l < 0; i++) {
        if (pl !== undefined && i % 2 !== pl % 2) continue;
        if ((laneEnd[i] ?? -Infinity) < r.x0 - 24) l = i;
      }
      if (l < 0) l = laneEnd.length;
      laneEnd[l] = r.x1 + 30;
      lane.set(r, l);
      r.y = (l % 2 === 0 ? -1 : 1) * (FIRST + Math.floor(l / 2) * LANE);
    }
    for (const r of this.roads) { if (r.y < 0) this.maxUp = Math.max(this.maxUp, -r.y); else this.maxDown = Math.max(this.maxDown, r.y); }
    // Stops, nudged apart so two runs never sit on each other.
    for (const r of order) {
      let lastX = -Infinity;
      r.stops = r.b.runs.map((run) => {
        const x = Math.max(this.xOf(runEnd(run)), lastX + 18);
        lastX = x;
        return { r: run, b: r, at: sec(run.at), end: runEnd(run), x, y: r.y };
      });
      r.x1 = r.merged ? Math.max(r.x1, lastX + 22) : Math.max(r.x1, lastX + 46);
      r.fromY = r.parent ? this.yAt(r.parent, r.x0) : 0;
    }
    this.main = h.main.map((run) => ({ r: run, at: sec(run.at), end: runEnd(run), x: this.xOf(runEnd(run)), y: 0 }));
    this.releases = this.main.filter((s) => s.r.status === "succeeded" && (s.r.release ?? 0) > 0);
    this.all = [...this.main, ...this.roads.flatMap((r) => r.stops)];
  }

  xOf(tt: number): number {
    const { times, xs } = this;
    if (tt <= times[0]) return xs[0];
    if (tt >= times[times.length - 1]) return xs[xs.length - 1];
    let lo = 0, hi = times.length - 1;
    while (hi - lo > 1) { const m = (lo + hi) >> 1; if (times[m] <= tt) lo = m; else hi = m; }
    return xs[lo] + (xs[hi] - xs[lo]) * ((tt - times[lo]) / (times[hi] - times[lo] || 1));
  }

  tOf(x: number): number {
    const { times, xs } = this;
    if (x <= xs[0]) return times[0];
    if (x >= xs[xs.length - 1]) return times[times.length - 1];
    let lo = 0, hi = xs.length - 1;
    while (hi - lo > 1) { const m = (lo + hi) >> 1; if (xs[m] <= x) lo = m; else hi = m; }
    return times[lo] + (times[hi] - times[lo]) * ((x - xs[lo]) / (xs[hi] - xs[lo] || 1));
  }

  /** The vereda's y at x: it leaves its origin, runs along its lane, and bends back where it merged. */
  yAt(r: Road, x: number): number {
    const first = r.stops[0]?.x ?? r.x0 + 40;
    const bendIn = Math.min(40, Math.max(14, first - r.x0 - 4));
    if (x <= r.x0) return r.fromY;
    if (x < r.x0 + bendIn) { const k = (x - r.x0) / bendIn, e = k * k * (3 - 2 * k); return r.fromY + (r.y - r.fromY) * e; }
    if (r.merged !== undefined) {
      const last = r.stops[r.stops.length - 1]?.x ?? r.x1 - 40;
      const bendOut = Math.min(40, Math.max(14, r.x1 - last - 4));
      const to = r.parent ? this.yAt(r.parent, r.x1) : 0;
      if (x > r.x1 - bendOut) { const k = clamp((x - (r.x1 - bendOut)) / bendOut, 0, 1), e = k * k * (3 - 2 * k); return r.y + (to - r.y) * e; }
    }
    return r.y;
  }
}

interface Fx { kind: "maiz" | "polvo" | "confeti" | "anillo"; x: number; y: number; vx?: number; vy?: number; c?: string; life: number; t: number }
type Hit = { kind: "run"; s: Stop; x: number; y: number; rad: number } | { kind: "branch"; r: Road; pts: { x: number; y: number }[] };

export interface CaminosHandlers {
  onSelect: (sel: CaminosSel | null) => void;
  onHover: (text: string) => void;
  onPlay: (state: PlayState) => void;
}

/** The canvas engine: draws a Layout, animates it, and reports picks. */
export class CaminosMap {
  L: Layout;
  private ctx: CanvasRenderingContext2D;
  private mctx: CanvasRenderingContext2D;
  private W = 0;
  private H = 0;
  private MW = 0;
  private MH = 0;
  private dpr = 1;
  private base = 1;
  private cam = { x: 0, y: 0, k: 1 };
  private tween?: { from: { x: number; k: number }; to: { x: number; k: number }; t0: number };
  private C!: Colors;
  private night = false;
  private sprites = new Map<string, HTMLCanvasElement>();
  private hits: Hit[] = [];
  private fx: Fx[] = [];
  private sel: CaminosSel | null = null;
  private hover?: Stop;
  private drag?: { x: number; y: number; cx: number; cy: number; moved: boolean };
  private play?: { pos: number; paused: boolean; fired: Set<object> };
  private speed = 180; // seconds for the whole history
  private last = performance.now();
  private frameId = 0;
  private S = 1;
  private cleanup: (() => void)[] = [];
  private tufts: { x: number; y: number; k: number }[];
  private decor: { x: number; side: number; k: number; kind: "nopal" | "maguey" }[];
  private dayFmt = new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric" });
  private whenFmt = new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

  constructor(private canvas: HTMLCanvasElement, private mini: HTMLCanvasElement, h: Caminos, private on: CaminosHandlers) {
    this.ctx = canvas.getContext("2d")!;
    this.mctx = mini.getContext("2d")!;
    this.L = new Layout(h);
    const r1 = rng(11), r2 = rng(23);
    this.tufts = Array.from({ length: 900 }, () => ({ x: r1() * this.L.world, y: r1() * 2 - 1, k: r1() }));
    this.decor = Array.from({ length: 70 }, () => ({ x: r2() * this.L.world, side: r2() < 0.5 ? -1 : 1, k: r2(), kind: r2() < 0.55 ? "nopal" : "maguey" }));
    this.readTheme();
    const obs = new MutationObserver(() => this.readTheme());
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    this.cleanup.push(() => obs.disconnect());
    const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
    const onMq = () => this.readTheme();
    mq?.addEventListener("change", onMq);
    this.cleanup.push(() => mq?.removeEventListener("change", onMq));
    const ro = new ResizeObserver(() => this.resize());
    ro.observe(canvas.parentElement!);
    ro.observe(mini);
    this.cleanup.push(() => ro.disconnect());
    this.resize();
    this.cam.k = 1;
    this.cam.y = (this.L.maxDown - this.L.maxUp) / 2;
    this.cam.x = this.L.world - this.W / this.U() + 40;
    this.clampCam();
    this.listen();
    this.frameId = requestAnimationFrame((n) => this.frame(n));
  }

  /** New data (the tab refreshes): keep the camera where it is. */
  setData(h: Caminos) {
    this.L = new Layout(h);
    this.resize();
  }

  destroy() {
    cancelAnimationFrame(this.frameId);
    for (const f of this.cleanup) f();
  }

  // ---- public controls ----

  select(sel: CaminosSel | null, fly = false) {
    this.sel = sel;
    if (!fly || !sel) return;
    if (sel.kind === "branch") { const r = this.L.byName.get(sel.name); if (r) this.goTo((r.x0 + r.x1) / 2); }
    else { const s = this.L.all.find((x) => x.r.id === sel.id); if (s) this.goTo(s.x); }
  }
  setSpeed(seconds: number) { this.speed = seconds; }
  zoom(f: number) { this.zoomAt(f, this.W / 2, (TOP + this.H - BOTTOM) / 2); }
  today() { this.goTo(this.L.world - this.W / 2 / this.U()); }
  togglePlay() {
    if (!this.play) {
      this.play = { pos: this.L.xs[0], paused: false, fired: new Set() };
      this.cam.x = this.L.xs[0] - this.W * 0.4 / this.U(); this.clampCam();
      this.on.onPlay("playing");
      return;
    }
    this.play.paused = !this.play.paused;
    this.on.onPlay(this.play.paused ? "paused" : "playing");
  }
  stop() { this.play = undefined; this.fx = []; this.on.onPlay("stopped"); }

  // ---- camera: one zoom for both axes, so the map keeps its shape ----

  private U() { return this.base * this.cam.k; }
  private X(x: number) { return (x - this.cam.x) * this.U(); }
  private Y(y: number) { return TOP + (this.H - TOP - BOTTOM) / 2 + (y - this.cam.y) * this.U(); }
  private clampCam() {
    const u = this.U(), L = this.L;
    this.cam.x = clamp(this.cam.x, -60 / u, L.world - (this.W - 60) / u);
    const half = (this.H - TOP - BOTTOM) / 2 / u, top = -L.maxUp - 40, bottom = L.maxDown + 40;
    this.cam.y = bottom - top <= half * 2 ? (L.maxDown - L.maxUp) / 2 : clamp(this.cam.y, top + half, bottom - half);
  }
  private goTo(x: number, k = this.cam.k) {
    const to = { x: x - this.W / 2 / (this.base * k), k };
    if (still()) { this.cam.x = to.x; this.cam.k = k; this.clampCam(); return; }
    this.tween = { from: { x: this.cam.x, k: this.cam.k }, to, t0: performance.now() };
  }
  private stepTween(now: number) {
    const tw = this.tween; if (!tw) return;
    const p = Math.min(1, (now - tw.t0) / 600), e = 1 - (1 - p) ** 3;
    this.cam.k = Math.exp(Math.log(tw.from.k) + (Math.log(tw.to.k) - Math.log(tw.from.k)) * e);
    this.cam.x = tw.from.x + (tw.to.x - tw.from.x) * e;
    this.clampCam();
    if (p >= 1) this.tween = undefined;
  }
  private zoomAt(f: number, sx: number, sy: number) {
    this.tween = undefined;
    const mid = TOP + (this.H - TOP - BOTTOM) / 2, u0 = this.U();
    const wx = this.cam.x + sx / u0, wy = this.cam.y + (sy - mid) / u0;
    this.cam.k = clamp(this.cam.k * f, 0.3, 5);
    const u1 = this.U();
    this.cam.x = wx - sx / u1; this.cam.y = wy - (sy - mid) / u1;
    this.clampCam();
  }
  private resize() {
    this.dpr = Math.min(2, window.devicePixelRatio || 1);
    const r = this.canvas.getBoundingClientRect();
    this.W = r.width; this.H = r.height;
    this.canvas.width = Math.round(this.W * this.dpr); this.canvas.height = Math.round(this.H * this.dpr);
    const m = this.mini.getBoundingClientRect();
    this.MW = m.width; this.MH = m.height;
    this.mini.width = Math.round(this.MW * this.dpr); this.mini.height = Math.round(this.MH * this.dpr);
    this.base = Math.min(1, (this.H - TOP - BOTTOM - 10) / (this.L.maxUp + this.L.maxDown + 60));
    this.clampCam();
  }
  private readTheme() {
    this.night = isNight();
    this.C = { ...palette(this.night), ...(this.night ? EXTRA.night : EXTRA.day) };
    this.sprites.clear();
  }

  // ---- input ----

  private listen() {
    const c = this.canvas;
    const pos = (e: PointerEvent | WheelEvent) => { const r = c.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
    const down = (e: PointerEvent) => { this.drag = { x: e.clientX, y: e.clientY, cx: this.cam.x, cy: this.cam.y, moved: false }; c.setPointerCapture(e.pointerId); };
    const move = (e: PointerEvent) => {
      const d = this.drag;
      if (d) {
        if (Math.abs(e.clientX - d.x) + Math.abs(e.clientY - d.y) > 4) d.moved = true;
        if (d.moved) { this.tween = undefined; this.cam.x = d.cx - (e.clientX - d.x) / this.U(); this.cam.y = d.cy - (e.clientY - d.y) / this.U(); this.clampCam(); c.classList.add("dragging"); }
        return;
      }
      const p = pos(e), h = this.pick(p.x, p.y);
      this.hover = h?.kind === "run" ? h.s : undefined;
      c.classList.toggle("pointing", !!h);
      this.on.onHover(h ? (h.kind === "run" ? this.describe(h.s) : t("Branch {name}", { name: h.r.b.name })) : "");
    };
    const up = (e: PointerEvent) => {
      c.classList.remove("dragging");
      const d = this.drag; this.drag = undefined;
      if (d?.moved) return;
      const p = pos(e), h = this.pick(p.x, p.y);
      const sel: CaminosSel | null = !h ? null : h.kind === "run" ? { kind: "run", id: h.s.r.id } : { kind: "branch", name: h.r.b.name };
      this.sel = sel;
      this.on.onSelect(sel);
    };
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const p = pos(e);
      if (e.ctrlKey || e.altKey) this.zoomAt(Math.exp(-e.deltaY * 0.002), p.x, p.y);
      else { this.tween = undefined; this.cam.x += (Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY) / this.U(); this.clampCam(); }
    };
    const leave = () => { this.hover = undefined; this.on.onHover(""); };
    c.addEventListener("pointerdown", down); c.addEventListener("pointermove", move); c.addEventListener("pointerup", up);
    c.addEventListener("pointerleave", leave); c.addEventListener("wheel", wheel, { passive: false });
    let mdrag = false;
    const jump = (e: PointerEvent) => {
      const r = this.mini.getBoundingClientRect(), x = ((e.clientX - r.left - 8) / (this.MW - 16)) * this.L.world;
      this.tween = undefined;
      if (this.play) { this.jumpPlay(x); this.cam.x = x - this.W * 0.4 / this.U(); } else this.cam.x = x - this.W / 2 / this.U();
      this.clampCam();
    };
    const mdown = (e: PointerEvent) => { mdrag = true; this.mini.setPointerCapture(e.pointerId); jump(e); };
    const mmove = (e: PointerEvent) => { if (mdrag) jump(e); };
    const mup = () => { mdrag = false; };
    this.mini.addEventListener("pointerdown", mdown); this.mini.addEventListener("pointermove", mmove); this.mini.addEventListener("pointerup", mup);
    this.cleanup.push(() => {
      c.removeEventListener("pointerdown", down); c.removeEventListener("pointermove", move); c.removeEventListener("pointerup", up);
      c.removeEventListener("pointerleave", leave); c.removeEventListener("wheel", wheel);
      this.mini.removeEventListener("pointerdown", mdown); this.mini.removeEventListener("pointermove", mmove); this.mini.removeEventListener("pointerup", mup);
    });
  }

  private pick(px: number, py: number): Hit | undefined {
    for (let i = this.hits.length - 1; i >= 0; i--) { const h = this.hits[i]; if (h.kind === "run" && (px - h.x) ** 2 + (py - h.y) ** 2 <= h.rad ** 2) return h; }
    let best: Hit | undefined, bd = 9;
    for (const h of this.hits) {
      if (h.kind !== "branch") continue;
      for (let i = 1; i < h.pts.length; i++) {
        const a = h.pts[i - 1], b = h.pts[i], dx = b.x - a.x, dy = b.y - a.y, len = dx * dx + dy * dy || 1;
        const k = clamp(((px - a.x) * dx + (py - a.y) * dy) / len, 0, 1), d = Math.hypot(px - a.x - k * dx, py - a.y - k * dy);
        if (d < bd) { bd = d; best = h; }
      }
    }
    return best;
  }

  /** One line for the hint under the map. */
  describe(s: Stop): string {
    const where = s.b ? s.b.b.name : t("Camino real");
    const state = s.r.status === "succeeded" ? t("passed") : s.r.status === "failed" ? t("failed") : s.r.status === "cancelled" ? t("cancelled") : t("on the way");
    return `${where}: ${state} · ${this.whenFmt.format(new Date(s.at * 1000))}`;
  }

  // ---- the story ----

  private jumpPlay(x: number) {
    const p = this.play; if (!p) return;
    const L = this.L;
    p.pos = clamp(x, L.xs[0], L.xs[L.xs.length - 1]);
    this.fx = [];
    p.fired = new Set<object>([...L.all.filter((s) => s.x <= p.pos), ...L.roads.filter((r) => r.merged !== undefined && r.x1 <= p.pos)]);
  }
  private spawn(kind: "tropiezo" | "fiesta" | "union", x: number, y: number) {
    const r = rng(hash(kind + x + y)), C = this.C;
    if (kind === "tropiezo") {
      for (let i = 0; i < 14; i++) this.fx.push({ kind: "maiz", x, y, vx: (r() - 0.5) * 120, vy: -60 - r() * 90, life: 1.6, t: 0 });
      this.fx.push({ kind: "polvo", x, y, life: 1.4, t: 0 });
    }
    if (kind === "fiesta") for (let i = 0; i < 22; i++) this.fx.push({ kind: "confeti", x, y, vx: (r() - 0.5) * 140, vy: -80 - r() * 110, c: [C.rosa, C.maiz, C.nopal, C.anil, C.cempasuchil][i % 5], life: 2.2, t: 0 });
    if (kind === "union") this.fx.push({ kind: "anillo", x, y, life: 1.2, t: 0 });
  }
  private stepPlay(now: number) {
    const dt = Math.min(0.05, (now - this.last) / 1000);
    this.last = now;
    for (const f of this.fx) { f.t += dt; if (f.vx !== undefined && f.vy !== undefined) { f.x += f.vx * dt; f.y += f.vy * dt; f.vy += 220 * dt; } }
    this.fx = this.fx.filter((f) => f.t < f.life);
    const p = this.play; if (!p) return;
    const L = this.L, end = L.xs[L.xs.length - 1], span = end - L.xs[0];
    if (!p.paused) p.pos += span * dt / this.speed;
    const cx = Math.min(p.pos, end);
    if (!this.drag?.moved && !this.tween) { const want = cx - this.W * 0.4 / this.U(); this.cam.x += (want - this.cam.x) * Math.min(1, dt * 3); this.clampCam(); }
    for (const s of L.all) {
      if (s.x > cx || p.fired.has(s)) continue;
      p.fired.add(s);
      if (s.r.status === "failed") this.spawn("tropiezo", s.x, s.y);
      if (!s.b && (s.r.release ?? 0) > 0) this.spawn("fiesta", s.x, -14);
    }
    for (const r of L.roads) if (r.merged !== undefined && r.x1 <= cx && !p.fired.has(r)) { p.fired.add(r); this.spawn("union", r.x1, r.parent ? L.yAt(r.parent, r.x1) : 0); }
    if (p.pos >= end + span * 0.03) this.stop();
  }

  // ---- frame ----

  private frame(now: number) {
    this.frameId = requestAnimationFrame((n) => this.frame(n));
    if (!this.W) return;
    this.stepTween(now);
    this.stepPlay(now);
    this.S = clamp(this.U(), 0.5, 1.6);
    this.hits = [];
    const ctx = this.ctx;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    const cut = this.play ? Math.min(this.play.pos, this.L.xs[this.L.xs.length - 1]) : undefined;
    this.drawGround(now);
    this.drawDays();
    this.drawRoads(now, cut);
    this.drawStops(now, cut);
    this.drawCaravan(now);
    this.drawPlay(now, cut);
    this.drawNames();
    this.drawMini(cut);
  }

  private sprite(color: string, glow: boolean) {
    const k = color + glow;
    let c = this.sprites.get(k);
    if (c) return c;
    c = document.createElement("canvas"); c.width = c.height = 32;
    const g = c.getContext("2d")!;
    if (glow) {
      const gr = g.createRadialGradient(16, 16, 0, 16, 16, 16);
      gr.addColorStop(0, "#fff"); gr.addColorStop(0.18, color); gr.addColorStop(0.45, color + "88"); gr.addColorStop(1, color + "00");
      g.fillStyle = gr; g.fillRect(0, 0, 32, 32);
    } else { g.translate(16, 16); g.rotate(Math.PI / 4); g.fillStyle = color; g.fillRect(-5, -5, 10, 10); }
    this.sprites.set(k, c);
    return c;
  }

  private drawGround(now: number) {
    const { ctx, C, W, H, L } = this;
    const g = ctx.createLinearGradient(0, 0, 0, H);
    g.addColorStop(0, C.sky); g.addColorStop(0.08, C.ground); g.addColorStop(1, C.ground2);
    ctx.fillStyle = g; ctx.fillRect(0, 0, W, H);
    if (this.night) {
      const r = rng(7);
      for (let i = 0; i < 90; i++) {
        const x = r() * W, y = TOP + r() * (H - TOP), s = 0.5 + r() * 1.3, p = r() * 6;
        ctx.globalAlpha = (still() ? 0.6 : 0.4 + 0.4 * Math.sin(now * 0.0016 + p * 3)) * 0.7;
        ctx.fillStyle = C.text; ctx.fillRect(x, y, s, s);
      }
      ctx.globalAlpha = 1;
    }
    const span = (L.maxUp + L.maxDown) / 2 + 30;
    ctx.strokeStyle = C.tuft; ctx.lineWidth = 1.2;
    for (const tf of this.tufts) {
      const sx = this.X(tf.x); if (sx < -10 || sx > W + 10) continue;
      const sy = this.Y(tf.y * span);
      ctx.beginPath(); ctx.moveTo(sx - 3, sy); ctx.lineTo(sx - 1, sy - 5 - tf.k * 3); ctx.moveTo(sx, sy); ctx.lineTo(sx + 1, sy - 6 - tf.k * 4); ctx.moveTo(sx + 3, sy); ctx.lineTo(sx + 4, sy - 4); ctx.stroke();
    }
    for (const d of this.decor) {
      const sx = this.X(d.x); if (sx < -30 || sx > W + 30) continue;
      const sy = this.Y(d.side < 0 ? -(L.maxUp + 18 + d.k * 8) : L.maxDown + 12 + d.k * 8), s = (0.8 + d.k * 0.5) * this.S;
      if (d.kind === "nopal") {
        ctx.fillStyle = C.nopal;
        for (const [ox, oy, rx, ry] of [[0, -10, 6, 9], [-7, -20, 5, 7], [6, -22, 5, 7]]) { ctx.beginPath(); ctx.ellipse(sx + ox * s, sy + oy * s, rx * s, ry * s, ox * 0.05, 0, Math.PI * 2); ctx.fill(); }
        ctx.fillStyle = C.rosa; ctx.beginPath(); ctx.arc(sx + 6 * s, sy - 29 * s, 2 * s, 0, Math.PI * 2); ctx.fill();
      } else {
        ctx.strokeStyle = C.nopal; ctx.lineWidth = 3 * s; ctx.lineCap = "round";
        for (let i = -3; i <= 3; i++) { ctx.beginPath(); ctx.moveTo(sx, sy); ctx.quadraticCurveTo(sx + i * 4 * s, sy - 10 * s, sx + i * 7 * s, sy - (16 - Math.abs(i) * 2) * s); ctx.stroke(); }
      }
    }
  }

  private points(r: Road, xA: number, xB: number) {
    const pts: { x: number; y: number }[] = [], step = 6 / this.U();
    for (let x = xA; x <= xB; x += step) pts.push({ x: this.X(x), y: this.Y(this.L.yAt(r, x)) });
    pts.push({ x: this.X(xB), y: this.Y(this.L.yAt(r, xB)) });
    return pts;
  }
  private path(pts: { x: number; y: number }[]) { const c = this.ctx; c.beginPath(); pts.forEach((p, i) => (i ? c.lineTo(p.x, p.y) : c.moveTo(p.x, p.y))); }
  private selRoad(): Road | undefined {
    const s = this.sel; if (!s) return undefined;
    if (s.kind === "branch") return this.L.byName.get(s.name);
    return this.L.all.find((x) => x.r.id === s.id)?.b;
  }

  private drawRoads(now: number, cut?: number) {
    const { ctx, C, L, S } = this;
    const end = cut === undefined ? L.world - 60 : Math.min(L.world - 60, cut);
    ctx.lineCap = "round";
    ctx.beginPath(); ctx.moveTo(this.X(-40), this.Y(0)); ctx.lineTo(this.X(end), this.Y(0));
    ctx.strokeStyle = C.roadEdge; ctx.lineWidth = 22 * S; ctx.stroke();
    ctx.strokeStyle = C.road; ctx.lineWidth = 17 * S; ctx.stroke();
    ctx.setLineDash([2, 9]); ctx.strokeStyle = C.huacalD + "55"; ctx.lineWidth = 2; ctx.stroke(); ctx.setLineDash([]);
    const selR = this.selRoad();
    for (const r of L.roads) {
      const e = cut === undefined ? r.x1 : Math.min(r.x1, cut);
      if (e <= r.x0) continue;
      const pts = this.points(r, r.x0, e);
      const dim = selR && selR !== r && selR.parent !== r && r.parent !== selR;
      ctx.globalAlpha = dim ? 0.35 : r.b.state === "left" ? 0.6 : 1;
      if (selR === r) { this.path(pts); ctx.strokeStyle = C.accent; ctx.globalAlpha = 0.35; ctx.lineWidth = 16 * S; ctx.stroke(); ctx.globalAlpha = 1; }
      this.path(pts); ctx.strokeStyle = C.roadEdge; ctx.lineWidth = 10 * S; ctx.stroke();
      this.path(pts); ctx.strokeStyle = C.path; ctx.lineWidth = 7 * S; if (r.b.state !== "merged") ctx.setLineDash([10, 6]); ctx.stroke(); ctx.setLineDash([]);
      ctx.globalAlpha = 1;
      this.hits.push({ kind: "branch", r, pts });
    }
    if (still()) return;
    // fireflies (night) or confetti (day) flowing toward the camino real
    const time = now / 1000;
    for (const r of L.roads) {
      const e = cut === undefined ? r.x1 : Math.min(r.x1, cut);
      if (e - r.x0 < 30 || r.b.state === "left" || this.X(e) < -20 || this.X(r.x0) > this.W + 20) continue;
      const n = clamp(Math.round((e - r.x0) / 90), 1, 6), h = hash(r.b.name);
      const sp = this.sprite(this.night ? C.flow : [C.rosa, C.maiz, C.nopal, C.anil][h % 4], this.night), rad = this.night ? 8 : 3.6;
      for (let i = 0; i < n; i++) {
        const k = (time * 0.05 + i / n + (h % 100) / 100) % 1, x = r.x0 + (e - r.x0) * k;
        ctx.drawImage(sp, this.X(x) - rad, this.Y(L.yAt(r, x)) - rad - 3, rad * 2, rad * 2);
      }
    }
  }

  private drawStops(now: number, cut?: number) {
    const { ctx, C, L, S, W } = this;
    const selR = this.selRoad();
    for (const s of L.releases) {
      if (cut !== undefined && s.x > cut) continue;
      const sx = this.X(s.x); if (sx < -30 || sx > W + 30) continue;
      ctx.save(); ctx.translate(sx, this.Y(0) - 13 * S); ctx.scale(S, S); this.puesto(s.r.release ?? 0, now); ctx.restore();
      if (this.U() > 0.5) { ctx.fillStyle = C.text; ctx.font = "700 10px ui-monospace, monospace"; ctx.textAlign = "center"; ctx.textBaseline = "bottom"; ctx.fillText("#" + s.r.release, sx, this.Y(0) - 34 * S); }
      this.hits.push({ kind: "run", s, x: sx, y: this.Y(0) - 20 * S, rad: 16 * S });
    }
    for (const s of L.main) {
      if ((s.r.release ?? 0) > 0 && s.r.status === "succeeded") continue;
      if (cut !== undefined && s.x > cut) continue;
      const sx = this.X(s.x); if (sx < -20 || sx > W + 20) continue;
      this.huacal(sx, this.Y(0) + 2, S, s.r.status);
      this.hits.push({ kind: "run", s, x: sx, y: this.Y(0), rad: 12 * S });
    }
    for (const r of L.roads) {
      if (r.merged === undefined || (cut !== undefined && r.x1 > cut)) continue;
      const sx = this.X(r.x1), sy = this.Y(r.parent ? L.yAt(r.parent, r.x1) : 0); if (sx < -20 || sx > W + 20) continue;
      ctx.fillStyle = C.surface; ctx.strokeStyle = selR === r ? C.accent : C.huacalD; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(sx, sy, 5 * S, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
    }
    for (const r of L.roads) {
      const dim = selR && selR !== r;
      for (const s of r.stops) {
        if (cut !== undefined && s.x > cut) continue;
        const sx = this.X(s.x), sy = this.Y(s.y); if (sx < -20 || sx > W + 20) continue;
        ctx.globalAlpha = dim ? 0.4 : 1;
        const sel = this.sel?.kind === "run" && this.sel.id === s.r.id, hov = this.hover === s;
        if (sel || hov) { ctx.beginPath(); ctx.arc(sx, sy, 13 * S, 0, Math.PI * 2); ctx.fillStyle = C.accent + "33"; ctx.fill(); ctx.strokeStyle = C.accent; ctx.lineWidth = 2; ctx.stroke(); }
        ctx.beginPath(); ctx.arc(sx, sy + 7 * S, 7 * S, 0, Math.PI * 2);
        ctx.fillStyle = s.r.status === "failed" ? C.bad + "33" : s.r.status === "succeeded" ? C.ok + "26" : C.off + "26"; ctx.fill();
        this.huacal(sx, sy, S * (sel ? 1.15 : 1), s.r.status);
        ctx.globalAlpha = 1;
        this.hits.push({ kind: "run", s, x: sx, y: sy, rad: 12 * S });
      }
      if (r.merged === undefined && (cut === undefined || r.x1 <= cut)) {
        const sx = this.X(r.x1), sy = this.Y(r.y);
        if (sx > -60 && sx < W + 20) {
          ctx.globalAlpha = dim ? 0.4 : 1;
          const lastRun = r.b.runs[r.b.runs.length - 1];
          const waiting = r.b.state === "open" && lastRun.status === "succeeded";
          const txt = r.b.state === "open" ? (waiting ? t("Waiting at the gate") : t("Stuck here")) : t("Left on the way");
          this.letrero(sx, sy + 4, txt, waiting ? C.nopal : C.grana);
          if (r.b.state === "open") this.burro(sx - 22 * S, sy + 4, 0.85 * S, now * 0.003);
          ctx.globalAlpha = 1;
        }
      }
    }
  }

  private drawNames() {
    const { ctx, C, L } = this;
    const placed: { x: number; y: number; w: number }[] = [];
    ctx.font = "800 12px 'Nunito Sans', system-ui, sans-serif"; ctx.textBaseline = "middle"; ctx.textAlign = "left";
    const selR = this.selRoad();
    const list = [...L.roads].sort((a, b) => Number(b === selR) - Number(a === selR));
    for (const r of list) {
      const first = r.stops[0];
      const x = this.X(Math.max(r.x0 + 34, (first?.x ?? r.x0) - 6)) + 12;
      const y = this.Y(r.y) + (r.y < 0 ? -17 : 19) * this.S;
      const label = r.b.name + (r.b.pr ? ` · #${r.b.pr}` : "");
      const w = ctx.measureText(label).width + 10;
      if (x > this.W || x + w < 0) continue;
      if (r !== selR && (this.X(r.x1) - x < w * 0.6 || this.U() < 0.4)) continue;
      if (r !== selR && placed.some((p) => x < p.x + p.w && x + w > p.x && Math.abs(y - p.y) < 15)) continue;
      placed.push({ x, y, w });
      ctx.fillStyle = C.surface + (this.night ? "d9" : "e6");
      ctx.beginPath(); ctx.roundRect(x - 5, y - 9, w, 18, 7); ctx.fill();
      ctx.fillStyle = r === selR ? C.accent : C.text; ctx.fillText(label, x, y + 0.5);
    }
  }

  private drawDays() {
    const { L, W } = this;
    let lastX = -Infinity;
    const d = new Date(L.tMin * 1000); d.setHours(0, 0, 0, 0);
    for (; d.getTime() / 1000 <= L.tMax; d.setDate(d.getDate() + 1)) {
      const tt = d.getTime() / 1000;
      if (tt < L.tMin) continue;
      const sx = this.X(L.xOf(tt));
      if (sx - lastX < 64) continue;
      lastX = sx;
      if (sx < -20 || sx > W + 20) continue;
      this.mojonera(sx, this.Y(0) + 26 * this.S, this.dayFmt.format(d));
    }
  }

  private drawCaravan(now: number) {
    if (this.play || still()) return;
    const span = this.W / this.U() + 200, x = this.cam.x - 100 + ((now / 1000) * 18) % span, ph = now * 0.0065, S = this.S;
    for (let i = 0; i < 2; i++) this.burro(this.X(x - i * 28), this.Y(0) + 5 * S, S, ph + i);
    this.arriero(this.X(x - 76), this.Y(0) + 6 * S, S, ph);
  }

  private drawPlay(now: number, cut?: number) {
    const { ctx, C, L, S } = this;
    for (const f of this.fx) {
      const a = 1 - f.t / f.life, sx = this.X(f.x);
      ctx.globalAlpha = a;
      if (f.kind === "maiz") { ctx.fillStyle = C.maiz; ctx.beginPath(); ctx.arc(sx, this.Y(f.y), 2.2 * S, 0, Math.PI * 2); ctx.fill(); }
      if (f.kind === "confeti") { ctx.fillStyle = f.c!; ctx.save(); ctx.translate(sx, this.Y(f.y)); ctx.rotate(f.t * 8 + (f.vx ?? 0)); ctx.fillRect(-3, -1.5, 6, 3); ctx.restore(); }
      if (f.kind === "polvo") for (let i = 0; i < 5; i++) { ctx.fillStyle = C.tuft + "aa"; ctx.beginPath(); ctx.arc(sx - 12 + i * 6, this.Y(f.y) + 6 - f.t * 14, 4 + f.t * 10, 0, Math.PI * 2); ctx.fill(); }
      if (f.kind === "anillo") { ctx.strokeStyle = C.accent; ctx.lineWidth = 3; ctx.beginPath(); ctx.arc(sx, this.Y(f.y), 6 + f.t * 30, 0, Math.PI * 2); ctx.stroke(); }
      ctx.globalAlpha = 1;
    }
    if (cut === undefined) return;
    const sx = this.X(cut);
    ctx.strokeStyle = C.accent + "55"; ctx.lineWidth = 2; ctx.setLineDash([4, 6]); ctx.beginPath(); ctx.moveTo(sx, TOP); ctx.lineTo(sx, this.H - 4); ctx.stroke(); ctx.setLineDash([]);
    const label = this.whenFmt.format(new Date(L.tOf(cut) * 1000));
    ctx.font = "700 12px ui-monospace, monospace";
    const w = ctx.measureText(label).width + 14, lx = clamp(sx - w / 2, 4, this.W - w - 4);
    ctx.fillStyle = C.surface; ctx.strokeStyle = C.accent; ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.roundRect(lx, TOP + 4, w, 22, 8); ctx.fill(); ctx.stroke();
    ctx.fillStyle = C.text; ctx.textAlign = "center"; ctx.textBaseline = "middle"; ctx.fillText(label, lx + w / 2, TOP + 15);
    // a burro on every vereda that is on its way now; the caravan on the camino real
    const ph = now * 0.0065, T = L.tOf(cut);
    for (const r of L.roads) {
      const lastRun = r.stops[r.stops.length - 1];
      const tEnd = r.merged ?? lastRun.end;
      if (T < r.start || T > tEnd) continue;
      const x = Math.min(cut, r.x1), bx = this.X(x), by = this.Y(L.yAt(r, x)) + 4;
      const fell = r.stops.some((s) => s.r.status === "failed" && Math.abs(s.x - x) < 14);
      if (fell) { ctx.save(); ctx.translate(bx, by); ctx.rotate(0.25); this.burro(0, 0, 0.9 * S, ph, false); ctx.restore(); }
      else this.burro(bx, by, 0.9 * S, ph);
    }
    for (let i = 0; i < 3; i++) this.burro(sx - (26 + i * 28) * S, this.Y(0) + 5 * S, S, ph + i);
    this.arriero(sx - 108 * S, this.Y(0) + 6 * S, S, ph);
  }

  private drawMini(cut?: number) {
    const { mctx: m, C, L, MW, MH } = this;
    if (!MW) return;
    m.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    m.fillStyle = C.surface; m.fillRect(0, 0, MW, MH);
    const sx = (x: number) => 8 + (x / L.world) * (MW - 16), mid = MH / 2, sy = (y: number) => mid + (y * (MH / 2 - 6)) / Math.max(L.maxUp, L.maxDown);
    m.strokeStyle = C.road; m.lineWidth = 3; m.beginPath(); m.moveTo(sx(0), mid); m.lineTo(sx(L.world), mid); m.stroke();
    m.lineWidth = 1.4;
    for (const r of L.roads) {
      m.strokeStyle = r.b.state === "merged" ? C.path : C.grana;
      m.beginPath(); m.moveTo(sx(r.x0), sy(r.fromY)); m.lineTo(sx(r.x0 + 10), sy(r.y)); m.lineTo(sx(r.x1 - (r.merged !== undefined ? 10 : 0)), sy(r.y));
      if (r.merged !== undefined) m.lineTo(sx(r.x1), sy(r.parent ? L.yAt(r.parent, r.x1) : 0));
      m.stroke();
    }
    for (const s of L.all) if (s.r.status === "failed") { m.fillStyle = C.bad; m.fillRect(sx(s.x) - 1.5, sy(s.y) - 3, 3, 6); }
    for (const s of L.releases) { m.fillStyle = C.maiz; m.fillRect(sx(s.x) - 1, mid - 5, 2, 4); }
    const v0 = sx(this.cam.x), v1 = sx(this.cam.x + this.W / this.U());
    m.strokeStyle = C.accent; m.lineWidth = 2; m.strokeRect(v0, 2, Math.max(6, v1 - v0), MH - 4);
    if (cut !== undefined) { m.fillStyle = C.accent; m.fillRect(sx(cut) - 1, 0, 2, MH); }
  }

  // ---- figures ----

  /** A huacal (wooden crate) of maize: whole, broken with the maize spilled, or grey. */
  private huacal(x: number, y: number, s: number, state: string) {
    const { ctx, C } = this, w = 14 * s, h = 10 * s;
    ctx.save(); ctx.translate(x, y);
    if (state === "failed") {
      ctx.rotate(-0.35); ctx.fillStyle = C.huacalD; ctx.fillRect(-w / 2, -h / 2, w * 0.45, h);
      ctx.rotate(0.7); ctx.fillStyle = C.huacal; ctx.fillRect(w * 0.05, -h / 2 - 2 * s, w * 0.45, h * 0.9);
      ctx.restore();
      ctx.fillStyle = C.maiz;
      for (let i = 0; i < 6; i++) { ctx.beginPath(); ctx.arc(x - 9 * s + i * 3.6 * s, y + 6 * s + ((i * 7) % 3) * s, 1.6 * s, 0, Math.PI * 2); ctx.fill(); }
      return;
    }
    ctx.fillStyle = state === "cancelled" ? C.off : C.huacal; ctx.fillRect(-w / 2, -h / 2, w, h);
    ctx.strokeStyle = C.huacalD; ctx.lineWidth = 1.2 * s; ctx.strokeRect(-w / 2, -h / 2, w, h);
    ctx.beginPath(); ctx.moveTo(-w / 2, 0); ctx.lineTo(w / 2, 0); ctx.moveTo(-w / 6, -h / 2); ctx.lineTo(-w / 6, h / 2); ctx.moveTo(w / 6, -h / 2); ctx.lineTo(w / 6, h / 2); ctx.stroke();
    if (state === "succeeded") { ctx.fillStyle = C.maiz; for (let i = -1; i <= 1; i++) { ctx.beginPath(); ctx.ellipse(i * 4 * s, -h / 2 - 1.5 * s, 2 * s, 2.6 * s, 0, 0, Math.PI * 2); ctx.fill(); } }
    ctx.restore();
  }

  /** A burro walking right, legs swinging with phase, with two huacales on its back. */
  private burro(x: number, y: number, s: number, phase: number, load = true) {
    const { ctx, C } = this;
    ctx.save(); ctx.translate(x, y); ctx.scale(s, s);
    const bob = still() ? 0 : Math.sin(phase * 2) * 0.8, sw = still() ? 0 : Math.sin(phase) * 3;
    ctx.strokeStyle = C.burroD; ctx.lineWidth = 2; ctx.lineCap = "round";
    for (const [lx, d] of [[-7, sw], [-3, -sw], [6, -sw], [9, sw]]) { ctx.beginPath(); ctx.moveTo(lx, -6 + bob); ctx.lineTo(lx + d * 0.6, 4); ctx.stroke(); }
    ctx.fillStyle = C.burro;
    ctx.beginPath(); ctx.ellipse(1, -9 + bob, 11, 6, 0, 0, Math.PI * 2); ctx.fill();
    ctx.beginPath(); ctx.moveTo(9, -12 + bob); ctx.lineTo(15, -19 + bob); ctx.lineTo(19, -16 + bob); ctx.lineTo(13, -8 + bob); ctx.closePath(); ctx.fill();
    ctx.beginPath(); ctx.ellipse(18.5, -16 + bob, 4, 3, 0.5, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = C.burroD;
    ctx.beginPath(); ctx.ellipse(14.5, -23 + bob, 1.4, 4.5, -0.3, 0, Math.PI * 2); ctx.fill();
    ctx.beginPath(); ctx.ellipse(17, -23 + bob, 1.4, 4.5, 0.25, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = C.burroD; ctx.lineWidth = 1.5; ctx.beginPath(); ctx.moveTo(-10, -10 + bob); ctx.quadraticCurveTo(-14, -6, -13, -2 + Math.sin(phase * 1.3)); ctx.stroke();
    ctx.fillStyle = "#1a1210"; ctx.beginPath(); ctx.arc(19.5, -17 + bob, 0.9, 0, Math.PI * 2); ctx.fill();
    if (load) {
      ctx.fillStyle = C.huacal; ctx.strokeStyle = C.huacalD; ctx.lineWidth = 1;
      ctx.fillRect(-7, -21 + bob, 8, 7); ctx.strokeRect(-7, -21 + bob, 8, 7);
      ctx.fillRect(1, -20 + bob, 7, 6); ctx.strokeRect(1, -20 + bob, 7, 6);
      ctx.fillStyle = C.maiz; ctx.beginPath(); ctx.arc(-3, -22 + bob, 1.6, 0, Math.PI * 2); ctx.arc(4.5, -21 + bob, 1.4, 0, Math.PI * 2); ctx.fill();
    }
    ctx.restore();
  }

  /** The arriero walks behind his burros: sombrero, sarape and a stick. */
  private arriero(x: number, y: number, s: number, phase: number) {
    const { ctx, C } = this;
    ctx.save(); ctx.translate(x, y); ctx.scale(s, s);
    const sw = still() ? 0 : Math.sin(phase) * 2.5;
    ctx.strokeStyle = C.burroD; ctx.lineWidth = 2; ctx.lineCap = "round";
    ctx.beginPath(); ctx.moveTo(-1, -8); ctx.lineTo(-1 + sw, 4); ctx.moveTo(1, -8); ctx.lineTo(1 - sw, 4); ctx.stroke();
    ctx.fillStyle = C.grana; ctx.beginPath(); ctx.moveTo(-5, -9); ctx.lineTo(5, -9); ctx.lineTo(3.5, -20); ctx.lineTo(-3.5, -20); ctx.closePath(); ctx.fill();
    ctx.fillStyle = C.maiz; ctx.fillRect(-5, -14, 10, 1.6);
    ctx.fillStyle = "#C68E5E"; ctx.beginPath(); ctx.arc(0, -23, 3, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = C.huacal; ctx.beginPath(); ctx.ellipse(0, -25, 9, 2.2, 0, 0, Math.PI * 2); ctx.fill();
    ctx.beginPath(); ctx.ellipse(0, -27.5, 3.6, 3, 0, Math.PI, 0); ctx.fill();
    ctx.strokeStyle = C.huacalD; ctx.lineWidth = 1.4; ctx.beginPath(); ctx.moveTo(5, -16); ctx.lineTo(10, 3); ctx.stroke();
    ctx.restore();
  }

  /** A market stall where a release was deployed: striped awning. */
  private puesto(num: number, now: number) {
    const { ctx, C } = this, w = 22, h = 14;
    ctx.fillStyle = this.night ? C.path : C.roadEdge; ctx.fillRect(-w / 2, -h, w, h);
    const colors = [C.rosa, C.maiz, C.nopal, C.anil];
    for (let i = 0; i < 4; i++) {
      ctx.fillStyle = colors[(num + i) % 4];
      ctx.beginPath(); ctx.moveTo(-w / 2 - 2 + (i * (w + 4)) / 4, -h); ctx.lineTo(-w / 2 - 2 + ((i + 1) * (w + 4)) / 4, -h); ctx.lineTo(-w / 2 - 2 + ((i + 0.5) * (w + 4)) / 4, -h - 7); ctx.closePath(); ctx.fill();
    }
    ctx.fillStyle = C.huacal; ctx.fillRect(-w / 2 + 3, -6, w - 6, 4);
    if (this.night) {
      const flick = 0.85 + 0.15 * Math.sin(now * 0.006 + num);
      const g = ctx.createRadialGradient(0, -10, 0, 0, -10, 26);
      g.addColorStop(0, `rgba(255,200,100,${0.35 * flick})`); g.addColorStop(1, "rgba(255,200,100,0)");
      ctx.fillStyle = g; ctx.fillRect(-26, -36, 52, 52);
    }
  }

  private letrero(x: number, y: number, text: string, color: string) {
    const { ctx, C } = this;
    ctx.strokeStyle = C.huacalD; ctx.lineWidth = 2.4;
    ctx.beginPath(); ctx.moveTo(x, y); ctx.lineTo(x, y - 30); ctx.stroke();
    ctx.font = "800 11px 'Nunito Sans', system-ui, sans-serif";
    const w = ctx.measureText(text).width + 14;
    ctx.fillStyle = color; ctx.beginPath(); ctx.moveTo(x - 4, y - 44); ctx.lineTo(x + w, y - 44); ctx.lineTo(x + w + 7, y - 36); ctx.lineTo(x + w, y - 28); ctx.lineTo(x - 4, y - 28); ctx.closePath(); ctx.fill();
    ctx.fillStyle = "#fff"; ctx.textAlign = "left"; ctx.textBaseline = "middle"; ctx.fillText(text, x + 3, y - 36);
  }

  private mojonera(x: number, y: number, text: string) {
    const { ctx, C } = this;
    ctx.fillStyle = this.night ? C.path : "#D9C8A8"; ctx.strokeStyle = C.huacalD; ctx.lineWidth = 1;
    ctx.beginPath(); ctx.roundRect(x - 5, y - 12, 10, 14, [5, 5, 1, 1]); ctx.fill(); ctx.stroke();
    ctx.fillStyle = C.muted; ctx.font = "700 11px 'Nunito Sans', system-ui, sans-serif"; ctx.textAlign = "center"; ctx.textBaseline = "top";
    ctx.fillText(text, x, y + 5);
  }
}
