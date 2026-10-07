import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, envApi, timeAgo, type App } from "../api";
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
function marchita(a: App): boolean {
  return (a.down?.length ?? 0) > 0 || a.status.phase === "Error" || a.status.phase === "Degraded";
}

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
      <div className="portales">
        {apps?.map((a, i) => {
          const urls = (a.status.services ?? []).flatMap((s) =>
            [s.url, s.lanURL].filter((u): u is string => !!u).map((u) => ({ name: s.name + u, url: u })));
          const caida = marchita(a);
          const banda = bandas[i % bandas.length];
          const estado = caida
            ? (a.down?.length ? t("Withered: {checks}", { checks: a.down.join(", ") }) : t("Withered: its workloads are failing"))
            : a.uptime24h !== undefined
              ? t("Blooming · {pct} available", { pct: fmtPct(Math.round(a.uptime24h * 100000), 100000) })
              : t("Blooming");
          return (
            <article key={a.id} className={`portal${caida ? " caida" : ""}`} style={{ ["--glow" as string]: caida ? "transparent" : banda }}
              onClick={() => nav(`/apps/${a.name}`)}>
              <Flor estado={caida ? "marchita" : "viva"} size={60} label={caida ? t("Withered flower") : t("Blooming flower")} />
              <div className="portal-body">
                <h3><Link to={`/apps/${a.name}`} onClick={(e) => e.stopPropagation()} style={{ color: "var(--text)" }}>{a.name}</Link></h3>
                <div className="small" style={{ fontWeight: 700, color: caida ? "var(--bad)" : "var(--ok)" }}
                  title={t("Share of successful uptime checks in the last 24 hours")}>{estado}</div>
                <div className="small muted">{a.repo} · {tn(a.spec.services.length, "{n} service", "{n} services")}
                  {a.status.release ? ` · ${t("release #{n}", { n: a.status.release })}` : ""}</div>
                {urls.map((s) => (
                  <a key={s.name} href={s.url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="small">{s.url}</a>
                ))}
                {a.lastRun && (
                  <div className="row small muted" style={{ justifyContent: "center" }}>
                    <RunBadge status={a.lastRun.status} />
                    <span className="mono">{a.lastRun.sha.slice(0, 7)}</span>
                    <span>{a.lastRun.branch}</span>
                    <span>{timeAgo(a.lastRun.createdAt)}</span>
                  </div>
                )}
                {a.status.phase !== "Healthy" && <PhaseBadge status={a.status} />}
              </div>
              <div className="portal-band" style={{ background: banda, boxShadow: `0 -2px 0 ${banda}` }} aria-hidden="true" />
            </article>
          );
        })}
      </div>
      {apps && apps.length > 0 && <div style={{ marginTop: 28 }}><VisitsPanel /><DeliveryPanel /></div>}
    </>
  );
}
