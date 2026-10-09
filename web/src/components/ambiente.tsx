// The look of Cotija, pueblo mágico: the roof and papel picado over every
// page (with faroles at night), the night sky, the guardapolvo at the foot,
// the day/night and movement switches, and the cempasúchil each app has.
// Decorations are aria-hidden; everything they show is also said in text.
import { useEffect, useRef, useState } from "react";
import { t } from "../i18n";

// ---- day / night and movement ----

type Tema = "day" | "night";

/** Remembers a choice in this browser, when it can. */
function guardar(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* not remembered; still applies to this page */
  }
}

/** The theme in effect: the one picked, else the device's. */
function temaActual(): Tema {
  const t = document.documentElement.dataset.theme;
  if (t === "day" || t === "night") return t;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "night" : "day";
}

/** Día / Noche: switches the theme and remembers it in this browser. */
export function TemaSwitch() {
  const [tema, setTema] = useState<Tema>(temaActual);
  useEffect(() => {
    const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
    const sigue = () => setTema(temaActual());
    mq?.addEventListener("change", sigue);
    return () => mq?.removeEventListener("change", sigue);
  }, []);
  const otro: Tema = tema === "night" ? "day" : "night";
  const label = otro === "night" ? t("Switch to night") : t("Switch to day");
  return (
    <button className="icon-btn" onClick={() => {
      document.documentElement.dataset.theme = otro;
      guardar("rendimiento.tema", otro);
      setTema(otro);
    }} title={label} aria-label={label}>
      {otro === "night" ? (
        <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinejoin="round" /></svg>
      ) : (
        <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4.5" fill="none" stroke="currentColor" strokeWidth="2.2" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" /></svg>
      )}
    </button>
  );
}

/** Movement on / off (the papel picado, the flowers, the candles). */
export function MovimientoSwitch() {
  const [quieto, setQuieto] = useState(document.documentElement.dataset.motion === "off");
  const label = quieto ? t("Turn movement on") : t("Turn movement off");
  return (
    <button className="icon-btn" aria-pressed={!quieto} title={label} aria-label={label} onClick={() => {
      const next = !quieto;
      if (next) document.documentElement.dataset.motion = "off";
      else delete document.documentElement.dataset.motion;
      guardar("rendimiento.movimiento", next ? "off" : "on");
      setQuieto(next);
    }}>
      <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
        <path d="M3 12c2.5-4 5-4 7.5 0s5 4 7.5 0 2.5-2 3-2" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />
        {quieto && <path d="M4 4l16 16" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />}
      </svg>
    </button>
  );
}

// ---- the roof, the papel picado and the night sky ----

const banderas = ["var(--grana)", "var(--cempasuchil)", "var(--anil)", "var(--rosa)", "var(--nopal)", "var(--maiz)"];

/** Roof tiles and papel picado; at night every fourth flag is a farol. */
export function Techo() {
  return (
    <div className="techo" aria-hidden="true">
      <div className="techo-viga" />
      <div className="techo-tejas" />
      <div className="papel-picado">
        {Array.from({ length: 14 }, (_, i) => (
          <div key={i} className="hueco">
            <span className={`bandera${i % 4 === 1 ? " cede" : ""}`}
              style={{ background: banderas[i % banderas.length], animationDelay: `${(-i * 0.33).toFixed(2)}s` }}>
              <i className="corte c1" /><i className="corte c2" /><i className="corte c3" />
            </span>
            {i % 4 === 1 && <span className="farol" style={{ animationDelay: `${(-i * 0.4).toFixed(2)}s` }}><i /></span>}
          </div>
        ))}
      </div>
    </div>
  );
}

/** Stars and the moon behind everything; shown only at night. */
export function Cielo() {
  return <div className="cielo" aria-hidden="true"><span className="luna" /></div>;
}

/** The guardapolvo band at the foot of every page. */
export function Guardapolvo() {
  return (
    <footer className="guardapolvo">
      <span>rendimiento · {t("gives your code life")}</span>
    </footer>
  );
}

// ---- the cempasúchil ----

export type FlorEstado = "viva" | "marchita";

/** A cempasúchil: alive (it breathes), withered (it droops and drops
 *  petals) and, when it comes back, reviving with a burst of petals. */
export function Flor({ estado, size = 56, label }: { estado: FlorEstado; size?: number; label?: string }) {
  const [reviviendo, setReviviendo] = useState(false);
  const antes = useRef(estado);
  useEffect(() => {
    if (antes.current === "marchita" && estado === "viva") {
      setReviviendo(true);
      const timer = setTimeout(() => setReviviendo(false), 1600);
      antes.current = estado;
      return () => clearTimeout(timer);
    }
    antes.current = estado;
  }, [estado]);
  const clase = `flor ${reviviendo ? "reviviendo" : estado}`;
  return (
    <span className={clase} style={{ width: size, height: size }} role={label ? "img" : undefined} aria-label={label} aria-hidden={label ? undefined : true}>
      <svg viewBox="0 0 88 88" width={size} height={size}>
        <g className="flor-cabeza">
          <circle className="p1" cx="44" cy="16" r="14" /><circle className="p2" cx="64" cy="24" r="14" />
          <circle className="p1" cx="72" cy="44" r="14" /><circle className="p2" cx="64" cy="64" r="14" />
          <circle className="p1" cx="44" cy="72" r="14" /><circle className="p2" cx="24" cy="64" r="14" />
          <circle className="p1" cx="16" cy="44" r="14" /><circle className="p2" cx="24" cy="24" r="14" />
          <circle className="centro" cx="44" cy="44" r="16" />
        </g>
      </svg>
      {estado === "marchita" && !reviviendo && (
        <>
          <i className="petalo-cae" style={{ ["--dx" as string]: "10px" }} />
          <i className="petalo-cae" style={{ ["--dx" as string]: "-8px", animationDelay: ".9s" }} />
        </>
      )}
      {reviviendo && Array.from({ length: 8 }, (_, i) => {
        const a = (i / 8) * Math.PI * 2;
        return <i key={i} className="petalo-chispa" style={{ ["--dx" as string]: `${Math.round(Math.cos(a) * size * 0.9)}px`, ["--dy" as string]: `${Math.round(Math.sin(a) * size * 0.9)}px` }} />;
      })}
    </span>
  );
}

/** A burst of petals over the page, for a release that just went live. */
export function LluviaDePetalos({ cuando }: { cuando: number }) {
  const [activa, setActiva] = useState(false);
  useEffect(() => {
    if (!cuando) return;
    setActiva(true);
    const timer = setTimeout(() => setActiva(false), 2600);
    return () => clearTimeout(timer);
  }, [cuando]);
  if (!activa) return null;
  return (
    <div className="lluvia" aria-hidden="true">
      {Array.from({ length: 28 }, (_, i) => (
        <i key={i} style={{
          left: `${(i * 37) % 100}%`, animationDelay: `${((i * 0.13) % 1.2).toFixed(2)}s`,
          background: banderas[i % banderas.length], ["--dx" as string]: `${((i % 5) - 2) * 18}px`,
        }} />
      ))}
    </div>
  );
}
