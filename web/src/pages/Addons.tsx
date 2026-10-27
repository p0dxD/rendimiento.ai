import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { addonsApi, installedApi, timeAgo, type Addon, type AddonCatalogEntry, type AddonPhase, type AddonRun, type RenovateSettings } from "../api";
import { ErrorBox, RunBadge, Switch, usePoll } from "../components/ui";
import { AddonDetail, InstallForm, sourceLabel } from "./Installed";
import { locale, t } from "../i18n";

const attentionPhases: AddonPhase[] = ["Error", "Blocked", "OutOfSync"];
const phaseTone: Record<AddonPhase, string> = {
  Synced: "ok", OutOfSync: "warn", Blocked: "warn", Pending: "idle", Error: "bad", Suspended: "idle",
};

type Elegido = { kind: "installed"; name: string } | { kind: "catalog"; entry: AddonCatalogEntry } | { kind: "builtin"; addon: Addon };

/**
 * The Add-ons page: on the left what is installed (what needs attention first), the built-in ones and the catalog,
 * with a search; on the right the one chosen: its state and what to do, or the form to install it.
 */
export function Addons() {
  const { name } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const { data: builtin, error, reload } = usePoll(() => addonsApi.list(), [], 10000);
  const { data: inst, error: instError, reload: reloadInst } = usePoll(() => installedApi.list(), [], 5000);
  const { data: catalog } = usePoll(() => installedApi.catalog(), []);
  const [q, setQ] = useState("");
  const needle = q.trim().toLowerCase();
  const match = (...s: (string | undefined)[]) => !needle || s.join(" ").toLowerCase().includes(needle);

  const installed = (inst?.addons ?? []).filter((a) => match(a.name, a.title, a.category, a.description, sourceLabel(a)));
  const attention = installed.filter((a) => a.status.phase && attentionPhases.includes(a.status.phase));
  const calm = installed.filter((a) => !attention.includes(a));
  const built = (builtin ?? []).filter((a) => a.name === "renovate" && match(a.name, a.title, a.description));
  const available = (catalog ?? []).filter((c) => match(c.id, c.title, c.category, c.description));

  const nuevo = params.get("nuevo");
  const integrado = params.get("integrado");
  const elegido: Elegido | undefined = name ? { kind: "installed", name }
    : nuevo && catalog?.find((c) => c.id === nuevo) ? { kind: "catalog", entry: catalog.find((c) => c.id === nuevo)! }
    : integrado && builtin?.find((a) => a.name === integrado) ? { kind: "builtin", addon: builtin.find((a) => a.name === integrado)! }
    : (attention[0] ?? calm[0]) ? { kind: "installed", name: (attention[0] ?? calm[0]).name }
    : built[0] ? { kind: "builtin", addon: built[0] } : undefined;
  const on = (kind: Elegido["kind"], id: string) => elegido?.kind === kind &&
    (elegido.kind === "installed" ? elegido.name : elegido.kind === "catalog" ? elegido.entry.id : elegido.addon.name) === id;

  return (
    <>
      <h1>{t("Add-ons")}</h1>
      <p className="sub" style={{ marginBottom: 0 }}>{t("On the left, what you have and what you can add; on the right, what is happening with it and what to do.")}</p>
      <ErrorBox error={error ?? instError} />
      {inst?.sync?.error && <div className="alert" style={{ marginTop: 12 }}>{t("Could not read {repo}: {err}", { repo: inst.repo ?? "", err: inst.sync.error })}</div>}
      {inst?.sync?.invalid && Object.entries(inst.sync.invalid).map(([f, e]) => (
        <div key={f} className="alert" style={{ marginTop: 12 }}>{t("{file} is invalid and was skipped: {err}", { file: `${inst.repo}/${f}`, err: e })}</div>
      ))}
      <div className="lista-detalle">
        <aside className="ld-lista">
          <label className="ld-buscar">
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="M15.5 15.5 21 21" /></svg>
            <input type="search" placeholder={t("Search add-ons: longhorn, monitoring, postgres…")} aria-label={t("Search add-ons")} value={q} onChange={(e) => setQ(e.target.value)} />
          </label>
          <Seccion label={t("Needs attention")} color="var(--bad)" n={attention.length}>
            {attention.map((a) => <Item key={a.name} on={on("installed", a.name)} title={a.title} sub={a.category} tone={phaseTone[a.status.phase ?? "Pending"]} go={() => nav(`/addons/${a.name}`)} />)}
          </Seccion>
          <Seccion label={t("Installed")} color="var(--nopal)" n={calm.length}>
            {calm.map((a) => <Item key={a.name} on={on("installed", a.name)} title={a.title} sub={a.category} tone={phaseTone[a.status.phase ?? "Pending"]} go={() => nav(`/addons/${a.name}`)} />)}
          </Seccion>
          <Seccion label={t("Built in")} color="var(--anil)" n={built.length}>
            {built.map((a) => <Item key={a.name} on={on("builtin", a.name)} title={a.title} sub={a.enabled ? t("on") : t("off")} tone={a.problems.length ? "warn" : a.enabled ? "ok" : undefined} go={() => nav(`/addons?integrado=${a.name}`)} />)}
          </Seccion>
          <Seccion label={t("Add from the catalog")} color="var(--cempasuchil)" n={available.length}>
            {available.map((c) => <Item key={c.id} on={on("catalog", c.id)} title={c.title} sub={c.category} go={() => setParams({ nuevo: c.id })} />)}
          </Seccion>
          {inst && installed.length + built.length + available.length === 0 && <p className="small muted" style={{ margin: "8px 4px" }}>{t("No installed add-on matches.")}</p>}
          {inst?.repo && <p className="small muted" style={{ margin: "8px 4px 0" }}>{t("Every add-on is a file in")} <a href={`https://github.com/${inst.repo}/tree/HEAD/addons`} target="_blank" rel="noreferrer">{inst.repo}/addons</a>{t(": changes here are commits there, and edits pushed there show up here.")}{inst.sync?.at && ` ${t("Last read {when}.", { when: timeAgo(inst.sync.at) })}`}</p>}
        </aside>
        {elegido?.kind === "installed" && <AddonDetail key={elegido.name} name={elegido.name} />}
        {elegido?.kind === "builtin" && <main className="ld-detalle" key={elegido.addon.name}><RenovateCard addon={elegido.addon} onChange={reload} /></main>}
        {elegido?.kind === "catalog" && (
          <main className="ld-detalle" key={elegido.entry.id}>
            <div>
              <h2 className="ld-titulo">{elegido.entry.title}</h2>
              <div className="small muted">{elegido.entry.category}{elegido.entry.helm && <> · <span className="mono">{elegido.entry.helm.chart} {elegido.entry.helm.version}</span></>}</div>
            </div>
            <p className="ld-desc">{elegido.entry.description}{elegido.entry.homepage && <> <a href={elegido.entry.homepage} target="_blank" rel="noreferrer">{t("About ↗")}</a></>}</p>
            <InstallForm entry={elegido.entry} existing={inst?.addons ?? []} onClose={() => setParams({})} onDone={reloadInst} />
          </main>
        )}
        {!elegido && inst && <main className="ld-detalle"><p className="muted" style={{ margin: 0 }}>{t("Nothing installed yet. Pick something from the catalog below.")}</p></main>}
      </div>
    </>
  );
}

/** One add-on in the list: its name, its category or state, and a dot for how it is. */
function Item({ on, title, sub, tone, go }: { on: boolean; title: string; sub?: string; tone?: string; go: () => void }) {
  return (
    <button className={`ld-item${on ? " on" : ""}`} style={{ ["--c" as string]: tone === "bad" ? "var(--bad)" : tone === "warn" ? "var(--warn)" : "var(--grana)" }} aria-current={on} onClick={go}>
      <span style={{ minWidth: 0 }}><span className="ld-nombre">{title}</span>{sub && <span className="ld-sub">{sub}</span>}</span>
      {tone && <span className={`ld-punto ${tone}`} />}
    </button>
  );
}

/** A group of the list, with its count; nothing when it is empty. */
function Seccion({ label, color, n, children }: { label: string; color: string; n: number; children: React.ReactNode }) {
  return n === 0 ? null : <div><div className="ld-grupo" style={{ color }}>{label} · {n}</div>{children}</div>;
}

/**
 * Renovate's card: on or off, when it runs, which repositories, what it may merge by itself, and its last runs.
 */
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
    <div className="stack">
      <div className="addon-head">
        <div>
          <h2 className="ld-titulo">{addon.title}</h2>
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

/** One Renovate run: when, how long, what it did; open, its log and each repository's result. */
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
