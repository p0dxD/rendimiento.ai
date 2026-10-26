import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { catalogApi, timeAgo, type CatalogEntry, type CatalogGroup } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t, tn } from "../i18n";

// Labels and blurbs are translated where shown.
const groups: { id: CatalogGroup; label: string; blurb: string }[] = [
  { id: "apps", label: "Your apps & add-ons", blurb: "Deployed by rendimiento: your apps (described by each repo's rendimiento.yaml) and installed add-ons." },
  { id: "other", label: "Elsewhere in the cluster", blurb: "Deployed another way (ArgoCD, Helm, kubectl). Apps can call them the same way; rendimiento only reads them." },
  { id: "infrastructure", label: "Cluster internals", blurb: "The cluster's own plumbing (ingress, certificates, storage, CI). Rarely something an app should call." },
];

// What services do, in the order sections appear, each with its color and icon.
const categories: { id: string; label: string; color: string; icon: string }[] = [
  { id: "web", label: "Web & APIs", color: "var(--grana)", icon: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c2.5 2.5 3.5 6 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-6-3.5-9s1-6.5 3.5-9z" },
  { id: "database", label: "Databases", color: "var(--anil)", icon: "M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zm0 0v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3" },
  { id: "messaging", label: "Caches & queues", color: "var(--rosa)", icon: "M4 7h16M4 12h16M4 17h10" },
  { id: "ai", label: "AI", color: "#6B1E4A", icon: "M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6" },
  { id: "storage", label: "Storage", color: "var(--nopal)", icon: "M3 7l9-4 9 4-9 4-9-4zm0 0v10l9 4 9-4V7" },
  { id: "monitoring", label: "Monitoring & analytics", color: "var(--teja)", icon: "M3 20h18M6 16l4-5 3 3 5-7" },
  { id: "devtools", label: "Developer tools", color: "#7A5A2A", icon: "m8 8-4 4 4 4M16 8l4 4-4 4" },
  { id: "platform", label: "Platform", color: "#5A4636", icon: "M12 3 3 8l9 5 9-5-9-5zM3 13l9 5 9-5" },
];
const categoryOf = (id: string) => categories.find((c) => c.id === id) ?? categories[categories.length - 1];
const categoryLabel = (id: string) => t(categories.find((c) => c.id === id)?.label ?? id);

const originLabel: Record<CatalogEntry["origin"]["kind"], string> = {
  rendimiento: "rendimiento",
  addon: "add-on",
  argocd: "ArgoCD",
  helm: "Helm",
  manual: "kubectl",
};

/**
 * The Services page: everything running in the cluster as a list on the left (by group and category, with a search),
 * and the one chosen on the right: what it is, how to connect, who uses it, where it comes from and what it exposes.
 */
export function Services() {
  const { ns, name } = useParams();
  const nav = useNavigate();
  const [group, setGroup] = useState<CatalogGroup>("apps");
  const [q, setQ] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const { data, error, setData } = usePoll(() => catalogApi.list(), [], 60000);

  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    data?.entries.forEach((e) => (c[e.group] = (c[e.group] ?? 0) + 1));
    return c;
  }, [data]);

  // Search spans every group; otherwise the group chosen.
  const shown = useMemo(() => {
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

  const refresh = async () => {
    setRefreshing(true);
    try {
      setData(await catalogApi.list(true));
    } finally {
      setRefreshing(false);
    }
  };

  if (error) return <ErrorBox error={error} />;
  if (!data) return <p className="muted">{t("Looking around the cluster…")}</p>;
  const focus = ns && name ? `${ns}/${name}` : undefined;
  const chosen = data.entries.find((e) => e.id === focus) ?? shown[0];
  // A link to a service of another group shows that group.
  if (chosen && focus === chosen.id && !q && chosen.group !== group) setGroup(chosen.group);

  return (
    <>
      <div className="row between">
        <div>
          <h1>{t("Services")}</h1>
          <p className="sub" style={{ marginBottom: 0 }}>{t("Choose one on the left: on the right, what it is, how to connect and who uses it.")}</p>
        </div>
        <div className="row small muted">
          {t("updated {when}", { when: timeAgo(data.generated) })}
          <button onClick={refresh} disabled={refreshing}>{refreshing ? t("Looking…") : t("Refresh")}</button>
        </div>
      </div>

      <div className="lista-detalle">
        <aside className="ld-lista">
          <label className="ld-buscar">
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="M15.5 15.5 21 21" /></svg>
            <input type="search" placeholder={t("Search by name, host, image or protocol…")} aria-label={t("Search services")} value={q} onChange={(e) => setQ(e.target.value)} />
          </label>
          {!q ? (
            <div className="ld-seg" role="group" aria-label={t("Where")}>
              {groups.map((g) => (
                <button key={g.id} aria-pressed={group === g.id} title={t(g.blurb)} onClick={() => setGroup(g.id)}>{t(g.label)} <span>{counts[g.id] ?? 0}</span></button>
              ))}
            </div>
          ) : <p className="small muted" style={{ margin: "0 4px" }}>{tn(shown.length, "{n} match across all groups", "{n} matches across all groups")}</p>}
          {categories.map((c) => {
            const items = shown.filter((e) => categoryOf(e.category).id === c.id);
            if (items.length === 0) return null;
            return (
              <div key={c.id}>
                <div className="ld-grupo">
                  <svg width="14" height="14" viewBox="0 0 24 24" style={{ stroke: c.color }} aria-hidden="true"><path d={c.icon} /></svg>{t(c.label)}
                </div>
                {items.map((e) => {
                  const tone = e.pods === 0 ? "idle" : e.ready === e.pods ? "ok" : e.ready === 0 ? "bad" : "warn";
                  return (
                    <button key={e.id} className={`ld-item${chosen?.id === e.id ? " on" : ""}`} style={{ ["--c" as string]: c.color }}
                      aria-current={chosen?.id === e.id} onClick={() => nav(`/services/${e.id}`)}>
                      <span className="ld-nombre">{e.title}</span>
                      <span className={`ld-punto ${tone}`} title={e.pods ? t("{ready}/{total} ready", { ready: e.ready, total: e.pods }) : undefined} />
                    </button>
                  );
                })}
              </div>
            );
          })}
          {shown.length === 0 && <p className="small muted" style={{ margin: "8px 4px" }}>{t("Nothing here.")}</p>}
        </aside>
        {chosen && <ServiceDetail key={chosen.id} e={chosen} />}
      </div>

      {group === "apps" && !q && <DocsHint />}
    </>
  );
}

/**
 * One service, in full: what it is, how to connect to it, who calls it, where it comes from and what it exposes.
 */
function ServiceDetail({ e }: { e: CatalogEntry }) {
  const c = categoryOf(e.category);
  const callers = [...new Map(e.usedBy.map((u) => [`${u.app ?? u.namespace}/${u.workload}`, u])).values()];
  return (
    <main className="ld-detalle">
      <div className="ld-cabeza">
        <span className="ld-icono" style={{ background: c.color }}>
          <svg width="28" height="28" viewBox="0 0 24 24" aria-hidden="true"><path d={c.icon} /></svg>
        </span>
        <div style={{ minWidth: 0 }}>
          <h2>{e.title}</h2>
          <div className="mono small muted">
            {e.namespace}/{e.name} · {e.protocol} · {t(originLabel[e.origin.kind])}
            {e.pods > 0 && <> · {t("{ready}/{total} ready", { ready: e.ready, total: e.pods })}</>}
          </div>
        </div>
      </div>
      <p className="ld-desc">{e.description ?? e.origin.summary}</p>

      <section className="ld-sec">
        <h3>{t("How to connect")}</h3>
        <div className="small muted">{t("From any app")}</div>
        <Copyable text={e.url} />
        {e.group === "apps" && (
          <>
            <div className="small muted" style={{ marginTop: 6 }}>{t("From another service of the {app} app", { app: e.origin.app ?? "" })}</div>
            <Copyable text={e.shortUrl} />
          </>
        )}
        <div className="small muted" style={{ marginTop: 8 }}>{t("In the calling service's rendimiento.yaml")}</div>
        <Copyable text={e.snippet} block />
      </section>

      <section className="ld-sec">
        <h3>{t("Used by")}</h3>
        {callers.length === 0 ? (
          <p className="small muted">{t("Nothing found. Only plain environment values are checked; addresses stored in secrets aren't read.")}</p>
        ) : (
          <div className="ld-quien">
            <div className="ld-quienes">
              {callers.map((u) => (
                <span key={u.namespace + u.workload} className="ld-chapa" title={t("via {env}", { env: u.env })}>
                  {u.app ? <Link to={`/apps/${u.app}`}>{u.app}</Link> : <span className="mono">{u.namespace}</span>}<span className="muted"> / {u.workload}</span>
                </span>
              ))}
            </div>
            <svg width="70" height="16" viewBox="0 0 70 16" aria-hidden="true" style={{ flex: "none" }}>
              <path className="ld-flujo" d="M2 8h60" stroke={c.color} strokeWidth="2.5" fill="none" />
              <path d="m58 3 8 5-8 5" fill="none" stroke={c.color} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span className="ld-chapa" style={{ borderColor: c.color, fontWeight: 700 }}>{e.title}</span>
          </div>
        )}
      </section>

      <div className="ld-dos">
        <section className="ld-sec">
          <h3>{t("Where it comes from")}</h3>
          <p className="small" style={{ marginTop: 0 }}>{e.origin.summary}</p>
          {e.origin.app && e.origin.kind === "addon" && (
            <div className="small">{t("Add-on")} <Link to={`/addons/${e.origin.app}`}>{e.origin.app}</Link></div>
          )}
          {e.origin.app && e.origin.kind !== "addon" && (
            <div className="small">{t("App")} <Link to={`/apps/${e.origin.app}`}>{e.origin.app}</Link>{e.origin.release ? ` · ${t("release #{n}", { n: e.origin.release })}` : ""}</div>
          )}
          {e.origin.repoUrl && (
            <div className="small">{t("Code")} <a href={e.origin.repoUrl} target="_blank" rel="noreferrer">{e.origin.repoUrl.replace("https://github.com/", "")}</a></div>
          )}
          {e.origin.argoApp && <div className="small">{t("ArgoCD application")} <span className="mono">{e.origin.argoApp}</span></div>}
          {e.origin.helmRelease && <div className="small">{t("Helm release")} <span className="mono">{e.origin.helmRelease}</span></div>}
          {(e.origin.images ?? []).map((i) => (
            <div key={i.ref} className="small svc-image">
              {i.built ? t("Image built from the repo") + " " : t("Image") + " "}
              {i.link ? <a href={i.link} target="_blank" rel="noreferrer" className="mono">{i.ref}</a> : <span className="mono">{i.ref}</span>}
            </div>
          ))}
          {e.docs && <div className="small" style={{ marginTop: 6 }}><a href={e.docs} target="_blank" rel="noreferrer">{t("API documentation ↗")}</a></div>}
        </section>

        <section className="ld-sec">
          <h3>{t("What it exposes")}</h3>
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
          {e.health && <div className="small">{t("Health check")} <span className="mono">{e.health}</span></div>}
          {(e.endpoints ?? []).length > 0 && (
            <ul className="small svc-endpoints">
              {e.endpoints!.map((x) => <li key={x}>{x}</li>)}
            </ul>
          )}
          {(e.public ?? []).map((u) => (
            <div key={u} className="small">{t("Public")} <a href={u} target="_blank" rel="noreferrer">{u.replace("https://", "")}</a></div>
          ))}
          {e.lan && (
            <div className="small">
              {t("Local network")}{" "}
              {e.lanURL
                ? <a href={e.lanURL} target="_blank" rel="noreferrer" className="mono">{e.lan} ↗</a>
                : <span className="mono">{e.lan}</span>}
              <span className="muted"> ({t("LoadBalancer: anyone on the home network can reach it")})</span>
            </div>
          )}
          <div className="small muted" style={{ marginTop: 6 }}>
            {e.networkPolicies === 0
              ? t("No network policy in its namespace: any pod in the cluster can connect.")
              : tn(e.networkPolicies, "{n} network policy limits who can connect in this namespace.", "{n} network policies limit who can connect in this namespace.")}
          </div>
        </section>
      </div>
    </main>
  );
}

/** Text with a button that copies it. */
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
      <button className="small" onClick={copy}>{done ? t("Copied") : t("Copy")}</button>
    </div>
  );
}

/** How to describe a service for others in rendimiento.yaml (catalog:). */
function DocsHint() {
  return (
    <details className="card" style={{ marginTop: 16 }}>
      <summary><strong>{t("Describe your service for others")}</strong> <span className="small muted">({t("optional, in rendimiento.yaml")})</span></summary>
      <p className="small">{t("Addresses, ports and callers are worked out automatically. Add a")} <code>catalog</code> {t("block to say what the service is and how to use it:")}</p>
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
