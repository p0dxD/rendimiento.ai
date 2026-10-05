// The Problems page: rendimiento's own warnings and errors from the last 30
// days, repeats grouped, open ones first. Problems about an app also show
// as a banner on its page (AppProblems).
import { Link, NavLink, useSearchParams } from "react-router-dom";
import { useState } from "react";
import { api, timeAgo, type Problem } from "../api";
import { ErrorBox, usePoll } from "../components/ui";

function isOpen(p: Problem, openForHours: number) {
  return !p.dismissedAt && Date.now() - new Date(p.lastAt).getTime() < openForHours * 3600_000;
}

/** "rendimiento.yaml rejected; the push was not built: services[0]: …" */
function headline(p: Problem) {
  return p.detail ? `${p.message}: ${p.detail}` : p.message;
}

function ProblemCard({ p, open, onDismiss }: { p: Problem; open: boolean; onDismiss: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="card stack problem">
      <div className="row between" style={{ alignItems: "flex-start" }}>
        <div className="row" style={{ alignItems: "baseline", flexWrap: "wrap" }}>
          <span className={`badge ${p.level === "ERROR" ? "bad" : "warn"}`}>{p.level === "ERROR" ? "Error" : "Warning"}</span>
          <strong>{p.message}</strong>
        </div>
        {open && (
          <button disabled={busy} title="Hide it until it happens again"
            onClick={() => { setBusy(true); api.dismissProblem(p.id).then(onDismiss, (e) => { setBusy(false); alert(e.message); }); }}>
            Dismiss
          </button>
        )}
        {p.dismissedAt && <span className="small muted">dismissed {timeAgo(p.dismissedAt)}</span>}
      </div>
      {p.detail && <pre className="problem-detail mono">{p.detail}</pre>}
      <div className="small muted">
        {p.app && <><Link to={`/apps/${p.app}`}>{p.app}</Link> · </>}
        {p.component && <>{p.component} · </>}
        {p.count > 1 ? `${p.count} times since ${timeAgo(p.firstAt)}, last ${timeAgo(p.lastAt)}` : timeAgo(p.lastAt)}
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
      <h1>Problems</h1>
      <p className="sub">
        Warnings and errors from rendimiento itself in the last 30 days: configs it rejected, emails it could not send, DNS or
        GitHub calls that failed. Repeats are grouped. A problem stays open for {data?.openForHours ?? 24} hours after it last
        happened, or until dismissed; it reopens if it happens again. Outages of your apps are on each app's Reliability tab.
      </p>
      <div className="rel-filters">
        <label className="small">App{" "}
          <select value={app} onChange={(e) => setParams(e.target.value ? { app: e.target.value } : {})}>
            <option value="">All apps and the platform</option>
            {apps?.map((a) => <option key={a.id} value={a.name}>{a.name}</option>)}
          </select>
        </label>
        <label className="small row" style={{ gap: 6 }}>
          <input type="checkbox" checked={dismissed} onChange={(e) => setDismissed(e.target.checked)} /> Show dismissed
        </label>
      </div>
      <ErrorBox error={error} />
      {data && data.problems.length === 0 && (
        <div className="card empty"><p>No problems in the last 30 days{app ? ` for ${app}` : ""}.</p></div>
      )}
      {open.length > 0 && (
        <section className="stack" style={{ marginBottom: 24 }}>
          <h2 className="problem-h">Open ({open.length})</h2>
          {open.map((p) => <ProblemCard key={p.id} p={p} open onDismiss={reload} />)}
        </section>
      )}
      {earlier.length > 0 && (
        <section className="stack">
          <h2 className="problem-h">Earlier</h2>
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
      <strong>rendimiento had {open.length === 1 ? "a problem" : `${open.length} problems`} with this app</strong>
      <ul className="problem-list">
        {open.slice(0, 3).map((p) => (
          <li key={p.id}>{headline(p)}{p.count > 1 ? ` (${p.count} times)` : ""} · {timeAgo(p.lastAt)}</li>
        ))}
      </ul>
      <Link to={`/problems?app=${encodeURIComponent(name)}`}>See {open.length > 3 ? `all ${open.length}` : "details"} →</Link>
    </div>
  );
}

/** The navigation link, with the number of open problems. */
export function ProblemsNavLink() {
  const { data } = usePoll(api.problemCount, [], 30000);
  const n = data?.open ?? 0;
  return (
    <NavLink to="/problems" className="nav-link" aria-label={n ? `Problems, ${n} open` : "Problems"}>
      Problems{n > 0 && <span className="nav-count">{n}</span>}
    </NavLink>
  );
}
