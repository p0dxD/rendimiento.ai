import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, catalogApi, timeAgo, type CatalogEntry, type Installation, type Need, type Proposal, type Repo, type Service, type Size } from "../api";
import { ErrorBox } from "../components/ui";
import { t } from "../i18n";

const langBadge: Record<string, string> = { go: "Go", node: "JS", python: "Py", java: "Jv", static: "</>", docker: "🐳" };
// Labels are translated where shown.
const sizes: { value: Size; label: string }[] = [
  { value: "small", label: "Small — 256 MB, ½ CPU" },
  { value: "medium", label: "Medium — 512 MB, 1 CPU" },
  { value: "large", label: "Large — 1 GB, 2 CPU" },
];

type Result = Awaited<ReturnType<typeof api.createApp>>;

/**
 * The "New app" page, on one screen: repositories on the left; on the right, what was detected in the one chosen, its
 * services with their address, size, instances and needs (more options folded away), then the pull request (or a build right away).
 */
export function NewApp() {
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [installs, setInstalls] = useState<{ installations: Installation[]; installURL: string }>();
  const [inst, setInst] = useState<number>();
  const [repos, setRepos] = useState<Repo[]>();
  const [filter, setFilter] = useState("");
  const [repo, setRepo] = useState<Repo>();
  const [proposal, setProposal] = useState<Proposal>();
  const [zones, setZones] = useState<string[]>([]);
  const [services, setServices] = useState<Service[]>([]);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<Result>();
  const [adopt, setAdopt] = useState(false);
  // With Railpack, generated Dockerfiles are optional: keeping one ("eject")
  // gives full control over the image, at the cost of maintaining it.
  const [keepDockerfiles, setKeepDockerfiles] = useState(false);

  useEffect(() => {
    api.installations().then((r) => {
      setInstalls(r);
      if (r.installations.length > 0) setInst(r.installations[0].id);
    }, setError);
    api.zones().then(setZones, () => setZones([]));
  }, []);

  useEffect(() => {
    if (!inst) return;
    setRepos(undefined);
    api.repos(inst).then(setRepos, setError);
  }, [inst]);

  const pick = async (r: Repo) => {
    setRepo(r);
    setBusy(true);
    setError(undefined);
    try {
      const p = await api.propose(inst!, r.full_name, r.default_branch);
      setProposal(p);
      setServices(p.spec.services);
      setName(p.suggestedName ?? r.name.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40));
      setAdopt(false);
      setKeepDockerfiles(!p.railpack);
      setStep(2);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const deploy = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const res = await api.createApp({
        installation: inst!,
        repo: repo!.full_name,
        defaultBranch: repo!.default_branch,
        name,
        spec: { ...proposal!.spec, services },
        files: keepDockerfiles ? proposal!.files : {},
        existing: proposal!.existing,
        adopt,
      });
      setResult(res);
      setStep(3);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  // Re-check for an existing deployment whenever the app name changes, so a
  // repo can be migrated from a namespace with a different name (personal → podoi).
  useEffect(() => {
    if (step !== 2 || !proposal || adopt || !/^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/.test(name)) return;
    const timer = setTimeout(() => {
      api.migration(name).then(
        (r) => setProposal((p) => (p ? { ...p, migration: r.migration ?? undefined } : p)),
        () => {},
      );
    }, 400);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, step]);

  const shown = useMemo(
    () => repos?.filter((r) => r.full_name.toLowerCase().includes(filter.toLowerCase())) ?? [],
    [repos, filter],
  );

  const zoneOf = (d?: string) => zones.find((z) => d?.endsWith("." + z)) ?? "";
  const setService = (i: number, patch: Partial<Service>) => setServices(services.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  const hasNeed = (s: Service, w: string) => (s.needs ?? []).some((n) => n === w);
  const toggleNeed = (i: number, w: string) => {
    const s = services[i];
    const needs = hasNeed(s, w) ? (s.needs ?? []).filter((n) => n !== w) : [...(s.needs ?? []), w];
    setService(i, { needs: needs.length ? needs : undefined });
  };

  return (
    <>
      <h1>{t("New app")}</h1>
      <p className="sub" style={{ marginBottom: 0 }}>{t("Choose a repository: on the right, what we found and how it would run.")}</p>
      <ErrorBox error={error} />

      <div className="lista-detalle">
        <aside className="ld-lista">
          {installs && installs.installations.length === 0 && (
            <div className="alert info">
              {t("The GitHub App is not installed on any account yet.")}{" "}
              <a href={installs.installURL} target="_blank" rel="noreferrer">{t("Install it on your repos")}</a>{t(", then reload this page.")}
            </div>
          )}
          {installs && installs.installations.length > 1 && (
            <select aria-label={t("GitHub account")} value={inst} onChange={(e) => setInst(Number(e.target.value))}>
              {installs.installations.map((i) => <option key={i.id} value={i.id}>{i.account.login}</option>)}
            </select>
          )}
          {installs && installs.installations.length > 0 && (
            <label className="ld-buscar">
              <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="M15.5 15.5 21 21" /></svg>
              <input type="search" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t("Filter repositories")} aria-label={t("Filter repositories")} />
            </label>
          )}
          {inst && !repos && <p className="small muted" style={{ margin: "8px 4px" }}>{t("Loading repositories…")}</p>}
          {shown.map((r) => (
            <button key={r.id} className={`ld-item${repo?.id === r.id ? " on" : ""}${busy && repo?.id === r.id ? " corre" : ""}`} aria-current={repo?.id === r.id}
              disabled={busy} onClick={() => { setResult(undefined); pick(r); }}>
              <span style={{ minWidth: 0 }}>
                <span className="ld-nombre">{r.name}</span>
                <span className="ld-sub">{busy && repo?.id === r.id ? t("Inspecting…") : `${r.private ? t("private") + " · " : ""}${t("pushed {when}", { when: timeAgo(r.pushed_at) })}`}</span>
              </span>
            </button>
          ))}
          {installs && (
            <p className="small muted" style={{ margin: "8px 4px 0" }}>{t("Missing a repo?")} <a href={installs.installURL} target="_blank" rel="noreferrer">{t("Give the GitHub App access to it")}</a>.</p>
          )}
        </aside>

        {step === 1 && (
          <main className="ld-detalle">
            <p className="muted" style={{ margin: 0 }}>{t("Pick a repo. rendimiento works out how to build it and where to run it.")}</p>
          </main>
        )}

        {step === 2 && proposal && (
          <main className="ld-detalle" key={proposal.repo}>
            <div className="row between" style={{ alignItems: "flex-end" }}>
              <label className="field" style={{ flex: "1 1 240px", maxWidth: 340 }}>
                <span>{t("App name (also its Kubernetes namespace)")}</span>
                <input value={name} disabled={adopt} onChange={(e) => setName(e.target.value)} />
              </label>
              <span className="small muted">
                {(proposal.detected ?? []).map((d) => `${d.path === "." ? t("repo root") : d.path}: ${d.language}${d.framework ? ` / ${d.framework}` : ""}`).join(" · ")}
              </span>
            </div>
            {proposal.existing && <div className="alert info">{t("This repo already has a")} <code>rendimiento.yaml</code>{t(", so its settings are used as-is.")}</div>}

            {proposal.migration && !proposal.migration.managed && (
              <div className={`card stack env-hero ${adopt ? "ok" : "warn"}`} style={{ display: "block" }}>
                <strong>{t("This app is already running in namespace")} <code>{proposal.migration.namespace}</code></strong>
                <div className="small">
                  {proposal.migration.deployments?.map((d) => <div key={d}>• {t("deployment {name}", { name: d })}</div>)}
                  {proposal.migration.hosts?.map((h) => <div key={h}>• {t("serving {url}", { url: `https://${h}` })}</div>)}
                  {proposal.migration.secrets && proposal.migration.secrets.length > 0 && <div>• {t("secrets kept as they are: {list}", { list: proposal.migration.secrets.join(", ") })}</div>}
                  {proposal.migration.argocd && <div>• {t("tracked by ArgoCD application")} <code>{proposal.migration.argocd}</code></div>}
                </div>
                <label className="row" style={{ gap: 8, cursor: "pointer" }}>
                  <input type="checkbox" style={{ width: "auto" }} checked={adopt} onChange={(e) => { setAdopt(e.target.checked); if (e.target.checked) setName(proposal.migration!.namespace); }} />
                  <span><strong>{t("Migrate it to rendimiento.")}</strong> {t("New pods start next to the old ones; traffic switches only when they are ready, then the old deployment is removed.")}</span>
                </label>
                {adopt && proposal.migration.argocd && (
                  <div className="alert">
                    {t("Detach it from ArgoCD first, without deleting anything, or the two will fight:")}
                    <pre className="file" style={{ marginTop: 6 }}>kubectl delete application {proposal.migration.argocd} -n argocd --cascade=orphan</pre>
                    {t("Also disable its Jenkins job, and remove its")} <code>argocd/</code> {t("folder from the repo only after detaching.")}
                  </div>
                )}
                {!adopt && <div className="small muted">{t("Or choose a different app name below to deploy a separate copy.")}</div>}
              </div>
            )}

            <section>
              <h3 className="ld-h3">{t("Services")}</h3>
              <fieldset className="nueva-servicios" disabled={proposal.existing}>
                <div className="nueva-fila nueva-cabeza" aria-hidden="true"><span>{t("Service")}</span><span>{t("Public URL")}</span><span>{t("Size")}</span><span>{t("Instances")}</span></div>
                {services.map((s, i) => {
                  const zone = zoneOf(s.domain);
                  const sub = zone ? s.domain!.slice(0, -(zone.length + 1)) : s.domain ?? "";
                  return (
                    <div key={i} className="nueva-servicio" style={{ ["--d" as string]: `${i * 70}ms` }}>
                      <div className="nueva-fila">
                        <strong>{s.name}</strong>
                        <div className="domain-input">
                          <input value={sub} placeholder={t("none: only inside the cluster")} aria-label={t("Public URL of {name}", { name: s.name })}
                            onChange={(e) => setService(i, { domain: e.target.value ? (zone ? `${e.target.value}.${zone}` : e.target.value) : undefined })} />
                          {zones.length > 0 && (
                            <select value={zone} aria-label={t("Domain")} onChange={(e) => setService(i, { domain: sub ? (e.target.value ? `${sub}.${e.target.value}` : sub) : undefined })}>
                              {zones.map((z) => <option key={z} value={z}>.{z}</option>)}
                              <option value="">{t("custom domain")}</option>
                            </select>
                          )}
                        </div>
                        <select value={s.size} aria-label={t("Size")} onChange={(e) => setService(i, { size: e.target.value as Size })}>
                          {sizes.map((o) => <option key={o.value} value={o.value}>{t(o.label)}</option>)}
                        </select>
                        <select value={s.replicas} aria-label={t("Instances")} onChange={(e) => setService(i, { replicas: Number(e.target.value) })}>
                          {[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}</option>)}
                        </select>
                      </div>
                      <div className="row small nueva-necesita">
                        <span className="muted">{t("Needs")}</span>
                        <button type="button" className={`chip-btn${hasNeed(s, "postgres") ? " on" : ""}`} aria-pressed={hasNeed(s, "postgres")} onClick={() => toggleNeed(i, "postgres")}>PostgreSQL</button>
                        <button type="button" className={`chip-btn${hasNeed(s, "redis") ? " on" : ""}`} aria-pressed={hasNeed(s, "redis")} onClick={() => toggleNeed(i, "redis")}>Redis</button>
                        <span className="muted">{t("rendimiento runs them and passes DATABASE_URL and REDIS_URL")}</span>
                      </div>
                    </div>
                  );
                })}
              </fieldset>
            </section>

            <details className="nueva-mas">
              <summary><strong>{t("More options")}</strong> <span className="small muted">{t("ports, health check, disk, secrets, tests, how to build")}</span></summary>
              <div className="stack" style={{ marginTop: 12 }}>
                {services.map((s, i) => (
                  <ServiceForm key={i} service={s} zones={zones} disabled={proposal.existing}
                    onChange={(ns) => setServices(services.map((x, j) => (j === i ? ns : x)))} />
                ))}
                {Object.keys(proposal.files).length > 0 && proposal.railpack && (
                  <div className="card stack">
                    <strong>{t("How to build")}</strong>
                    <label className="row" style={{ gap: 8, cursor: "pointer", alignItems: "flex-start" }}>
                      <input type="radio" checked={!keepDockerfiles} onChange={() => setKeepDockerfiles(false)} />
                      <span><strong>{t("From source, no Dockerfile")}</strong> <span className="muted small">({t("recommended")})</span><br />
                        <span className="muted small">{t("Railpack detects the language, installs dependencies and sets the start command on every build. Nothing to maintain in your repo; override the start command with")} <code>build.start</code> {t("if needed.")}</span></span>
                    </label>
                    <label className="row" style={{ gap: 8, cursor: "pointer", alignItems: "flex-start" }}>
                      <input type="radio" checked={keepDockerfiles} onChange={() => setKeepDockerfiles(true)} />
                      <span><strong>{t("Add a Dockerfile to the repo")}</strong><br />
                        <span className="muted small">{t("Full control over the image (system packages, multi-stage builds). You maintain it from then on. You can switch any time: a service with a Dockerfile uses it, one without is built from source.")}</span></span>
                    </label>
                  </div>
                )}
                {Object.keys(proposal.files).length > 0 && keepDockerfiles && (
                  <details className="card">
                    <summary><strong>{t("Files added to your repo")}</strong> <span className="muted small">({t("in the pull request, along with rendimiento.yaml")})</span></summary>
                    {Object.entries(proposal.files).map(([path, content]) => (
                      <div key={path} style={{ marginTop: 12 }}>
                        <div className="mono small" style={{ marginBottom: 4 }}>{path}</div>
                        <pre className="file">{content}</pre>
                      </div>
                    ))}
                  </details>
                )}
                <details className="card">
                  <summary><strong>{t("What we found in {repo}", { repo: proposal.repo })}</strong></summary>
                  {(proposal.detected ?? []).map((d) => (
                    <div key={d.path} className="detect">
                      <div className="lang">{langBadge[d.language] ?? d.language.slice(0, 2)}</div>
                      <div>
                        <div><strong>{d.path === "." ? t("repo root") : d.path}</strong> — {d.language}{d.framework ? ` / ${d.framework}` : ""}{d.version ? ` ${d.version}` : ""}, {t("port {n}", { n: d.port })}</div>
                        <ul className="reasons">{d.reasons.map((r) => <li key={r}>{r}</li>)}</ul>
                      </div>
                    </div>
                  ))}
                </details>
              </div>
            </details>

            <div className="nueva-pie">
              <button className="primary big" disabled={busy || !name} onClick={deploy}>
                {busy ? t("Working…") : adopt ? t("Migrate & deploy") : proposal.existing ? t("Deploy") : t("Open PR & deploy")}
              </button>
            </div>
          </main>
        )}

        {step === 3 && result && (
          <main className="ld-detalle">
            <h2 className="ld-titulo">{t("{name} is set up", { name: result.app.name })}</h2>
            {result.pr ? (
              <>
                <p style={{ margin: 0 }}>{t("We opened")} <a href={result.pr.html_url} target="_blank" rel="noreferrer">{t("pull request #{n}", { n: result.pr.number })}</a> {t("with the build config. Its build runs right away;")} <strong>{t("merge it to deploy")}</strong>.</p>
                <p className="muted small" style={{ margin: 0 }}>{t("After that, every push to {branch} builds and deploys automatically. Other branches build and show their status on GitHub, but do not deploy.", { branch: result.app.defaultBranch })}</p>
              </>
            ) : (
              <p style={{ margin: 0 }}>{t("The first build is running now.")}</p>
            )}
            <div className="row">
              <Link className="btn primary" to={`/apps/${result.app.name}`}>{t("Go to app")}</Link>
              {result.pr && <a className="btn" href={result.pr.html_url} target="_blank" rel="noreferrer">{t("Review the PR")}</a>}
            </div>
          </main>
        )}
      </div>
    </>
  );
}

/** What a service depends on: rendimiento runs it (database, cache) or
 *  looks it up (another service), and injects how to reach it. */
function Needs({ needs, onChange }: { needs: Need[]; onChange: (n: Need[]) => void }) {
  const [catalog, setCatalog] = useState<CatalogEntry[]>();
  const [picking, setPicking] = useState("");
  const has = (w: string) => needs.some((n) => n === w);
  const toggle = (w: string) => onChange(has(w) ? needs.filter((n) => n !== w) : [...needs, w]);
  const services = needs.filter((n): n is { service: string; env?: string } => typeof n === "object" && "service" in n);
  useEffect(() => {
    if (picking === "open" && !catalog) catalogApi.list().then((c) => setCatalog(c.entries.filter((e) => e.group !== "infrastructure")), () => setCatalog([]));
  }, [picking, catalog]);
  return (
    <div className="stack">
      <div className="small"><strong>{t("Needs")}</strong> <span className="muted">{t("rendimiento provides these and injects how to reach them; nothing to configure")}</span></div>
      <div className="row">
        <label className="row small" style={{ gap: 6 }}><input type="checkbox" checked={has("postgres")} onChange={() => toggle("postgres")} /> {t("PostgreSQL database")} <span className="mono muted">DATABASE_URL</span></label>
        <label className="row small" style={{ gap: 6 }}><input type="checkbox" checked={has("redis")} onChange={() => toggle("redis")} /> {t("Redis cache")} <span className="mono muted">REDIS_URL</span></label>
        <button type="button" className="small" onClick={() => setPicking(picking ? "" : "open")}>{picking ? t("Done") : t("+ Call another service")}</button>
      </div>
      {services.map((n) => (
        <div key={n.service} className="row small">
          <span className="mono">{n.service}</span> → <span className="mono">{n.env ?? n.service.split("/").pop()!.toUpperCase().replace(/[^A-Z0-9]+/g, "_") + "_URL"}</span>
          <button type="button" className="small" onClick={() => onChange(needs.filter((x) => x !== n))}>{t("remove")}</button>
        </div>
      ))}
      {picking && (
        <select value="" onChange={(e) => {
          const entry = catalog?.find((c) => c.id === e.target.value);
          if (entry) onChange([...needs, { service: entry.id, env: entry.env }]);
        }}>
          <option value="">{catalog ? t("Pick a service from the Services tab…") : t("Loading services…")}</option>
          {catalog?.filter((c) => !services.some((n) => n.service === c.id)).map((c) => (
            <option key={c.id} value={c.id}>{c.title} ({c.id}) → {c.env}</option>
          ))}
        </select>
      )}
    </div>
  );
}

/** One service's form: port, size, replicas, domain, health check, volume, environment and secrets. */
function ServiceForm({ service: s, zones, disabled, onChange }: { service: Service; zones: string[]; disabled: boolean; onChange: (s: Service) => void }) {
  const set = (patch: Partial<Service>) => onChange({ ...s, ...patch });
  const zone = zones.find((z) => s.domain?.endsWith("." + z)) ?? "";
  const sub = zone ? s.domain!.slice(0, -(zone.length + 1)) : s.domain ?? "";
  const [envText, setEnvText] = useState(Object.entries(s.env ?? {}).map(([k, v]) => `${k}=${v}`).join("\n"));

  return (
    <fieldset className="card stack" disabled={disabled} style={{ margin: 0 }}>
      <div className="row between">
        <strong>{t("Service: {name}", { name: s.name })}</strong>
        <span className="muted small">{s.path === "." ? t("repo root") : s.path} · {s.language}</span>
      </div>
      <div className="form-grid">
        <label className="field"><span>{t("Public URL")}</span>
          <div className="domain-input">
            <input value={sub} placeholder={t("none")} onChange={(e) => set({ domain: e.target.value ? (zone ? `${e.target.value}.${zone}` : e.target.value) : undefined })} />
            {zones.length > 0 && (
              <select value={zone} onChange={(e) => set({ domain: sub ? (e.target.value ? `${sub}.${e.target.value}` : sub) : undefined })}>
                {zones.map((z) => <option key={z} value={z}>.{z}</option>)}
                <option value="">{t("custom domain")}</option>
              </select>
            )}
          </div>
        </label>
        <label className="field"><span>{t("Size")}</span>
          <select value={s.size} onChange={(e) => set({ size: e.target.value as Size })}>
            {sizes.map((o) => <option key={o.value} value={o.value}>{t(o.label)}</option>)}
          </select>
        </label>
        <label className="field"><span>{t("Instances")}</span>
          <select value={s.replicas} onChange={(e) => set({ replicas: Number(e.target.value) })}>
            {[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}{n > 1 ? ` (${t("zero-downtime")})` : ""}</option>)}
          </select>
        </label>
        <label className="field"><span>{t("Port the app listens on")}</span>
          <input type="number" value={s.port} onChange={(e) => set({ port: Number(e.target.value) })} />
        </label>
        <label className="field"><span>{t("Health check path")}</span>
          <input value={s.health?.path ?? ""} placeholder={t("none (e.g. /healthz)")} onChange={(e) => set({ health: e.target.value ? { path: e.target.value } : undefined })} />
        </label>
        <label className="field"><span>{t("Persistent storage")}</span>
          <select value={s.volume?.size ?? ""} onChange={(e) => set({ volume: e.target.value ? { size: e.target.value, mount: s.volume?.mount ?? "/data" } : undefined })}>
            <option value="">{t("none (stateless)")}</option>
            {["1Gi", "5Gi", "10Gi", "20Gi"].map((v) => <option key={v} value={v}>{t("{size} Longhorn volume", { size: v })}</option>)}
          </select>
        </label>
        {s.volume && (
          <label className="field"><span>{t("Mounted at")}</span>
            <input value={s.volume.mount} onChange={(e) => set({ volume: { ...s.volume!, mount: e.target.value } })} />
          </label>
        )}
        <label className="field"><span>{t("Secrets (names, comma-separated)")}</span>
          <input value={(s.secrets ?? []).join(", ")} placeholder={t("e.g. api-keys")}
            onChange={(e) => set({ secrets: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })} />
        </label>
      </div>
      <Needs needs={s.needs ?? []} onChange={(needs) => set({ needs: needs.length ? needs : undefined })} />
      <div className="form-grid">
        <label className="field"><span>{t("Test image")}</span>
          <input value={s.test?.image ?? ""} placeholder={t("no tests")}
            onChange={(e) => set({ test: e.target.value ? { image: e.target.value, command: s.test?.command ?? "" } : undefined })} />
        </label>
        <label className="field" style={{ gridColumn: "span 2" }}><span>{t("Test command")}</span>
          <input className="mono" value={s.test?.command ?? ""} disabled={!s.test}
            onChange={(e) => set({ test: { image: s.test!.image, command: e.target.value } })} />
        </label>
      </div>
      <label className="field"><span>{t("Environment variables (KEY=value per line, not secret)")}</span>
        <textarea rows={2} className="mono" value={envText} onChange={(e) => {
          setEnvText(e.target.value);
          const env: Record<string, string> = {};
          for (const line of e.target.value.split("\n")) {
            const i = line.indexOf("=");
            if (i > 0) env[line.slice(0, i).trim()] = line.slice(i + 1);
          }
          set({ env: Object.keys(env).length ? env : undefined });
        }} />
      </label>
      {(s.secretEnv || s.secretFiles || s.streaming) && (
        <p className="small muted">{t("Also from rendimiento.yaml:")}{s.secretEnv && ` ${t("{n} variable(s) from secret keys;", { n: Object.keys(s.secretEnv).length })}`}{s.secretFiles && ` ${t("{n} secret file mount(s);", { n: s.secretFiles.length })}`}{s.streaming && ` ${t("streaming responses enabled;")}`}</p>
      )}
      {s.secrets && s.secrets.length > 0 && <p className="small muted">{t("You'll enter secret values on the app page after the first deploy. They go straight into Kubernetes, never into git.")}</p>}
    </fieldset>
  );
}
