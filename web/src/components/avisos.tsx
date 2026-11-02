import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { subscribe } from "../api";
import { t } from "../i18n";

/** Something that just happened in the organization (internal/store.Activity). */
interface Actividad {
  kind: "run" | "release" | "encargo";
  event: string;
  app?: string;
  branch?: string;
  deploy?: boolean;
  run?: number;
  release?: number;
  encargo?: number;
  title?: string;
  who?: string;
  at: string;
}

type Tono = "ok" | "warn" | "bad" | "info";

interface Aviso {
  id: number;
  tono: Tono;
  titulo: string;
  detalle?: string;
  a: string;
}

/** What a notice says, its tone and where it leads; undefined for what is not worth a notice. */
function aviso(x: Actividad): Omit<Aviso, "id"> | undefined {
  if (x.kind === "run") {
    const a = `/apps/${x.app}/runs/${x.run}`;
    const donde = x.deploy ? t("{app}, deploying {branch}", { app: x.app ?? "", branch: x.branch ?? "" }) : t("{app}, branch {branch}", { app: x.app ?? "", branch: x.branch ?? "" });
    switch (x.event) {
      case "started": return { tono: "info", titulo: t("Run #{n} started", { n: x.run ?? 0 }), detalle: donde, a };
      case "succeeded": return { tono: "ok", titulo: t("Run #{n} passed", { n: x.run ?? 0 }), detalle: donde, a };
      case "failed": return { tono: "bad", titulo: t("Run #{n} failed", { n: x.run ?? 0 }), detalle: donde, a };
      case "cancelled": return { tono: "warn", titulo: t("Run #{n} was cancelled", { n: x.run ?? 0 }), detalle: donde, a };
    }
    return undefined;
  }
  if (x.kind === "release") {
    const a = `/apps/${x.app}/releases`;
    return x.event === "rolledBack"
      ? { tono: "warn", titulo: t("{app} rolled back", { app: x.app ?? "" }), detalle: t("release #{n} undoes the last one, which broke something", { n: x.release ?? 0 }), a }
      : { tono: "ok", titulo: t("{app}: release #{n} is live", { app: x.app ?? "", n: x.release ?? 0 }), a };
  }
  const a = "/ruta?tab=encargos";
  const detalle = x.title;
  const n = x.encargo ?? 0;
  switch (x.event) {
    case "created": return { tono: "info", titulo: t("New encargo #{n}", { n }), detalle, a };
    case "sent": return { tono: "info", titulo: t("Encargo #{n} sent to the coding agent", { n }), detalle, a };
    case "taken": return { tono: "info", titulo: t("{who} took encargo #{n}", { who: x.who ?? "", n }), detalle, a };
    case "delivered": return { tono: "ok", titulo: t("{who} delivered encargo #{n}", { who: x.who ?? "", n }), detalle, a };
    case "released": return { tono: "warn", titulo: t("{who} gave encargo #{n} back", { who: x.who ?? "", n }), detalle, a };
    case "returned": return { tono: "warn", titulo: t("Encargo #{n} was sent back", { n }), detalle, a };
    case "reviewed": return { tono: "ok", titulo: t("Encargo #{n} reviewed", { n }), detalle, a };
  }
  return undefined;
}

/** How long a notice stays: failures longer, so they are not missed. */
const dura: Record<Tono, number> = { ok: 6000, info: 5000, warn: 9000, bad: 12000 };

/**
 * Notices in a corner of every page as things happen in the organization: runs that start, pass or fail, releases
 * that go live or roll back, encargos that move. Each leads to what happened; the newest is on top, four at most.
 */
export function Avisos() {
  const [lista, setLista] = useState<Aviso[]>([]);
  const siguiente = useRef(1);
  useEffect(() => subscribe("/api/activity/stream", {
    activity: (x: Actividad) => {
      const v = aviso(x);
      if (!v) return;
      const id = siguiente.current++;
      setLista((l) => [{ id, ...v }, ...l].slice(0, 4));
      setTimeout(() => setLista((l) => l.filter((a) => a.id !== id)), dura[v.tono]);
    },
  }), []);
  const cerrar = (id: number) => setLista((l) => l.filter((a) => a.id !== id));
  return (
    <div className="avisos" role="status" aria-live="polite">
      {lista.map((v) => (
        <div key={v.id} className={`aviso ${v.tono}`}>
          <Link to={v.a} className="aviso-cuerpo" onClick={() => cerrar(v.id)}>
            <strong>{v.titulo}</strong>
            {v.detalle && <span className="small muted">{v.detalle}</span>}
          </Link>
          <button className="aviso-cerrar" aria-label={t("Close")} onClick={() => cerrar(v.id)}>×</button>
        </div>
      ))}
    </div>
  );
}
