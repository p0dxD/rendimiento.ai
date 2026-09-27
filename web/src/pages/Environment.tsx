import { useState } from "react";
import { envApi, timeAgo, type EnvCheck, type EnvNode, type EnvProvider, type EnvReport, type EnvStatus } from "../api";
import { ErrorBox, usePoll } from "../components/ui";

const tone: Record<EnvStatus, string> = { ok: "ok", warning: "warn", missing: "bad", error: "bad" };
const label: Record<EnvStatus, string> = { ok: "OK", warning: "Attention", missing: "Missing", error: "Error" };
const categories = ["Cluster", "Networking", "TLS", "Build", "Integrations"];

export function StatusBadge({ status }: { status: EnvStatus }) {
  return <span className={`badge ${tone[status]}`}>{label[status]}</span>;
}

export function Environment() {
  const [refreshing, setRefreshing] = useState(false);
  const { data: r, error, setData } = usePoll(() => envApi.report(), [], 30000);

  const refresh = async () => {
    setRefreshing(true);
    try {
      setData(await envApi.report(true));
    } finally {
      setRefreshing(false);
    }
  };

  if (error) return <ErrorBox error={error} />;
  if (!r) return <p className="muted">Inspecting the cluster…</p>;
  const problems = r.problems ?? [];

  return (
    <>
      <div className="row between">
        <div>
          <h1>Environment</h1>
          <p className="sub" style={{ marginBottom: 0 }}>What rendimiento runs on, what it needs, and how healthy it is.</p>
        </div>
        <div className="row small muted">
          checked {timeAgo(r.generatedAt)}
          <button onClick={refresh} disabled={refreshing}>{refreshing ? "Checking…" : "Re-check"}</button>
        </div>
      </div>

      <div className={`card env-hero ${tone[r.overall]}`} style={{ marginTop: 16 }}>
        <StatusBadge status={r.overall} />
        <strong>{r.summary}</strong>
        <span className="muted small">
          {r.cluster.name} {r.cluster.version} · {r.cluster.nodesReady}/{r.cluster.nodes} nodes · {r.cluster.pods} pods · {r.cluster.namespaces} namespaces
        </span>
      </div>

      <h2>Providers</h2>
      <div className="grid">
        {r.providers.map((p) => (
          <ProviderCard key={p.slot} p={p}>
            {p.slot === "dns" && r.ddns && <DynamicDNS ddns={r.ddns} onSynced={refresh} />}
          </ProviderCard>
        ))}
      </div>

      <h2>Requirements</h2>
      <div className="stack">
        {categories.map((cat) => {
          const items = r.checks.filter((c) => c.category === cat);
          if (items.length === 0) return null;
          return (
            <div key={cat} className="card" style={{ padding: 0 }}>
              <div className="env-cat">{cat}</div>
              {items.map((c) => <CheckRow key={c.id} c={c} />)}
            </div>
          );
        })}
      </div>

      <h2>Nodes</h2>
      <div className="grid">
        {r.nodes.map((n) => <NodeCard key={n.name} n={n} />)}
      </div>

      <h2>Problems in the cluster {problems.length > 0 && <span className="badge warn">{problems.length}</span>}</h2>
      {problems.length === 0 ? (
        <div className="card muted">No failing pods or certificates.</div>
      ) : (
        <div className="card table-wrap" style={{ padding: 0 }}>
          <table>
            <thead><tr><th>Kind</th><th>Namespace</th><th>Name</th><th>Reason</th><th className="hide-sm">Since</th></tr></thead>
            <tbody>
              {problems.map((p) => (
                <tr key={p.kind + p.namespace + p.name}>
                  <td className="small">{p.kind}</td>
                  <td className="mono small">{p.namespace}</td>
                  <td className="mono small">{p.name}</td>
                  <td className="small">{p.reason}</td>
                  <td className="small muted hide-sm">{p.since ? timeAgo(p.since) : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function DynamicDNS({ ddns, onSynced }: { ddns: NonNullable<EnvReport["ddns"]>; onSynced: () => void }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string>();
  const sync = async () => {
    setBusy(true);
    setMsg(undefined);
    try {
      const res = await envApi.dnsSync();
      const n = res.updated?.length ?? 0;
      setMsg(res.changed ? `IP changed to ${res.ip}; ${n} record(s) updated` : n ? `${n} record(s) corrected` : `Still ${res.ip}; nothing to update`);
      onSynced();
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="env-more" style={{ margin: 0 }}>
      <div className="small"><strong>Dynamic DNS</strong> · public IP <span className="mono">{ddns.ip ?? "unknown"}</span></div>
      <div className="small muted">
        checked {timeAgo(ddns.checkedAt)}{ddns.changedAt && ` · last change ${timeAgo(ddns.changedAt)}`}
      </div>
      {ddns.error && <div className="small" style={{ color: "var(--bad)" }}>{ddns.error}</div>}
      <div className="row" style={{ marginTop: 6 }}>
        <button onClick={sync} disabled={busy}>{busy ? "Checking…" : "Update now"}</button>
        {msg && <span className="small muted">{msg}</span>}
      </div>
      <div className="small muted" style={{ marginTop: 6 }}>
        App records follow your IP automatically. To include a record you made by hand, add <code>rendimiento-ddns</code> to its comment in Cloudflare.
      </div>
    </div>
  );
}

function ProviderCard({ p, children }: { p: EnvProvider; children?: React.ReactNode }) {
  return (
    <div className="card stack">
      <div className="row between">
        <span className="small muted" style={{ textTransform: "uppercase", letterSpacing: ".05em" }}>{p.title}</span>
        <StatusBadge status={p.status} />
      </div>
      <strong style={{ fontSize: 16 }}>{p.name}</strong>
      <div className="small muted">{p.detail}</div>
      <label className="field">
        <span>Provider</span>
        <select value={p.options.find((o) => o.id === p.active) ? p.active : p.options[0]?.id} disabled
          title="Switching providers is coming; today the provider is set by the platform's configuration.">
          {p.options.map((o) => (
            <option key={o.id} value={o.id}>{o.name}{o.available ? "" : " (coming soon)"}</option>
          ))}
        </select>
      </label>
      {children}
    </div>
  );
}

function CheckRow({ c }: { c: EnvCheck }) {
  const [open, setOpen] = useState(c.status !== "ok");
  const hasMore = (c.details?.length ?? 0) > 0 || !!c.fix;
  return (
    <div className="env-check">
      <div className="row between" style={{ cursor: hasMore ? "pointer" : undefined }} onClick={() => hasMore && setOpen(!open)}>
        <div className="row" style={{ gap: 12, flexWrap: "nowrap", minWidth: 0 }}>
          <StatusBadge status={c.status} />
          <div style={{ minWidth: 0 }}>
            <div>
              <strong>{c.name}</strong>
              {c.version && <span className="mono small muted"> {c.version}</span>}
              {!c.required && <span className="small muted"> · optional</span>}
            </div>
            <div className="small muted">{c.summary}</div>
          </div>
        </div>
        {hasMore && <span className="small muted">{open ? "▾" : "▸"}</span>}
      </div>
      {open && hasMore && (
        <div className="env-more">
          {c.details?.map((d) => <div key={d} className="small">• {d}</div>)}
          {c.fix && c.status !== "ok" && (
            <div style={{ marginTop: 8 }}>
              <div className="small muted">How to fix</div>
              <pre className="file" style={{ whiteSpace: "pre-wrap" }}>{c.fix}</pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Meter({ label: l, used, total, fmt }: { label: string; used?: number; total: number; fmt: (n: number) => string }) {
  if (used === undefined) return <div className="small muted">{l}: {fmt(total)} (no live usage)</div>;
  const pct = Math.min(100, Math.round((used / total) * 100));
  const color = pct >= 90 ? "var(--bad)" : pct >= 75 ? "var(--warn)" : "var(--ok)";
  return (
    <div>
      <div className="row between small"><span>{l}</span><span className="muted">{fmt(used)} / {fmt(total)} · {pct}%</span></div>
      <div className="meter" role="meter" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} aria-label={l}>
        <div style={{ width: `${pct}%`, background: color }} />
      </div>
    </div>
  );
}

const gib = (b: number) => `${(b / 1024 ** 3).toFixed(1)} GiB`;
const cores = (c: number) => `${c.toFixed(c < 10 ? 2 : 0)} cores`;

function NodeCard({ n }: { n: EnvNode }) {
  const status: EnvStatus = !n.ready ? "error" : (n.pressure?.length || n.unschedulable) ? "warning" : "ok";
  return (
    <div className="card stack">
      <div className="row between">
        <strong>{n.name}</strong>
        <span className={`badge ${tone[status]}`}>{n.ready ? (status === "ok" ? "Ready" : "Degraded") : "NotReady"}</span>
      </div>
      <div className="small muted">
        {(n.roles?.length ? n.roles.join(", ") : "worker")} · {n.arch} · {n.pods} pods
        <br />{n.os} · {n.kubelet}
      </div>
      {n.pressure?.map((p) => <div key={p} className="small" style={{ color: "var(--bad)" }}>{p}</div>)}
      {n.unschedulable && <div className="small" style={{ color: "var(--warn)" }}>cordoned (no new pods)</div>}
      <Meter label="CPU" used={n.cpuUsed} total={n.cpuCores} fmt={cores} />
      <Meter label="Memory" used={n.memUsed} total={n.memBytes} fmt={gib} />
    </div>
  );
}
