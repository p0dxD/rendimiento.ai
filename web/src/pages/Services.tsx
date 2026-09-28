import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { catalogApi, timeAgo, type CatalogEntry, type CatalogGroup } from "../api";
import { ErrorBox, usePoll } from "../components/ui";

const groups: { id: CatalogGroup; label: string; blurb: string }[] = [
  { id: "apps", label: "Your apps & add-ons", blurb: "Deployed by rendimiento: your apps (described by each repo's rendimiento.yaml) and installed add-ons." },
  { id: "other", label: "Elsewhere in the cluster", blurb: "Deployed another way (ArgoCD, Helm, kubectl). Apps can call them the same way; rendimiento only reads them." },
  { id: "infrastructure", label: "Cluster internals", blurb: "The cluster's own plumbing (ingress, certificates, storage, CI). Rarely something an app should call." },
];

// What services do, in the order sections appear.
const categories: { id: string; label: string }[] = [
  { id: "database", label: "Databases" },
  { id: "messaging", label: "Caches & queues" },
  { id: "ai", label: "AI" },
  { id: "storage", label: "Storage" },
  { id: "monitoring", label: "Monitoring & analytics" },
  { id: "web", label: "Web & APIs" },
  { id: "devtools", label: "Developer tools" },
  { id: "platform", label: "Platform" },
];
const categoryLabel = (id: string) => categories.find((c) => c.id === id)?.label ?? id;

const originLabel: Record<CatalogEntry["origin"]["kind"], string> = {
  rendimiento: "rendimiento",
  addon: "add-on",
  argocd: "ArgoCD",
  helm: "Helm",
  manual: "kubectl",
};

export function Services() {
  const { ns, name } = useParams();
  const [group, setGroup] = useState<CatalogGroup>("apps");
  const [q, setQ] = useState("");
  const [category, setCategory] = useState<string>();
  const [refreshing, setRefreshing] = useState(false);
  const { data, error, setData } = usePoll(() => catalogApi.list(), [], 60000);

  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    data?.entries.forEach((e) => (c[e.group] = (c[e.group] ?? 0) + 1));
    return c;
  }, [data]);

  // Search spans every group; otherwise the current tab. Categories filter both.
  const inScope = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (data?.entries ?? []).filter((e) => {
      if (needle) {
        const hay = [e.id, e.title, e.description, e.origin.summary, e.protocol, categoryLabel(e.category), ...(e.public ?? []), ...(e.origin.images ?? []).map((i) => i.ref)]
          .join(" ")
          .toLowerCase();
        return hay.includes(needle);
      }
      return e.group === group;
    });
  }, [data, group, q]);
  const shown = inScope.filter((e) => !category || e.category === category);
  const catCounts: Record<string, number> = {};
  inScope.forEach((e) => (catCounts[e.category] = (catCounts[e.category] ?? 0) + 1));

  const refresh = async () => {
    setRefreshing(true);
    try {
      setData(await catalogApi.list(true));
    } finally {
      setRefreshing(false);
    }
  };

  if (error) return <ErrorBox error={error} />;
  if (!data) return <p className="muted">Looking around the cluster…</p>;
  const focus = ns && name ? `${ns}/${name}` : undefined;

  return (
    <>
      <div className="row between">
        <div>
          <h1>Services</h1>
          <p className="sub" style={{ marginBottom: 0 }}>
            Everything your apps can call: where it comes from, what it exposes and how to connect to it.
          </p>
        </div>
        <div className="row small muted">
          updated {timeAgo(data.generated)}
          <button onClick={refresh} disabled={refreshing}>{refreshing ? "Looking…" : "Refresh"}</button>
        </div>
      </div>

      <div className="row" style={{ marginTop: 16 }}>
        <input className="svc-search" placeholder="Search by name, host, image or protocol…" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>

      {!q && (
        <div className="tabs">
          {groups.map((g) => (
            <a key={g.id} href="#" className={group === g.id ? "active" : ""}
              onClick={(e) => { e.preventDefault(); setGroup(g.id); setCategory(undefined); }}>
              {g.label} <span className="muted small">{counts[g.id] ?? 0}</span>
            </a>
          ))}
        </div>
      )}
      {!q && <p className="small muted" style={{ marginTop: 0 }}>{groups.find((g) => g.id === group)?.blurb}</p>}
      {q && <p className="small muted" style={{ marginTop: 16 }}>{shown.length} match{shown.length === 1 ? "" : "es"} across all groups</p>}

      <div className="row svc-cats">
        <button className={`chip-btn ${!category ? "on" : ""}`} onClick={() => setCategory(undefined)}>All <span>{inScope.length}</span></button>
        {categories.filter((c) => catCounts[c.id]).map((c) => (
          <button key={c.id} className={`chip-btn ${category === c.id ? "on" : ""}`} onClick={() => setCategory(category === c.id ? undefined : c.id)}>
            {c.label} <span>{catCounts[c.id]}</span>
          </button>
        ))}
      </div>

      {categories.map((c) => {
        const items = shown.filter((e) => e.category === c.id);
        if (items.length === 0) return null;
        return (
          <section key={c.id} className="svc-section">
            <h2>{c.label} <span className="muted small">{items.length}</span></h2>
            <div className="stack">
              {items.map((e) => <ServiceCard key={e.id} e={e} open={focus === e.id} />)}
            </div>
          </section>
        );
      })}
      {shown.length === 0 && <div className="card muted" style={{ marginTop: 16 }}>Nothing here.</div>}

      {group === "apps" && !q && <DocsHint />}
    </>
  );
}

function ServiceCard({ e, open: initiallyOpen }: { e: CatalogEntry; open: boolean }) {
  const [open, setOpen] = useState(initiallyOpen);
  const apps = new Set(e.usedBy.map((u) => u.app ?? u.namespace));
  const readyTone = e.pods === 0 ? "idle" : e.ready === e.pods ? "ok" : e.ready === 0 ? "bad" : "warn";
  return (
    <div className="card svc" id={e.id}>
      <div className="row between" style={{ cursor: "pointer", flexWrap: "nowrap" }} onClick={() => setOpen(!open)}>
        <div style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 8 }}>
            <strong>{e.title}</strong>
            {e.title !== e.name && <span className="mono small muted">{e.name}</span>}
            <span className="chip">{e.namespace}</span>
            <span className="chip">{e.protocol}</span>
            <span className="chip">{originLabel[e.origin.kind]}</span>
          </div>
          <div className="small muted svc-line">
            {e.description ?? e.origin.summary}
          </div>
        </div>
        <div className="row" style={{ gap: 8, flexWrap: "nowrap" }}>
          {e.usedBy.length > 0 && <span className="small muted hide-sm">used by {apps.size} app{apps.size === 1 ? "" : "s"}</span>}
          {e.pods > 0 && <span className={`badge ${readyTone}`}>{e.ready}/{e.pods} ready</span>}
          <span className="small muted">{open ? "▾" : "▸"}</span>
        </div>
      </div>

      {open && (
        <div className="svc-body">
          <section>
            <h3>Where it comes from</h3>
            <p className="small">{e.origin.summary}</p>
            {e.origin.app && e.origin.kind === "addon" && (
              <div className="small">Add-on <Link to={`/addons/${e.origin.app}`}>{e.origin.app}</Link></div>
            )}
            {e.origin.app && e.origin.kind !== "addon" && (
              <div className="small">App <Link to={`/apps/${e.origin.app}`}>{e.origin.app}</Link>{e.origin.release ? ` · release #${e.origin.release}` : ""}</div>
            )}
            {e.origin.repoUrl && (
              <div className="small">Code <a href={e.origin.repoUrl} target="_blank" rel="noreferrer">{e.origin.repoUrl.replace("https://github.com/", "")}</a></div>
            )}
            {e.origin.argoApp && <div className="small">ArgoCD application <span className="mono">{e.origin.argoApp}</span></div>}
            {e.origin.helmRelease && <div className="small">Helm release <span className="mono">{e.origin.helmRelease}</span></div>}
            {(e.origin.images ?? []).map((i) => (
              <div key={i.ref} className="small svc-image">
                {i.built ? "Image built from the repo " : "Image "}
                {i.link ? <a href={i.link} target="_blank" rel="noreferrer" className="mono">{i.ref}</a> : <span className="mono">{i.ref}</span>}
              </div>
            ))}
            {e.docs && <div className="small" style={{ marginTop: 6 }}><a href={e.docs} target="_blank" rel="noreferrer">API documentation ↗</a></div>}
          </section>

          <section>
            <h3>What it exposes</h3>
            <table className="svc-ports">
              <tbody>
                {e.ports.map((p) => (
                  <tr key={p.port}>
                    <td className="mono small">{p.port}</td>
                    <td className="small muted">→ {p.target}</td>
                    <td className="small">{p.protocol}{p.name ? ` · ${p.name}` : ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {e.health && <div className="small">Health check <span className="mono">{e.health}</span></div>}
            {(e.endpoints ?? []).length > 0 && (
              <ul className="small svc-endpoints">
                {e.endpoints!.map((x) => <li key={x}>{x}</li>)}
              </ul>
            )}
            {(e.public ?? []).map((u) => (
              <div key={u} className="small">Public <a href={u} target="_blank" rel="noreferrer">{u.replace("https://", "")}</a></div>
            ))}
            {e.lan && (
              <div className="small" style={{ color: "var(--warn)" }}>
                Also open on your local network at <span className="mono">{e.lan}</span> (LoadBalancer)
              </div>
            )}
            <div className="small muted" style={{ marginTop: 6 }}>
              {e.networkPolicies === 0
                ? "No network policy in its namespace: any pod in the cluster can connect."
                : `${e.networkPolicies} network polic${e.networkPolicies === 1 ? "y limits" : "ies limit"} who can connect in this namespace.`}
            </div>
          </section>

          <section>
            <h3>How to connect</h3>
            <div className="small muted">From any app</div>
            <Copyable text={e.url} />
            {e.group === "apps" && (
              <>
                <div className="small muted" style={{ marginTop: 6 }}>From another service of the {e.origin.app} app</div>
                <Copyable text={e.shortUrl} />
              </>
            )}
            <div className="small muted" style={{ marginTop: 8 }}>In the calling service's rendimiento.yaml</div>
            <Copyable text={e.snippet} block />
          </section>

          <section>
            <h3>Used by</h3>
            {e.usedBy.length === 0 ? (
              <p className="small muted">Nothing found. Only plain environment values are checked; addresses stored in secrets aren't read.</p>
            ) : (
              <ul className="small svc-users">
                {e.usedBy.map((u) => (
                  <li key={u.namespace + u.workload + u.env}>
                    {u.app ? <Link to={`/apps/${u.app}`}>{u.app}</Link> : <span className="mono">{u.namespace}</span>}
                    <span className="muted"> / {u.workload}</span> via <span className="mono">{u.env}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      )}
    </div>
  );
}

function Copyable({ text, block }: { text: string; block?: boolean }) {
  const [done, setDone] = useState(false);
  const copy = async (ev: React.MouseEvent) => {
    ev.stopPropagation();
    try {
      await navigator.clipboard.writeText(text);
      setDone(true);
      setTimeout(() => setDone(false), 1500);
    } catch {
      /* clipboard blocked: the text is selectable */
    }
  };
  return (
    <div className={`copyable ${block ? "block" : ""}`}>
      {block ? <pre className="file">{text}</pre> : <code>{text}</code>}
      <button className="small" onClick={copy}>{done ? "Copied" : "Copy"}</button>
    </div>
  );
}

function DocsHint() {
  return (
    <details className="card" style={{ marginTop: 16 }}>
      <summary><strong>Describe your service for others</strong> <span className="small muted">(optional, in rendimiento.yaml)</span></summary>
      <p className="small">Addresses, ports and callers are worked out automatically. Add a <code>catalog</code> block to say what the service is and how to use it:</p>
      <pre className="file">{`services:
  - name: ollama-internal
    image: dustynv/ollama:r36.4.0
    port: 11434
    catalog:
      title: Ollama (llama3.2:3b on a GPU node)
      description: Local LLM inference. No API key; cluster-internal only.
      env: OLLAMA_HOST          # the variable callers usually set
      docs: https://github.com/ollama/ollama/blob/main/docs/api.md
      endpoints:
        - "POST /api/generate: complete a prompt"
        - "GET /api/tags: list installed models"`}</pre>
    </details>
  );
}
