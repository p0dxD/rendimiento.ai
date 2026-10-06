import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { addonsApi, api, appSecretNames, duration, subscribe, timeAgo, type Release, type ReleaseTask } from "../api";
import { ErrorBox, PhaseBadge, ResourceTree, RunBadge, Switch, usePoll } from "../components/ui";
import { ReliabilityTab } from "../components/reliability";
import { AppProblems } from "./Problems";
import { t, tn } from "../i18n";

const tabs = [
  { id: "", label: "Overview" }, // labels are translated where shown
  { id: "runs", label: "Runs" },
  { id: "releases", label: "Releases" },
  { id: "reliability", label: "Reliability" },
  { id: "settings", label: "Settings" },
];

export function AppPage() {
  const { name = "", tab = "" } = useParams();
  const { data: app, error, reload } = usePoll(() => api.app(name), [name], 5000);

  useEffect(() => subscribe(`/api/apps/${name}/events`, { run: reload, release: reload }), [name]);

  if (error) return <ErrorBox error={error} />;
  if (!app) return <p className="muted">{t("Loading…")}</p>;

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
            {app.status.release ? ` · ${t("release #{n}", { n: app.status.release })}` : ""}
          </p>
        </div>
        <button onClick={() => api.triggerRun(name).then(reload, (e) => alert(e.message))}>{t("Build now")}</button>
      </div>
      {app.status.message && app.status.phase !== "Healthy" && (
        <div className={`alert ${app.status.phase === "Error" || app.status.phase === "Degraded" ? "" : "info"}`} style={{ marginTop: 12 }}>{app.status.message}</div>
      )}
      <VerificationBanner name={name} />
      <AppProblems name={name} />
      <nav className="tabs">
        {tabs.map((tb) => (
          <Link key={tb.id} to={`/apps/${name}${tb.id ? "/" + tb.id : ""}`} className={tab === tb.id ? "active" : ""}>{t(tb.label)}</Link>
        ))}
      </nav>
      {tab === "" && <Overview name={name} app={app} />}
      {tab === "runs" && <Runs name={name} />}
      {tab === "releases" && <Releases name={name} current={app.status.release} />}
      {tab === "reliability" && <ReliabilityTab app={app} />}
      {tab === "settings" && <Settings name={name} secrets={appSecretNames(app.spec)} />}
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
                <span className="small muted">{st ? t("{ready}/{total} ready", { ready: st.readyReplicas, total: st.replicas }) : t("not deployed")}</span>
              </div>
              {svc.domain && <a href={`https://${svc.domain}`} target="_blank" rel="noreferrer">https://{svc.domain}</a>}
              {st?.lanURL && (
                <div className="row small">
                  <a href={st.lanURL} target="_blank" rel="noreferrer">{st.lanURL}</a>
                  <span className="badge">{t("local network")}</span>
                </div>
              )}
              {svc.lan && !st?.lanURL && <div className="small muted">{t("waiting for a local network address…")}</div>}
              {svc.domain && st && (
                <div className="row small">
                  <span className={`badge ${st.dnsReady ? "ok" : "warn"}`}>DNS</span>
                  <span className={`badge ${st.certReady ? "ok" : "warn"}`}>{t("TLS certificate")}</span>
                </div>
              )}
              {st?.message && <div className="small" style={{ color: "var(--bad)" }}>{st.message}</div>}
              {st?.image && <div className="mono small muted" style={{ wordBreak: "break-all" }}>{st.image.split("/").pop()}</div>}
            </div>
          );
        })}
      </div>
      <ProvidedNeeds name={name} app={app} />
      <DependencyUpdates name={name} />
      <h2>{t("Resources")}</h2>
      <ErrorBox error={error} />
      {tree && <ResourceTree node={tree} />}
      {app.lastRun && (
        <>
          <h2>{t("Latest run")}</h2>
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

const needTitle: Record<string, string> = { postgres: "PostgreSQL", redis: "Redis" };

/** The database and cache rendimiento runs because the app's services need them. */
function ProvidedNeeds({ name, app }: { name: string; app: Awaited<ReturnType<typeof api.app>> }) {
  const provided = (app.status.services ?? []).filter((s) => needTitle[s.name] && !app.spec.services.some((x) => x.name === s.name));
  if (provided.length === 0) return null;
  const users = (kind: string) => app.spec.services
    .map((svc) => {
      const n = (svc.needs ?? []).find((x) => x === kind || (typeof x === "object" && kind in x));
      if (!n) return null;
      const env = typeof n === "object" && kind in n ? (n as Record<string, { env: string }>)[kind].env : kind === "postgres" ? "DATABASE_URL" : "REDIS_URL";
      return `${svc.name} (${env}${kind === "postgres" ? ", PGHOST…" : ""})`;
    })
    .filter(Boolean)
    .join(", ");
  return (
    <>
      <h2>{t("Provided for this app")}</h2>
      <div className="grid">
        {provided.map((st) => (
          <div key={st.name} className="card stack">
            <div className="row between">
              <strong>{needTitle[st.name]}</strong>
              <span className={`badge ${st.readyReplicas >= 1 ? "ok" : "warn"}`}>{st.readyReplicas >= 1 ? t("running") : t("starting")}</span>
            </div>
            <div className="small muted">{st.image}{st.name === "postgres" ? ` · ${t("data on a Longhorn volume")}` : ` · ${t("in memory (a cache)")}`}</div>
            <div className="small">{users(st.name) ? t("Used by {list}", { list: users(st.name) }) : t("Used by no service yet")}</div>
            {st.message && <div className="small" style={{ color: "var(--bad)" }}>{st.message}</div>}
            <details className="small">
              <summary>{t("Connect from your machine")}</summary>
              <pre className="file" style={{ marginTop: 6 }}>{`kubectl -n ${name} port-forward svc/${st.name} ${st.name === "postgres" ? 5432 : 6379}
# ${t("address and password:")}
kubectl -n ${name} get secret ${st.name}-credentials -o jsonpath='{.data.uri}' | base64 -d`}</pre>
            </details>
          </div>
        ))}
      </div>
    </>
  );
}

/** The app's Renovate switch and what the last run did for its repo. */
function DependencyUpdates({ name }: { name: string }) {
  const { data, error, setData } = usePoll(() => addonsApi.forApp(name), [name], 30000);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  if (error || !data) return null;
  const r = data.renovate;
  const toggle = async (on: boolean) => {
    setBusy(true);
    setErr(undefined);
    try {
      setData(await addonsApi.setForApp(name, "renovate", on));
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  };
  const res = r.lastRun?.result;
  return (
    <div className="card stack">
      <div className="addon-head">
        <div>
          <strong>{t("Dependency updates")}</strong> <span className="small muted">Renovate</span>
          <div className="small muted">
            {!r.enabled && t("Off: this app's dependencies are not updated automatically.")}
            {r.enabled && !r.addonEnabled && <>{t("On for this app, but the Renovate add-on is switched off in")} <Link to="/addons">{t("Add-ons")}</Link>.</>}
            {r.enabled && r.addonEnabled && t("On: Renovate opens a pull request per update; patch and minor updates merge once rendimiento's checks pass.")}
          </div>
        </div>
        <Switch checked={r.enabled} disabled={busy} label={r.enabled ? t("Switch dependency updates off") : t("Switch dependency updates on")} onChange={toggle} />
      </div>
      <ErrorBox error={err} />
      {r.enabled && (
        <div className="row small">
          {r.lastRun ? (
            <>
              <RunBadge status={r.lastRun.status} />
              <span className="muted">{t("last run {when}", { when: timeAgo(r.lastRun.startedAt) })}{res?.result ? `: ${res.result}` : ""}</span>
              {res?.prs?.length ? <span>{tn(res.prs.length, "opened {n} PR", "opened {n} PRs")}</span> : null}
              {res?.automerged?.length ? <span>{t("merged {n}", { n: res.automerged.length })}</span> : null}
              {res?.errors?.length ? <span style={{ color: "var(--bad)" }}>{res.errors[0]}</span> : null}
            </>
          ) : <span className="muted">{t("Not run yet for this app.")}</span>}
          <a href={r.prsUrl} target="_blank" rel="noreferrer">{t("Open update PRs ↗")}</a>
          <Link to="/addons">{t("Add-on settings")}</Link>
        </div>
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
      {runs && runs.length === 0 && <div className="empty">{t("No runs yet. Merge the onboarding PR or press “Build now”.")}</div>}
      {runs && runs.length > 0 && (
        <div className="card table-wrap" style={{ padding: 0 }}>
          <table>
            <thead><tr><th>#</th><th>{t("Status")}</th><th>{t("Commit")}</th><th>{t("Branch")}</th><th className="hide-sm">{t("Result")}</th><th>{t("Duration")}</th><th>{t("Started")}</th></tr></thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id} className="clickable" onClick={() => nav(`/apps/${name}/runs/${r.id}`)}>
                  <td>{r.id}</td>
                  <td><RunBadge status={r.status} /></td>
                  <td className="mono">{r.sha.slice(0, 7)}</td>
                  <td>{r.branch}{r.deploy && <span className="badge" style={{ marginLeft: 6 }}>{t("deploys")}</span>}</td>
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
    if (!confirm(t("Roll back {name} to release #{n}? This creates a new release with #{n}'s images.", { name, n }))) return;
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
      {rels && rels.length === 0 && <div className="empty">{t("No releases yet. A release is created by every successful build of the default branch.")}</div>}
      <ul className="timeline">
        {rels?.map((r) => (
          <li key={r.id}>
            <span className={`dot ${r.number === current ? "current" : ""}`} />
            <div>
              <div className="row">
                <strong>{t("Release #{n}", { n: r.number })}</strong>
                {r.number === current && <span className="badge ok">{t("live")}</span>}
                {r.rollbackOf && <span className="badge warn">{t("rollback to #{n}", { n: r.rollbackOf })}</span>}
                <VerifyBadge r={r} />
              </div>
              {(r.tasks?.length ?? 0) > 0 && (
                <div className="stack" style={{ marginTop: 6 }}>
                  {r.tasks!.map((task) => <PostDeployTask key={task.name} app={name} release={r.number} task={task} />)}
                </div>
              )}
              {r.verifyMessage && r.verifyStatus !== "skipped" && r.verifyStatus !== "superseded" && (
                <div className="small" style={{ color: r.verifyStatus === "failed" || r.verifyStatus === "failed-kept" || r.verifyStatus === "blocked" ? "var(--bad)" : "var(--muted)" }}>{r.verifyMessage}</div>
              )}
              <div className="small muted">
                <span className="mono">{r.sha.slice(0, 7)}</span> · {timeAgo(r.createdAt)}
                {r.runId && <> · <Link to={`/apps/${name}/runs/${r.runId}`}>{t("run #{n}", { n: r.runId })}</Link></>}
              </div>
            </div>
            {r.number !== current && r.verifyStatus !== "blocked" && (
              <button disabled={busy !== undefined} onClick={() => rollback(r.number)}>{busy === r.number ? t("Rolling back…") : t("Roll back")}</button>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

const taskBadge: Record<ReleaseTask["status"], [string, string]> = {
  running: ["live", "Running"], succeeded: ["ok", "Passed"], failed: ["bad", "Failed"], skipped: ["", "Skipped"], // translated where shown
};

/** One post-deploy task under its release: status, duration, and its log on demand. */
function PostDeployTask({ app, release, task }: { app: string; release: number; task: ReleaseTask }) {
  const [log, setLog] = useState<string>();
  const [open, setOpen] = useState(false);
  const toggle = async () => {
    if (!open && log === undefined) setLog(await api.releaseTaskLog(app, release, task.name).catch((e) => String(e)));
    setOpen(!open);
  };
  const [tone, label] = taskBadge[task.status];
  return (
    <div className="small">
      <div className="row" style={{ gap: 8 }}>
        <span className="muted">{t(task.stage)}</span>
        <strong className="mono">{task.name}</strong>
        <span className={`badge ${tone}`}>{t(label)}{task.optional && task.status === "failed" ? ` (${t("optional")})` : ""}</span>
        {task.startedAt && task.finishedAt && <span className="muted">{duration(task.startedAt, task.finishedAt)}</span>}
        {task.status !== "skipped" && <button className="chip-btn" onClick={toggle}>{open ? t("Hide log") : t("Log")}</button>}
      </div>
      {task.message && task.status !== "succeeded" && <div style={{ color: task.status === "failed" ? "var(--bad)" : "var(--muted)" }}>{task.message}</div>}
      {open && <pre className="log" style={{ maxHeight: 240, marginTop: 6 }}>{log || t("No output.")}</pre>}
    </div>
  );
}

const verifyBadge: Record<string, [string, string]> = {
  verifying: ["live", "Verifying…"],
  passed: ["ok", "Verified"],
  failed: ["bad", "Failed · rolled back"],
  "failed-kept": ["bad", "Failed verification"],
  superseded: ["", "Not verified: replaced"],
  blocked: ["bad", "Not deployed"],
};

/** What release verification concluded, with its message on hover. */
function VerifyBadge({ r }: { r: Release }) {
  const b = r.verifyStatus ? verifyBadge[r.verifyStatus] : undefined;
  if (!b) return null;
  return <span className={`badge ${b[0]}`} title={r.verifyMessage}>{t(b[1])}</span>;
}

/** A banner while the newest release is verified, or after it was rolled back. */
function VerificationBanner({ name }: { name: string }) {
  const { data: rels } = usePoll(() => api.releases(name), [name], 10000);
  const newest = rels?.find((r) => !r.rollbackOf);
  if (!newest) return null;
  if (newest.verifyStatus === "verifying") {
    return <div className="alert info" style={{ marginTop: 12 }}>{t("Verifying release #{n}: {msg}. It is rolled back automatically if it breaks a service.", { n: newest.number, msg: newest.verifyMessage ?? "" })}</div>;
  }
  const recent = newest.verifiedAt && Date.now() - new Date(newest.verifiedAt).getTime() < 24 * 3600 * 1000;
  if (recent && (newest.verifyStatus === "failed" || newest.verifyStatus === "failed-kept")) {
    return <div className="alert" style={{ marginTop: 12 }}>{t("Release #{n} failed verification: {msg}", { n: newest.number, msg: newest.verifyMessage ?? "" })}</div>;
  }
  if (recent && newest.verifyStatus === "blocked") {
    return <div className="alert" style={{ marginTop: 12 }}>{t("Release #{n} was not deployed: a pre-deploy task failed ({msg}). The app keeps running its current release.", { n: newest.number, msg: newest.verifyMessage?.replace(/^(not deployed|sin desplegar): /, "") ?? "" })}</div>;
  }
  return null;
}

function Settings({ name, secrets }: { name: string; secrets: string[] }) {
  const nav = useNavigate();
  return (
    <div className="stack">
      <h2 style={{ marginTop: 0 }}>{t("Secrets")}</h2>
      {secrets.length === 0 && <p className="muted">{t("This app declares no secrets. Add names under")} <code>secrets:</code> {t("in rendimiento.yaml.")}</p>}
      {secrets.map((s) => <SecretForm key={s} app={name} secret={s} />)}
      <h2>{t("Danger zone")}</h2>
      <DangerZone name={name} onGone={() => nav("/")} />
    </div>
  );
}

function DangerZone({ name, onGone }: { name: string; onGone: () => void }) {
  const { data: impact } = usePoll(() => api.deleteImpact(name), [name]);
  const [busy, setBusy] = useState(false);
  const run = async (what: "disconnect" | "delete") => {
    if (prompt(what === "delete" ? t("Type {name} to delete it", { name }) : t("Type {name} to disconnect it", { name })) !== name) return;
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
          <strong>{t("Disconnect")}</strong>
          <div className="small muted">
            {t("Stop managing this app with rendimiento. Everything keeps running as it is now (pods, volumes, secrets, DNS), with no rendimiento labels or ownership, so it can be managed another way or migrated back later. Builds and deploys stop.")}
          </div>
        </div>
        <button disabled={busy} onClick={() => run("disconnect")}>{t("Disconnect")}</button>
      </div>
      <div className="card row between">
        <div style={{ flex: 1, minWidth: 240 }}>
          <strong>{t("Delete app")}</strong>
          <div className="small muted">
            {impact?.sharedNamespace
              ? <>{t("Deletes what rendimiento runs in namespace")} <code>{name}</code>{t("; the namespace and everything else in it stay. Also removes the DNS records rendimiento created and all history.")}</>
              : <>{t("Deletes the namespace")} <code>{name}</code> {t("and everything in it, the DNS records rendimiento created, and all history.")}</>}
            {" "}{t("The GitHub repo is not touched.")}
          </div>
          {impact && (impact.volumes.length > 0 || impact.secrets.length > 0) && (
            <div className="alert" style={{ marginTop: 8 }}>
              <strong>{t("Permanently lost:")}</strong>
              {impact.volumes.length > 0 && <div className="small">{t("Volumes and their data:")} {impact.volumes.join(", ")}</div>}
              {impact.secrets.length > 0 && <div className="small">{t("Secrets:")} {impact.secrets.join(", ")}</div>}
              {impact.adopted && <div className="small">{t("This app was migrated: these include data and secrets that existed before rendimiento.")}</div>}
            </div>
          )}
        </div>
        <button className="danger" disabled={busy} onClick={() => run("delete")}>{t("Delete")}</button>
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
      <p className="small muted" style={{ margin: 0 }}>{t("Values are write-only: they replace the whole secret and restart the app. They are never shown again.")}</p>
      <textarea rows={3} className="mono" value={text} onChange={(e) => { setText(e.target.value); setState("idle"); }} placeholder={t("KEY=value\nOTHER_KEY=value")} autoComplete="off" spellCheck={false} />
      <ErrorBox error={error} />
      <div className="row">
        <button className="primary" disabled={!text.includes("=") || state === "saving"} onClick={save}>{state === "saving" ? t("Saving…") : t("Save secret")}</button>
        {state === "saved" && <span className="badge ok">{t("saved, app restarting")}</span>}
      </div>
    </div>
  );
}
