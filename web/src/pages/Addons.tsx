import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { addonsApi, timeAgo, type Addon, type AddonRun, type RenovateSettings } from "../api";
import { ErrorBox, RunBadge, Switch, usePoll } from "../components/ui";

export function Addons() {
  const { data, error, reload } = usePoll(() => addonsApi.list(), [], 10000);
  if (error) return <ErrorBox error={error} />;
  if (!data) return <p className="muted">Loading…</p>;
  return (
    <>
      <h1>Add-ons</h1>
      <p className="sub">Optional features rendimiento runs for you. Switch them on here; some also have a switch on each app.</p>
      <div className="stack">
        {data.map((a) => (a.name === "renovate" ? <RenovateCard key={a.name} addon={a} onChange={reload} /> : null))}
      </div>
    </>
  );
}

function RenovateCard({ addon, onChange }: { addon: Addon; onChange: () => void }) {
  const [settings, setSettings] = useState<RenovateSettings>(addon.settings);
  const [extra, setExtra] = useState(addon.settings.extraRepos.join("\n"));
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [openRun, setOpenRun] = useState<number>();
  const { data: runs, reload: reloadRuns } = usePoll(() => addonsApi.runs("renovate"), [], addon.running ? 5000 : 30000);

  // Take in server changes unless the form has unsaved edits.
  useEffect(() => {
    if (!dirty) {
      setSettings(addon.settings);
      setExtra(addon.settings.extraRepos.join("\n"));
    }
  }, [addon, dirty]);

  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    setError(undefined);
    try {
      await f();
      onChange();
      reloadRuns();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const current = (): RenovateSettings => ({ ...settings, extraRepos: extra.split(/[\s,]+/).filter(Boolean) });
  const save = (enabled: boolean) => act(async () => {
    await addonsApi.save("renovate", enabled, current());
    setDirty(false);
  });
  const edit = (patch: Partial<RenovateSettings>) => {
    setSettings({ ...settings, ...patch });
    setDirty(true);
  };

  const last = addon.lastRun;
  return (
    <div className="card stack">
      <div className="addon-head">
        <div>
          <h2 style={{ margin: 0 }}>{addon.title}</h2>
          <p className="small muted" style={{ margin: "4px 0 0" }}>{addon.description}</p>
        </div>
        <Switch checked={addon.enabled} disabled={busy} label={addon.enabled ? "Switch Renovate off" : "Switch Renovate on"} onChange={save} />
      </div>
      <ErrorBox error={error} />
      {addon.problems.length > 0 && (
        <div className="alert">
          {addon.problems.map((p) => <div key={p}>{p}</div>)}
          <div className="small" style={{ marginTop: 6, color: "var(--text)" }}>
            Fix: open the <a href={addon.permissionsUrl} target="_blank" rel="noreferrer">GitHub App's permissions</a>, set the ones listed, and save.
            GitHub then asks the account owner to accept them (a banner on the app's installation page, and an email); runs work once accepted.
          </div>
        </div>
      )}

      <div className="row small">
        {addon.running ? <span className="badge warn live">running</span> : addon.enabled ? <span className="badge ok">on</span> : <span className="badge">off</span>}
        {addon.enabled && addon.nextRun && !addon.running && <span className="muted">next run {new Date(addon.nextRun).toLocaleString()}</span>}
        {last && <span className="muted">last run {timeAgo(last.startedAt)}: {last.message || last.status}</span>}
        <button disabled={busy || addon.running || addon.repos.length === 0} onClick={() => act(() => addonsApi.run("renovate"))}>
          {addon.running ? "Running…" : "Run now"}
        </button>
      </div>

      <div>
        <h3 className="addon-h">Repositories</h3>
        <p className="small muted" style={{ marginTop: 0 }}>Switch Renovate on per app here or on the app's page.</p>
        <div className="addon-repos">
          {addon.apps.map((a) => (
            <label key={a.name}>
              <Switch checked={a.enabled} disabled={busy} label={`Renovate for ${a.name}`}
                onChange={(on) => act(() => addonsApi.setForApp(a.name, "renovate", on))} />
              <span><Link to={`/apps/${a.name}`}>{a.name}</Link> <span className="mono small muted">{a.repo}</span></span>
            </label>
          ))}
        </div>
        <label className="field" style={{ marginTop: 12 }}>
          <span>Other repositories (not rendimiento apps), one per line</span>
          <textarea rows={3} className="mono" value={extra} onChange={(e) => { setExtra(e.target.value); setDirty(true); }} placeholder="p0dxD/main_configs" />
        </label>
      </div>

      <details>
        <summary><strong>Settings</strong> <span className="small muted">schedule, image, Renovate config</span></summary>
        <div className="stack" style={{ marginTop: 12 }}>
          <div className="row" style={{ alignItems: "flex-end" }}>
            <label className="field">
              <span>Schedule (cron, UTC)</span>
              <input className="mono" value={settings.schedule} onChange={(e) => edit({ schedule: e.target.value })} />
            </label>
            <label className="field" style={{ minWidth: 260 }}>
              <span>Image</span>
              <input className="mono" value={settings.image} onChange={(e) => edit({ image: e.target.value })} />
            </label>
            <label className="field">
              <span>Log level</span>
              <select value={settings.logLevel ?? "info"} onChange={(e) => edit({ logLevel: e.target.value as "info" | "debug" })}>
                <option value="info">info</option>
                <option value="debug">debug (to see why a repository fails)</option>
              </select>
            </label>
          </div>
          <label className="field">
            <span>Renovate config (config.js). The token, repositories and bot identity are set by rendimiento.</span>
            <textarea className="code" spellCheck={false} value={settings.config} onChange={(e) => edit({ config: e.target.value })} />
          </label>
          {addon.defaults && (
            <div><button onClick={() => edit({ config: addon.defaults!.config })}>Reset config to the default</button></div>
          )}
        </div>
      </details>

      {dirty && (
        <div className="row">
          <button className="primary" disabled={busy} onClick={() => save(addon.enabled)}>Save changes</button>
          <button disabled={busy} onClick={() => { setDirty(false); setSettings(addon.settings); setExtra(addon.settings.extraRepos.join("\n")); }}>Discard</button>
        </div>
      )}

      <div>
        <h3 className="addon-h">Runs</h3>
        {!runs?.length && <p className="small muted">No runs yet.</p>}
        {runs?.map((r) => (
          <RunRow key={r.id} run={r} open={openRun === r.id} onToggle={() => setOpenRun(openRun === r.id ? undefined : r.id)} />
        ))}
      </div>
    </div>
  );
}

function RunRow({ run, open, onToggle }: { run: AddonRun; open: boolean; onToggle: () => void }) {
  const { data: full } = usePoll(() => (open ? addonsApi.runLog("renovate", run.id) : Promise.resolve(undefined)), [open, run.id, run.status], open && run.status === "running" ? 5000 : 0);
  const took = run.finishedAt ? Math.round((new Date(run.finishedAt).getTime() - new Date(run.startedAt).getTime()) / 1000) : undefined;
  return (
    <div className="env-check" style={{ padding: "8px 0" }}>
      <div className="row between" style={{ cursor: "pointer" }} onClick={onToggle}>
        <div className="row" style={{ gap: 8 }}>
          <RunBadge status={run.status} />
          <span className="small">{run.trigger === "schedule" ? "scheduled" : "manual"}</span>
          <span className="small muted">{timeAgo(run.startedAt)}{took !== undefined && ` · ${took < 120 ? `${took}s` : `${Math.round(took / 60)} min`}`}</span>
          <span className="small muted">{run.message}</span>
        </div>
        <span className="small muted">{open ? "▾" : "▸"}</span>
      </div>
      {open && (
        <div className="env-more">
          {Object.entries(run.results ?? {}).map(([repo, r]) => (
            <div key={repo} className="small">
              <span className="mono">{repo}</span>: {r.result ?? "no result"}
              {r.prs?.length ? ` · opened ${r.prs.join(", ")}` : ""}
              {r.automerged?.length ? ` · merged ${r.automerged.join(", ")}` : ""}
              {r.errors?.length ? <span style={{ color: "var(--bad)" }}> · {r.errors.join("; ")}</span> : null}
            </div>
          ))}
          {full?.log ? <pre className="log" style={{ marginTop: 8, maxHeight: 400 }}>{full.log}</pre> : run.status === "running" && <p className="small muted">The log appears when the run finishes.</p>}
        </div>
      )}
    </div>
  );
}
