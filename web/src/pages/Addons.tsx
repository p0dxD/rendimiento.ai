import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { addonsApi, timeAgo, type Addon, type AddonRun, type RenovateSettings } from "../api";
import { ErrorBox, RunBadge, Switch, usePoll } from "../components/ui";
import { InstalledSection } from "./Installed";
import { locale, t } from "../i18n";

export function Addons() {
  const { data, error, reload } = usePoll(() => addonsApi.list(), [], 10000);
  const [q, setQ] = useState("");
  return (
    <>
      <h1>{t("Add-ons")}</h1>
      <p className="sub">{t("Software rendimiento installs and keeps in sync (Helm charts or manifests in git), plus built-in features.")}</p>
      <input className="svc-search" placeholder={t("Search add-ons: longhorn, monitoring, postgres…")} value={q} onChange={(e) => setQ(e.target.value)} />
      <InstalledSection query={q} />
      <h2>{t("Built in")}</h2>
      <ErrorBox error={error} />
      <div className="stack">
        {data?.filter((a) => !q || `${a.name} ${a.title} ${a.description}`.toLowerCase().includes(q.toLowerCase()))
          .map((a) => (a.name === "renovate" ? <RenovateCard key={a.name} addon={a} onChange={reload} /> : null))}
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
        <Switch checked={addon.enabled} disabled={busy} label={addon.enabled ? t("Switch Renovate off") : t("Switch Renovate on")} onChange={save} />
      </div>
      <ErrorBox error={error} />
      {addon.problems.length > 0 && (
        <div className="alert">
          {addon.problems.map((p) => <div key={p}>{p}</div>)}
          <div className="small" style={{ marginTop: 6, color: "var(--text)" }}>
            {t("Fix: open the")} <a href={addon.permissionsUrl} target="_blank" rel="noreferrer">{t("GitHub App's permissions")}</a>{t(", set the ones listed, and save. GitHub then asks the account owner to accept them (a banner on the app's installation page, and an email); runs work once accepted.")}
          </div>
        </div>
      )}

      <div className="row small">
        {addon.running ? <span className="badge warn live">{t("running")}</span> : addon.enabled ? <span className="badge ok">{t("on")}</span> : <span className="badge">{t("off")}</span>}
        {addon.enabled && addon.nextRun && !addon.running && <span className="muted">{t("next run {when}", { when: new Date(addon.nextRun).toLocaleString(locale) })}</span>}
        {last && <span className="muted">{t("last run {when}", { when: timeAgo(last.startedAt) })}: {last.message || t(last.status)}</span>}
        <button disabled={busy || addon.running || addon.repos.length === 0} onClick={() => act(() => addonsApi.run("renovate"))}>
          {addon.running ? t("Running…") : t("Run now")}
        </button>
      </div>

      <div>
        <h3 className="addon-h">{t("Repositories")}</h3>
        <p className="small muted" style={{ marginTop: 0 }}>{t("Switch Renovate on per app here or on the app's page.")}</p>
        <div className="addon-repos">
          {addon.apps.map((a) => (
            <label key={a.name}>
              <Switch checked={a.enabled} disabled={busy} label={t("Renovate for {app}", { app: a.name })}
                onChange={(on) => act(() => addonsApi.setForApp(a.name, "renovate", on))} />
              <span><Link to={`/apps/${a.name}`}>{a.name}</Link> <span className="mono small muted">{a.repo}</span></span>
            </label>
          ))}
        </div>
        <label className="field" style={{ marginTop: 12 }}>
          <span>{t("Other repositories (not rendimiento apps), one per line")}</span>
          <textarea rows={3} className="mono" value={extra} onChange={(e) => { setExtra(e.target.value); setDirty(true); }} placeholder="p0dxD/main_configs" />
        </label>
      </div>

      <details>
        <summary><strong>{t("Settings")}</strong> <span className="small muted">{t("schedule, image, Renovate config")}</span></summary>
        <div className="stack" style={{ marginTop: 12 }}>
          <div className="row" style={{ alignItems: "flex-end" }}>
            <label className="field">
              <span>{t("Schedule (cron, UTC)")}</span>
              <input className="mono" value={settings.schedule} onChange={(e) => edit({ schedule: e.target.value })} />
            </label>
            <label className="field" style={{ minWidth: 260 }}>
              <span>{t("Image")}</span>
              <input className="mono" value={settings.image} onChange={(e) => edit({ image: e.target.value })} />
            </label>
            <label className="field">
              <span>{t("Log level")}</span>
              <select value={settings.logLevel ?? "info"} onChange={(e) => edit({ logLevel: e.target.value as "info" | "debug" })}>
                <option value="info">info</option>
                <option value="debug">{t("debug (to see why a repository fails)")}</option>
              </select>
            </label>
          </div>
          <label className="field">
            <span>{t("Renovate config (config.js). The token, repositories and bot identity are set by rendimiento.")}</span>
            <textarea className="code" spellCheck={false} value={settings.config} onChange={(e) => edit({ config: e.target.value })} />
          </label>
          {addon.defaults && (
            <div><button onClick={() => edit({ config: addon.defaults!.config })}>{t("Reset config to the default")}</button></div>
          )}
        </div>
      </details>

      {dirty && (
        <div className="row">
          <button className="primary" disabled={busy} onClick={() => save(addon.enabled)}>{t("Save changes")}</button>
          <button disabled={busy} onClick={() => { setDirty(false); setSettings(addon.settings); setExtra(addon.settings.extraRepos.join("\n")); }}>{t("Discard")}</button>
        </div>
      )}

      <div>
        <h3 className="addon-h">{t("Runs")}</h3>
        {!runs?.length && <p className="small muted">{t("No runs yet.")}</p>}
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
          <span className="small">{run.trigger === "schedule" ? t("scheduled") : t("manual")}</span>
          <span className="small muted">{timeAgo(run.startedAt)}{took !== undefined && ` · ${took < 120 ? t("{s}s", { s: took }) : t("{n} min", { n: Math.round(took / 60) })}`}</span>
          <span className="small muted">{run.message}</span>
        </div>
        <span className="small muted">{open ? "▾" : "▸"}</span>
      </div>
      {open && (
        <div className="env-more">
          {Object.entries(run.results ?? {}).map(([repo, r]) => (
            <div key={repo} className="small">
              <span className="mono">{repo}</span>: {r.result ?? t("no result")}
              {r.prs?.length ? ` · ${t("opened {list}", { list: r.prs.join(", ") })}` : ""}
              {r.automerged?.length ? ` · ${t("merged {list}", { list: r.automerged.join(", ") })}` : ""}
              {r.errors?.length ? <span style={{ color: "var(--bad)" }}> · {r.errors.join("; ")}</span> : null}
            </div>
          ))}
          {full?.log ? <pre className="log" style={{ marginTop: 8, maxHeight: 400 }}>{full.log}</pre> : run.status === "running" && <p className="small muted">{t("The log appears when the run finishes.")}</p>}
        </div>
      )}
    </div>
  );
}
