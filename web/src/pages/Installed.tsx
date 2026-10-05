import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  catalogApi, installedApi, timeAgo,
  type AddonCatalogEntry, type AddonDefinition, type AddonPhase, type CatalogEntry, type CatalogField, type InstalledAddon,
} from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t } from "../i18n";

const phaseTone: Record<AddonPhase, string> = {
  Synced: "ok", OutOfSync: "warn", Blocked: "warn", Pending: "idle", Error: "bad", Suspended: "idle",
};
// Translated where shown.
const phaseLabel: Record<AddonPhase, string> = {
  Synced: "In sync", OutOfSync: "Changes waiting", Blocked: "Review needed", Pending: "Pending", Error: "Error", Suspended: "Suspended",
};

export function AddonPhaseBadge({ phase }: { phase?: AddonPhase }) {
  const p = phase ?? "Pending";
  return <span className={`badge ${phaseTone[p]}`}>{t(phaseLabel[p])}</span>;
}

export function sourceLabel(a: { source: InstalledAddon["source"] }) {
  if (a.source.helm) return `${a.source.helm.chart} ${a.source.helm.version} (Helm)`;
  if (a.source.git) return `${a.source.git.repo}/${a.source.git.path} (git)`;
  return "";
}

/** The add-ons' services reachable on the local network, by add-on name. */
function useLANByAddon(): Record<string, CatalogEntry[]> {
  const { data } = usePoll(() => catalogApi.list(), [], 30000);
  return useMemo(() => {
    const out: Record<string, CatalogEntry[]> = {};
    for (const e of data?.entries ?? []) {
      if (e.origin.kind === "addon" && e.origin.app && e.lan) (out[e.origin.app] ??= []).push(e);
    }
    return out;
  }, [data]);
}

/** Links to an add-on's UIs on the local network (plain addresses for non-web services). */
function LANLinks({ entries }: { entries?: CatalogEntry[] }) {
  if (!entries?.length) return null;
  return (
    <div className="row small" style={{ gap: 10 }}>
      {entries.map((e) => e.lanURL
        ? <a key={e.id} href={e.lanURL} target="_blank" rel="noreferrer" className="mono" title={t("{name}: open on your local network", { name: e.name })}
            onClick={(ev) => ev.stopPropagation()}>{e.lan} ↗</a>
        : <span key={e.id} className="mono muted" title={`${e.name} (${e.protocol})`}>{e.lan}</span>)}
    </div>
  );
}

/** Installed add-ons, the catalog and its install form. */
export function InstalledSection({ query }: { query: string }) {
  const { data, error, reload } = usePoll(() => installedApi.list(), [], 5000);
  const { data: catalog } = usePoll(() => installedApi.catalog(), []);
  const [installing, setInstalling] = useState<AddonCatalogEntry>();
  const lan = useLANByAddon();
  const nav = useNavigate();
  const q = query.trim().toLowerCase();
  const match = (...s: (string | undefined)[]) => !q || s.join(" ").toLowerCase().includes(q);

  const installed = (data?.addons ?? []).filter((a) => match(a.name, a.title, a.category, a.description, sourceLabel(a)));
  const available = (catalog ?? []).filter((c) => match(c.id, c.title, c.category, c.description));

  return (
    <>
      <h2>{t("Installed")}</h2>
      <ErrorBox error={error} />
      {data?.sync?.error && <div className="alert">{t("Could not read {repo}: {err}", { repo: data.repo ?? "", err: data.sync.error })}</div>}
      {data?.sync?.invalid && Object.entries(data.sync.invalid).map(([f, e]) => (
        <div key={f} className="alert">{t("{file} is invalid and was skipped: {err}", { file: `${data.repo}/${f}`, err: e })}</div>
      ))}
      {data && installed.length === 0 && <div className="card muted">{q ? t("No installed add-on matches.") : t("Nothing installed yet. Pick something from the catalog below.")}</div>}
      <div className="stack">
        {installed.map((a) => (
          <div key={a.name} className="card row between" style={{ cursor: "pointer" }} onClick={() => nav(`/addons/${a.name}`)}>
            <div style={{ minWidth: 0 }}>
              <div className="row" style={{ gap: 8 }}>
                <strong>{a.title}</strong>
                {a.category && <span className="chip">{a.category}</span>}
                <span className="chip">{a.namespace}</span>
                {a.manualSync && <span className="chip">{t("manual sync")}</span>}
              </div>
              <div className="small muted svc-line">{sourceLabel(a)}{a.status.message ? ` · ${a.status.message}` : ""}</div>
              <LANLinks entries={lan[a.name]} />
            </div>
            <AddonPhaseBadge phase={a.status.phase} />
          </div>
        ))}
      </div>
      {data?.repo && <p className="small muted">{t("Every add-on is a file in")} <a href={`https://github.com/${data.repo}/tree/HEAD/addons`} target="_blank" rel="noreferrer">{data.repo}/addons</a>{t(": changes here are commits there, and edits pushed there show up here.")}{data.sync?.at && ` ${t("Last read {when}.", { when: timeAgo(data.sync.at) })}`}</p>}

      <h2>{t("Add from the catalog")}</h2>
      <div className="grid">
        {available.map((c) => (
          <div key={c.id} className="card stack">
            <div className="row between">
              <strong>{c.title}</strong>
              {c.category && <span className="chip">{c.category}</span>}
            </div>
            <div className="small muted">{c.description}</div>
            {c.helm && <div className="small mono muted">{c.helm.chart} {c.helm.version}</div>}
            <div className="row">
              <button className="primary" onClick={() => setInstalling(c)}>{c.kind === "helm" ? t("Install") : t("Set up")}</button>
              {c.homepage && <a className="small" href={c.homepage} target="_blank" rel="noreferrer">{t("About ↗")}</a>}
            </div>
          </div>
        ))}
      </div>
      {installing && <InstallForm entry={installing} existing={data?.addons ?? []} onClose={() => setInstalling(undefined)} onDone={reload} />}
    </>
  );
}

function fieldDefault(f: CatalogField): string | boolean {
  if (f.type === "boolean") return Boolean(f.default);
  return f.default === undefined ? "" : String(f.default);
}

function setPath(values: Record<string, unknown>, key: string, v: unknown) {
  const parts = key.split(".");
  let m = values as Record<string, unknown>;
  parts.forEach((p, i) => {
    if (i === parts.length - 1) m[p] = v;
    else m = (m[p] ??= {}) as Record<string, unknown>;
  });
}

function InstallForm({ entry, existing, onClose, onDone }: { entry: AddonCatalogEntry; existing: InstalledAddon[]; onClose: () => void; onDone: () => void }) {
  const nav = useNavigate();
  const custom = entry.kind !== "helm";
  const [name, setName] = useState(custom ? "" : entry.id);
  const [namespace, setNamespace] = useState(entry.namespace ?? "");
  const [repo, setRepo] = useState(entry.helm?.repo ?? "");
  const [chart, setChart] = useState(entry.helm?.chart ?? "");
  const [version, setVersion] = useState(entry.helm?.version ?? "");
  const [gitRepo, setGitRepo] = useState("");
  const [gitPath, setGitPath] = useState("");
  const [fields, setFields] = useState<Record<string, string | boolean>>(
    Object.fromEntries((entry.fields ?? []).map((f) => [f.key, fieldDefault(f)])));
  const [valuesYaml, setValuesYaml] = useState("");
  const [adopt, setAdopt] = useState(false);
  const [manualSync, setManualSync] = useState(Boolean(entry.manualSync));
  const [prune, setPrune] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const taken = existing.some((a) => a.name === name);

  const definition = useMemo((): AddonDefinition => {
    const values: Record<string, unknown> = {};
    for (const f of entry.fields ?? []) {
      const v = fields[f.key];
      if (v === "" || v === undefined) continue;
      setPath(values, f.key, f.type === "number" ? Number(v) : v);
    }
    const d: AddonDefinition = {
      name, namespace, createNamespace: true, adopt, prune, manualSync,
      title: custom ? undefined : entry.title, category: entry.category || undefined, description: custom ? undefined : entry.description,
    };
    if (entry.kind === "custom-git") d.git = { repo: gitRepo || undefined, path: gitPath };
    else {
      d.helm = { repo, chart, version };
      if (Object.keys(values).length) d.values = values;
    }
    return d;
  }, [name, namespace, adopt, prune, manualSync, repo, chart, version, gitRepo, gitPath, fields, entry, custom]);

  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await installedApi.save(name, { definition, valuesYaml: entry.kind === "custom-git" ? undefined : valuesYaml });
      onDone();
      nav(`/addons/${name}`);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card stack" style={{ marginTop: 16, borderColor: "var(--accent)" }}>
      <div className="row between">
        <h3 style={{ margin: 0 }}>{custom ? entry.title : t("Install {title}", { title: entry.title })}</h3>
        <button onClick={onClose}>{t("Cancel")}</button>
      </div>
      {entry.notes && <div className="alert info small">{entry.notes}</div>}
      <div className="row" style={{ alignItems: "flex-end" }}>
        <label className="field"><span>{t("Name")}</span><input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("my-addon")} /></label>
        <label className="field"><span>{t("Namespace")}</span><input value={namespace} onChange={(e) => setNamespace(e.target.value)} /></label>
        {entry.kind !== "custom-git" && (
          <>
            {custom && <label className="field" style={{ minWidth: 280 }}><span>{t("Chart repository URL")}</span><input value={repo} onChange={(e) => setRepo(e.target.value)} placeholder="https://charts.example.com" /></label>}
            {custom && <label className="field"><span>{t("Chart")}</span><input value={chart} onChange={(e) => setChart(e.target.value)} /></label>}
            <label className="field"><span>{t("Version")}</span><input value={version} onChange={(e) => setVersion(e.target.value)} /></label>
          </>
        )}
        {entry.kind === "custom-git" && (
          <>
            <label className="field" style={{ minWidth: 220 }}><span>{t("Repository (empty = the gitops repo)")}</span><input value={gitRepo} onChange={(e) => setGitRepo(e.target.value)} placeholder={t("owner/name")} /></label>
            <label className="field"><span>{t("Folder")}</span><input value={gitPath} onChange={(e) => setGitPath(e.target.value)} placeholder="umami" /></label>
          </>
        )}
      </div>
      {taken && <div className="small" style={{ color: "var(--warn)" }}>{t("An add-on named {name} is already installed: this changes it.", { name })}</div>}

      {(entry.fields?.length ?? 0) > 0 && (
        <div className="row" style={{ alignItems: "flex-end" }}>
          {entry.fields!.map((f) => (
            f.type === "boolean" ? (
              <label key={f.key} className="row small" style={{ gap: 6 }} title={f.description}>
                <input type="checkbox" checked={Boolean(fields[f.key])} onChange={(e) => setFields({ ...fields, [f.key]: e.target.checked })} /> {f.label}
              </label>
            ) : (
              <label key={f.key} className="field" title={f.description}>
                <span>{f.label}</span>
                <input type={f.type === "number" ? "number" : "text"} value={String(fields[f.key] ?? "")} onChange={(e) => setFields({ ...fields, [f.key]: e.target.value })} />
              </label>
            )
          ))}
        </div>
      )}

      {entry.kind !== "custom-git" && (
        <details>
          <summary className="small"><strong>{t("Values (YAML)")}</strong>{t(": override or add anything the chart supports; merged over the fields above")}</summary>
          <textarea className="code" style={{ minHeight: 160, marginTop: 8 }} spellCheck={false} value={valuesYaml} onChange={(e) => setValuesYaml(e.target.value)} placeholder={t("# e.g.") + "\nresources:\n  limits:\n    memory: 256Mi"} />
        </details>
      )}

      <div className="stack small">
        <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={adopt} onChange={(e) => setAdopt(e.target.checked)} />
          {t("Take over what is already running (installed by ArgoCD, Helm or kubectl). The first sync only proceeds if it would change nothing.")}</label>
        <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={manualSync} onChange={(e) => setManualSync(e.target.checked)} />
          {t("Manual sync: changes wait for you to review and press Sync.")}</label>
        <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={prune} onChange={(e) => setPrune(e.target.checked)} />
          {t("Delete objects that are removed from the chart or folder (never CRDs, namespaces or volumes).")}</label>
      </div>
      <ErrorBox error={error} />
      <div className="row">
        <button className="primary" disabled={busy || !name || !namespace} onClick={submit}>{busy ? t("Committing…") : taken ? t("Save changes") : t("Install")}</button>
        <span className="small muted">{t("This commits {file} to the gitops repository; rendimiento then previews and applies it.", { file: `addons/${name || "…"}.yaml` })}</span>
      </div>
    </div>
  );
}

/** One installed add-on: status, what a sync would change, and actions. */
export function AddonDetail() {
  const { name = "" } = useParams();
  const nav = useNavigate();
  const { data: a, error, reload } = usePoll(() => installedApi.get(name), [name], 5000);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const [editing, setEditing] = useState<string>();
  const [confirm, setConfirm] = useState<"remove" | "uninstall">();
  const lan = useLANByAddon();

  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    setErr(undefined);
    try {
      await f();
      reload();
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  };
  if (error) return <ErrorBox error={error} />;
  if (!a) return <p className="muted">{t("Loading…")}</p>;
  const st = a.status;
  const p = st.preview;
  const changesWaiting = (p?.create ?? 0) + (p?.update ?? 0) + (p?.prune ?? 0) > 0;

  return (
    <>
      <p className="small"><Link to="/addons">← {t("Add-ons")}</Link></p>
      <div className="row between">
        <div>
          <h1 style={{ marginBottom: 4 }}>{a.title}</h1>
          <div className="small muted">{sourceLabel(a)} · {t("namespace {ns}", { ns: a.namespace })}{st.revision ? ` · ${t("rendered {rev}", { rev: st.revision.length === 40 ? st.revision.slice(0, 7) : st.revision })}` : ""}</div>
        </div>
        <AddonPhaseBadge phase={st.phase} />
      </div>
      {lan[a.name] && <div className="row small" style={{ gap: 8, marginTop: 8 }}><span className="muted">{t("On your local network:")}</span><LANLinks entries={lan[a.name]} /></div>}
      {st.message && <p>{st.message}</p>}
      <ErrorBox error={err} />

      <div className="row">
        {(st.phase === "OutOfSync" || (a.manualSync && changesWaiting)) && (
          <button className="primary" disabled={busy} onClick={() => act(() => installedApi.sync(a.name))}>{t("Sync: apply the changes below")}</button>
        )}
        {st.phase === "Blocked" && a.fromGit && (
          <button disabled={busy} onClick={() => act(() => installedApi.patch(a.name, { allowAdoptChanges: true }))}>
            {t("Allow these changes and take over")}
          </button>
        )}
        {a.fromGit && (
          <button disabled={busy} onClick={() => act(() => installedApi.patch(a.name, { suspend: !a.suspend }))}>
            {a.suspend ? t("Resume") : t("Suspend")}
          </button>
        )}
        {a.fromGit && <button disabled={busy} onClick={() => setEditing(a.definition ?? "")}>{t("Edit definition")}</button>}
        {a.sourceFile && <span className="small muted">{t("from")} <span className="mono">{a.sourceFile.split("@")[0]}</span></span>}
      </div>

      {editing !== undefined && (
        <div className="card stack" style={{ marginTop: 12 }}>
          <strong>addons/{a.name}.yaml</strong>
          <textarea className="code" spellCheck={false} value={editing} onChange={(e) => setEditing(e.target.value)} />
          <div className="row">
            <button className="primary" disabled={busy} onClick={() => act(async () => { await installedApi.save(a.name, { yaml: editing }); setEditing(undefined); })}>{t("Commit")}</button>
            <button onClick={() => setEditing(undefined)}>{t("Cancel")}</button>
          </div>
        </div>
      )}

      {st.hooks && st.hooks.length > 0 && (
        <details className="card small" style={{ marginTop: 12 }}>
          <summary><strong>{t("Helm hooks")}</strong> <span className="muted">{t("run like Helm: on install, on upgrade (a new version or values) and on uninstall; never on a plain resync")}</span></summary>
          <ul style={{ margin: "8px 0", paddingLeft: 18 }}>
            {st.hooks.map((h) => <li key={h.name}><span className="mono">{h.kind} {h.name}</span> <span className="muted">{t("on {events}", { events: h.events.join(", ") })}</span></li>)}
          </ul>
          {(st.hookRuns ?? []).length > 0 && (
            <>
              <div className="muted">{t("Recent runs")}</div>
              {[...st.hookRuns!].reverse().map((r, i) => (
                <div key={i} className="row" style={{ gap: 8 }}>
                  <span className={`badge ${r.status === "succeeded" ? "ok" : "bad"}`}>{t(r.status)}</span>
                  <span className="mono">{r.event} {r.name}</span>
                  <span className="muted">{timeAgo(r.finished)}</span>
                  {r.message && <span style={{ color: "var(--bad)" }}>{r.message}</span>}
                </div>
              ))}
            </>
          )}
        </details>
      )}

      <h2>{changesWaiting ? t("What a sync will change") : t("Sync status")}</h2>
      {p ? (
        <div className="card stack">
          <div className="row small">
            <span><strong>{p.create}</strong> {t("to create")}</span>
            <span><strong>{p.update}</strong> {t("to update")}</span>
            <span><strong>{p.prune}</strong> {t("to delete")}</span>
            <span className="muted">{t("{n} unchanged", { n: p.unchanged })}</span>
            {st.lastSynced && <span className="muted">{t("last applied {when}", { when: timeAgo(st.lastSynced) })}</span>}
          </div>
          {(p.items ?? []).map((it) => (
            <details key={`${it.kind}/${it.namespace}/${it.name}`}>
              <summary className="small"><span className={`badge ${it.action === "create" ? "ok" : it.action === "prune" ? "bad" : "warn"}`}>{t(it.action)}</span> <span className="mono">{it.kind} {it.namespace ? `${it.namespace}/` : ""}{it.name}</span></summary>
              {it.diff && <pre className="file" style={{ marginTop: 6 }}>{it.diff}</pre>}
            </details>
          ))}
        </div>
      ) : <p className="muted">{t("Not previewed yet.")}</p>}

      {st.objects && st.objects.length > 0 && (
        <>
          <h2>{t("Objects")} <span className="muted small">{st.objects.length}</span></h2>
          <details className="card">
            <summary className="small">{t("Show everything this add-on manages")}</summary>
            <table className="svc-ports" style={{ marginTop: 8 }}>
              <tbody>
                {st.objects.map((o) => (
                  <tr key={`${o.group}/${o.kind}/${o.namespace}/${o.name}`}><td className="small">{o.kind}</td><td className="mono small">{o.namespace ? `${o.namespace}/` : ""}{o.name}</td></tr>
                ))}
              </tbody>
            </table>
          </details>
        </>
      )}

      {a.fromGit && (
        <>
          <h2>{t("Remove")}</h2>
          <div className="card stack small">
            <div className="row">
              <button disabled={busy} onClick={() => setConfirm("remove")}>{t("Stop managing (keep running)")}</button>
              <button disabled={busy} className="danger" onClick={() => setConfirm("uninstall")}>{t("Uninstall")}</button>
            </div>
            {confirm && (
              <div className="alert">
                {confirm === "remove"
                  ? t("rendimiento stops managing this add-on; everything it installed keeps running as it is.")
                  : t("This deletes the {n} objects this add-on installed, except CRDs, namespaces and volumes (their data is kept).", { n: st.objects?.length ?? 0 })}
                <div className="row" style={{ marginTop: 8 }}>
                  <button className={confirm === "uninstall" ? "danger" : "primary"} disabled={busy}
                    onClick={() => act(async () => { await installedApi.remove(a.name, confirm === "uninstall"); nav("/addons"); })}>
                    {confirm === "uninstall" ? t("Uninstall") : t("Stop managing")}
                  </button>
                  <button onClick={() => setConfirm(undefined)}>{t("Cancel")}</button>
                </div>
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}
