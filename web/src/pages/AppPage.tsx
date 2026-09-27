import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, duration, secretNames, subscribe, timeAgo } from "../api";
import { ErrorBox, PhaseBadge, ResourceTree, RunBadge, usePoll } from "../components/ui";

const tabs = [
  { id: "", label: "Overview" },
  { id: "runs", label: "Runs" },
  { id: "releases", label: "Releases" },
  { id: "settings", label: "Settings" },
];

export function AppPage() {
  const { name = "", tab = "" } = useParams();
  const { data: app, error, reload } = usePoll(() => api.app(name), [name], 5000);

  useEffect(() => subscribe(`/api/apps/${name}/events`, { run: reload, release: reload }), [name]);

  if (error) return <ErrorBox error={error} />;
  if (!app) return <p className="muted">Loading…</p>;

  return (
    <>
      <div className="row between">
        <div>
          <div className="row">
            <h1>{app.name}</h1>
            <PhaseBadge status={app.status} />
          </div>
          <p className="sub" style={{ marginBottom: 0 }}>
            <a href={`https://github.com/${app.repo}`} target="_blank" rel="noreferrer">{app.repo}</a>
            {app.status.release ? ` · release #${app.status.release}` : ""}
          </p>
        </div>
        <button onClick={() => api.triggerRun(name).then(reload, (e) => alert(e.message))}>Build now</button>
      </div>
      {app.status.message && app.status.phase !== "Healthy" && (
        <div className={`alert ${app.status.phase === "Error" || app.status.phase === "Degraded" ? "" : "info"}`} style={{ marginTop: 12 }}>{app.status.message}</div>
      )}
      <nav className="tabs">
        {tabs.map((t) => (
          <Link key={t.id} to={`/apps/${name}${t.id ? "/" + t.id : ""}`} className={tab === t.id ? "active" : ""}>{t.label}</Link>
        ))}
      </nav>
      {tab === "" && <Overview name={name} app={app} />}
      {tab === "runs" && <Runs name={name} />}
      {tab === "releases" && <Releases name={name} current={app.status.release} />}
      {tab === "settings" && <Settings name={name} secrets={[...new Set(app.spec.services.flatMap(secretNames))]} />}
    </>
  );
}

function Overview({ name, app }: { name: string; app: Awaited<ReturnType<typeof api.app>> }) {
  const { data: tree, error } = usePoll(() => api.resources(name), [name], 5000);
  return (
    <div className="stack">
      <div className="grid">
        {app.spec.services.map((svc) => {
          const st = app.status.services?.find((s) => s.name === svc.name);
          return (
            <div key={svc.name} className="card stack">
              <div className="row between">
                <strong>{svc.name}</strong>
                <span className="small muted">{st ? `${st.readyReplicas}/${st.replicas} ready` : "not deployed"}</span>
              </div>
              {svc.domain && <a href={`https://${svc.domain}`} target="_blank" rel="noreferrer">https://{svc.domain}</a>}
              {svc.domain && st && (
                <div className="row small">
                  <span className={`badge ${st.dnsReady ? "ok" : "warn"}`}>DNS</span>
                  <span className={`badge ${st.certReady ? "ok" : "warn"}`}>TLS certificate</span>
                </div>
              )}
              {st?.message && <div className="small" style={{ color: "var(--bad)" }}>{st.message}</div>}
              {st?.image && <div className="mono small muted" style={{ wordBreak: "break-all" }}>{st.image.split("/").pop()}</div>}
            </div>
          );
        })}
      </div>
      <h2>Resources</h2>
      <ErrorBox error={error} />
      {tree && <ResourceTree node={tree} />}
      {app.lastRun && (
        <>
          <h2>Latest run</h2>
          <Link to={`/apps/${name}/runs/${app.lastRun.id}`} className="card row">
            <RunBadge status={app.lastRun.status} />
            <span className="mono">{app.lastRun.sha.slice(0, 7)}</span>
            <span>{app.lastRun.branch}</span>
            <span className="muted small">{app.lastRun.message}</span>
            <span className="muted small">{timeAgo(app.lastRun.createdAt)}</span>
          </Link>
        </>
      )}
    </div>
  );
}

function Runs({ name }: { name: string }) {
  const { data: runs, error } = usePoll(() => api.runs(name), [name], 5000);
  const nav = useNavigate();
  return (
    <>
      <ErrorBox error={error} />
      {runs && runs.length === 0 && <div className="empty">No runs yet. Merge the onboarding PR or press “Build now”.</div>}
      {runs && runs.length > 0 && (
        <div className="card table-wrap" style={{ padding: 0 }}>
          <table>
            <thead><tr><th>#</th><th>Status</th><th>Commit</th><th>Branch</th><th className="hide-sm">Result</th><th>Duration</th><th>Started</th></tr></thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id} className="clickable" onClick={() => nav(`/apps/${name}/runs/${r.id}`)}>
                  <td>{r.id}</td>
                  <td><RunBadge status={r.status} /></td>
                  <td className="mono">{r.sha.slice(0, 7)}</td>
                  <td>{r.branch}{r.deploy && <span className="badge" style={{ marginLeft: 6 }}>deploys</span>}</td>
                  <td className="small muted hide-sm">{r.message}</td>
                  <td className="small">{duration(r.startedAt, r.finishedAt)}</td>
                  <td className="small muted">{timeAgo(r.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function Releases({ name, current }: { name: string; current?: number }) {
  const { data: rels, error, reload } = usePoll(() => api.releases(name), [name], 10000);
  const [busy, setBusy] = useState<number>();
  const rollback = async (n: number) => {
    if (!confirm(`Roll back ${name} to release #${n}? This creates a new release with #${n}'s images.`)) return;
    setBusy(n);
    try {
      await api.rollback(name, n);
      reload();
    } catch (e) {
      alert((e as Error).message);
    } finally {
      setBusy(undefined);
    }
  };
  return (
    <>
      <ErrorBox error={error} />
      {rels && rels.length === 0 && <div className="empty">No releases yet. A release is created by every successful build of the default branch.</div>}
      <ul className="timeline">
        {rels?.map((r) => (
          <li key={r.id}>
            <span className={`dot ${r.number === current ? "current" : ""}`} />
            <div>
              <div className="row">
                <strong>Release #{r.number}</strong>
                {r.number === current && <span className="badge ok">live</span>}
                {r.rollbackOf && <span className="badge warn">rollback to #{r.rollbackOf}</span>}
              </div>
              <div className="small muted">
                <span className="mono">{r.sha.slice(0, 7)}</span> · {timeAgo(r.createdAt)}
                {r.runId && <> · <Link to={`/apps/${name}/runs/${r.runId}`}>run #{r.runId}</Link></>}
              </div>
            </div>
            {r.number !== current && (
              <button disabled={busy !== undefined} onClick={() => rollback(r.number)}>{busy === r.number ? "Rolling back…" : "Roll back"}</button>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

function Settings({ name, secrets }: { name: string; secrets: string[] }) {
  const nav = useNavigate();
  return (
    <div className="stack">
      <h2 style={{ marginTop: 0 }}>Secrets</h2>
      {secrets.length === 0 && <p className="muted">This app declares no secrets. Add names under <code>secrets:</code> in rendimiento.yaml.</p>}
      {secrets.map((s) => <SecretForm key={s} app={name} secret={s} />)}
      <h2>Danger zone</h2>
      <DangerZone name={name} onGone={() => nav("/")} />
    </div>
  );
}

function DangerZone({ name, onGone }: { name: string; onGone: () => void }) {
  const { data: impact } = usePoll(() => api.deleteImpact(name), [name]);
  const [busy, setBusy] = useState(false);
  const run = async (what: "disconnect" | "delete") => {
    if (prompt(`Type ${name} to ${what} it`) !== name) return;
    setBusy(true);
    try {
      await (what === "delete" ? api.deleteApp(name) : api.disconnectApp(name));
      onGone();
    } catch (e) {
      alert((e as Error).message);
      setBusy(false);
    }
  };
  return (
    <div className="stack">
      <div className="card row between">
        <div style={{ flex: 1, minWidth: 240 }}>
          <strong>Disconnect</strong>
          <div className="small muted">
            Stop managing this app with rendimiento. Everything keeps running as it is now (pods, volumes, secrets, DNS), with no
            rendimiento labels or ownership, so it can be managed another way or migrated back later. Builds and deploys stop.
          </div>
        </div>
        <button disabled={busy} onClick={() => run("disconnect")}>Disconnect</button>
      </div>
      <div className="card row between">
        <div style={{ flex: 1, minWidth: 240 }}>
          <strong>Delete app</strong>
          <div className="small muted">
            {impact?.sharedNamespace
              ? <>Deletes what rendimiento runs in namespace <code>{name}</code>; the namespace and everything else in it stay. Also removes the DNS records rendimiento created and all history.</>
              : <>Deletes the namespace <code>{name}</code> and everything in it, the DNS records rendimiento created, and all history.</>}
            {" "}The GitHub repo is not touched.
          </div>
          {impact && (impact.volumes.length > 0 || impact.secrets.length > 0) && (
            <div className="alert" style={{ marginTop: 8 }}>
              <strong>Permanently lost:</strong>
              {impact.volumes.length > 0 && <div className="small">Volumes and their data: {impact.volumes.join(", ")}</div>}
              {impact.secrets.length > 0 && <div className="small">Secrets: {impact.secrets.join(", ")}</div>}
              {impact.adopted && <div className="small">This app was migrated: these include data and secrets that existed before rendimiento.</div>}
            </div>
          )}
        </div>
        <button className="danger" disabled={busy} onClick={() => run("delete")}>Delete</button>
      </div>
    </div>
  );
}

function SecretForm({ app, secret }: { app: string; secret: string }) {
  const [text, setText] = useState("");
  const [state, setState] = useState<"idle" | "saving" | "saved">("idle");
  const [error, setError] = useState<unknown>();
  const save = async () => {
    const data: Record<string, string> = {};
    for (const line of text.split("\n")) {
      const i = line.indexOf("=");
      if (i > 0) data[line.slice(0, i).trim()] = line.slice(i + 1);
    }
    setState("saving");
    setError(undefined);
    try {
      await api.putSecret(app, secret, data);
      setText("");
      setState("saved");
    } catch (e) {
      setError(e);
      setState("idle");
    }
  };
  return (
    <div className="card stack">
      <strong className="mono">{secret}</strong>
      <p className="small muted" style={{ margin: 0 }}>Values are write-only: they replace the whole secret and restart the app. They are never shown again.</p>
      <textarea rows={3} className="mono" value={text} onChange={(e) => { setText(e.target.value); setState("idle"); }} placeholder={"KEY=value\nOTHER_KEY=value"} autoComplete="off" spellCheck={false} />
      <ErrorBox error={error} />
      <div className="row">
        <button className="primary" disabled={!text.includes("=") || state === "saving"} onClick={save}>{state === "saving" ? "Saving…" : "Save secret"}</button>
        {state === "saved" && <span className="badge ok">saved, app restarting</span>}
      </div>
    </div>
  );
}
