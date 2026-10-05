// The Problems page: rendimiento's own warnings and errors from the last 30
// days, repeats grouped, open ones first. Problems about an app also show
// as a banner on its page (AppProblems).
import { Link, NavLink, useSearchParams } from "react-router-dom";
import { useState } from "react";
import { api, timeAgo, type Problem } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t, tn } from "../i18n";

function isOpen(p: Problem, openForHours: number) {
  return !p.dismissedAt && !p.resolvedAt && Date.now() - new Date(p.lastAt).getTime() < openForHours * 3600_000;
}

/** "rendimiento.yaml rejected; the push was not built: services[0]: …" */
function headline(p: Problem) {
  return p.detail ? `${t(p.message)}: ${p.detail}` : t(p.message);
}

/** The server's note on what fixed a problem, e.g. "fixed by c6eae54". */
function resolutionText(r: string) {
  const m = /^fixed by (\w+)$/.exec(r);
  return m ? t("fixed by {sha}", { sha: m[1] }) : t(r);
}

function ProblemCard({ p, open, onDismiss }: { p: Problem; open: boolean; onDismiss: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="card stack problem">
      <div className="row between" style={{ alignItems: "flex-start" }}>
        <div className="row" style={{ alignItems: "baseline", flexWrap: "wrap" }}>
          <span className={`badge ${p.level === "ERROR" ? "bad" : "warn"}`}>{p.level === "ERROR" ? t("Error") : t("Warning")}</span>
          <strong>{t(p.message)}</strong>
        </div>
        {open && (
          <button disabled={busy} title={t("Hide it until it happens again")}
            onClick={() => { setBusy(true); api.dismissProblem(p.id).then(onDismiss, (e) => { setBusy(false); alert(e.message); }); }}>
            {t("Dismiss")}
          </button>
        )}
        {p.resolvedAt ? (
          <span className="small problem-resolved">✓ {t("Resolved {when}", { when: timeAgo(p.resolvedAt) })}{p.resolution ? `: ${resolutionText(p.resolution)}` : ""}</span>
        ) : p.dismissedAt && <span className="small muted">{t("dismissed {when}", { when: timeAgo(p.dismissedAt) })}</span>}
      </div>
      {p.detail && <pre className="problem-detail mono">{p.detail}</pre>}
      <div className="small muted">
        {p.app && <><Link to={`/apps/${p.app}`}>{p.app}</Link> · </>}
        {p.component && <>{p.component} · </>}
        {p.count > 1 ? t("{n} times since {first}, last {last}", { n: p.count, first: timeAgo(p.firstAt), last: timeAgo(p.lastAt) }) : timeAgo(p.lastAt)}
        {p.attrs && <span className="mono"> · {p.attrs}</span>}
      </div>
    </div>
  );
}

export function Problems() {
  const [params, setParams] = useSearchParams();
  const app = params.get("app") ?? "";
  const [dismissed, setDismissed] = useState(false);
  const { data, error, reload } = usePoll(() => api.problems({ app, dismissed }), [app, dismissed], 15000);
  const { data: apps } = usePoll(api.apps, [], 0);
  const open = data?.problems.filter((p) => isOpen(p, data.openForHours)) ?? [];
  const earlier = data?.problems.filter((p) => !isOpen(p, data.openForHours)) ?? [];

  return (
    <>
      <h1>{t("Problems")}</h1>
      <p className="sub">
        {t("Warnings and errors from rendimiento itself in the last 30 days: configs it rejected, emails it could not send, DNS or GitHub calls that failed. Repeats are grouped. A problem stays open for {n} hours after it last happened, until rendimiento sees it fixed (a later push accepted, a later email sent), or until dismissed; it reopens if it happens again. Outages of your apps are on each app's Reliability tab.", { n: data?.openForHours ?? 24 })}
      </p>
      <div className="rel-filters">
        <label className="small">{t("App")}{" "}
          <select value={app} onChange={(e) => setParams(e.target.value ? { app: e.target.value } : {})}>
            <option value="">{t("All apps and the platform")}</option>
            {apps?.map((a) => <option key={a.id} value={a.name}>{a.name}</option>)}
          </select>
        </label>
        <label className="small row" style={{ gap: 6 }}>
          <input type="checkbox" checked={dismissed} onChange={(e) => setDismissed(e.target.checked)} /> {t("Show dismissed")}
        </label>
      </div>
      <ErrorBox error={error} />
      {data && data.problems.length === 0 && (
        <div className="card empty"><p>{app ? t("No problems in the last 30 days for {app}.", { app }) : t("No problems in the last 30 days.")}</p></div>
      )}
      {open.length > 0 && (
        <section className="stack" style={{ marginBottom: 24 }}>
          <h2 className="problem-h">{t("Open ({n})", { n: open.length })}</h2>
          {open.map((p) => <ProblemCard key={p.id} p={p} open onDismiss={reload} />)}
        </section>
      )}
      {earlier.length > 0 && (
        <section className="stack">
          <h2 className="problem-h">{t("Earlier")}</h2>
          {earlier.map((p) => <ProblemCard key={p.id} p={p} open={false} onDismiss={reload} />)}
        </section>
      )}
    </>
  );
}

/** Open problems about one app, as a banner on its page. */
export function AppProblems({ name }: { name: string }) {
  const { data } = usePoll(() => api.problems({ app: name }), [name], 30000);
  const open = data?.problems.filter((p) => isOpen(p, data.openForHours)) ?? [];
  if (open.length === 0) return null;
  return (
    <div className="alert" style={{ marginTop: 12 }} role="status">
      <strong>{tn(open.length, "rendimiento had a problem with this app", "rendimiento had {n} problems with this app")}</strong>
      <ul className="problem-list">
        {open.slice(0, 3).map((p) => (
          <li key={p.id}>{headline(p)}{p.count > 1 ? ` (${t("{n} times", { n: p.count })})` : ""} · {timeAgo(p.lastAt)}</li>
        ))}
      </ul>
      <Link to={`/problems?app=${encodeURIComponent(name)}`}>{open.length > 3 ? t("See all {n} →", { n: open.length }) : t("See details →")}</Link>
    </div>
  );
}

/** The navigation link, with the number of open problems. */
export function ProblemsNavLink() {
  const { data } = usePoll(api.problemCount, [], 30000);
  const n = data?.open ?? 0;
  return (
    <NavLink to="/problems" className="nav-link" aria-label={n ? t("Problems, {n} open", { n }) : t("Problems")}>
      {t("Problems")}{n > 0 && <span className="nav-count">{n}</span>}
    </NavLink>
  );
}
