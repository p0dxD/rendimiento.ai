// Zoom and pan for an SVG map: the wheel or a pinch zooms around the
// pointer, a drag pans, and fit() glides to a box. The view is the SVG's
// viewBox, so everything stays sharp at any zoom.
import { useCallback, useEffect, useRef, useState } from "react";

export interface Box { x: number; y: number; w: number; h: number }

/** True when the page should not animate (the switch in the top bar, or the device setting). */
function still() {
  return document.documentElement.dataset.motion === "off" || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

/**
 * Pan and zoom for an SVG, starting at home. Pass the element itself (from
 * a callback ref): the map often appears after the page, and the pointer
 * listeners are attached when it does. Clicks that end a drag should be
 * ignored: check dragged() in click handlers.
 */
export function usePanZoom(svg: SVGSVGElement | null, home: Box) {
  const [vb, setVb] = useState<Box>(home);
  const vbRef = useRef(vb);
  vbRef.current = vb;
  const homeRef = useRef(home);
  homeRef.current = home;
  const frame = useRef(0);
  const moved = useRef(false);

  const set = useCallback((b: Box) => {
    cancelAnimationFrame(frame.current);
    setVb(b);
  }, []);

  /** Glides to a box (instantly when motion is off). */
  const fit = useCallback((to: Box) => {
    cancelAnimationFrame(frame.current);
    if (still()) return setVb(to);
    const from = vbRef.current;
    const t0 = performance.now();
    const step = (now: number) => {
      const k = Math.min(1, (now - t0) / 380);
      const e = 1 - (1 - k) ** 3;
      setVb({ x: from.x + (to.x - from.x) * e, y: from.y + (to.y - from.y) * e, w: from.w + (to.w - from.w) * e, h: from.h + (to.h - from.h) * e });
      if (k < 1) frame.current = requestAnimationFrame(step);
    };
    frame.current = requestAnimationFrame(step);
  }, []);

  /** The map point under a screen point (the SVG letterboxes: xMidYMid meet). */
  const toMap = useCallback((clientX: number, clientY: number) => {
    const r = svg!.getBoundingClientRect();
    const v = vbRef.current;
    const s = Math.max(v.w / r.width, v.h / r.height);
    return { x: v.x - (r.width * s - v.w) / 2 + (clientX - r.left) * s, y: v.y - (r.height * s - v.h) / 2 + (clientY - r.top) * s, s };
  }, [svg]);

  /** Zooms by factor (< 1 closer) keeping the map point (cx, cy) in place. */
  const zoomAt = useCallback((factor: number, cx: number, cy: number, animate = false) => {
    const v = vbRef.current, h = homeRef.current;
    const w = Math.min(h.w * 2.5, Math.max(h.w / 16, v.w * factor));
    const k = w / v.w;
    const to = { x: cx - (cx - v.x) * k, y: cy - (cy - v.y) * k, w, h: v.h * k };
    if (animate) fit(to);
    else set(to);
  }, [fit, set]);

  /** The +/− buttons: zoom around the middle of the view. */
  const zoom = useCallback((factor: number) => {
    const v = vbRef.current;
    zoomAt(factor, v.x + v.w / 2, v.y + v.h / 2, true);
  }, [zoomAt]);

  useEffect(() => {
    if (!svg) return;
    // The wheel zooms the map instead of scrolling the page (non-passive).
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const p = toMap(e.clientX, e.clientY);
      const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
      zoomAt(Math.exp(Math.max(-60, Math.min(60, dy)) * 0.004), p.x, p.y);
    };
    // One pointer pans, two pinch. A drag only starts after a few pixels, so
    // plain clicks still reach the nodes.
    const pointers = new Map<number, { x: number; y: number }>();
    let start: { x: number; y: number } | undefined;
    let pinch: number | undefined;
    const down = (e: PointerEvent) => {
      pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
      start = { x: e.clientX, y: e.clientY };
      moved.current = false;
      pinch = undefined;
    };
    const move = (e: PointerEvent) => {
      const prev = pointers.get(e.pointerId);
      if (!prev) return;
      if (e.pointerType === "mouse" && e.buttons === 0) {
        // The button went up outside the map: stop dragging.
        pointers.delete(e.pointerId);
        start = undefined;
        return;
      }
      const cur = { x: e.clientX, y: e.clientY };
      if (pointers.size === 2) {
        const [a, b] = [...pointers.entries()].map(([id, p]) => (id === e.pointerId ? cur : p));
        const d = Math.hypot(a.x - b.x, a.y - b.y);
        if (pinch) {
          const mid = toMap((a.x + b.x) / 2, (a.y + b.y) / 2);
          zoomAt(pinch / d, mid.x, mid.y);
        }
        pinch = d;
        moved.current = true;
      } else if (start && (moved.current || Math.hypot(cur.x - start.x, cur.y - start.y) > 4)) {
        if (!moved.current) svg.setPointerCapture(e.pointerId);
        moved.current = true;
        const { s } = toMap(cur.x, cur.y);
        const v = vbRef.current;
        set({ ...v, x: v.x - (cur.x - prev.x) * s, y: v.y - (cur.y - prev.y) * s });
      }
      pointers.set(e.pointerId, cur);
    };
    const up = (e: PointerEvent) => {
      pointers.delete(e.pointerId);
      pinch = undefined;
      if (pointers.size === 0) start = undefined;
    };
    // Safari's own pinch would zoom the whole page instead of the map.
    const gesture = (e: Event) => e.preventDefault();
    svg.addEventListener("gesturestart", gesture);
    svg.addEventListener("wheel", wheel, { passive: false });
    svg.addEventListener("pointerdown", down);
    svg.addEventListener("pointermove", move);
    svg.addEventListener("pointerup", up);
    svg.addEventListener("pointercancel", up);
    return () => {
      svg.removeEventListener("gesturestart", gesture);
      svg.removeEventListener("wheel", wheel);
      svg.removeEventListener("pointerdown", down);
      svg.removeEventListener("pointermove", move);
      svg.removeEventListener("pointerup", up);
      svg.removeEventListener("pointercancel", up);
    };
  }, [svg, toMap, zoomAt, set]);

  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  return {
    viewBox: `${vb.x} ${vb.y} ${vb.w} ${vb.h}`,
    /** How far in the view is: 1 at home, 2 at twice as close. */
    scale: home.w / vb.w,
    zoom,
    fit,
    /** True when the pointer that just went up was dragging, not clicking. */
    dragged: () => moved.current,
  };
}
