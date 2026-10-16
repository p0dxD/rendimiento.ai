// The maqueta: the Universe drawn as a model village on a canvas, seen at
// an angle (isometric). Barrios are raised plates, each app is a pueblo of
// whitewashed houses with its guardapolvo in the barrio's color, and calls
// are roads with fireflies (at night) or confetti (by day) running from
// the caller to the callee: more of them for more calls, faster for less
// latency. Inside a pueblo each service is a house, a database a well
// (pozo) and a cache a clay jar (cántaro); calls from outside come in
// through an arch and calls to other apps leave by signposts.
//
// The page owns what is shown (the level and the picked thing); this file
// only lays it out, draws it, and says what was clicked.
import { t } from "../i18n";

export type Status = "none" | "ok" | "slow" | "bad";
export type NodeKind = "service" | "postgres" | "redis" | "workload" | "external" | "internet";

export interface MapNode { id: string; name: string; kind: NodeKind; lang?: string; status: Status }
export interface MapPueblo { id: string; title: string; status: Status; perMinute: number; p95?: number; nodes: MapNode[] }
export interface MapBarrio { id: string; title: string; tone: number; status: Status; perMinute: number; pueblos: MapPueblo[] }
/** A road between two barrios, pueblos or nodes ("internet" for visitors). */
export interface MapRoad { id: string; from: string; to: string; perMinute: number; p50?: number; status: Status }
export interface MapModel {
  barrios: MapBarrio[];
  barrioRoads: MapRoad[];
  puebloRoads: MapRoad[];
  nodeRoads: MapRoad[];
  /** The pueblo each node belongs to. */
  puebloOf: Map<string, string>;
}
export type Level = "region" | "barrio" | "pueblo";
export type HitKind = "barrio" | "pueblo" | "node" | "road" | "internet" | "arch" | "sign";
export interface Pick { kind: HitKind; id: string }
export interface MapView { level: Level; barrio?: string; pueblo?: string; sel?: Pick }
/** What the page asks of a maqueta, in 2.5D or in 3D. */
export interface Engine {
  set(model: MapModel, view: MapView): void;
  zoom(f: number): void;
  fit(): void;
  /** Turns the village, in degrees; only the 3D one turns. */
  turn?(deg: number): void;
  destroy(): void;
}

export const INTERNET = "internet";
const TW = 32, TH = 16; // half a tile, across and down
const RANK: Record<Status, number> = { none: 0, ok: 1, slow: 2, bad: 3 };
export const worst = (list: Status[]): Status => list.reduce<Status>((w, s) => (RANK[s] > RANK[w] ? s : w), "none");

/** Calls per minute, short: "12/min", "1.2k/min". */
export function rate(n: number) {
  if (n >= 1000) return t("{n}k/min", { n: Math.round(n / 100) / 10 });
  return t("{n}/min", { n: n >= 10 ? Math.round(n) : Math.round(n * 10) / 10 });
}
export const ms = (n: number) => (n >= 1000 ? `${Math.round(n / 100) / 10} s` : `${Math.round(n)} ms`);

/** True when the page should hold still (the switch in the top bar, or the device setting). */
export const still = () => document.documentElement.dataset.motion === "off" || !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
/** True at night: chosen in the top bar, or following a dark device. */
export function isNight() {
  const th = document.documentElement.dataset.theme;
  return th ? th === "night" : !!window.matchMedia?.("(prefers-color-scheme: dark)").matches;
}

// The village's own colors, by day and by night; the rest comes from the page's tokens.
const DAY = {
  sky1: "#FDF8EF", sky2: "#F2E3C8", ground: "#F3E7D2", plaza: "#E8D6B8", wallL: "#EFE1CB", wallR: "#E0CDB0",
  roof: "#B5532A", roofD: "#8F3F1E", road: "#C9AF8E", roadEdge: "#F9F1E4", flow: "#E8890C", window: "#6B5444cc", cantera: "#D9A88A", water: "#23408E",
  tones: ["#8E1B2C", "#D6246E", "#3E7B3A", "#23408E", "#B5532A", "#6B1E4A", "#E8890C"], outskirts: "#8C7B6B",
};
const NIGHT = {
  sky1: "#0D0A24", sky2: "#1C1F55", ground: "#1D1840", plaza: "#2A2350", wallL: "#45366A", wallR: "#362A55",
  roof: "#8A3B5A", roofD: "#642842", road: "#4E4178", roadEdge: "#2A2350", flow: "#F2C14E", window: "#FFC864", cantera: "#7B5A86", water: "#2E5BD0",
  tones: ["#E0607A", "#F06AA6", "#7FC27A", "#7C9BEA", "#E58A5C", "#C36BA0", "#F2A93B"], outskirts: "#8F84A8",
};
export type Palette = typeof DAY & { ok: string; slow: string; bad: string; text: string; muted: string; surface: string; border: string; accent: string; rosa: string; maiz: string; nopal: string; anil: string; teja: string; grana: string; cempasuchil: string };

export interface P { x: number; y: number }
interface Box { x0: number; y0: number; x1: number; y1: number }
interface Cam { x: number; y: number; s: number }
interface Road3 { a: P; b: P; c: P }
export interface PlacedBarrio { b: MapBarrio; x0: number; y0: number; side: number }
export interface PlacedPueblo { p: MapPueblo; barrio: string; gx: number; gy: number; size: number; houses: number; well: boolean; jar: boolean }
export interface Inside { pueblo: MapPueblo; c: number; nodes: Map<string, { n: MapNode; gx: number; gy: number }>; arch: { gx: number; gy: number }; signs: { to: string; gx: number; gy: number; status: Status; perMinute: number }[] }
type Hit = Pick & ({ x: number; y: number; r: number } | { pts: P[] } | { poly: P[] });
export interface Label { p: P; title: string; sub?: string; subColor?: string; big?: boolean; dy?: number; prio: number; alpha?: number; ring?: string; force?: boolean }

const iso = (gx: number, gy: number, z = 0): P => ({ x: (gx - gy) * TW, y: (gx + gy) * TH - z });
export const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v));
export function hash(s: string) { let h = 2166136261; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; }
const qpt = (g: Road3, k: number): P => ({ x: (1 - k) ** 2 * g.a.x + 2 * (1 - k) * k * g.c.x + k * k * g.b.x, y: (1 - k) ** 2 * g.a.y + 2 * (1 - k) * k * g.c.y + k * k * g.b.y });
function roadGeom(a: P, b: P, bend = 0.14): Road3 { const dx = b.x - a.x, dy = b.y - a.y; return { a, b, c: { x: (a.x + b.x) / 2 - dy * bend, y: (a.y + b.y) / 2 + dx * bend } }; }
export const roadWidth = (perMinute: number, k = 1) => (1.6 + Math.log10(perMinute + 1) * 1.7) * k;
export const INTERNET_AT = { gx: -4.6, gy: -4.6 };

export class Maqueta implements Engine {
  private ctx: CanvasRenderingContext2D;
  private W = 0;
  private H = 0;
  private dpr = 1;
  private cam: Cam = { x: 0, y: 0, s: 1 };
  private tween?: { from: Cam; to: Cam; t0: number };
  private frameId = 0;
  private P!: Palette;
  private night = false;
  private sprites = new Map<string, HTMLCanvasElement>();
  private model?: MapModel;
  private view: MapView = { level: "region" };
  private barrios: Map<string, PlacedBarrio> = new Map();
  private pueblos: Map<string, PlacedPueblo> = new Map();
  private inside?: Inside;
  private hits: Hit[] = [];
  private labels: Label[] = [];
  private stars: { x: number; y: number; s: number; p: number }[] = [];
  private pointers = new Map<number, P>();
  private drag?: { x: number; y: number; moved: boolean };
  private pinch?: number;
  private lastTap = { id: "", t: 0 };
  private cleanup: (() => void)[] = [];

  constructor(private canvas: HTMLCanvasElement, private onPick: (p: Pick | null, twice: boolean) => void) {
    this.ctx = canvas.getContext("2d")!;
    let r = 7;
    const rnd = () => ((r = (r * 16807) % 2147483647) / 2147483647);
    this.stars = Array.from({ length: 140 }, () => ({ x: rnd(), y: rnd(), s: 0.5 + rnd() * 1.4, p: rnd() * 6 }));
    this.readTheme();
    const obs = new MutationObserver(() => this.readTheme());
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
    const onMq = () => this.readTheme();
    mq?.addEventListener("change", onMq);
    const ro = new ResizeObserver(() => this.resize());
    ro.observe(canvas);
    this.cleanup.push(() => obs.disconnect(), () => mq?.removeEventListener("change", onMq), () => ro.disconnect());
    this.listen();
    this.resize();
    const frame = (now: number) => { this.draw(now); this.frameId = requestAnimationFrame(frame); };
    this.frameId = requestAnimationFrame(frame);
  }

  destroy() {
    cancelAnimationFrame(this.frameId);
    this.cleanup.forEach((f) => f());
  }

  /** Shows a model at a level; the camera glides when the level or its place changes. */
  set(model: MapModel, view: MapView) {
    const relayout = model !== this.model;
    const before = this.view;
    this.model = model;
    if (relayout) this.layout();
    this.view = view;
    if (view.level === "pueblo" && view.pueblo && (relayout || before.pueblo !== view.pueblo || !this.inside)) this.layoutInside(view.pueblo);
    const moved = before.level !== view.level || before.barrio !== view.barrio || before.pueblo !== view.pueblo;
    if (!moved && !(relayout && !this.placedOnce)) return;
    this.placedOnce = true;
    const to = this.home();
    if (!to) return;
    if (view.level === "pueblo" && before.level !== "pueblo") this.fly(to, { ...to, s: to.s * 0.35 });
    else if (before.level === "pueblo" && view.level !== "pueblo" && before.pueblo && this.pueblos.has(before.pueblo)) {
      const p = this.pueblos.get(before.pueblo)!, at = iso(p.gx, p.gy);
      this.fly(to, { x: at.x, y: at.y, s: 5 });
    } else this.fly(to);
  }
  private placedOnce = false;

  /** Zooms around the middle (> 1 closer). */
  zoom(f: number) { this.zoomAt(f, this.W / 2, this.H / 2); }
  /** Glides back to the whole of the current level. */
  fit() { const to = this.home(); if (to) this.fly(to); }

  // ---- layout ----

  private layout() {
    const placed = placeWorld(this.model!);
    this.barrios = placed.barrios;
    this.pueblos = placed.pueblos;
  }

  private layoutInside(id: string) {
    const p = this.pueblos.get(id)?.p;
    this.inside = p && placeInside(this.model!, p);
  }

  /** Where the internet's arch stands: at the barrio's corner when one is shown, else above the region. */
  private internetAt() {
    const pb = this.view.level === "barrio" && this.view.barrio ? this.barrios.get(this.view.barrio) : undefined;
    return pb ? { gx: pb.x0 - 2.4, gy: pb.y0 - 2.4 } : INTERNET_AT;
  }

  private boxOfTiles(x0: number, y0: number, x1: number, y1: number, pad = 0): Box {
    const pts = [iso(x0, y0), iso(x1, y0), iso(x1, y1), iso(x0, y1)];
    const xs = pts.map((p) => p.x), ys = pts.map((p) => p.y);
    return { x0: Math.min(...xs) - pad, x1: Math.max(...xs) + pad, y0: Math.min(...ys) - pad - 40, y1: Math.max(...ys) + pad };
  }

  private home(): Cam | undefined {
    if (!this.W || !this.model) return undefined;
    const v = this.view;
    if (v.level === "pueblo" && this.inside) { const c = this.inside.c; return this.fitBox(this.boxOfTiles(-1.2, -2.8, 2 * c + 2.6, 2 * c + 0.4, 6)); }
    const pb = v.barrio ? this.barrios.get(v.barrio) : undefined;
    if (v.level !== "region" && pb) return this.fitBox(this.boxOfTiles(pb.x0 - 3.4, pb.y0 - 3.4, pb.x0 + pb.side + 0.6, pb.y0 + pb.side + 0.6, 10));
    let x1 = 0, y1 = 0;
    for (const b of this.barrios.values()) { x1 = Math.max(x1, b.x0 + b.side); y1 = Math.max(y1, b.y0 + b.side); }
    return this.fitBox(this.boxOfTiles(INTERNET_AT.gx - 1, INTERNET_AT.gy - 1, x1 + 0.5, y1 + 0.5, 30));
  }

  private fitBox(b: Box): Cam {
    // On a phone the edges may be cropped a little, for houses big enough to tap.
    const s = clamp(Math.min(this.W / (b.x1 - b.x0), this.H / (b.y1 - b.y0)) * (this.W < 600 ? 1.15 : 0.96), 0.12, 6);
    return { x: (b.x0 + b.x1) / 2, y: (b.y0 + b.y1) / 2, s };
  }

  // ---- camera ----

  private fly(to: Cam, from?: Cam) {
    if (from) this.cam = { ...from };
    if (still()) { this.cam = { ...to }; this.tween = undefined; return; }
    this.tween = { from: { ...this.cam }, to, t0: performance.now() };
  }
  private stepTween(now: number) {
    const tw = this.tween;
    if (!tw) return;
    const k = Math.min(1, (now - tw.t0) / 700), e = 1 - (1 - k) ** 3;
    const s = Math.exp(Math.log(tw.from.s) + (Math.log(tw.to.s) - Math.log(tw.from.s)) * e);
    this.cam = { x: tw.from.x + (tw.to.x - tw.from.x) * e, y: tw.from.y + (tw.to.y - tw.from.y) * e, s };
    if (k >= 1) this.tween = undefined;
  }
  private toScreen(p: P): P { return { x: (p.x - this.cam.x) * this.cam.s + this.W / 2, y: (p.y - this.cam.y) * this.cam.s + this.H / 2 }; }
  private toWorld(x: number, y: number): P { return { x: (x - this.W / 2) / this.cam.s + this.cam.x, y: (y - this.H / 2) / this.cam.s + this.cam.y }; }
  private zoomAt(f: number, x: number, y: number) {
    this.tween = undefined;
    const before = this.toWorld(x, y);
    this.cam.s = clamp(this.cam.s * f, 0.12, 7);
    const after = this.toWorld(x, y);
    this.cam.x += before.x - after.x; this.cam.y += before.y - after.y;
  }
  private resize() {
    const r = this.canvas.getBoundingClientRect();
    const first = !this.W;
    this.W = r.width; this.H = r.height; this.dpr = Math.min(2, window.devicePixelRatio || 1);
    this.canvas.width = Math.round(this.W * this.dpr); this.canvas.height = Math.round(this.H * this.dpr);
    if (first && this.model) { const to = this.home(); if (to) this.cam = to; }
  }

  private readTheme() {
    this.night = isNight();
    this.P = palette(this.night);
    this.sprites.clear();
  }

  // ---- input ----

  private listen() {
    const c = this.canvas;
    const pos = (e: PointerEvent | WheelEvent) => { const r = c.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
    const down = (e: PointerEvent) => {
      const p = pos(e);
      this.pointers.set(e.pointerId, p);
      if (this.pointers.size === 1) this.drag = { ...p, moved: false };
      else { this.drag = undefined; const [a, b] = [...this.pointers.values()]; this.pinch = Math.hypot(a.x - b.x, a.y - b.y); }
      c.setPointerCapture(e.pointerId);
    };
    const move = (e: PointerEvent) => {
      const prev = this.pointers.get(e.pointerId), cur = pos(e);
      if (!prev) { c.classList.toggle("pointing", !!this.pick(cur.x, cur.y)); return; }
      if (this.pointers.size === 2 && this.pinch) {
        this.pointers.set(e.pointerId, cur);
        const [a, b] = [...this.pointers.values()], d = Math.hypot(a.x - b.x, a.y - b.y);
        this.zoomAt(d / this.pinch, (a.x + b.x) / 2, (a.y + b.y) / 2); this.pinch = d; return;
      }
      if (this.drag && (this.drag.moved || Math.hypot(cur.x - this.drag.x, cur.y - this.drag.y) > 5)) {
        this.drag.moved = true; this.tween = undefined; c.classList.add("dragging");
        this.cam.x -= (cur.x - prev.x) / this.cam.s; this.cam.y -= (cur.y - prev.y) / this.cam.s;
      }
      this.pointers.set(e.pointerId, cur);
    };
    const up = (e: PointerEvent) => {
      const wasDrag = this.drag?.moved, p = pos(e);
      this.pointers.delete(e.pointerId); c.classList.remove("dragging");
      if (this.pointers.size < 2) this.pinch = undefined;
      if (this.pointers.size === 0) {
        if (this.drag && !wasDrag && e.type === "pointerup") {
          const h = this.pick(p.x, p.y), now = performance.now(), key = h ? h.kind + ":" + h.id : "";
          const twice = !!h && this.lastTap.id === key && now - this.lastTap.t < 420;
          this.lastTap = { id: key, t: now };
          this.onPick(h ? { kind: h.kind, id: h.id } : null, twice);
        }
        this.drag = undefined;
      }
    };
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const p = pos(e), dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
      this.zoomAt(Math.exp(-clamp(dy, -60, 60) * 0.004), p.x, p.y);
    };
    const gesture = (e: Event) => e.preventDefault();
    c.addEventListener("pointerdown", down); c.addEventListener("pointermove", move);
    c.addEventListener("pointerup", up); c.addEventListener("pointercancel", up);
    c.addEventListener("wheel", wheel, { passive: false }); c.addEventListener("gesturestart", gesture);
    this.cleanup.push(() => {
      c.removeEventListener("pointerdown", down); c.removeEventListener("pointermove", move);
      c.removeEventListener("pointerup", up); c.removeEventListener("pointercancel", up);
      c.removeEventListener("wheel", wheel); c.removeEventListener("gesturestart", gesture);
    });
  }

  private pick(x: number, y: number): Hit | undefined {
    let best: Hit | undefined, bd = Infinity;
    for (const h of this.hits) if ("r" in h) { const d = Math.hypot(x - h.x, y - h.y) / h.r; if (d < 1 && d < bd) { bd = d; best = h; } }
    if (best) return best;
    bd = 12; // a road takes a click a finger's width away
    for (const h of this.hits) if ("pts" in h) for (let i = 1; i < h.pts.length; i++) { const d = distSeg(x, y, h.pts[i - 1], h.pts[i]); if (d < bd) { bd = d; best = h; } }
    if (best) return best;
    return this.hits.find((h) => "poly" in h && inPoly(x, y, h.poly));
  }
  private hitCircle(kind: HitKind, id: string, wp: P, r: number) { const p = this.toScreen(wp); this.hits.push({ kind, id, x: p.x, y: p.y, r: Math.max(14, r * this.cam.s) }); }
  private hitRoad(id: string, g: Road3) { const pts: P[] = []; for (let i = 0; i <= 24; i++) pts.push(this.toScreen(qpt(g, i / 24))); this.hits.push({ kind: "road", id, pts }); }

  // ---- drawing ----

  private draw(now: number) {
    this.stepTween(now);
    this.hits = []; this.labels = [];
    const { ctx, dpr, cam } = this;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.sky(now);
    if (!this.model) return;
    ctx.setTransform(dpr * cam.s, 0, 0, dpr * cam.s, dpr * (this.W / 2 - cam.x * cam.s), dpr * (this.H / 2 - cam.y * cam.s));
    if (this.view.level === "pueblo" && this.inside) this.drawInside(now); else this.drawWorld(now);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.drawLabels();
  }

  private tone(barrio: string) {
    const b = this.barrios.get(barrio)?.b;
    if (!b || b.tone < 0) return this.P.outskirts;
    return this.P.tones[b.tone % this.P.tones.length];
  }
  private statusColor(s: Status) { return s === "bad" ? this.P.bad : s === "slow" ? this.P.slow : this.P.road; }
  private ringColor(s: Status) { return s === "none" ? this.P.muted + "88" : s === "ok" ? this.P.ok + (this.night ? "cc" : "99") : this.statusColor(s); }
  private selected(kind: HitKind, id: string) { const s = this.view.sel; return !!s && s.kind === kind && s.id === id; }

  private sky(now: number) {
    const { ctx, W, H, P } = this;
    const g = ctx.createLinearGradient(0, 0, 0, H);
    g.addColorStop(0, P.sky1); g.addColorStop(1, P.sky2);
    ctx.fillStyle = g; ctx.fillRect(0, 0, W, H);
    if (this.night) {
      for (const s of this.stars) {
        ctx.globalAlpha = still() ? 0.7 : 0.45 + 0.4 * Math.sin(now * 0.0016 + s.p * 3);
        ctx.fillStyle = "#F6EBDD"; ctx.fillRect(s.x * W, s.y * H, s.s, s.s);
      }
      ctx.globalAlpha = 1;
      ctx.beginPath(); ctx.arc(W - 70, 80, 20, 0, Math.PI * 2); ctx.fillStyle = "#F7D88A"; ctx.fill();
      ctx.beginPath(); ctx.arc(W - 62, 74, 18, 0, Math.PI * 2); ctx.fillStyle = P.sky1; ctx.fill();
    } else {
      const sg = ctx.createRadialGradient(W - 80, 70, 0, W - 80, 70, 140);
      sg.addColorStop(0, "rgba(233,184,36,.35)"); sg.addColorStop(1, "rgba(233,184,36,0)");
      ctx.fillStyle = sg; ctx.fillRect(W - 220, 0, 220, 220);
    }
  }

  private drawWorld(now: number) {
    const m = this.model!, v = this.view, { ctx } = this;
    const focus = v.level === "barrio" ? v.barrio : undefined;
    for (const pb of this.barrios.values()) {
      ctx.globalAlpha = focus && pb.b.id !== focus ? 0.38 : 1;
      const { x0, y0, side } = pb, x1 = x0 + side, y1 = y0 + side, tone = this.tone(pb.b.id);
      this.slab(x0, y0, x1, y1, 12, this.P.ground, tone);
      const top = [iso(x0, y0), iso(x1, y0), iso(x1, y1), iso(x0, y1)];
      this.poly(top, tone + (this.night ? "22" : "14"), this.selected("barrio", pb.b.id) ? this.P.accent : tone + "66", 2);
      this.hits.push({ kind: "barrio", id: pb.b.id, poly: top.map((p) => this.toScreen(p)) });
      ctx.globalAlpha = 1;
    }
    const net = this.internetAt();
    const center = (id: string) => {
      if (id === INTERNET) return INTERNET_AT;
      const pb = this.barrios.get(id);
      return pb ? { gx: pb.x0 + pb.side / 2, gy: pb.y0 + pb.side / 2 } : undefined;
    };
    const at = (id: string) => (id === INTERNET ? net : this.pueblos.get(id));
    const hasInternet = (v.level === "region" ? m.barrioRoads : m.puebloRoads).some((r) => r.from === INTERNET);
    if (v.level === "region") {
      for (const r of m.barrioRoads) {
        const f = center(r.from), to = center(r.to);
        if (!f || !to) continue;
        const g = roadGeom(iso(f.gx, f.gy), iso(to.gx, to.gy), 0.12);
        this.road(g, r, roadWidth(r.perMinute, 2.1), now, this.selected("road", r.id));
        this.hitRoad(r.id, g);
      }
    } else {
      for (const r of m.puebloRoads) {
        const fb = r.from === INTERNET ? undefined : this.pueblos.get(r.from)?.barrio, tb = this.pueblos.get(r.to)?.barrio;
        if (fb !== focus && tb !== focus) continue;
        const f = at(r.from), to = at(r.to);
        if (!f || !to) continue;
        const g = roadGeom(iso(f.gx, f.gy), iso(to.gx, to.gy), 0.14);
        this.road(g, r, roadWidth(r.perMinute), now, this.selected("road", r.id), (r.from === INTERNET || fb === focus) && tb === focus ? 1 : 0.75);
        this.hitRoad(r.id, g);
      }
    }
    if (hasInternet) {
      this.arch(net.gx, net.gy);
      this.hitCircle("internet", INTERNET, iso(net.gx, net.gy, 20), 40);
      this.label(iso(net.gx, net.gy), t("Internet"), undefined, { dy: 10, prio: 5 });
    }
    const linked = new Set<string>();
    if (focus) for (const r of m.puebloRoads) {
      const fb = this.pueblos.get(r.from)?.barrio, tb = this.pueblos.get(r.to)?.barrio;
      if (fb === focus && tb) linked.add(r.to);
      if (tb === focus && fb) linked.add(r.from);
    }
    const list = [...this.pueblos.values()].sort((a, b) => a.gx + a.gy - (b.gx + b.gy));
    const offs = [[-0.45, -0.35], [0.4, -0.5], [-0.35, 0.45], [0.45, 0.35]];
    for (const pp of list) {
      const mine = pp.barrio === focus;
      ctx.globalAlpha = !focus ? 1 : mine ? 1 : linked.has(pp.p.id) ? 0.7 : 0.32;
      const sel = this.selected("pueblo", pp.p.id);
      this.ring(pp.gx, pp.gy, 1.25 * pp.size, this.ringColor(pp.p.status), sel ? 4 : 2.4, pp.p.status === "bad" && !still() ? 1.15 + 0.12 * Math.sin(now * 0.005) : 0);
      if (sel) this.ring(pp.gx, pp.gy, 1.5 * pp.size, this.P.accent, 2);
      const order = offs.slice(0, pp.houses).sort((a, b) => a[0] + a[1] - (b[0] + b[1]));
      for (const [ox, oy] of order) this.house(pp.gx + ox * pp.size, pp.gy + oy * pp.size, 0.62 * pp.size, this.tone(pp.barrio));
      if (pp.well) this.well(pp.gx + 0.9 * pp.size, pp.gy - 0.1, 0.5 * pp.size);
      if (pp.jar) this.jar(pp.gx - 0.15, pp.gy + 0.95 * pp.size, 0.5 * pp.size);
      this.hitCircle("pueblo", pp.p.id, iso(pp.gx, pp.gy, 12), 40 * pp.size);
      ctx.globalAlpha = 1;
      const zoomedIn = !focus && this.cam.s > 1.1;
      if (mine || (focus && linked.has(pp.p.id)) || zoomedIn) {
        this.label(iso(pp.gx, pp.gy), pp.p.title, this.numbers(pp.p.perMinute, pp.p.p95), {
          dy: 26 * this.cam.s * pp.size, alpha: mine || zoomedIn ? 1 : 0.75, subColor: this.subColor(pp.p.status),
          prio: (mine ? 3 : 1) + (sel ? 9 : 0), ring: sel ? this.P.accent : undefined, force: sel,
        });
      }
    }
    if (v.level === "region") for (const pb of this.barrios.values()) {
      const b = pb.b;
      this.label(iso(pb.x0, pb.y0), b.title, b.perMinute ? rate(b.perMinute) : t("{n} apps", { n: b.pueblos.length }), {
        big: true, dy: -54, prio: 10, force: true, subColor: this.subColor(b.status), ring: b.status === "slow" || b.status === "bad" ? this.statusColor(b.status) : undefined,
      });
    }
  }

  private drawInside(now: number) {
    const ins = this.inside!, m = this.model!, { ctx, P } = this, c = ins.c;
    const barrio = this.pueblos.get(ins.pueblo.id)?.barrio ?? "";
    this.slab(-1, -1, 2 * c + 1, 2 * c + 1, 14, P.ground, this.tone(barrio));
    // the plaza and its cobbles
    const q = 1.6;
    this.poly([iso(c - q, c - q), iso(c + q, c - q), iso(c + q, c + q), iso(c - q, c + q)], P.plaza, P.border, 1);
    ctx.strokeStyle = P.border; ctx.lineWidth = 0.6;
    for (let i = 1; i < 4; i++) {
      const u = c - q + (i * 2 * q) / 4, a = iso(u, c - q), b = iso(u, c + q), d = iso(c - q, u), e = iso(c + q, u);
      ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.moveTo(d.x, d.y); ctx.lineTo(e.x, e.y); ctx.stroke();
    }
    // roads: inside, in through the arch, out by the signposts
    const signOf = (to: string) => ins.signs.find((s) => s.to === (to === INTERNET ? INTERNET : m.puebloOf.get(to) ?? to));
    let fan = 0;
    for (const r of m.nodeRoads) {
      const f = ins.nodes.get(r.from), to = ins.nodes.get(r.to);
      let g: Road3 | undefined;
      if (f && to) g = roadGeom(iso(f.gx, f.gy), iso(to.gx, to.gy), 0.12);
      else if (to) { const k = ((fan++ % 5) - 2) * 0.18; g = roadGeom(iso(ins.arch.gx + k, ins.arch.gy), iso(to.gx, to.gy), 0.1 + k * 0.2); }
      else if (f) { const s = signOf(r.to); if (s) g = roadGeom(iso(f.gx, f.gy), iso(s.gx, s.gy), 0.1); }
      if (!g) continue;
      this.road(g, r, roadWidth(r.perMinute, 1.15), now, this.selected("road", r.id));
      this.hitRoad(r.id, g);
    }
    const items = [...ins.nodes.values()].sort((a, b) => a.gx + a.gy - (b.gx + b.gy));
    const tops = new Map<string, P>();
    for (const it of items) {
      const n = it.n, sel = this.selected("node", n.id);
      this.ring(it.gx, it.gy, 1.35, this.ringColor(n.status), sel ? 4 : 2.4, n.status === "bad" && !still() ? 1.15 + 0.12 * Math.sin(now * 0.005) : 0);
      if (sel) this.ring(it.gx, it.gy, 1.65, P.accent, 2);
      let h: number;
      if (n.kind === "postgres") h = this.well(it.gx, it.gy, 1.6);
      else if (n.kind === "redis") h = this.jar(it.gx, it.gy, 1.4);
      else h = this.house(it.gx, it.gy, n.kind === "external" ? 1.2 : 1.7, this.tone(barrio), { tall: n.kind === "workload", portal: n.kind === "service" && !!n.lang && n.lang === "static" });
      tops.set(n.id, iso(it.gx, it.gy, h));
      const glyph = badge(n);
      if (glyph) {
        const p = iso(it.gx, it.gy, h + 12);
        ctx.beginPath(); ctx.arc(p.x, p.y, 11, 0, Math.PI * 2); ctx.fillStyle = P.surface; ctx.fill();
        ctx.strokeStyle = n.status === "none" ? P.muted : n.status === "ok" ? P.ok : this.statusColor(n.status); ctx.lineWidth = 2.4; ctx.stroke();
        ctx.fillStyle = P.text; ctx.font = "800 10px 'Nunito Sans', system-ui, sans-serif"; ctx.textAlign = "center"; ctx.textBaseline = "middle";
        ctx.fillText(glyph, p.x, p.y + 0.5);
      }
      this.hitCircle("node", n.id, iso(it.gx, it.gy, h / 2), 56);
      this.label(iso(it.gx, it.gy), n.name, undefined, { dy: 38 * this.cam.s, prio: sel ? 12 : 4, force: sel, ring: sel ? P.accent : undefined });
    }
    // papel picado strung from the kiosco to every house
    const k = this.kiosco(c, c);
    const colors = [P.rosa, P.maiz, P.nopal, P.anil, P.cempasuchil];
    ctx.lineWidth = 1;
    let ci = 0;
    for (const it of items) {
      const tp = tops.get(it.n.id);
      if (!tp) continue;
      const a = { x: k.x, y: k.y + 14 }, mid = { x: (k.x + tp.x) / 2, y: (k.y + tp.y) / 2 + 26 };
      ctx.strokeStyle = this.night ? "#C9B8E866" : "#6B544466";
      ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.quadraticCurveTo(mid.x, mid.y, tp.x, tp.y); ctx.stroke();
      for (let i = 1; i < 8; i++) {
        const p = qpt({ a, c: mid, b: tp }, i / 8);
        this.poly([{ x: p.x - 3, y: p.y }, { x: p.x + 3, y: p.y }, { x: p.x, y: p.y + 7 }], colors[ci++ % colors.length]);
      }
    }
    for (const [x, y] of [[c - 2.1, c - 2.1], [c + 2.1, c - 2.1], [c - 2.1, c + 2.1], [c + 2.1, c + 2.1]]) this.farol(x, y, now);
    // the arch where calls come in
    const ids = new Set(ins.nodes.keys());
    const callers = [...new Set(m.nodeRoads.filter((r) => ids.has(r.to) && !ids.has(r.from)).map((r) => (r.from === INTERNET ? t("Internet") : m.puebloOf.get(r.from) ?? r.from)))];
    this.arch(ins.arch.gx, ins.arch.gy);
    this.hitCircle("arch", ins.pueblo.id, iso(ins.arch.gx, ins.arch.gy, 20), 44);
    this.label(iso(ins.arch.gx, ins.arch.gy), callers.length ? t("Coming in") : t("Nobody comes in"),
      callers.length > 3 ? t("{list} and {n} more", { list: callers.slice(0, 3).join(", "), n: callers.length - 3 }) : callers.join(", ") || undefined, { dy: 12, prio: 6 });
    for (const s of ins.signs) {
      this.signpost(s.gx, s.gy, s.status);
      this.hitCircle("sign", s.to, iso(s.gx, s.gy, 30), 30);
      this.label(iso(s.gx, s.gy), "→ " + (s.to === INTERNET ? t("Internet") : s.to), s.perMinute ? rate(s.perMinute) : undefined, { dy: 8, prio: 5, subColor: this.subColor(s.status) });
    }
  }

  private numbers(perMinute: number, p95?: number) {
    if (!perMinute) return undefined;
    return p95 ? `${rate(perMinute)} · p95 ${ms(p95)}` : rate(perMinute);
  }
  private subColor(s: Status) { return s === "slow" || s === "bad" ? this.statusColor(s) : this.P.muted; }

  // ---- labels, in screen space, skipping any that would cover another ----

  private label(wp: P, title: string, sub: string | undefined, o: Omit<Label, "p" | "title" | "sub">) { this.labels.push({ p: this.toScreen(wp), title, sub, ...o }); }
  private drawLabels() { drawLabels(this.ctx, this.labels, this.W, this.H, this.P); }

  // ---- the village's pieces (world space) ----

  private poly(pts: P[], fill?: string, stroke?: string, lw = 1) {
    const { ctx } = this;
    ctx.beginPath(); ctx.moveTo(pts[0].x, pts[0].y);
    for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i].x, pts[i].y);
    ctx.closePath();
    if (fill) { ctx.fillStyle = fill; ctx.fill(); }
    if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = lw; ctx.stroke(); }
  }
  private slab(x0: number, y0: number, x1: number, y1: number, depth: number, top: string, side: string) {
    this.poly([iso(x0, y1), iso(x1, y1), iso(x1, y1, -depth), iso(x0, y1, -depth)], side);
    this.poly([iso(x1, y0), iso(x1, y1), iso(x1, y1, -depth), iso(x1, y0, -depth)], side);
    this.poly([iso(x0, y0), iso(x1, y0), iso(x1, y1), iso(x0, y1)], top);
  }
  private box(gx: number, gy: number, w: number, d: number, h: number, c: { t: string; l: string; r: string }, z0 = 0) {
    this.poly([iso(gx, gy + d, z0), iso(gx + w, gy + d, z0), iso(gx + w, gy + d, z0 + h), iso(gx, gy + d, z0 + h)], c.l);
    this.poly([iso(gx + w, gy, z0), iso(gx + w, gy + d, z0), iso(gx + w, gy + d, z0 + h), iso(gx + w, gy, z0 + h)], c.r);
    this.poly([iso(gx, gy, z0 + h), iso(gx + w, gy, z0 + h), iso(gx + w, gy + d, z0 + h), iso(gx, gy + d, z0 + h)], c.t);
  }
  /** A whitewashed house with its guardapolvo band, a door, windows and a tile roof; returns its height. */
  private house(cx: number, cy: number, sz: number, band: string, o: { tall?: boolean; portal?: boolean } = {}) {
    const { P, ctx } = this;
    const w = 0.92 * sz * (o.portal ? 1.3 : 1), d = 0.92 * sz, gx = cx - w / 2, gy = cy - d / 2;
    const h = (o.tall ? 30 : 19) * sz, bh = 4.5 * sz;
    const L = (u: number, z: number) => iso(gx + w * u, gy + d, z), R = (u: number, z: number) => iso(gx + w, gy + d * u, z);
    this.poly([L(0, 0), L(1, 0), L(1, h), L(0, h)], P.wallL);
    this.poly([R(0, 0), R(1, 0), R(1, h), R(0, h)], P.wallR);
    this.poly([L(0, 0), L(1, 0), L(1, bh), L(0, bh)], band);
    this.poly([R(0, 0), R(1, 0), R(1, bh), R(0, bh)], band);
    this.poly([L(0.4, 0), L(0.6, 0), L(0.6, h * 0.55), L(0.4, h * 0.55)], this.night ? P.roofD : P.teja);
    for (const u of o.tall ? [0.3, 0.7] : [0.25, 0.75]) for (const z of o.tall ? [h * 0.35, h * 0.68] : [h * 0.5]) {
      this.poly([R(u - 0.1, z), R(u + 0.1, z), R(u + 0.1, z + 4 * sz), R(u - 0.1, z + 4 * sz)], P.window);
    }
    if (o.portal) for (const u of [0.15, 0.85]) this.poly([L(u - 0.06, 0), L(u + 0.06, 0), L(u + 0.06, h * 0.6), L(u - 0.06, h * 0.6)], this.night ? P.roofD : P.cantera);
    const e = 0.1 * sz, apex = iso(cx, cy, h + (o.tall ? 10 : 12) * sz);
    this.poly([iso(gx - e, gy + d + e, h), iso(gx + w + e, gy + d + e, h), apex], P.roof);
    this.poly([iso(gx + w + e, gy - e, h), iso(gx + w + e, gy + d + e, h), apex], P.roofD);
    if (this.night) {
      const p = R(0.5, h * 0.5), g = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, 16 * sz);
      g.addColorStop(0, "rgba(255,200,100,.35)"); g.addColorStop(1, "rgba(255,200,100,0)");
      ctx.fillStyle = g; ctx.fillRect(p.x - 16 * sz, p.y - 16 * sz, 32 * sz, 32 * sz);
    }
    return h + 12 * sz;
  }
  private ellipse(cx: number, cy: number, z: number, r: number) { return { p: iso(cx, cy, z), rx: r * TW * Math.SQRT2, ry: r * TH * Math.SQRT2 }; }
  /** A stone well with water and a wooden frame: a database. */
  private well(cx: number, cy: number, sz: number) {
    const { ctx, P } = this, r = 0.42 * sz, h = 12 * sz, b = this.ellipse(cx, cy, 0, r), tp = this.ellipse(cx, cy, h, r);
    ctx.beginPath(); ctx.moveTo(b.p.x - b.rx, b.p.y); ctx.ellipse(b.p.x, b.p.y, b.rx, b.ry, 0, Math.PI, 0, true); ctx.lineTo(tp.p.x + tp.rx, tp.p.y);
    ctx.ellipse(tp.p.x, tp.p.y, tp.rx, tp.ry, 0, 0, Math.PI, false); ctx.closePath(); ctx.fillStyle = P.cantera; ctx.fill();
    ctx.beginPath(); ctx.ellipse(tp.p.x, tp.p.y, tp.rx, tp.ry, 0, 0, Math.PI * 2); ctx.fillStyle = P.wallL; ctx.fill();
    ctx.beginPath(); ctx.ellipse(tp.p.x, tp.p.y, tp.rx * 0.72, tp.ry * 0.72, 0, 0, Math.PI * 2); ctx.fillStyle = P.water; ctx.fill();
    ctx.strokeStyle = this.night ? P.cantera : P.roofD; ctx.lineWidth = 2.2 * sz;
    const a = iso(cx - r, cy, h), c = iso(cx + r, cy, h);
    ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(a.x, a.y - 16 * sz); ctx.lineTo(c.x, c.y - 16 * sz); ctx.lineTo(c.x, c.y); ctx.stroke();
    return h + 18 * sz;
  }
  /** A clay jar: a cache. */
  private jar(cx: number, cy: number, sz: number) {
    const { ctx, P } = this, p = iso(cx, cy, 0);
    ctx.beginPath(); ctx.ellipse(p.x, p.y, 13 * sz, 5 * sz, 0, 0, Math.PI * 2); ctx.fillStyle = "rgba(0,0,0,.12)"; ctx.fill();
    ctx.beginPath(); ctx.ellipse(p.x, p.y - 13 * sz, 12 * sz, 13 * sz, 0, 0, Math.PI * 2); ctx.fillStyle = P.teja; ctx.fill();
    ctx.fillStyle = P.roofD; ctx.fillRect(p.x - 5 * sz, p.y - 31 * sz, 10 * sz, 7 * sz);
    ctx.beginPath(); ctx.ellipse(p.x, p.y - 31 * sz, 6.5 * sz, 2.4 * sz, 0, 0, Math.PI * 2); ctx.fillStyle = P.teja; ctx.fill();
    ctx.strokeStyle = P.maiz; ctx.lineWidth = 2 * sz; ctx.beginPath(); ctx.ellipse(p.x, p.y - 14 * sz, 11 * sz, 3.4 * sz, 0, 0, Math.PI); ctx.stroke();
    return 34 * sz;
  }
  /** The bandstand in the plaza; returns the tip of its roof. */
  private kiosco(cx: number, cy: number) {
    const { ctx, P } = this, r = 1.1, h = 26, tp = this.ellipse(cx, cy, 6, r), base = this.ellipse(cx, cy, 0, r + 0.2);
    ctx.beginPath(); ctx.ellipse(base.p.x, base.p.y, base.rx, base.ry, 0, 0, Math.PI * 2); ctx.fillStyle = P.cantera; ctx.fill();
    ctx.beginPath(); ctx.ellipse(tp.p.x, tp.p.y, tp.rx, tp.ry, 0, 0, Math.PI * 2); ctx.fillStyle = P.wallL; ctx.fill();
    ctx.strokeStyle = this.night ? P.maiz : P.grana; ctx.lineWidth = 2.4;
    for (let i = 0; i < 8; i++) {
      const a = (i / 8) * Math.PI * 2;
      if (Math.sin(a) < -0.2) continue;
      const x = tp.p.x + Math.cos(a) * tp.rx * 0.9, y = tp.p.y + Math.sin(a) * tp.ry * 0.9;
      ctx.beginPath(); ctx.moveTo(x, y); ctx.lineTo(x, y - h); ctx.stroke();
    }
    const top = this.ellipse(cx, cy, 6 + h, r + 0.15), apex = iso(cx, cy, 6 + h + 22);
    ctx.beginPath(); ctx.moveTo(top.p.x - top.rx, top.p.y); ctx.lineTo(apex.x, apex.y); ctx.lineTo(top.p.x + top.rx, top.p.y);
    ctx.ellipse(top.p.x, top.p.y, top.rx, top.ry, 0, 0, Math.PI, false); ctx.closePath(); ctx.fillStyle = P.grana; ctx.fill();
    ctx.beginPath(); ctx.ellipse(top.p.x, top.p.y, top.rx, top.ry, 0, 0, Math.PI, false); ctx.strokeStyle = P.maiz; ctx.lineWidth = 2; ctx.stroke();
    return apex;
  }
  private farol(gx: number, gy: number, now: number) {
    const { ctx, P } = this, p = iso(gx, gy);
    ctx.strokeStyle = this.night ? "#8C7AB0" : P.roofD; ctx.lineWidth = 1.6;
    ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x, p.y - 26); ctx.stroke();
    ctx.fillStyle = this.night ? "#FFD27A" : P.maiz; ctx.fillRect(p.x - 3, p.y - 32, 6, 7);
    if (this.night) {
      const flick = still() ? 1 : 0.85 + 0.15 * Math.sin(now * 0.006 + gx * 3);
      const g = ctx.createRadialGradient(p.x, p.y - 28, 0, p.x, p.y - 28, 30);
      g.addColorStop(0, `rgba(255,210,122,${0.45 * flick})`); g.addColorStop(1, "rgba(255,210,122,0)");
      ctx.fillStyle = g; ctx.fillRect(p.x - 30, p.y - 58, 60, 60);
    }
  }
  /** A cantera arch with papel picado: where calls come in. */
  private arch(gx: number, gy: number) {
    const { P } = this, c = { t: P.wallL, l: P.cantera, r: this.night ? "#5E4470" : "#C18F72" };
    this.box(gx - 0.2, gy - 1.1, 0.4, 0.4, 36, c);
    this.box(gx - 0.2, gy + 0.7, 0.4, 0.4, 36, c);
    this.box(gx - 0.25, gy - 1.15, 0.5, 2.3, 9, { t: P.teja, l: P.cantera, r: P.roofD }, 36);
    const colors = [P.rosa, P.maiz, P.nopal, P.anil];
    for (let i = 0; i < 6; i++) {
      const a = iso(gx + 0.25, gy - 1 + i * 0.38, 36), b = iso(gx + 0.25, gy - 0.62 + i * 0.38, 36);
      this.poly([a, b, { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 + 9 }], colors[i % 4]);
    }
  }
  private signpost(gx: number, gy: number, status: Status) {
    const { ctx, P } = this, p = iso(gx, gy);
    ctx.strokeStyle = this.night ? "#8C7AB0" : P.roofD; ctx.lineWidth = 2.4;
    ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x, p.y - 30); ctx.stroke();
    ctx.beginPath(); ctx.moveTo(p.x - 4, p.y - 38); ctx.lineTo(p.x + 22, p.y - 38); ctx.lineTo(p.x + 28, p.y - 32); ctx.lineTo(p.x + 22, p.y - 26); ctx.lineTo(p.x - 4, p.y - 26); ctx.closePath();
    ctx.fillStyle = status === "slow" || status === "bad" ? this.statusColor(status) : this.night ? P.cantera : P.teja; ctx.fill();
  }
  private ring(cx: number, cy: number, r: number, color: string, lw: number, pulse = 0) {
    const { ctx } = this, e = this.ellipse(cx, cy, 0, r);
    ctx.beginPath(); ctx.ellipse(e.p.x, e.p.y, e.rx, e.ry, 0, 0, Math.PI * 2); ctx.strokeStyle = color; ctx.lineWidth = lw; ctx.stroke();
    if (pulse) {
      const a = ctx.globalAlpha;
      ctx.globalAlpha = a * 0.35;
      ctx.beginPath(); ctx.ellipse(e.p.x, e.p.y, e.rx * pulse, e.ry * pulse, 0, 0, Math.PI * 2); ctx.stroke();
      ctx.globalAlpha = a;
    }
  }
  private sprite(color: string) {
    const k = color + this.night;
    let c = this.sprites.get(k);
    if (c) return c;
    c = document.createElement("canvas"); c.width = c.height = 32;
    const g = c.getContext("2d")!;
    if (this.night) {
      const gr = g.createRadialGradient(16, 16, 0, 16, 16, 16);
      gr.addColorStop(0, "#fff"); gr.addColorStop(0.18, color); gr.addColorStop(0.45, color + "88"); gr.addColorStop(1, color + "00");
      g.fillStyle = gr; g.fillRect(0, 0, 32, 32);
    } else {
      g.translate(16, 16); g.rotate(Math.PI / 4); g.fillStyle = color; g.fillRect(-6, -6, 12, 12);
    }
    this.sprites.set(k, c);
    return c;
  }
  /**
   * A road: its width is the calls, its color the health (red failing,
   * amber slow); a chevron says which way it goes. Measured roads carry
   * fireflies or confetti, as many as the calls and as fast as the calls
   * are quick; a road nobody measured is a dashed footpath.
   */
  private road(g: Road3, r: MapRoad, width: number, now: number, sel: boolean, alpha = 1) {
    const { ctx, P } = this, measured = r.status !== "none";
    ctx.save(); ctx.globalAlpha *= alpha; ctx.lineCap = "round";
    const path = () => { ctx.beginPath(); ctx.moveTo(g.a.x, g.a.y); ctx.quadraticCurveTo(g.c.x, g.c.y, g.b.x, g.b.y); };
    if (sel) { path(); ctx.strokeStyle = P.accent; ctx.globalAlpha *= 0.4; ctx.lineWidth = width + 10; ctx.stroke(); ctx.globalAlpha /= 0.4; }
    if (measured) { path(); ctx.strokeStyle = P.roadEdge; ctx.lineWidth = width + 3; ctx.stroke(); }
    path(); ctx.strokeStyle = this.statusColor(r.status);
    ctx.globalAlpha *= measured ? 0.9 : 0.75; ctx.lineWidth = measured ? width : 2.2;
    ctx.setLineDash(measured ? [] : [6, 6]); ctx.stroke(); ctx.setLineDash([]);
    ctx.restore();
    const p = qpt(g, 0.58), q = qpt(g, 0.6), ang = Math.atan2(q.y - p.y, q.x - p.x), s = Math.max(4, (measured ? width : 3) * 0.8);
    ctx.save(); ctx.globalAlpha *= alpha * 0.9; ctx.translate(p.x, p.y); ctx.rotate(ang);
    ctx.beginPath(); ctx.moveTo(s, 0); ctx.lineTo(-s * 0.6, -s * 0.8); ctx.lineTo(-s * 0.6, s * 0.8); ctx.closePath();
    ctx.fillStyle = r.status === "ok" || r.status === "none" ? (this.night ? P.flow : P.roofD) : P.roadEdge; ctx.fill(); ctx.restore();
    if (!measured || r.perMinute <= 0) return;
    const n = clamp(Math.round(1 + Math.log10(r.perMinute + 1) * 2.2), 2, 10);
    const speed = clamp(0.22 * (60 / ((r.p50 ?? 60) + 30)), 0.03, 0.5);
    const sp = this.sprite(r.status === "ok" ? P.flow : this.statusColor(r.status)), rad = this.night ? 9 : 4.5;
    const phase = (hash(r.id) % 1000) / 1000, time = still() ? 0 : now / 1000;
    ctx.save(); ctx.globalAlpha *= alpha;
    for (let i = 0; i < n; i++) { const pt = qpt(g, (time * speed + i / n + phase) % 1); ctx.drawImage(sp, pt.x - rad, pt.y - rad - 2, rad * 2, rad * 2); }
    ctx.restore();
  }
}

/** The village's colors for day or night, with the page's own tokens. */
export function palette(night: boolean): Palette {
  const cs = getComputedStyle(document.documentElement), v = (k: string) => cs.getPropertyValue("--" + k).trim();
  return { ...(night ? NIGHT : DAY), ok: v("ok"), slow: v("warn"), bad: v("bad"), text: v("text"), muted: v("muted"), surface: v("surface"), border: v("border"), accent: v("accent"),
    rosa: v("rosa"), maiz: v("maiz"), nopal: v("nopal"), anil: v("anil"), teja: v("teja"), grana: v("grana"), cempasuchil: v("cempasuchil") };
}

/** Where every barrio and pueblo goes, in tiles; both the 2.5D and the 3D maqueta use it. */
export function placeWorld(m: MapModel) {
  const barrios = new Map<string, PlacedBarrio>(), pueblos = new Map<string, PlacedPueblo>();
  const sides = m.barrios.map((b) => Math.max(6, Math.ceil(Math.sqrt(b.pueblos.length)) * 3.3 + 1.2));
  const cell = Math.max(6, ...sides) + 2.6;
  const cols = Math.ceil(Math.sqrt(m.barrios.length));
  m.barrios.forEach((b, i) => {
    const side = sides[i];
    const x0 = (i % cols) * cell + (cell - 2.6 - side) / 2, y0 = Math.floor(i / cols) * cell + (cell - 2.6 - side) / 2;
    barrios.set(b.id, { b, x0, y0, side });
    const per = Math.ceil(Math.sqrt(b.pueblos.length));
    const pad = (side - per * 3.3) / 2;
    b.pueblos.forEach((p, j) => {
      const h = hash(p.id);
      const jx = ((h % 100) / 100 - 0.5) * 0.5, jy = (((h >> 8) % 100) / 100 - 0.5) * 0.5;
      const kinds = p.nodes.map((n) => n.kind);
      pueblos.set(p.id, {
        p, barrio: b.id,
        gx: x0 + pad + 1.65 + (j % per) * 3.3 + jx, gy: y0 + pad + 1.65 + Math.floor(j / per) * 3.3 + jy,
        size: clamp(0.82 + Math.log10(p.perMinute + 1) * 0.12, 0.82, 1.2),
        houses: clamp(kinds.filter((k) => k === "service" || k === "workload" || k === "external").length, 1, 4),
        well: kinds.includes("postgres"), jar: kinds.includes("redis"),
      });
    });
  });
  return { barrios, pueblos };
}

/** Inside a pueblo: its houses in a ring around the plaza, the arch, and one signpost per app called. */
export function placeInside(m: MapModel, p: MapPueblo): Inside {
  const order: Record<NodeKind, number> = { service: 0, workload: 1, external: 2, postgres: 3, redis: 4, internet: 5 };
  const list = [...p.nodes].sort((a, b) => order[a.kind] - order[b.kind] || a.name.localeCompare(b.name));
  const R = Math.max(4.4, list.length * 0.8), c = R + 2.4;
  const nodes = new Map<string, { n: MapNode; gx: number; gy: number }>();
  list.forEach((n, i) => {
    const a = -Math.PI * 0.75 + (i / list.length) * Math.PI * 2;
    nodes.set(n.id, { n, gx: c + Math.cos(a) * R, gy: c + Math.sin(a) * R });
  });
  const out = new Map<string, { to: string; status: Status; perMinute: number }>();
  for (const r of m.nodeRoads) {
    if (!nodes.has(r.from) || nodes.has(r.to)) continue;
    const to = r.to === INTERNET ? INTERNET : m.puebloOf.get(r.to) ?? r.to;
    const s = out.get(to) ?? { to, status: "none" as Status, perMinute: 0 };
    s.status = worst([s.status, r.status]); s.perMinute += r.perMinute;
    out.set(to, s);
  }
  const outs = [...out.values()].sort((a, b) => a.to.localeCompare(b.to));
  const signs = outs.map((s, i) => ({ ...s, gx: 2 * c + 1.8, gy: outs.length === 1 ? c : 1 + (i * (2 * c - 2)) / (outs.length - 1) }));
  return { pueblo: p, c, nodes, arch: { gx: c, gy: -1.8 }, signs };
}

/** Draws labels in screen space, the most important first, skipping any that would cover another. */
export function drawLabels(ctx: CanvasRenderingContext2D, labels: Label[], W: number, H: number, P: Palette) {
  const placed: { x: number; y: number; w: number; h: number }[] = [];
  const titleFont = (big?: boolean) => (big ? "700 17px Fraunces, Georgia, serif" : "800 12.5px 'Nunito Sans', system-ui, sans-serif");
  const subFont = "600 11px ui-monospace, 'SF Mono', Menlo, Consolas, monospace";
  labels.sort((a, b) => b.prio - a.prio);
  for (const L of labels) {
    ctx.font = titleFont(L.big);
    const tw = ctx.measureText(L.title).width;
    ctx.font = subFont;
    const sw = L.sub ? ctx.measureText(L.sub).width : 0;
    const w = Math.max(tw, sw) + 16, h = L.sub ? (L.big ? 42 : 36) : L.big ? 28 : 22;
    const x = L.p.x - w / 2, y = L.p.y + (L.dy ?? 0);
    if (x > W || y > H || x + w < 0 || y + h < 0) continue;
    if (!L.force && placed.some((r) => x < r.x + r.w + 4 && x + w + 4 > r.x && y < r.y + r.h + 3 && y + h + 3 > r.y)) continue;
    placed.push({ x, y, w, h });
    ctx.globalAlpha = L.alpha ?? 1;
    ctx.beginPath(); ctx.roundRect(x, y, w, h, 9);
    ctx.fillStyle = P.surface; ctx.globalAlpha *= 0.92; ctx.fill(); ctx.globalAlpha = L.alpha ?? 1;
    ctx.strokeStyle = L.ring ?? P.border; ctx.lineWidth = L.ring ? 2 : 1; ctx.stroke();
    ctx.textAlign = "center"; ctx.textBaseline = "top"; ctx.fillStyle = P.text; ctx.font = titleFont(L.big);
    ctx.fillText(L.title, L.p.x, y + 4);
    if (L.sub) { ctx.font = subFont; ctx.fillStyle = L.subColor ?? P.muted; ctx.fillText(L.sub, L.p.x, y + (L.big ? 25 : 20)); }
    ctx.globalAlpha = 1;
  }
}

/** The letters on a house: its language, like the Services page. */
export function badge(n: MapNode) {
  if (n.kind === "postgres" || n.kind === "redis") return "";
  if (n.kind === "external") return "↗";
  const lang: Record<string, string> = { node: "JS", python: "Py", java: "Jv", go: "Go", static: "◇" };
  return (n.lang && lang[n.lang]) || (n.kind === "workload" ? "⏱" : "");
}
function distSeg(px: number, py: number, a: P, b: P) {
  const dx = b.x - a.x, dy = b.y - a.y, l = dx * dx + dy * dy || 1, k = clamp(((px - a.x) * dx + (py - a.y) * dy) / l, 0, 1);
  return Math.hypot(px - (a.x + k * dx), py - (a.y + k * dy));
}
function inPoly(x: number, y: number, pts: P[]) {
  let c = false;
  for (let i = 0, j = pts.length - 1; i < pts.length; j = i++) {
    const a = pts[i], b = pts[j];
    if (a.y > y !== b.y > y && x < ((b.x - a.x) * (y - a.y)) / (b.y - a.y) + a.x) c = !c;
  }
  return c;
}
