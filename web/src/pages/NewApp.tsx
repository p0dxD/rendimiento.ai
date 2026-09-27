import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, timeAgo, type Installation, type Proposal, type Repo, type Service, type Size } from "../api";
import { ErrorBox } from "../components/ui";

const langBadge: Record<string, string> = { go: "Go", node: "JS", python: "Py", java: "Jv", static: "</>", docker: "🐳" };
const sizes: { value: Size; label: string }[] = [
  { value: "small", label: "Small — 256 MB, ½ CPU" },
  { value: "medium", label: "Medium — 512 MB, 1 CPU" },
  { value: "large", label: "Large — 1 GB, 2 CPU" },
];

type Result = Awaited<ReturnType<typeof api.createApp>>;

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
    const t = setTimeout(() => {
      api.migration(name).then(
        (r) => setProposal((p) => (p ? { ...p, migration: r.migration ?? undefined } : p)),
        () => {},
      );
    }, 400);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, step]);

  const shown = useMemo(
    () => repos?.filter((r) => r.full_name.toLowerCase().includes(filter.toLowerCase())) ?? [],
    [repos, filter],
  );

  return (
    <>
      <h1>New app</h1>
      <p className="sub">Pick a repo. rendimiento works out how to build it and where to run it.</p>
      <div className="steps">
        <span className={step === 1 ? "on" : ""}>1 · Repository</span>
        <span className={step === 2 ? "on" : ""}>2 · Configure</span>
        <span className={step === 3 ? "on" : ""}>3 · Deploy</span>
      </div>
      <ErrorBox error={error} />

      {step === 1 && (
        <div className="card stack">
          {installs && installs.installations.length === 0 && (
            <div className="alert info">
              The GitHub App is not installed on any account yet.{" "}
              <a href={installs.installURL} target="_blank" rel="noreferrer">Install it on your repos</a>, then reload this page.
            </div>
          )}
          {installs && installs.installations.length > 0 && (
            <div className="row">
              <label className="field" style={{ flex: 1, minWidth: 180 }}>
                <span>GitHub account</span>
                <select value={inst} onChange={(e) => setInst(Number(e.target.value))}>
                  {installs.installations.map((i) => (
                    <option key={i.id} value={i.id}>{i.account.login}</option>
                  ))}
                </select>
              </label>
              <label className="field" style={{ flex: 2, minWidth: 200 }}>
                <span>Search</span>
                <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter repositories" />
              </label>
            </div>
          )}
          {inst && !repos && <p className="muted">Loading repositories…</p>}
          <div className="repo-list">
            {shown.map((r) => (
              <div key={r.id} className="repo-item" onClick={() => !busy && pick(r)} role="button" tabIndex={0}
                onKeyDown={(e) => e.key === "Enter" && !busy && pick(r)}>
                <div>
                  <strong>{r.full_name}</strong> {r.private && <span className="badge">private</span>}
                  <div className="small muted">default branch {r.default_branch} · pushed {timeAgo(r.pushed_at)}</div>
                </div>
                <span className="btn">{busy && repo?.id === r.id ? "Inspecting…" : "Select"}</span>
              </div>
            ))}
          </div>
          {installs && (
            <p className="small muted">Missing a repo? <a href={installs.installURL} target="_blank" rel="noreferrer">Give the GitHub App access to it</a>.</p>
          )}
        </div>
      )}

      {step === 2 && proposal && (
        <div className="stack">
          <div className="card stack">
            <strong>What we found in {proposal.repo}</strong>
            {proposal.detected.map((d) => (
              <div key={d.path} className="detect">
                <div className="lang">{langBadge[d.language] ?? d.language.slice(0, 2)}</div>
                <div>
                  <div><strong>{d.path === "." ? "repo root" : d.path}</strong> — {d.language}{d.framework ? ` / ${d.framework}` : ""}{d.version ? ` ${d.version}` : ""}, port {d.port}</div>
                  <ul className="reasons">{d.reasons.map((r) => <li key={r}>{r}</li>)}</ul>
                </div>
              </div>
            ))}
            {proposal.existing && <div className="alert info">This repo already has a <code>rendimiento.yaml</code>, so its settings are used as-is.</div>}
          </div>

          {proposal.migration && !proposal.migration.managed && (
            <div className={`card stack env-hero ${adopt ? "ok" : "warn"}`} style={{ display: "block" }}>
              <strong>This app is already running in namespace <code>{proposal.migration.namespace}</code></strong>
              <div className="small">
                {proposal.migration.deployments?.map((d) => <div key={d}>• deployment {d}</div>)}
                {proposal.migration.hosts?.map((h) => <div key={h}>• serving https://{h}</div>)}
                {proposal.migration.secrets && proposal.migration.secrets.length > 0 && <div>• secrets kept as they are: {proposal.migration.secrets.join(", ")}</div>}
                {proposal.migration.argocd && <div>• tracked by ArgoCD application <code>{proposal.migration.argocd}</code></div>}
              </div>
              <label className="row" style={{ gap: 8, cursor: "pointer" }}>
                <input type="checkbox" style={{ width: "auto" }} checked={adopt} onChange={(e) => { setAdopt(e.target.checked); if (e.target.checked) setName(proposal.migration!.namespace); }} />
                <span><strong>Migrate it to rendimiento.</strong> New pods start next to the old ones; traffic switches only when they are ready, then the old deployment is removed.</span>
              </label>
              {adopt && proposal.migration.argocd && (
                <div className="alert">
                  Detach it from ArgoCD first, without deleting anything, or the two will fight:
                  <pre className="file" style={{ marginTop: 6 }}>kubectl delete application {proposal.migration.argocd} -n argocd --cascade=orphan</pre>
                  Also disable its Jenkins job, and remove its <code>argocd/</code> folder from the repo only after detaching.
                </div>
              )}
              {!adopt && <div className="small muted">Or choose a different app name below to deploy a separate copy.</div>}
            </div>
          )}

          <div className="card">
            <label className="field" style={{ maxWidth: 360 }}>
              <span>App name (also its Kubernetes namespace)</span>
              <input value={name} disabled={adopt} onChange={(e) => setName(e.target.value)} />
            </label>
          </div>

          {services.map((s, i) => (
            <ServiceForm key={i} service={s} zones={zones} disabled={proposal.existing}
              onChange={(ns) => setServices(services.map((x, j) => (j === i ? ns : x)))} />
          ))}

          {Object.keys(proposal.files).length > 0 && proposal.railpack && (
            <div className="card stack">
              <strong>How to build</strong>
              <label className="row" style={{ gap: 8, cursor: "pointer", alignItems: "flex-start" }}>
                <input type="radio" checked={!keepDockerfiles} onChange={() => setKeepDockerfiles(false)} />
                <span><strong>From source, no Dockerfile</strong> <span className="muted small">(recommended)</span><br />
                  <span className="muted small">Railpack detects the language, installs dependencies and sets the start command on every build. Nothing to maintain in your repo; override the start command with <code>build.start</code> if needed.</span></span>
              </label>
              <label className="row" style={{ gap: 8, cursor: "pointer", alignItems: "flex-start" }}>
                <input type="radio" checked={keepDockerfiles} onChange={() => setKeepDockerfiles(true)} />
                <span><strong>Add a Dockerfile to the repo</strong><br />
                  <span className="muted small">Full control over the image (system packages, multi-stage builds). You maintain it from then on. You can switch any time: a service with a Dockerfile uses it, one without is built from source.</span></span>
              </label>
            </div>
          )}

          {Object.keys(proposal.files).length > 0 && keepDockerfiles && (
            <details className="card">
              <summary><strong>Files added to your repo</strong> <span className="muted small">(in the pull request, along with rendimiento.yaml)</span></summary>
              {Object.entries(proposal.files).map(([path, content]) => (
                <div key={path} style={{ marginTop: 12 }}>
                  <div className="mono small" style={{ marginBottom: 4 }}>{path}</div>
                  <pre className="file">{content}</pre>
                </div>
              ))}
            </details>
          )}

          <div className="row between">
            <button onClick={() => setStep(1)}>Back</button>
            <button className="primary big" disabled={busy || !name} onClick={deploy}>
              {busy ? "Working…" : adopt ? "Migrate & deploy" : proposal.existing ? "Deploy" : "Open PR & deploy"}
            </button>
          </div>
        </div>
      )}

      {step === 3 && result && (
        <div className="card stack">
          <h2 style={{ marginTop: 0 }}>{result.app.name} is set up</h2>
          {result.pr ? (
            <>
              <p>We opened <a href={result.pr.html_url} target="_blank" rel="noreferrer">pull request #{result.pr.number}</a> with the build config.
                Its build runs right away; <strong>merge it to deploy</strong>.</p>
              <p className="muted small">After that, every push to {result.app.defaultBranch} builds and deploys automatically. Other branches build and show their status on GitHub, but do not deploy.</p>
            </>
          ) : (
            <p>The first build is running now.</p>
          )}
          <div className="row">
            <Link className="btn primary" to={`/apps/${result.app.name}`}>Go to app</Link>
            {result.pr && <a className="btn" href={result.pr.html_url} target="_blank" rel="noreferrer">Review the PR</a>}
          </div>
        </div>
      )}
    </>
  );
}

function ServiceForm({ service: s, zones, disabled, onChange }: { service: Service; zones: string[]; disabled: boolean; onChange: (s: Service) => void }) {
  const set = (patch: Partial<Service>) => onChange({ ...s, ...patch });
  const zone = zones.find((z) => s.domain?.endsWith("." + z)) ?? "";
  const sub = zone ? s.domain!.slice(0, -(zone.length + 1)) : s.domain ?? "";
  const [envText, setEnvText] = useState(Object.entries(s.env ?? {}).map(([k, v]) => `${k}=${v}`).join("\n"));

  return (
    <fieldset className="card stack" disabled={disabled} style={{ margin: 0 }}>
      <div className="row between">
        <strong>Service: {s.name}</strong>
        <span className="muted small">{s.path === "." ? "repo root" : s.path} · {s.language}</span>
      </div>
      <div className="form-grid">
        <label className="field"><span>Public URL</span>
          <div className="domain-input">
            <input value={sub} placeholder="none" onChange={(e) => set({ domain: e.target.value ? (zone ? `${e.target.value}.${zone}` : e.target.value) : undefined })} />
            {zones.length > 0 && (
              <select value={zone} onChange={(e) => set({ domain: sub ? (e.target.value ? `${sub}.${e.target.value}` : sub) : undefined })}>
                {zones.map((z) => <option key={z} value={z}>.{z}</option>)}
                <option value="">custom domain</option>
              </select>
            )}
          </div>
        </label>
        <label className="field"><span>Size</span>
          <select value={s.size} onChange={(e) => set({ size: e.target.value as Size })}>
            {sizes.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </label>
        <label className="field"><span>Instances</span>
          <select value={s.replicas} onChange={(e) => set({ replicas: Number(e.target.value) })}>
            {[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}{n > 1 ? " (zero-downtime)" : ""}</option>)}
          </select>
        </label>
        <label className="field"><span>Port the app listens on</span>
          <input type="number" value={s.port} onChange={(e) => set({ port: Number(e.target.value) })} />
        </label>
        <label className="field"><span>Health check path</span>
          <input value={s.health?.path ?? ""} placeholder="none (e.g. /healthz)" onChange={(e) => set({ health: e.target.value ? { path: e.target.value } : undefined })} />
        </label>
        <label className="field"><span>Persistent storage</span>
          <select value={s.volume?.size ?? ""} onChange={(e) => set({ volume: e.target.value ? { size: e.target.value, mount: s.volume?.mount ?? "/data" } : undefined })}>
            <option value="">none (stateless)</option>
            {["1Gi", "5Gi", "10Gi", "20Gi"].map((v) => <option key={v} value={v}>{v} Longhorn volume</option>)}
          </select>
        </label>
        {s.volume && (
          <label className="field"><span>Mounted at</span>
            <input value={s.volume.mount} onChange={(e) => set({ volume: { ...s.volume!, mount: e.target.value } })} />
          </label>
        )}
        <label className="field"><span>Secrets (names, comma-separated)</span>
          <input value={(s.secrets ?? []).join(", ")} placeholder="e.g. api-keys"
            onChange={(e) => set({ secrets: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })} />
        </label>
      </div>
      <div className="form-grid">
        <label className="field"><span>Test image</span>
          <input value={s.test?.image ?? ""} placeholder="no tests"
            onChange={(e) => set({ test: e.target.value ? { image: e.target.value, command: s.test?.command ?? "" } : undefined })} />
        </label>
        <label className="field" style={{ gridColumn: "span 2" }}><span>Test command</span>
          <input className="mono" value={s.test?.command ?? ""} disabled={!s.test}
            onChange={(e) => set({ test: { image: s.test!.image, command: e.target.value } })} />
        </label>
      </div>
      <label className="field"><span>Environment variables (KEY=value per line, not secret)</span>
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
        <p className="small muted">Also from rendimiento.yaml:{s.secretEnv && ` ${Object.keys(s.secretEnv).length} variable(s) from secret keys;`}{s.secretFiles && ` ${s.secretFiles.length} secret file mount(s);`}{s.streaming && " streaming responses enabled;"}</p>
      )}
      {s.secrets && s.secrets.length > 0 && <p className="small muted">You'll enter secret values on the app page after the first deploy. They go straight into Kubernetes, never into git.</p>}
    </fieldset>
  );
}
