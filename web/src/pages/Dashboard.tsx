import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, envApi, timeAgo, type App, session } from "../api";
import { ErrorBox, PhaseBadge, RunBadge, usePoll } from "../components/ui";
import { fmtPct } from "../components/reliability";
import { DeliveryPanel } from "../components/delivery";
import { VisitsPanel } from "../components/visits";
import { Flor, LluviaDePetalos } from "../components/ambiente";
import { t, tn } from "../i18n";

// Each app under an arch gets one of the papel picado colors for its band.
const bandas = ["var(--grana)", "var(--cempasuchil)", "var(--anil)", "var(--rosa)", "var(--nopal)", "var(--maiz)"];

/** An app is withered while one of its checks is in an outage, or its
 *  workloads are failing. */
export function marchita(a: App): boolean {
  return (a.down?.length ?? 0) > 0 || a.status.phase === "Error" || a.status.phase === "Degraded";
}

type Filtro = "todas" | "vivas" | "marchitas" | "fallaron";
const filtros: { id: Filtro; label: string; tone: string; test: (a: App) => boolean }[] = [
  { id: "todas", label: "All of them", tone: "var(--text)", test: () => true },
  { id: "vivas", label: "In bloom", tone: "var(--ok)", test: (a) => !marchita(a) },
  { id: "marchitas", label: "Withered", tone: "var(--bad)", test: marchita },
  { id: "fallaron", label: "Last run failed", tone: "var(--warn)", test: (a) => a.lastRun?.status === "failed" },
];

/** Whether an app matches what was searched: its name, repository or addresses. */
function coincide(a: App, q: string): boolean {
  const s = q.trim().toLowerCase();
  if (!s) return true;
  const urls = (a.status.services ?? []).flatMap((x) => [x.url, x.lanURL]);
  return [a.name, a.repo, ...urls].some((v) => v?.toLowerCase().includes(s));
}

/** The text with what was searched marked. */
function resaltar(text: string, q: string): ReactNode {
  const s = q.trim().toLowerCase();
  const at = s ? text.toLowerCase().indexOf(s) : -1;
  if (at < 0) return text;
  return <>{text.slice(0, at)}<mark>{text.slice(at, at + s.length)}</mark>{text.slice(at + s.length)}</>;
}

/** The Apps page: every app with its state, last run, uptime and visitors, and the delivery numbers. */
export function Dashboard() {
  const { data: apps, error } = usePoll(api.apps, [], 10000);
  const nav = useNavigate();
  const { data: env } = usePoll(() => envApi.report(), [], 60000);

  // Petals fall when an app's live release changes while the page is open.
  const [lluvia, setLluvia] = useState(0);
  const releases = useRef<Map<string, number> | undefined>(undefined);
  useEffect(() => {
    if (!apps) return;
    const now = new Map(apps.map((a) => [a.name, a.status.release ?? 0]));
    const before = releases.current;
    if (before && apps.some((a) => (a.status.release ?? 0) > (before.get(a.name) ?? Infinity))) setLluvia(Date.now());
    releases.current = now;
  }, [apps]);

  const caidas = apps?.filter(marchita).length ?? 0;
  const vivas = (apps?.length ?? 0) - caidas;

  // The search and the filter live in the address (?q=…&ver=…), so a view can be shared and survives a reload.
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const ver = (filtros.find((f) => f.id === params.get("ver"))?.id ?? "todas") as Filtro;
  const poner = (k: string, v: string) => setParams((p) => { if (v) p.set(k, v); else p.delete(k); return p; }, { replace: true });
  const filtro = filtros.find((f) => f.id === ver)!;
  // Withered first, then by name; each keeps the band color of its place.
  const shown = (apps ?? []).map((a, i) => ({ a, i }))
    .filter(({ a }) => coincide(a, q) && filtro.test(a))
    .sort((x, y) => Number(marchita(y.a)) - Number(marchita(x.a)) || x.a.name.localeCompare(y.a.name));

  // "/" jumps to the search box; Escape clears it.
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      if (e.key === "/" && !typing) { e.preventDefault(); box.current?.focus(); }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);

  return (
    <>
      <LluviaDePetalos cuando={lluvia} />
      <div className="hero-vida">
        <div>
          <h1>{t("Your apps,")} <em>{t("alive")}</em>.</h1>
          <p className="sub" style={{ marginBottom: 0 }}>
            {apps && (caidas > 0
              ? `${tn(vivas, "{n} blooming", "{n} blooming")} · ${tn(caidas, "{n} withered", "{n} withered")}`
              : tn(vivas, "{n} app blooming on the internet", "{n} apps blooming on the internet"))}
          </p>
        </div>
        <svg className="latido" width="300" height="64" viewBox="0 0 300 64" aria-hidden="true">
          <path d="M0 40 L90 40 L102 40 L110 20 L122 60 L132 4 L144 50 L154 40 L220 40 L228 30 L236 40 L300 40" fill="none" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </div>
      <ErrorBox error={error} />
      {env && env.overall !== "ok" && (
        <Link to="/environment" className={`card env-hero ${env.overall === "warning" ? "warn" : "bad"}`} style={{ marginBottom: 16, color: "var(--text)" }}>
          <span className={`badge ${env.overall === "warning" ? "warn" : "bad"}`}>{t("Environment")}</span>
          <span>{env.summary}</span>
          <span className="small" style={{ color: "var(--link)" }}>{t("View details →")}</span>
        </Link>
      )}
      {apps && apps.length === 0 && (
        <div className="card empty">
          <p>{t("No apps yet.")}</p>
          <Link to="/new" className="btn primary big">{t("Deploy your first app")}</Link>
        </div>
      )}
      {apps && apps.length > 0 && (
        <>
          <div className="mosaico-cuentas" role="group" aria-label={t("Show")}>
            {filtros.map((f) => (
              <button key={f.id} className="mosaico-cuenta" aria-pressed={ver === f.id} onClick={() => poner("ver", f.id === "todas" ? "" : f.id)}>
                <span>{t(f.label)}</span>
                <b style={{ color: f.tone }}>{apps.filter((a) => coincide(a, q) && f.test(a)).length}</b>
              </button>
            ))}
          </div>
          <label className="mosaico-buscar">
            <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="M15.5 15.5 21 21" /></svg>
            <input ref={box} type="search" value={q} onChange={(e) => poner("q", e.target.value)} onKeyDown={(e) => { if (e.key === "Escape") poner("q", ""); }}
              placeholder={t("Search by name, repository or address")} aria-label={t("Search apps")} />
            <kbd aria-hidden="true">/</kbd>
          </label>
          {shown.length === 0 && (
            <div className="card empty">
              <p>{q ? t("No app matches “{q}”.", { q }) : t("No app here.")}</p>
              <button onClick={() => setParams({}, { replace: true })}>{t("Show them all")}</button>
            </div>
          )}
        </>
      )}
      {/* The key replays the entrance when the view changes. */}
      <div className="mosaico" key={`${ver}|${q}`}>
        {shown.map(({ a, i }, n) => {
          const url = (a.status.services ?? []).map((s) => s.url ?? s.lanURL).find((u): u is string => !!u);
          const caida = marchita(a);
          const banda = bandas[i % bandas.length];
          const estado = caida
            ? (a.down?.length ? t("Withered: {checks}", { checks: a.down.join(", ") }) : t("Withered: its workloads are failing"))
            : a.uptime24h !== undefined
              ? t("Blooming · {pct} available", { pct: fmtPct(Math.round(a.uptime24h * 100000), 100000) })
              : t("Blooming");
          return (
            <article key={a.id} className={`teja${caida ? " caida" : ""}`} style={{ ["--banda" as string]: banda, ["--i" as string]: Math.min(n, 12) }}
              onClick={() => nav(`/apps/${a.name}`)}>
              <div className="teja-picado" aria-hidden="true" />
              <div className="teja-cuerpo">
                <div className="teja-nombre">
                  <Flor estado={caida ? "marchita" : "viva"} size={30} label={caida ? t("Withered flower") : t("Blooming flower")} />
                  <h3><Link to={`/apps/${a.name}`} onClick={(e) => e.stopPropagation()}>{resaltar(a.name, q)}</Link></h3>
                </div>
                <div className={`teja-estado ${caida ? "mal" : "bien"}`} title={t("Share of successful uptime checks in the last 24 hours")}>{estado}</div>
                {url && <a className="teja-url small" href={url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>{resaltar(url.replace(/^https?:\/\//, ""), q)}</a>}
                {a.lastRun && (
                  <div className="row small muted" title={`${a.lastRun.branch} · ${a.lastRun.sha.slice(0, 7)}`}>
                    <RunBadge status={a.lastRun.status} />
                    <span>{timeAgo(a.lastRun.createdAt)}</span>
                  </div>
                )}
                {a.status.phase !== "Healthy" && !caida && <PhaseBadge status={a.status} />}
              </div>
            </article>
          );
        })}
      </div>
      {apps && apps.length > 0 && <div style={{ marginTop: 28 }}><VisitsPanel />{session.operator && <DeliveryPanel />}</div>}
    </>
  );
}
