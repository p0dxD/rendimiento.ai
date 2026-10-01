import { Link, useNavigate } from "react-router-dom";
import { api, envApi, timeAgo } from "../api";
import { ErrorBox, PhaseBadge, RunBadge, usePoll } from "../components/ui";
import { fmtPct } from "../components/reliability";

export function Dashboard() {
  const { data: apps, error } = usePoll(api.apps, [], 10000);
  const nav = useNavigate();
  const { data: env } = usePoll(() => envApi.report(), [], 60000);

  return (
    <>
      <div className="row between">
        <div>
          <h1>Apps</h1>
          <p className="sub">Everything rendimiento builds and runs.</p>
        </div>
      </div>
      <ErrorBox error={error} />
      {env && env.overall !== "ok" && (
        <Link to="/environment" className={`card env-hero ${env.overall === "warning" ? "warn" : "bad"}`} style={{ marginBottom: 16, color: "var(--text)" }}>
          <span className={`badge ${env.overall === "warning" ? "warn" : "bad"}`}>Environment</span>
          <span>{env.summary}</span>
          <span className="small" style={{ color: "var(--accent)" }}>View details →</span>
        </Link>
      )}
      {apps && apps.length === 0 && (
        <div className="card empty">
          <p>No apps yet.</p>
          <Link to="/new" className="btn primary big">Deploy your first app</Link>
        </div>
      )}
      <div className="grid">
        {apps?.map((a) => {
          const urls = (a.status.services ?? []).flatMap((s) =>
            [s.url, s.lanURL].filter((u): u is string => !!u).map((u) => ({ name: s.name + u, url: u })));
          return (
            <div key={a.id} className="card stack" style={{ cursor: "pointer" }} onClick={() => nav(`/apps/${a.name}`)}>
              <div className="row between">
                <strong style={{ fontSize: 16 }}>{a.name}</strong>
                <PhaseBadge status={a.status} />
              </div>
              {a.uptime24h !== undefined && (
                <div className="small muted" title="Share of successful uptime checks in the last 24 hours">
                  <span className={`badge ${a.uptime24h >= 0.999 ? "ok" : a.uptime24h >= 0.98 ? "warn" : "bad"}`}>{fmtPct(Math.round(a.uptime24h * 100000), 100000)} up</span> last 24 h
                </div>
              )}
              <div className="small muted">{a.repo} · {a.spec.services.length} service{a.spec.services.length > 1 ? "s" : ""}
                {a.status.release ? ` · release #${a.status.release}` : ""}</div>
              {urls.map((s) => (
                <a key={s.name} href={s.url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="small">{s.url}</a>
              ))}
              {a.lastRun && (
                <div className="row small muted">
                  <RunBadge status={a.lastRun.status} />
                  <span className="mono">{a.lastRun.sha.slice(0, 7)}</span>
                  <span>{a.lastRun.branch}</span>
                  <span>{timeAgo(a.lastRun.createdAt)}</span>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </>
  );
}
