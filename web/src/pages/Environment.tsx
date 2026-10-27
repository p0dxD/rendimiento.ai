import { useState } from "react";
import { envApi, timeAgo, type EnvCheck, type EnvNode, type EnvProvider, type EnvReport, type EnvStatus } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { locale, t, tn } from "../i18n";
import { PlatformVersion } from "./Version";

const tone: Record<EnvStatus, string> = { ok: "ok", warning: "warn", missing: "bad", error: "bad" };
const label: Record<EnvStatus, string> = { ok: "OK", warning: "Attention", missing: "Missing", error: "Error" }; // translated where shown
const categories = ["Cluster", "Networking", "TLS", "Build", "Integrations"];

/** A check's status as a colored label: OK, Attention, Missing or Error. */
export function StatusBadge({ status }: { status: EnvStatus }) {
  return <span className={`badge ${tone[status]}`}>{t(label[status])}</span>;
}

// Each provider's sign opens the same requirement's details and its fix.
const slotCheck: Record<string, string> = { cluster: "kubernetes", dns: "dns", source: "github", registry: "registry" };
// Requirements that also hang a sign in the square, beside the providers.
const signChecks = ["notifications", "storage", "database"];
const picado = ["var(--grana)", "var(--cempasuchil)", "var(--anil)", "var(--rosa)", "var(--nopal)", "var(--maiz)"];

/** A sign over the square: a provider, or a requirement worth reaching at once. */
interface Letrero {
  key: string;
  slot: string;
  name: string;
  status: EnvStatus;
  detail: string;
  provider?: EnvProvider;
  check?: EnvCheck;
}

/**
 * The Environment page, as a Cotija town: the bando with the platform's version, the nodes as houses on a cobbled street
 * (the control plane is the parroquia), the providers as hanging signs that open their ventanilla, the cluster's problems
 * pinned to a cork board, and what the town needs.
 */
export function Environment() {
  const [refreshing, setRefreshing] = useState(false);
  const { data: r, error, setData } = usePoll(() => envApi.report(), [], 30000);
  const [sel, setSel] = useState<string>();
  const [abierto, setAbierto] = useState<string>();

  const refresh = async () => {
    setRefreshing(true);
    try {
      setData(await envApi.report(true));
    } finally {
      setRefreshing(false);
    }
  };

  if (error) return <ErrorBox error={error} />;
  if (!r) return <p className="muted">{t("Inspecting the cluster…")}</p>;
  const problems = r.problems ?? [];
  const nodo = r.nodes.find((n) => n.name === sel);
  const letreros: Letrero[] = [
    ...r.providers.map((p) => {
      const check = r.checks.find((c) => c.id === slotCheck[p.slot]);
      return { key: p.slot, slot: p.title, name: p.name, status: p.status, detail: p.detail, provider: p, check };
    }),
    ...signChecks.map((id) => r.checks.find((c) => c.id === id)).filter((c): c is EnvCheck => !!c)
      .map((c) => ({ key: c.id, slot: t(c.category), name: c.name, status: c.status, detail: c.summary, check: c })),
  ];
  const ventanilla = letreros.find((l) => l.key === abierto);
  const avisos = r.checks.filter((c) => c.status !== "ok");

  return (
    <>
      <div className="row between">
        <div>
          <h1>{t("Environment")}</h1>
          <p className="sub" style={{ marginBottom: 0 }}>{t("The town where your apps live: its houses, its services and its notices.")}</p>
        </div>
        <div className="row small muted">
          {t("checked {when}", { when: timeAgo(r.generatedAt) })}
          <button onClick={refresh} disabled={refreshing}>{refreshing ? t("Checking…") : t("Re-check")}</button>
        </div>
      </div>

      <div className={`card env-hero ${tone[r.overall]}`} style={{ marginTop: 16 }}>
        <StatusBadge status={r.overall} />
        <strong>{r.summary}</strong>
        <span className="muted small">
          {r.cluster.name} {r.cluster.version} · {t("{ready}/{n} nodes", { ready: r.cluster.nodesReady, n: r.cluster.nodes })} · {tn(r.cluster.pods, "{n} pod", "{n} pods")} · {tn(r.cluster.namespaces, "{n} namespace", "{n} namespaces")}
        </span>
      </div>
      <PlatformVersion />

      <div className="calle" role="group" aria-label={t("Nodes")}>
        <div className="calle-picado" aria-hidden="true">{Array.from({ length: 12 }, (_, i) => <i key={i} style={{ background: picado[i % picado.length], ["--d" as string]: `${i * -330}ms` }} />)}</div>
        <div className="empedrado" aria-hidden="true" />
        {r.nodes.map((n, i) => (
          <Casa key={n.name} n={n} i={i} on={sel === n.name} onPick={() => setSel(sel === n.name ? undefined : n.name)} />
        ))}
      </div>
      <p className="small muted calle-leyenda">{t("Lit windows are the processor; the red skirting rises with memory. A note on the door says the node is missing something. The parroquia is the node in charge.")}</p>
      {nodo && <div className="calle-nodo"><NodeCard n={nodo} /></div>}

      <div className="letreros">
        {letreros.map((l, i) => (
          <button key={l.key} className="letrero" aria-expanded={abierto === l.key} onClick={() => setAbierto(abierto === l.key ? undefined : l.key)}>
            <span className="cuerdas" aria-hidden="true" />
            <span className={`tabla${abierto === l.key ? " on" : ""}`} style={{ ["--d" as string]: `${i * -700}ms` }}>
              <small>{l.slot}</small>
              <strong>{l.name}</strong>
              <span className={`letrero-est ${tone[l.status]}`}>{t(label[l.status])}</span>
            </span>
          </button>
        ))}
      </div>
      {ventanilla && (
        <div className="ventanilla" key={ventanilla.key}>
          <div className="row between" style={{ alignItems: "baseline" }}>
            <h3>{ventanilla.slot}: {ventanilla.name}</h3>
            <StatusBadge status={ventanilla.status} />
          </div>
          <p style={{ margin: 0 }}>{ventanilla.detail}</p>
          {ventanilla.provider && ventanilla.provider.options.length > 0 && (
            <label className="field" style={{ maxWidth: 360 }}>
              <span>{t("Provider")}</span>
              <select value={ventanilla.provider.options.find((o) => o.id === ventanilla.provider!.active) ? ventanilla.provider.active : ventanilla.provider.options[0]?.id} disabled>
                {ventanilla.provider.options.map((o) => <option key={o.id} value={o.id}>{o.name}{o.available ? "" : ` (${t("coming soon")})`}</option>)}
              </select>
              <span className="small muted">{t("Switching providers is coming; today the provider is set by the platform's configuration.")}</span>
            </label>
          )}
          {ventanilla.key === "dns" && r.ddns && <DynamicDNS ddns={r.ddns} onSynced={refresh} />}
          {ventanilla.key === "notifications" && ventanilla.status === "ok" && <div className="row"><TestEmail /></div>}
          {ventanilla.check && <CheckRow c={ventanilla.check} />}
        </div>
      )}

      <div className="plaza">
        <section className="plaza-avisos">
          <h2>{t("Notices in the square")} {problems.length + avisos.length > 0 && <span className="badge warn">{problems.length + avisos.length}</span>}</h2>
          <div className="tablero">
            {problems.length + avisos.length === 0 && <div className="nota" style={{ ["--r" as string]: "-1deg", ["--t" as string]: "var(--ok)" }}>{t("No failing pods or certificates.")}</div>}
            {avisos.map((c, i) => (
              <button key={c.id} className="nota" style={{ ["--r" as string]: `${(i % 3) - 1}deg`, ["--t" as string]: c.status === "warning" ? "var(--warn)" : "var(--bad)" }}
                onClick={() => setAbierto(letreros.some((l) => l.check?.id === c.id) ? letreros.find((l) => l.check?.id === c.id)!.key : abierto)}>
                <strong style={{ color: c.status === "warning" ? "var(--warn)" : "var(--bad)" }}>{c.name}</strong>
                <span>{c.summary}</span>
              </button>
            ))}
            {problems.map((p, i) => (
              <div key={p.kind + p.namespace + p.name} className="nota" style={{ ["--r" as string]: `${((i + avisos.length) % 3) - 1}deg`, ["--t" as string]: "var(--bad)" }}>
                <strong style={{ color: "var(--bad)" }}>{p.kind} <span className="mono">{p.namespace}/{p.name}</span></strong>
                <span>{p.reason}{p.since ? ` · ${timeAgo(p.since)}` : ""}</span>
              </div>
            ))}
          </div>
        </section>
        <section className="card plaza-necesita">
          <h2>{t("What the town needs")}</h2>
          {categories.map((cat) => {
            const items = r.checks.filter((c) => c.category === cat);
            if (items.length === 0) return null;
            return (
              <div key={cat}>
                <div className="env-cat">{t(cat)}</div>
                {items.map((c) => <CheckRow key={c.id} c={c} />)}
              </div>
            );
          })}
        </section>
      </div>
    </>
  );
}

const LIT = "#F2C14E";
const OFF = "#5A6E7A";

/**
 * A node as a house of Cotija: lit windows for how busy its processor is, a red skirting that rises with its memory,
 * and a note on the door when something is wrong. The control plane is the parroquia, with its two towers.
 */
function Casa({ n, i, on, onPick }: { n: EnvNode; i: number; on: boolean; onPick: () => void }) {
  const cpu = n.cpuUsed !== undefined ? n.cpuUsed / n.cpuCores : 0;
  const mem = n.memUsed !== undefined ? n.memUsed / n.memBytes : 0;
  const lit = Math.round(cpu * 4);
  const aviso = !n.ready || (n.pressure?.length ?? 0) > 0 || !!n.unschedulable || mem >= 0.85;
  const guard = mem >= 0.85 ? "var(--rosa)" : "var(--grana)";
  const parroquia = (n.roles ?? []).some((r) => r === "control-plane" || r === "master");
  const d = `${i * 140}ms`;
  const pct = (x: number) => `${Math.round(x * 100)}%`;
  const label = t("{name}: processor {cpu}, memory {mem}", { name: n.name, cpu: pct(cpu), mem: pct(mem) });
  let dibujo;
  if (parroquia) {
    const ws = ["M24 70 q7 -10 14 0 v18 h-14 z", "M132 70 q7 -10 14 0 v18 h-14 z", "M58 96 q9 -12 18 0 v26 h-18 z", "M94 96 q9 -12 18 0 v26 h-18 z"];
    const gH = Math.round(14 + mem * 40);
    dibujo = (
      <svg width="170" height="210" viewBox="0 0 170 210" aria-hidden="true">
        <rect x="14" y="40" width="34" height="160" fill="#F4E9D8" stroke="#C9B79F" strokeWidth="2" />
        <rect x="122" y="40" width="34" height="160" fill="#F4E9D8" stroke="#C9B79F" strokeWidth="2" />
        <path d="M12 42 Q31 6 50 42 Z" fill="#E9B824" stroke="#B5532A" strokeWidth="2" /><path d="M120 42 Q139 6 158 42 Z" fill="#E9B824" stroke="#B5532A" strokeWidth="2" />
        <path d="M31 4 v10 M27 8 h8 M139 4 v10 M135 8 h8" stroke="#7A3E0B" strokeWidth="2.4" />
        <rect x="44" y="72" width="82" height="128" fill="#FBF7EF" stroke="#C9B79F" strokeWidth="2" />
        <path d="M44 74 L85 40 L126 74 Z" fill="#F4E9D8" stroke="#C9B79F" strokeWidth="2" />
        <circle cx="85" cy="64" r="11" fill="#FFFDF8" stroke="#7A3E0B" strokeWidth="2" /><path d="M85 64 V57 M85 64 L90 66" stroke="#2B1B12" strokeWidth="2" strokeLinecap="round" />
        {ws.map((p, j) => <path key={j} className={j < lit ? "luz" : undefined} d={p} fill={j < lit ? LIT : OFF} stroke="#7A3E0B" strokeWidth="2" />)}
        <path className="campana" d="M27 50 q4 -8 8 0 v4 h-8 z" fill="#B5532A" /><path className="campana" d="M135 50 q4 -8 8 0 v4 h-8 z" fill="#B5532A" style={{ animationDelay: "1.5s" }} />
        <rect className="guard" x="14" y={200 - gH} width="142" height={gH} fill={guard} style={{ ["--d" as string]: d }} />
        <path d="M70 200 V160 Q85 140 100 160 V200 Z" fill="#7A3E0B" stroke="#4A2A10" strokeWidth="2" /><path d="M85 152 V200" stroke="#4A2A10" strokeWidth="1.5" />
        {aviso && <Aviso x={78} y={168} />}
      </svg>
    );
  } else {
    const ancha = i % 3 === 2;
    const w = ancha ? 170 : 140, wi = w - 20, mid = w / 2;
    const cols = ancha ? [24, 74, 124] : [24, 96];
    const vent = [...cols.map((x) => ({ x, y: 66 })), { x: cols[0], y: 116 }, ...(ancha ? [{ x: cols[2], y: 116 }] : [])].slice(0, 4);
    let tejas = "";
    for (let x = 14; x < w - 8; x += 9) tejas += `M${x} ${52 - Math.max(0, 28 - (Math.abs(x - mid) * 28) / mid) + 3} V50 `;
    const gH = Math.round(10 + mem * 50);
    const dx = ancha ? mid - 14 : w - 58, dr = dx + 28;
    dibujo = (
      <svg width={w} height="170" viewBox={`0 0 ${w} 170`} aria-hidden="true">
        <path d={`M4 52 L${mid} 24 L${w - 4} 52 Z`} fill="#B5532A" stroke="#8A3C1E" strokeWidth="2" />
        <path d={tejas} stroke="#8A3C1E" strokeWidth="1.4" fill="none" />
        <rect x="4" y="50" width={w - 8} height="7" fill="#8A3C1E" />
        <rect x="10" y="57" width={wi} height="105" fill="#FBF7EF" stroke="#D8C8B0" strokeWidth="2" />
        <rect className="guard" x="10" y={162 - gH} width={wi} height={gH} fill={guard} style={{ ["--d" as string]: d }} />
        {vent.map((v, j) => (
          <g key={j}>
            <rect className={j < lit ? "luz" : undefined} x={v.x} y={v.y} width="20" height="24" rx="2" fill={j < lit ? LIT : OFF} stroke="#7A3E0B" strokeWidth="2.4" />
            <path d={`M${v.x} ${v.y + 12} h20 M${v.x + 10} ${v.y} v24`} stroke="#7A3E0B" strokeWidth="1.2" />
            <path d={`M${v.x - 4} ${v.y + 26} h28 M${v.x - 4} ${v.y + 34} h28 M${v.x - 4} ${v.y + 26} v8 M${v.x + 24} ${v.y + 26} v8 M${v.x + 10} ${v.y + 26} v8`} stroke="#2B1B12" strokeWidth="1.6" />
            <circle cx={v.x + 3} cy={v.y + 24} r="3.4" fill="#D6246E" /><circle cx={v.x + 10} cy={v.y + 24} r="3.4" fill="#E8890C" /><circle cx={v.x + 17} cy={v.y + 24} r="3" fill="#3E7B3A" />
          </g>
        ))}
        <path d={`M${dx} 162 V132 Q${dx + 14} 118 ${dr} 132 V162 Z`} fill="#7A3E0B" stroke="#4A2A10" strokeWidth="2" />
        <circle cx={dx + 22} cy="148" r="1.8" fill="#E9B824" />
        <path className="farol" d={`M${dr + 6} 122 h8 v10 h-8 z`} fill="#F2C14E" stroke="#2B1B12" strokeWidth="1.5" />
        {aviso && <Aviso x={dx + 7} y={128} />}
      </svg>
    );
  }
  return (
    <button className={`casa${on ? " on" : ""}`} style={{ ["--d" as string]: d }} onClick={onPick} aria-pressed={on} aria-label={label}>
      {dibujo}
      <span className="rot">{n.name}</span>
      <span className="small">{parroquia ? t("in charge") : (n.roles?.length ? n.roles.join(", ") : t("worker"))}</span>
    </button>
  );
}

/** The note pinned to a door: something is wrong in that house. */
function Aviso({ x, y }: { x: number; y: number }) {
  return (
    <g className="aviso-puerta">
      <rect x={x} y={y} width="13" height="16" fill="#FFFDF8" stroke="#B3261E" strokeWidth="1.6" />
      <path d={`M${x + 6.5} ${y + 3} v6 M${x + 6.5} ${y + 12} v1.5`} stroke="#B3261E" strokeWidth="2" strokeLinecap="round" />
    </g>
  );
}

/** Dynamic DNS: the public IP the records follow, when it last changed, and a button to check it now. */
function DynamicDNS({ ddns, onSynced }: { ddns: NonNullable<EnvReport["ddns"]>; onSynced: () => void }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string>();
  const sync = async () => {
    setBusy(true);
    setMsg(undefined);
    try {
      const res = await envApi.dnsSync();
      const n = res.updated?.length ?? 0;
      setMsg(res.changed ? tn(n, "IP changed to {ip}; {n} record updated", "IP changed to {ip}; {n} records updated", { ip: res.ip ?? "" })
        : n ? tn(n, "{n} record corrected", "{n} records corrected") : t("Still {ip}; nothing to update", { ip: res.ip ?? "" }));
      onSynced();
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="env-more" style={{ margin: 0 }}>
      <div className="small"><strong>{t("Dynamic DNS")}</strong> · {t("public IP")} <span className="mono">{ddns.ip ?? t("unknown")}</span></div>
      <div className="small muted">
        {t("checked {when}", { when: timeAgo(ddns.checkedAt) })}{ddns.changedAt && ` · ${t("last change {when}", { when: timeAgo(ddns.changedAt) })}`}
      </div>
      {ddns.error && <div className="small" style={{ color: "var(--bad)" }}>{ddns.error}</div>}
      <div className="row" style={{ marginTop: 6 }}>
        <button onClick={sync} disabled={busy}>{busy ? t("Checking…") : t("Update now")}</button>
        {msg && <span className="small muted">{msg}</span>}
      </div>
      <div className="small muted" style={{ marginTop: 6 }}>
        {t("App records follow your IP automatically. To include a record you made by hand, add")} <code>rendimiento-ddns</code> {t("to its comment in Cloudflare.")}
      </div>
    </div>
  );
}

/**
 * One requirement: its status and summary; open (already open when it fails) it shows the details and how to fix it.
 */
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
              {!c.required && <span className="small muted"> · {t("optional")}</span>}
            </div>
            <div className="small muted">{c.summary}</div>
          </div>
        </div>
        <div className="row" style={{ gap: 8, flexWrap: "nowrap" }}>
          {c.id === "notifications" && c.status === "ok" && <TestEmail />}
          {hasMore && <span className="small muted">{open ? "▾" : "▸"}</span>}
        </div>
      </div>
      {open && hasMore && (
        <div className="env-more">
          {c.details?.map((d) => <div key={d} className="small">• {d}</div>)}
          {c.fix && c.status !== "ok" && (
            <div style={{ marginTop: 8 }}>
              <div className="small muted">{t("How to fix")}</div>
              <pre className="file" style={{ whiteSpace: "pre-wrap" }}>{c.fix}</pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** Sends a sample notification now, and says what happened. */
function TestEmail() {
  const [state, setState] = useState<"idle" | "sending" | "sent" | string>("idle");
  const send = async (e: React.MouseEvent) => {
    e.stopPropagation();
    setState("sending");
    try {
      const r = await envApi.testEmail();
      setState(t("sent to {to}", { to: r.to.join(", ") }));
    } catch (err) {
      setState(t("failed: {msg}", { msg: (err as Error).message }));
    }
  };
  return (
    <>
      {state !== "idle" && state !== "sending" && <span className="small muted">{state}</span>}
      <button disabled={state === "sending"} onClick={send}>{state === "sending" ? t("Sending…") : t("Send test email")}</button>
    </>
  );
}

/** A bar of how much of something is used (green, then yellow from 75%, red from 90%). */
function Meter({ label: l, used, total, fmt }: { label: string; used?: number; total: number; fmt: (n: number) => string }) {
  if (used === undefined) return <div className="small muted">{l}: {fmt(total)} ({t("no live usage")})</div>;
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

const gib = (b: number) => `${(b / 1024 ** 3).toLocaleString(locale, { maximumFractionDigits: 1, minimumFractionDigits: 1 })} GiB`;
const cores = (c: number) => t("{n} cores", { n: c.toLocaleString(locale, { maximumFractionDigits: c < 10 ? 2 : 0, minimumFractionDigits: c < 10 ? 2 : 0 }) });

/** One node: ready or not, pressure, its CPU and memory in use, and what it runs on. */
function NodeCard({ n }: { n: EnvNode }) {
  const status: EnvStatus = !n.ready ? "error" : (n.pressure?.length || n.unschedulable) ? "warning" : "ok";
  return (
    <div className="card stack">
      <div className="row between">
        <strong>{n.name}</strong>
        <span className={`badge ${tone[status]}`}>{n.ready ? (status === "ok" ? t("Ready") : t("Degraded")) : t("NotReady")}</span>
      </div>
      <div className="small muted">
        {(n.roles?.length ? n.roles.join(", ") : t("worker"))} · {n.arch} · {tn(n.pods, "{n} pod", "{n} pods")}
        <br />{n.os} · {n.kubelet}
      </div>
      {n.pressure?.map((p) => <div key={p} className="small" style={{ color: "var(--bad)" }}>{p}</div>)}
      {n.unschedulable && <div className="small" style={{ color: "var(--warn)" }}>{t("cordoned (no new pods)")}</div>}
      <Meter label={t("CPU")} used={n.cpuUsed} total={n.cpuCores} fmt={cores} />
      <Meter label={t("Memory")} used={n.memUsed} total={n.memBytes} fmt={gib} />
    </div>
  );
}
