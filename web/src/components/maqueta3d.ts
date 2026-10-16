// The maqueta in 3D: the same village as maqueta.ts (the same barrios,
// pueblos, roads and levels, from the same layout), built with three.js so
// it can be turned and tilted. Roads between barrios are arches of light
// over the land, roads inside a barrio low garlands; the fireflies or the
// confetti, the rings and the labels mean what they mean in 2.5D.
//
// The page loads this file only when 3D is chosen, so three.js is not in
// the page's first download.
import * as THREE from "three";
import { MapControls } from "three/examples/jsm/controls/MapControls.js";
import { t } from "../i18n";
import {
  INTERNET, INTERNET_AT, badge, clamp, drawLabels, hash, isNight, ms, palette, placeInside, placeWorld, rate, roadWidth, still,
  type Engine, type HitKind, type Inside, type Label, type MapModel, type MapRoad, type MapView, type Palette, type Pick, type PlacedBarrio, type PlacedPueblo, type Status,
} from "./maqueta";

/** A pixel of height in the 2.5D drawing is this much of a tile here. */
const Z = 1 / 32;
const GROUND = -0.4;
/** The way the camera looks the first time: from the same corner as the 2.5D view, a little higher. */
const LOOK = new THREE.Vector3(1, 1.25, 1).normalize();

interface Anchor { at: THREE.Vector3; title: string; sub?: string; o: Omit<Label, "p" | "title" | "sub"> }
interface Flow { curve: THREE.QuadraticBezierCurve3; sprites: THREE.Sprite[]; speed: number; phase: number; spin: boolean }
interface Proxy { mesh: THREE.Mesh; pick: Pick }
interface RoadHit { id: string; pts: THREE.Vector3[] }
interface Badge { at: THREE.Vector3; glyph: string; status: Status }
interface Pose { target: THREE.Vector3; pos: THREE.Vector3 }

export class Maqueta3D implements Engine {
  private renderer: THREE.WebGLRenderer;
  private gl: HTMLCanvasElement;
  private overlay: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private scene = new THREE.Scene();
  private camera = new THREE.PerspectiveCamera(40, 1, 0.1, 2000);
  private controls: MapControls;
  private root = new THREE.Group();
  private hemi = new THREE.HemisphereLight();
  private sun = new THREE.DirectionalLight();
  private W = 0;
  private H = 0;
  private P!: Palette;
  private night = false;
  private model?: MapModel;
  private view: MapView = { level: "region" };
  private barrios: Map<string, PlacedBarrio> = new Map();
  private pueblos: Map<string, PlacedPueblo> = new Map();
  private inside?: Inside;
  private placedOnce = false;
  private homeDist = 30;
  private tween?: { from: Pose; to: Pose; t0: number };
  private frameId = 0;
  // what the last build made
  private owned: { dispose(): void }[] = [];
  private anchors: Anchor[] = [];
  private badges: Badge[] = [];
  private flows: Flow[] = [];
  private pulses: THREE.Mesh[] = [];
  private proxies: Proxy[] = [];
  private plates: Proxy[] = [];
  private roadHits: RoadHit[] = [];
  private zoomedLabels: Anchor[] = [];
  // shared pieces, kept for the life of the maqueta
  private mats = new Map<string, THREE.Material>();
  private geo = {
    box: new THREE.BoxGeometry(1, 1, 1),
    cyl: new THREE.CylinderGeometry(1, 1, 1, 14),
    post: new THREE.CylinderGeometry(1, 1, 1, 6),
    roof: new THREE.ConeGeometry(Math.SQRT1_2, 1, 4, 1).rotateY(Math.PI / 4),
    cone: new THREE.ConeGeometry(1, 1, 12),
    sphere: new THREE.SphereGeometry(1, 16, 12),
    flag: new THREE.BufferGeometry().setFromPoints([new THREE.Vector3(-0.5, 0, 0), new THREE.Vector3(0.5, 0, 0), new THREE.Vector3(0, -1, 0)]),
  };
  private textures = new Map<string, THREE.Texture>();
  private ray = new THREE.Raycaster();
  private down?: { x: number; y: number; t: number; moved: boolean };
  private touches = 0;
  private lastTap = { id: "", t: 0 };
  private cleanup: (() => void)[] = [];

  constructor(private host: HTMLElement, private onPick: (p: Pick | null, twice: boolean) => void) {
    this.gl = document.createElement("canvas");
    this.overlay = document.createElement("canvas");
    this.gl.className = "u-3d";
    this.overlay.className = "u-3d u-3d-labels";
    // Throws when the device has no WebGL; the page then stays in 2.5D.
    this.renderer = new THREE.WebGLRenderer({ canvas: this.gl, antialias: true, powerPreference: "low-power" });
    this.renderer.shadowMap.enabled = true;
    this.renderer.shadowMap.type = THREE.PCFShadowMap;
    host.append(this.gl, this.overlay);
    this.ctx = this.overlay.getContext("2d")!;
    this.geo.flag.computeVertexNormals();

    this.scene.add(this.root, this.hemi, this.sun, this.sun.target);
    this.sun.castShadow = true;
    this.sun.shadow.mapSize.set(1024, 1024);
    this.sun.shadow.bias = -0.0008;
    this.camera.position.copy(LOOK).multiplyScalar(30);

    this.controls = new MapControls(this.camera, this.overlay);
    this.controls.screenSpacePanning = false;
    this.controls.minPolarAngle = 0.12;
    this.controls.maxPolarAngle = 1.32;
    this.controls.minDistance = 1.5;
    this.controls.maxDistance = 600;
    this.controls.zoomToCursor = true;
    this.controls.enableDamping = !still();
    this.controls.dampingFactor = 0.12;
    this.controls.addEventListener("start", () => { this.tween = undefined; });

    this.readTheme();
    const obs = new MutationObserver(() => { this.readTheme(); this.build(); });
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "data-motion"] });
    const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
    const onMq = () => { this.readTheme(); this.build(); };
    mq?.addEventListener("change", onMq);
    const ro = new ResizeObserver(() => this.resize());
    ro.observe(host);
    this.cleanup.push(() => obs.disconnect(), () => mq?.removeEventListener("change", onMq), () => ro.disconnect());
    this.listen();
    this.resize();
    const frame = (now: number) => { this.draw(now); this.frameId = requestAnimationFrame(frame); };
    this.frameId = requestAnimationFrame(frame);
  }

  destroy() {
    cancelAnimationFrame(this.frameId);
    this.cleanup.forEach((f) => f());
    this.clear();
    this.controls.dispose();
    this.mats.forEach((m) => m.dispose());
    Object.values(this.geo).forEach((g) => g.dispose());
    this.textures.forEach((x) => x.dispose());
    (this.scene.background as THREE.Texture | null)?.dispose();
    this.renderer.dispose();
    this.gl.remove();
    this.overlay.remove();
  }

  set(model: MapModel, view: MapView) {
    const relayout = model !== this.model, before = this.view;
    this.model = model;
    if (relayout) ({ barrios: this.barrios, pueblos: this.pueblos } = placeWorld(model));
    this.view = view;
    if (view.level === "pueblo" && view.pueblo && (relayout || before.pueblo !== view.pueblo || !this.inside)) {
      const p = this.pueblos.get(view.pueblo)?.p;
      this.inside = p && placeInside(model, p);
    }
    this.build();
    const moved = before.level !== view.level || before.barrio !== view.barrio || before.pueblo !== view.pueblo;
    if (!moved && !(relayout && !this.placedOnce)) return;
    const first = !this.placedOnce;
    this.placedOnce = true;
    const to = this.home(first);
    if (!to) return;
    const back = to.pos.clone().sub(to.target);
    if (view.level === "pueblo" && before.level !== "pueblo") {
      // into a pueblo: come down from above it
      this.fly(to, { target: to.target.clone(), pos: to.target.clone().add(back.multiplyScalar(2.6)) });
    } else if (before.level === "pueblo" && view.level !== "pueblo" && before.pueblo && this.pueblos.has(before.pueblo)) {
      // out of one: rise from where it stands
      const p = this.pueblos.get(before.pueblo)!, at = new THREE.Vector3(p.gx, 0, p.gy);
      this.fly(to, { target: at, pos: at.clone().add(back.setLength(3)) });
    } else this.fly(to);
  }

  zoom(f: number) {
    this.tween = undefined;
    const off = this.camera.position.clone().sub(this.controls.target);
    off.setLength(clamp(off.length() / f, this.controls.minDistance, this.controls.maxDistance));
    this.camera.position.copy(this.controls.target).add(off);
  }

  fit() { const to = this.home(false); if (to) this.fly(to); }

  /** Turns the village around its middle, in degrees. */
  turn(deg: number) {
    const target = this.controls.target.clone(), off = this.camera.position.clone().sub(target);
    off.applyAxisAngle(new THREE.Vector3(0, 1, 0), (deg * Math.PI) / 180);
    this.fly({ target, pos: target.clone().add(off) });
  }

  // ---- camera ----

  /** Where the camera goes to see the whole of the current level; it keeps the way it looks unless asked. */
  private home(fresh: boolean): Pose | undefined {
    if (!this.W || !this.model) return undefined;
    const v = this.view;
    let b: [number, number, number, number];
    if (v.level === "pueblo" && this.inside) { const c = this.inside.c; b = [-1.2, -2.8, 2 * c + 2.6, 2 * c + 0.4]; }
    else {
      const pb = v.barrio ? this.barrios.get(v.barrio) : undefined;
      if (v.level !== "region" && pb) b = [pb.x0 - 3.4, pb.y0 - 3.4, pb.x0 + pb.side + 0.6, pb.y0 + pb.side + 0.6];
      else {
        let x1 = 0, y1 = 0;
        for (const q of this.barrios.values()) { x1 = Math.max(x1, q.x0 + q.side); y1 = Math.max(y1, q.y0 + q.side); }
        b = [INTERNET_AT.gx - 1, INTERNET_AT.gy - 1, x1 + 0.5, y1 + 0.5];
      }
    }
    const target = new THREE.Vector3((b[0] + b[2]) / 2, 0, (b[1] + b[3]) / 2);
    const r = Math.hypot(b[2] - b[0], b[3] - b[1]) / 2;
    const vfov = (this.camera.fov * Math.PI) / 180, hfov = 2 * Math.atan(Math.tan(vfov / 2) * this.camera.aspect);
    // On a phone the edges may be cropped a little, for houses big enough to tap.
    const dist = (r / Math.sin(Math.min(vfov, hfov) / 2)) * (this.W < 600 ? 0.7 : 0.8);
    this.homeDist = dist;
    const dir = fresh ? LOOK.clone() : this.camera.position.clone().sub(this.controls.target).normalize();
    return { target, pos: target.clone().add(dir.multiplyScalar(dist)) };
  }

  private fly(to: Pose, from?: Pose) {
    if (from) { this.controls.target.copy(from.target); this.camera.position.copy(from.pos); }
    if (still()) { this.controls.target.copy(to.target); this.camera.position.copy(to.pos); this.tween = undefined; return; }
    this.tween = { from: { target: this.controls.target.clone(), pos: this.camera.position.clone() }, to, t0: performance.now() };
  }

  private stepTween(now: number) {
    const tw = this.tween;
    if (!tw) return;
    const k = Math.min(1, (now - tw.t0) / 800), e = 1 - (1 - k) ** 3;
    this.controls.target.lerpVectors(tw.from.target, tw.to.target, e);
    // glide around the middle rather than straight through it
    const a = tw.from.pos.clone().sub(tw.from.target), b = tw.to.pos.clone().sub(tw.to.target);
    const len = Math.exp(Math.log(a.length()) + (Math.log(b.length()) - Math.log(a.length())) * e);
    const dir = a.normalize().lerp(b.normalize(), e).normalize();
    this.camera.position.copy(this.controls.target).add(dir.multiplyScalar(len));
    if (k >= 1) this.tween = undefined;
  }

  private resize() {
    const r = this.host.getBoundingClientRect();
    const first = !this.W;
    this.W = r.width; this.H = r.height;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    this.renderer.setPixelRatio(dpr);
    this.renderer.setSize(this.W, this.H, false);
    this.overlay.width = Math.round(this.W * dpr); this.overlay.height = Math.round(this.H * dpr);
    this.camera.aspect = this.W / Math.max(1, this.H);
    this.camera.updateProjectionMatrix();
    if (first && this.model) { const to = this.home(true); if (to) { this.controls.target.copy(to.target); this.camera.position.copy(to.pos); } }
  }

  private readTheme() {
    this.night = isNight();
    this.P = palette(this.night);
    this.controls.enableDamping = !still();
    const P = this.P;
    this.hemi.color.set(this.night ? "#6E6CB8" : "#FFF4E2");
    this.hemi.groundColor.set(this.night ? "#1D1840" : "#B99A74");
    this.hemi.intensity = this.night ? 2.4 : 2.1;
    this.sun.color.set(this.night ? "#B9C4FF" : "#FFE2B0");
    this.sun.intensity = this.night ? 1.3 : 2.2;
    this.scene.fog = new THREE.Fog(P.sky2, 60, 400);
    (this.scene.background as THREE.Texture | null)?.dispose();
    this.scene.background = this.skyTexture();
    this.mats.forEach((m) => m.dispose());
    this.mats.clear();
    this.textures.forEach((x) => x.dispose());
    this.textures.clear();
  }

  /** The sky behind the village: a gradient, and by night stars and the moon. */
  private skyTexture() {
    const c = document.createElement("canvas"), P = this.P;
    c.width = 512; c.height = 512;
    const g = c.getContext("2d")!, gr = g.createLinearGradient(0, 0, 0, 512);
    gr.addColorStop(0, P.sky1); gr.addColorStop(1, P.sky2);
    g.fillStyle = gr; g.fillRect(0, 0, 512, 512);
    if (this.night) {
      let r = 7;
      const rnd = () => ((r = (r * 16807) % 2147483647) / 2147483647);
      g.fillStyle = "#F6EBDD";
      for (let i = 0; i < 160; i++) { g.globalAlpha = 0.4 + rnd() * 0.5; const s = 0.6 + rnd() * 1.6; g.fillRect(rnd() * 512, rnd() * 380, s, s); }
      g.globalAlpha = 1;
      g.beginPath(); g.arc(440, 70, 22, 0, Math.PI * 2); g.fillStyle = "#F7D88A"; g.fill();
      g.beginPath(); g.arc(449, 63, 20, 0, Math.PI * 2); g.fillStyle = P.sky1; g.fill();
    } else {
      const sg = g.createRadialGradient(430, 70, 0, 430, 70, 150);
      sg.addColorStop(0, "rgba(233,184,36,.35)"); sg.addColorStop(1, "rgba(233,184,36,0)");
      g.fillStyle = sg; g.fillRect(250, 0, 262, 260);
    }
    const tex = new THREE.CanvasTexture(c);
    tex.colorSpace = THREE.SRGBColorSpace;
    return tex;
  }

  // ---- input ----

  private listen() {
    const c = this.overlay;
    const pos = (e: PointerEvent) => { const r = c.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
    const down = (e: PointerEvent) => {
      this.touches++;
      const p = pos(e);
      // only the main button taps; the right one turns
      this.down = this.touches === 1 && e.button === 0 ? { ...p, t: performance.now(), moved: false } : undefined;
    };
    const move = (e: PointerEvent) => {
      const p = pos(e);
      if (this.down && Math.hypot(p.x - this.down.x, p.y - this.down.y) > 5) { this.down.moved = true; c.classList.add("dragging"); }
      if (!this.touches) c.classList.toggle("pointing", !!this.pick(p.x, p.y));
    };
    const up = (e: PointerEvent) => {
      this.touches = Math.max(0, this.touches - 1);
      c.classList.remove("dragging");
      const d = this.down, p = pos(e);
      if (!this.touches) this.down = undefined;
      if (!d || d.moved || e.type !== "pointerup" || performance.now() - d.t > 600) return;
      const h = this.pick(p.x, p.y), now = performance.now(), key = h ? h.kind + ":" + h.id : "";
      const twice = !!h && this.lastTap.id === key && now - this.lastTap.t < 420;
      this.lastTap = { id: key, t: now };
      this.onPick(h, twice);
    };
    const gesture = (e: Event) => e.preventDefault();
    const menu = (e: Event) => e.preventDefault();
    // Listen before the controls do, so a tap is seen even when they capture the pointer.
    c.addEventListener("pointerdown", down, { capture: true }); c.addEventListener("pointermove", move, { capture: true });
    c.addEventListener("pointerup", up, { capture: true }); c.addEventListener("pointercancel", up, { capture: true });
    c.addEventListener("gesturestart", gesture); c.addEventListener("contextmenu", menu);
    this.cleanup.push(() => {
      c.removeEventListener("pointerdown", down, { capture: true }); c.removeEventListener("pointermove", move, { capture: true });
      c.removeEventListener("pointerup", up, { capture: true }); c.removeEventListener("pointercancel", up, { capture: true });
      c.removeEventListener("gesturestart", gesture); c.removeEventListener("contextmenu", menu);
    });
  }

  /** What is under a point: a thing first, then a road a finger's width away, then a barrio. */
  private pick(x: number, y: number): Pick | null {
    if (!this.W) return null;
    this.ray.setFromCamera(new THREE.Vector2((x / this.W) * 2 - 1, -(y / this.H) * 2 + 1), this.camera);
    const first = (list: Proxy[]) => {
      const hit = this.ray.intersectObjects(list.map((p) => p.mesh), false)[0];
      return hit ? list.find((p) => p.mesh === hit.object)!.pick : null;
    };
    const thing = first(this.proxies);
    if (thing) return thing;
    let best: string | undefined, bd = 12;
    for (const r of this.roadHits) {
      const pts = r.pts.map((v) => this.screen(v));
      for (let i = 1; i < pts.length; i++) {
        const a = pts[i - 1], b = pts[i];
        if (!a || !b) continue;
        const d = distSeg(x, y, a, b);
        if (d < bd) { bd = d; best = r.id; }
      }
    }
    if (best) return { kind: "road", id: best };
    return first(this.plates);
  }

  private screen(v: THREE.Vector3) {
    const p = v.clone().project(this.camera);
    if (p.z > 1 || p.z < -1) return undefined;
    return { x: ((p.x + 1) / 2) * this.W, y: ((1 - p.y) / 2) * this.H };
  }

  // ---- drawing ----

  private draw(now: number) {
    this.stepTween(now);
    this.controls.update();
    const time = still() ? 0 : now / 1000;
    for (const f of this.flows) {
      const n = f.sprites.length;
      f.sprites.forEach((s, i) => {
        f.curve.getPoint((time * f.speed + i / n + f.phase) % 1, s.position);
        s.position.y += 0.06;
        if (f.spin) s.material.rotation = time * 2 + i;
      });
    }
    for (const r of this.pulses) { const k = still() ? 1.15 : 1.15 + 0.12 * Math.sin(now * 0.005); r.scale.set(k, k, k); }
    const dist = this.camera.position.distanceTo(this.controls.target);
    if (this.scene.fog instanceof THREE.Fog) { this.scene.fog.near = dist * 1.4; this.scene.fog.far = dist * 4 + 40; }
    this.renderer.render(this.scene, this.camera);

    const { ctx } = this, dpr = this.overlay.width / Math.max(1, this.W);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, this.W, this.H);
    const P = this.P;
    for (const b of this.badges) {
      const p = this.screen(b.at);
      if (!p) continue;
      ctx.beginPath(); ctx.arc(p.x, p.y, 11, 0, Math.PI * 2); ctx.fillStyle = P.surface; ctx.fill();
      ctx.strokeStyle = b.status === "none" ? P.muted : b.status === "ok" ? P.ok : this.statusColor(b.status); ctx.lineWidth = 2.4; ctx.stroke();
      ctx.fillStyle = P.text; ctx.font = "800 10px 'Nunito Sans', system-ui, sans-serif"; ctx.textAlign = "center"; ctx.textBaseline = "middle";
      ctx.fillText(b.glyph, p.x, p.y + 0.5);
    }
    const labels: Label[] = [];
    const close = dist < this.homeDist * 0.6;
    for (const a of close ? [...this.anchors, ...this.zoomedLabels] : this.anchors) {
      const p = this.screen(a.at);
      if (p) labels.push({ p, title: a.title, sub: a.sub, ...a.o });
    }
    drawLabels(ctx, labels, this.W, this.H, P);
  }

  private clear() {
    this.root.traverse((o) => { if (o instanceof THREE.Sprite) o.material.dispose(); });
    this.root.clear();
    this.owned.forEach((d) => d.dispose());
    this.owned = []; this.anchors = []; this.zoomedLabels = []; this.badges = []; this.flows = []; this.pulses = []; this.proxies = []; this.plates = []; this.roadHits = [];
  }

  /** Builds the scene for the current level from scratch: a few hundred pieces, quick enough on every change. */
  private build() {
    this.clear();
    if (!this.model) return;
    const ground = new THREE.Mesh(new THREE.CircleGeometry(900, 48).rotateX(-Math.PI / 2), this.mat(this.night ? "#16122F" : "#E9D9BE"));
    this.owned.push(ground.geometry);
    ground.position.y = GROUND - 0.01;
    ground.receiveShadow = true;
    this.root.add(ground);
    if (this.view.level === "pueblo" && this.inside) this.buildInside(); else this.buildWorld();
    // the sun follows what is shown, so its shadows stay sharp
    const box = new THREE.Box3().setFromObject(this.root.children.length > 1 ? this.root.children[1] : this.root);
    for (const o of this.root.children.slice(2)) box.expandByObject(o);
    const c = box.getCenter(new THREE.Vector3()), r = Math.min(120, box.getSize(new THREE.Vector3()).length() / 2 + 2);
    this.sun.position.copy(c).add(new THREE.Vector3(-0.6, 1.4, 0.35).setLength(r * 1.6));
    this.sun.target.position.copy(c);
    const sc = this.sun.shadow.camera;
    sc.left = -r; sc.right = r; sc.top = r; sc.bottom = -r; sc.near = 0.1; sc.far = r * 4;
    sc.updateProjectionMatrix();
  }

  private buildWorld() {
    const m = this.model!, v = this.view, P = this.P;
    const focus = v.level === "barrio" ? v.barrio : undefined;
    for (const pb of this.barrios.values()) {
      const a = focus && pb.b.id !== focus ? 0.4 : 1, tone = this.tone(pb.b.id), sel = this.selected("barrio", pb.b.id);
      const top = mix(P.ground, tone, this.night ? 0.14 : 0.08);
      const g = this.box(pb.x0, GROUND, pb.y0, pb.side, -GROUND, pb.side, [tone, tone, top, tone, tone, tone], a);
      g.receiveShadow = true;
      this.plates.push({ mesh: g, pick: { kind: "barrio", id: pb.b.id } });
      this.frame(pb.x0, pb.y0, pb.x0 + pb.side, pb.y0 + pb.side, sel ? P.accent : tone, sel ? 0.12 : 0.06, sel ? 1 : 0.55 * a);
    }
    const net = this.internetAt();
    const center = (id: string) => {
      if (id === INTERNET) return { gx: INTERNET_AT.gx, gy: INTERNET_AT.gy, y: GROUND + 1.2 };
      const pb = this.barrios.get(id);
      return pb ? { gx: pb.x0 + pb.side / 2, gy: pb.y0 + pb.side / 2, y: 0 } : undefined;
    };
    const at = (id: string) => (id === INTERNET ? { gx: net.gx, gy: net.gy, y: GROUND + 1.2 } : this.pueblos.has(id) ? { ...this.pueblos.get(id)!, y: 0.05 } : undefined);
    const hasInternet = (v.level === "region" ? m.barrioRoads : m.puebloRoads).some((r) => r.from === INTERNET);
    if (v.level === "region") {
      for (const r of m.barrioRoads) {
        const f = center(r.from), to = center(r.to);
        if (f && to) this.road(r, f, to, roadWidth(r.perMinute, 2.1), { lift: 0.2, bend: 0.12 });
      }
    } else {
      for (const r of m.puebloRoads) {
        const fb = r.from === INTERNET ? undefined : this.pueblos.get(r.from)?.barrio, tb = this.pueblos.get(r.to)?.barrio;
        if (fb !== focus && tb !== focus) continue;
        const f = at(r.from), to = at(r.to);
        if (f && to) this.road(r, f, to, roadWidth(r.perMinute), { lift: 0.07, bend: 0.14, alpha: (r.from === INTERNET || fb === focus) && tb === focus ? 1 : 0.7 });
      }
    }
    if (hasInternet) {
      this.arch(net.gx, net.gy, GROUND);
      this.proxy("internet", INTERNET, net.gx, GROUND, net.gy, 1.3, 1.6);
      this.anchor(net.gx, GROUND, net.gy, t("Internet"), undefined, { dy: 8, prio: 5 });
    }
    const linked = new Set<string>();
    if (focus) for (const r of m.puebloRoads) {
      const fb = this.pueblos.get(r.from)?.barrio, tb = this.pueblos.get(r.to)?.barrio;
      if (fb === focus && tb) linked.add(r.to);
      if (tb === focus && fb) linked.add(r.from);
    }
    const offs = [[-0.45, -0.35], [0.4, -0.5], [-0.35, 0.45], [0.45, 0.35]];
    for (const pp of this.pueblos.values()) {
      const mine = pp.barrio === focus;
      const a = !focus ? 1 : mine ? 1 : linked.has(pp.p.id) ? 0.7 : 0.32;
      const sel = this.selected("pueblo", pp.p.id);
      this.ring(pp.gx, pp.gy, 0, 1.25 * pp.size, this.ringColor(pp.p.status), sel ? 0.13 : 0.08, a, pp.p.status === "bad");
      if (sel) this.ring(pp.gx, pp.gy, 0, 1.5 * pp.size, P.accent, 0.07, 1);
      let h = 0;
      for (const [ox, oy] of offs.slice(0, pp.houses)) h = Math.max(h, this.house(pp.gx + ox * pp.size, pp.gy + oy * pp.size, 0.62 * pp.size, this.tone(pp.barrio), {}, a));
      if (pp.well) this.well(pp.gx + 0.9 * pp.size, pp.gy - 0.1, 0.5 * pp.size, a);
      if (pp.jar) this.jar(pp.gx - 0.15, pp.gy + 0.95 * pp.size, 0.5 * pp.size, a);
      this.proxy("pueblo", pp.p.id, pp.gx, 0, pp.gy, 1.25 * pp.size, h + 0.2);
      const label: Anchor = {
        at: new THREE.Vector3(pp.gx, 0, pp.gy + 1.1 * pp.size), title: pp.p.title, sub: this.numbers(pp.p.perMinute, pp.p.p95),
        o: { dy: 4, alpha: mine || !focus ? 1 : 0.75, subColor: this.subColor(pp.p.status), prio: (mine ? 3 : 1) + (sel ? 9 : 0), ring: sel ? P.accent : undefined, force: sel },
      };
      if (mine || (focus && linked.has(pp.p.id)) || sel) this.anchors.push(label);
      else if (!focus) this.zoomedLabels.push(label);
    }
    if (v.level === "region") for (const pb of this.barrios.values()) {
      const b = pb.b;
      this.anchor(pb.x0, 0, pb.y0, b.title, b.perMinute ? rate(b.perMinute) : t("{n} apps", { n: b.pueblos.length }), {
        big: true, dy: -50, prio: 10, force: true, subColor: this.subColor(b.status), ring: b.status === "slow" || b.status === "bad" ? this.statusColor(b.status) : undefined,
      });
    }
  }

  private buildInside() {
    const ins = this.inside!, m = this.model!, P = this.P, c = ins.c;
    const barrio = this.pueblos.get(ins.pueblo.id)?.barrio ?? "", tone = this.tone(barrio);
    this.box(-1, GROUND, -1, 2 * c + 2, -GROUND, 2 * c + 2, [tone, tone, P.ground, tone, tone, tone]).receiveShadow = true;
    // the plaza and its cobbles
    const q = 1.6;
    this.box(c - q, 0, c - q, 2 * q, 0.02, 2 * q, P.plaza).receiveShadow = true;
    for (let i = 1; i < 4; i++) {
      const u = c - q + (i * 2 * q) / 4;
      this.box(u - 0.01, 0.02, c - q, 0.02, 0.005, 2 * q, P.border);
      this.box(c - q, 0.02, u - 0.01, 2 * q, 0.005, 0.02, P.border);
    }
    // roads: inside, in through the arch, out by the signposts
    const signOf = (to: string) => ins.signs.find((s) => s.to === (to === INTERNET ? INTERNET : m.puebloOf.get(to) ?? to));
    let fan = 0;
    for (const r of m.nodeRoads) {
      const f = ins.nodes.get(r.from), to = ins.nodes.get(r.to);
      const w = roadWidth(r.perMinute, 1.15);
      if (f && to) this.road(r, { ...f, y: 0.05 }, { ...to, y: 0.05 }, w, { lift: 0.05, bend: 0.12 });
      else if (to) { const k = ((fan++ % 5) - 2) * 0.18; this.road(r, { gx: ins.arch.gx + k, gy: ins.arch.gy, y: 0.05 }, { ...to, y: 0.05 }, w, { lift: 0.05, bend: 0.1 + k * 0.2 }); }
      else if (f) { const s = signOf(r.to); if (s) this.road(r, { ...f, y: 0.05 }, { gx: s.gx, gy: s.gy, y: 0.05 }, w, { lift: 0.05, bend: 0.1 }); }
    }
    const tops = new Map<string, THREE.Vector3>();
    for (const it of ins.nodes.values()) {
      const n = it.n, sel = this.selected("node", n.id);
      this.ring(it.gx, it.gy, 0, 1.35, this.ringColor(n.status), sel ? 0.13 : 0.08, 1, n.status === "bad");
      if (sel) this.ring(it.gx, it.gy, 0, 1.65, P.accent, 0.07, 1);
      let h: number;
      if (n.kind === "postgres") h = this.well(it.gx, it.gy, 1.6);
      else if (n.kind === "redis") h = this.jar(it.gx, it.gy, 1.4);
      else h = this.house(it.gx, it.gy, n.kind === "external" ? 1.2 : 1.7, tone, { tall: n.kind === "workload", portal: n.kind === "service" && n.lang === "static" });
      tops.set(n.id, new THREE.Vector3(it.gx, h, it.gy));
      const glyph = badge(n);
      if (glyph) this.badges.push({ at: new THREE.Vector3(it.gx, h + 0.35, it.gy), glyph, status: n.status });
      this.proxy("node", n.id, it.gx, 0, it.gy, 1.1, h);
      this.anchor(it.gx, 0, it.gy + 1.2, n.name, undefined, { dy: 6, prio: sel ? 12 : 4, force: sel, ring: sel ? P.accent : undefined });
    }
    // papel picado strung from the kiosco to every house
    const k = this.kiosco(c, c), colors = [P.rosa, P.maiz, P.nopal, P.anil, P.cempasuchil];
    let ci = 0;
    for (const tp of tops.values()) {
      const a = k.clone().setY(k.y - 0.4), mid = a.clone().lerp(tp, 0.5).setY(Math.min(a.y, tp.y) - 0.5);
      const curve = new THREE.QuadraticBezierCurve3(a, mid, tp);
      const line = new THREE.Line(new THREE.BufferGeometry().setFromPoints(curve.getPoints(16)), this.mat(this.night ? "#C9B8E8" : "#6B5444", 0.45, "line"));
      this.owned.push(line.geometry);
      this.root.add(line);
      for (let i = 1; i < 8; i++) {
        const f = new THREE.Mesh(this.geo.flag, this.mat(colors[ci++ % colors.length], 1, "flag"));
        f.position.copy(curve.getPoint(i / 8));
        f.scale.setScalar(0.22);
        f.lookAt(f.position.clone().add(new THREE.Vector3(1, 0, 1)));
        this.root.add(f);
      }
    }
    for (const [x, y] of [[c - 2.1, c - 2.1], [c + 2.1, c - 2.1], [c - 2.1, c + 2.1], [c + 2.1, c + 2.1]]) this.farol(x, y);
    // the arch where calls come in
    const ids = new Set(ins.nodes.keys());
    const callers = [...new Set(m.nodeRoads.filter((r) => ids.has(r.to) && !ids.has(r.from)).map((r) => (r.from === INTERNET ? t("Internet") : m.puebloOf.get(r.from) ?? r.from)))];
    this.arch(ins.arch.gx, ins.arch.gy, 0);
    this.proxy("arch", ins.pueblo.id, ins.arch.gx, 0, ins.arch.gy, 1.3, 1.5);
    this.anchor(ins.arch.gx, 0, ins.arch.gy, callers.length ? t("Coming in") : t("Nobody comes in"),
      callers.length > 3 ? t("{list} and {n} more", { list: callers.slice(0, 3).join(", "), n: callers.length - 3 }) : callers.join(", ") || undefined, { dy: 10, prio: 6 });
    for (const s of ins.signs) {
      this.signpost(s.gx, s.gy, s.status);
      this.proxy("sign", s.to, s.gx, 0, s.gy, 0.7, 1.3);
      this.anchor(s.gx, 0, s.gy, "→ " + (s.to === INTERNET ? t("Internet") : s.to), s.perMinute ? rate(s.perMinute) : undefined, { dy: 8, prio: 5, subColor: this.subColor(s.status) });
    }
  }

  /** Where the internet's arch stands: at the barrio's corner when one is shown, else above the region. */
  private internetAt() {
    const pb = this.view.level === "barrio" && this.view.barrio ? this.barrios.get(this.view.barrio) : undefined;
    return pb ? { gx: pb.x0 - 2.4, gy: pb.y0 - 2.4 } : INTERNET_AT;
  }

  // ---- colors and words, as in 2.5D ----

  private tone(barrio: string) {
    const b = this.barrios.get(barrio)?.b;
    if (!b || b.tone < 0) return this.P.outskirts;
    return this.P.tones[b.tone % this.P.tones.length];
  }
  private statusColor(s: Status) { return s === "bad" ? this.P.bad : s === "slow" ? this.P.slow : this.P.road; }
  private ringColor(s: Status) { return s === "none" ? this.P.muted : s === "ok" ? this.P.ok : this.statusColor(s); }
  private subColor(s: Status) { return s === "slow" || s === "bad" ? this.statusColor(s) : this.P.muted; }
  private selected(kind: HitKind, id: string) { const s = this.view.sel; return !!s && s.kind === kind && s.id === id; }
  private numbers(perMinute: number, p95?: number) {
    if (!perMinute) return undefined;
    return p95 ? `${rate(perMinute)} · p95 ${ms(p95)}` : rate(perMinute);
  }

  // ---- the village's pieces ----

  private mat(color: string, alpha = 1, kind: "solid" | "glow" | "line" | "flag" = "solid") {
    // a color may carry its own alpha (#rrggbbaa), as some of the 2.5D ones do
    if (/^#[0-9a-f]{8}$/i.test(color)) { alpha *= parseInt(color.slice(7), 16) / 255; color = color.slice(0, 7); }
    alpha = Math.round(alpha * 100) / 100;
    const key = `${kind}|${color}|${alpha}`;
    let m = this.mats.get(key);
    if (m) return m;
    const see = alpha < 1;
    if (kind === "line") m = new THREE.LineBasicMaterial({ color: new THREE.Color(color), transparent: see, opacity: alpha });
    else if (kind === "glow") m = new THREE.MeshBasicMaterial({ color: new THREE.Color(color), transparent: see, opacity: alpha, toneMapped: false });
    else m = new THREE.MeshLambertMaterial({ color: new THREE.Color(color), transparent: see, opacity: alpha, depthWrite: !see, side: kind === "flag" ? THREE.DoubleSide : THREE.FrontSide, flatShading: true });
    this.mats.set(key, m);
    return m;
  }
  private part(geo: THREE.BufferGeometry, mat: THREE.Material, x: number, y: number, z: number, sx: number, sy: number, sz: number, shadow = true) {
    const m = new THREE.Mesh(geo, mat);
    m.position.set(x, y, z);
    m.scale.set(sx, sy, sz);
    m.castShadow = shadow && !(mat as THREE.MeshLambertMaterial).transparent;
    m.receiveShadow = shadow;
    this.root.add(m);
    return m;
  }
  /** A box from a corner (x, y, z) and its size; one color, or six (+x, −x, top, bottom, +z, −z). */
  private box(x: number, y: number, z: number, w: number, h: number, d: number, color: string | string[], alpha = 1) {
    const mat = Array.isArray(color) ? color.map((c) => this.mat(c, alpha)) : this.mat(color, alpha);
    const m = new THREE.Mesh(this.geo.box, mat);
    m.position.set(x + w / 2, y + h / 2, z + d / 2);
    m.scale.set(w, h, d);
    m.castShadow = alpha === 1 && h > 0.05;
    this.root.add(m);
    return m;
  }
  /** A thin border around a barrio's top. */
  private frame(x0: number, y0: number, x1: number, y1: number, color: string, w: number, alpha: number) {
    const y = 0.005, h = 0.012;
    this.box(x0, y, y0, x1 - x0, h, w, color, alpha); this.box(x0, y, y1 - w, x1 - x0, h, w, color, alpha);
    this.box(x0, y, y0, w, h, y1 - y0, color, alpha); this.box(x1 - w, y, y0, w, h, y1 - y0, color, alpha);
  }
  /** An invisible piece that takes taps for a thing. */
  private proxy(kind: HitKind, id: string, gx: number, y: number, gy: number, r: number, h: number) {
    const m = new THREE.Mesh(this.geo.cyl, this.mat("#000", 0));
    (m.material as THREE.Material).visible = false;
    m.position.set(gx, y + h / 2, gy);
    m.scale.set(r, h, r);
    this.root.add(m);
    this.proxies.push({ mesh: m, pick: { kind, id } });
  }
  private anchor(gx: number, y: number, gy: number, title: string, sub: string | undefined, o: Anchor["o"]) {
    this.anchors.push({ at: new THREE.Vector3(gx, y, gy), title, sub, o });
  }

  /** A whitewashed house with its guardapolvo band, a door, windows and a tile roof; returns its height. */
  private house(cx: number, cy: number, sz: number, band: string, o: { tall?: boolean; portal?: boolean } = {}, alpha = 1) {
    const P = this.P;
    const w = 0.92 * sz * (o.portal ? 1.3 : 1), d = 0.92 * sz, x = cx - w / 2, z = cy - d / 2;
    const h = (o.tall ? 30 : 19) * sz * Z, bh = 4.5 * sz * Z, e = 0.1 * sz;
    this.box(x, 0, z, w, h, d, [P.wallR, P.wallR, P.wallL, P.wallL, P.wallL, P.wallL], alpha);
    this.box(x - 0.01, 0, z - 0.01, w + 0.02, bh, d + 0.02, band, alpha);
    // the door faces the same way as in 2.5D, the windows the other side
    this.box(cx - w * 0.1, 0, z + d, w * 0.2, h * 0.55, 0.02, this.night ? P.roofD : P.teja, alpha);
    const lit = this.night ? this.mat(P.window, alpha, "glow") : this.mat(P.window, alpha);
    for (const u of o.tall ? [0.3, 0.7] : [0.25, 0.75]) for (const zz of o.tall ? [h * 0.35, h * 0.68] : [h * 0.5]) {
      this.part(this.geo.box, lit, x + w + 0.011, zz + 2 * sz * Z, z + d * u, 0.02, 4 * sz * Z, d * 0.2, false);
    }
    if (o.portal) for (const u of [0.15, 0.85]) this.box(x + w * u - w * 0.06, 0, z + d, w * 0.12, h * 0.6, 0.03, this.night ? P.roofD : P.cantera, alpha);
    const rh = (o.tall ? 10 : 12) * sz * Z;
    this.part(this.geo.roof, this.mat(P.roof, alpha), cx, h + rh / 2, cy, w + 2 * e, rh, d + 2 * e);
    return h + rh;
  }
  /** A stone well with water and a wooden frame: a database. */
  private well(cx: number, cy: number, sz: number, alpha = 1) {
    const P = this.P, r = 0.42 * sz, h = 12 * sz * Z;
    this.part(this.geo.cyl, this.mat(P.cantera, alpha), cx, h / 2, cy, r, h, r);
    this.part(this.geo.cyl, this.mat(P.water, alpha), cx, h + 0.005, cy, r * 0.72, 0.01, r * 0.72, false);
    const wood = this.mat(this.night ? P.cantera : P.roofD, alpha), ph = 16 * sz * Z, pr = 0.035 * sz;
    this.part(this.geo.post, wood, cx - r, h + ph / 2, cy, pr, ph, pr);
    this.part(this.geo.post, wood, cx + r, h + ph / 2, cy, pr, ph, pr);
    this.part(this.geo.box, wood, cx, h + ph, cy, 2 * r + pr * 2, pr * 2, pr * 2);
    return h + ph + 0.1;
  }
  /** A clay jar: a cache. */
  private jar(cx: number, cy: number, sz: number, alpha = 1) {
    const P = this.P, r = 12 * sz * Z;
    this.part(this.geo.sphere, this.mat(P.teja, alpha), cx, 13 * sz * Z, cy, r, 13 * sz * Z, r);
    this.part(this.geo.cyl, this.mat(P.roofD, alpha), cx, 28 * sz * Z, cy, 5 * sz * Z, 7 * sz * Z, 5 * sz * Z);
    this.part(this.geo.cyl, this.mat(P.maiz, alpha), cx, 14 * sz * Z, cy, r * 1.02, 2 * sz * Z, r * 1.02, false);
    return 34 * sz * Z;
  }
  /** The bandstand in the plaza; returns the tip of its roof. */
  private kiosco(cx: number, cy: number) {
    const P = this.P, r = 1.1, fl = 6 * Z, h = 26 * Z, rh = 22 * Z;
    this.part(this.geo.cyl, this.mat(P.cantera), cx, 0.02, cy, r + 0.2, 0.04, r + 0.2);
    this.part(this.geo.cyl, this.mat(P.wallL), cx, fl / 2, cy, r, fl, r);
    const post = this.mat(this.night ? P.maiz : P.grana);
    for (let i = 0; i < 8; i++) {
      const a = (i / 8) * Math.PI * 2;
      this.part(this.geo.post, post, cx + Math.cos(a) * r * 0.9, fl + h / 2, cy + Math.sin(a) * r * 0.9, 0.04, h, 0.04);
    }
    this.part(this.geo.cone, this.mat(P.grana), cx, fl + h + rh / 2, cy, r + 0.15, rh, r + 0.15);
    this.part(this.geo.cyl, this.mat(P.maiz), cx, fl + h + 0.02, cy, r + 0.17, 0.04, r + 0.17, false);
    return new THREE.Vector3(cx, fl + h + rh, cy);
  }
  private farol(gx: number, gy: number) {
    const P = this.P, h = 26 * Z;
    this.part(this.geo.post, this.mat(this.night ? "#8C7AB0" : P.roofD), gx, h / 2, gy, 0.025, h, 0.025);
    this.part(this.geo.box, this.night ? this.mat("#FFD27A", 1, "glow") : this.mat(P.maiz), gx, h + 0.1, gy, 0.16, 0.2, 0.16, false);
    if (this.night) this.glow(gx, h + 0.1, gy, "#FFD27A", 1.6, 0.5);
  }
  /** A soft halo of light, always facing the camera. */
  private glow(x: number, y: number, z: number, color: string, size: number, alpha: number) {
    const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.texture(color, true), color: "#fff", transparent: true, opacity: alpha, depthWrite: false, blending: THREE.AdditiveBlending }));
    s.position.set(x, y, z);
    s.scale.setScalar(size);
    this.root.add(s);
    return s;
  }
  /** A cantera arch with papel picado: where calls come in. */
  private arch(gx: number, gy: number, y: number) {
    const P = this.P, h = 36 * Z, side = this.night ? "#5E4470" : "#C18F72";
    const stone = [side, side, P.wallL, P.cantera, P.cantera, P.cantera];
    this.box(gx - 0.2, y, gy - 1.1, 0.4, h, 0.4, stone);
    this.box(gx - 0.2, y, gy + 0.7, 0.4, h, 0.4, stone);
    this.box(gx - 0.25, y + h, gy - 1.15, 0.5, 9 * Z, 2.3, [P.roofD, P.roofD, P.teja, P.cantera, P.cantera, P.cantera]);
    const colors = [P.rosa, P.maiz, P.nopal, P.anil];
    for (let i = 0; i < 6; i++) {
      const f = new THREE.Mesh(this.geo.flag, this.mat(colors[i % 4], 1, "flag"));
      f.position.set(gx + 0.27, y + h, gy - 0.81 + i * 0.38);
      f.scale.setScalar(0.32);
      f.rotation.y = Math.PI / 2;
      this.root.add(f);
    }
  }
  private signpost(gx: number, gy: number, status: Status) {
    const P = this.P, h = 30 * Z;
    this.part(this.geo.post, this.mat(this.night ? "#8C7AB0" : P.roofD), gx, h / 2, gy, 0.035, h, 0.035);
    const c = status === "slow" || status === "bad" ? this.statusColor(status) : this.night ? P.cantera : P.teja;
    this.part(this.geo.box, this.mat(c), gx + 0.32, h + 0.1, gy, 0.7, 0.32, 0.06);
    const tip = this.part(this.geo.roof, this.mat(c), gx + 0.75, h + 0.1, gy, 0.3, 0.2, 0.06);
    tip.rotation.z = -Math.PI / 2;
  }
  /** The ring on the ground around a pueblo or a house: its health. */
  private ring(cx: number, cy: number, y: number, r: number, color: string, lw: number, alpha: number, pulse = false) {
    const g = new THREE.RingGeometry(r - lw / 2, r + lw / 2, 48).rotateX(-Math.PI / 2);
    this.owned.push(g);
    const m = new THREE.Mesh(g, this.mat(color, alpha * (color === this.P.muted ? 0.6 : 0.9), "glow"));
    m.position.set(cx, y + 0.012, cy);
    this.root.add(m);
    if (pulse && alpha > 0.5) {
      const p = new THREE.Mesh(g, this.mat(color, 0.35, "glow"));
      p.position.copy(m.position);
      this.root.add(p);
      this.pulses.push(p);
    }
  }
  /** The firefly (night) or the confetti (day), drawn once per color. */
  private texture(color: string, night: boolean) {
    const k = color + night;
    let tex = this.textures.get(k);
    if (tex) return tex;
    const c = document.createElement("canvas");
    c.width = c.height = 64;
    const g = c.getContext("2d")!;
    if (night) {
      const gr = g.createRadialGradient(32, 32, 0, 32, 32, 32);
      gr.addColorStop(0, "#fff"); gr.addColorStop(0.18, color); gr.addColorStop(0.45, color + "88"); gr.addColorStop(1, color + "00");
      g.fillStyle = gr; g.fillRect(0, 0, 64, 64);
    } else {
      g.translate(32, 32); g.rotate(Math.PI / 4); g.fillStyle = color; g.fillRect(-14, -14, 28, 28);
    }
    tex = new THREE.CanvasTexture(c);
    tex.colorSpace = THREE.SRGBColorSpace;
    this.textures.set(k, tex);
    return tex;
  }
  /**
   * A road: its thickness is the calls, its color the health (red failing,
   * amber slow); a cone says which way it goes. Measured roads carry
   * fireflies or confetti, as many as the calls and as fast as the calls
   * are quick; a road nobody measured is a dashed footpath.
   */
  private road(r: MapRoad, a: { gx: number; gy: number; y: number }, b: { gx: number; gy: number; y: number }, width: number, o: { lift: number; bend: number; alpha?: number }) {
    const P = this.P, alpha = o.alpha ?? 1, measured = r.status !== "none", sel = this.selected("road", r.id);
    const A = new THREE.Vector3(a.gx, a.y, a.gy), B = new THREE.Vector3(b.gx, b.y, b.gy);
    const len = A.distanceTo(B), dx = B.x - A.x, dz = B.z - A.z;
    const C = A.clone().lerp(B, 0.5).add(new THREE.Vector3(-dz * o.bend, Math.max(a.y, b.y) - (a.y + b.y) / 2 + 0.3 + len * o.lift, dx * o.bend));
    const curve = new THREE.QuadraticBezierCurve3(A, C, B);
    const radius = Math.max(0.035, width * Z * 0.5), color = this.statusColor(r.status);
    if (sel) this.tube(curve, radius + 0.09, P.accent, 0.4, "glow");
    if (measured) this.tube(curve, radius, color, 0.92 * alpha, this.night ? "glow" : "solid");
    else {
      // a dashed footpath
      const m = this.mat(color, 0.8 * alpha), n = Math.max(8, Math.round(len * 3));
      for (let i = 0; i < n; i += 2) {
        const p = curve.getPoint(i / n), q = curve.getPoint((i + 1) / n);
        const piece = this.part(this.geo.post, m, (p.x + q.x) / 2, (p.y + q.y) / 2, (p.z + q.z) / 2, 0.03, p.distanceTo(q), 0.03, false);
        piece.quaternion.setFromUnitVectors(new THREE.Vector3(0, 1, 0), q.clone().sub(p).normalize());
      }
    }
    // which way it goes
    const at = curve.getPoint(0.6), tan = curve.getTangent(0.6), s = Math.max(0.09, (measured ? radius : 0.05) * 1.6);
    const cone = this.part(this.geo.cone, this.mat(r.status === "ok" || r.status === "none" ? (this.night ? P.flow : P.roofD) : P.roadEdge, 0.95 * alpha), at.x, at.y, at.z, s, s * 2.2, s, false);
    cone.quaternion.setFromUnitVectors(new THREE.Vector3(0, 1, 0), tan);
    this.roadHits.push({ id: r.id, pts: curve.getPoints(24) });
    if (!measured || r.perMinute <= 0) return;
    const n = clamp(Math.round(1 + Math.log10(r.perMinute + 1) * 2.2), 2, 10);
    const speed = clamp(0.22 * (60 / ((r.p50 ?? 60) + 30)), 0.03, 0.5);
    const tex = this.texture(r.status === "ok" ? P.flow : color, this.night);
    const sprites: THREE.Sprite[] = [];
    for (let i = 0; i < n; i++) {
      const sp = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, transparent: true, opacity: alpha, depthWrite: false, blending: this.night ? THREE.AdditiveBlending : THREE.NormalBlending }));
      sp.scale.setScalar(this.night ? 0.5 : 0.2);
      this.root.add(sp);
      sprites.push(sp);
    }
    this.flows.push({ curve, sprites, speed, phase: (hash(r.id) % 1000) / 1000, spin: !this.night });
  }
  private tube(curve: THREE.Curve<THREE.Vector3>, radius: number, color: string, alpha: number, kind: "solid" | "glow") {
    const g = new THREE.TubeGeometry(curve, 40, radius, 8, false);
    this.owned.push(g);
    const m = new THREE.Mesh(g, this.mat(color, Math.round(alpha * 100) / 100, kind));
    m.castShadow = kind === "solid" && alpha === 1;
    this.root.add(m);
    return m;
  }
}

/** Mixes two #rrggbb colors. */
function mix(a: string, b: string, k: number) {
  return "#" + new THREE.Color(a).lerp(new THREE.Color(b), k).getHexString();
}
function distSeg(px: number, py: number, a: { x: number; y: number }, b: { x: number; y: number }) {
  const dx = b.x - a.x, dy = b.y - a.y, l = dx * dx + dy * dy || 1, k = clamp(((px - a.x) * dx + (py - a.y) * dy) / l, 0, 1);
  return Math.hypot(px - (a.x + k * dx), py - (a.y + k * dy));
}
