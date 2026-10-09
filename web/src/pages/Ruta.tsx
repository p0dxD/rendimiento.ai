// La Ruta: what the platform's people and agents know, kept here rather than
// in any one agent's session. Agents read and write it over MCP (/mcp) with a
// key made on the Keys tab; lo vivido (what people lived) only people write.
import { useState } from "react";
import { api, timeAgo, type AgentKey, type CreatedAgentKey, type RutaEntry, type RutaInput, type RutaKind } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t, tn } from "../i18n";

type Tab = "ruta" | "vivido" | "cargos" | "llaves";

const kinds: { id: RutaKind; label: string }[] = [
  { id: "decision", label: "Decisions" },
  { id: "manual", label: "Manuals" },
  { id: "pendiente", label: "To do" },
  { id: "nota", label: "Notes" },
];

const kindLabel: Record<RutaKind, string> = {
  decision: "Decision", manual: "Manual", pendiente: "To do", nota: "Note", vivido: "Lived",
};

/** A monarch, which no single one of makes the whole journey. */
function Monarca({ size = 34 }: { size?: number }) {
  return (
    <svg className="monarca" width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
      <g stroke="#1b1b1b" strokeWidth="2.2" strokeLinejoin="round">
        <path d="M31 30 C22 12 6 10 5 20 C4 30 16 33 30 33 Z" fill="#E8890C" />
        <path d="M33 30 C42 12 58 10 59 20 C60 30 48 33 34 33 Z" fill="#E8890C" />
        <path d="M30 34 C18 36 10 46 16 52 C22 57 29 46 31 36 Z" fill="#D9760A" />
        <path d="M34 34 C46 36 54 46 48 52 C42 57 35 46 33 36 Z" fill="#D9760A" />
        <path d="M12 18 L28 31 M52 18 L36 31 M17 47 L30 36 M47 47 L34 36" fill="none" strokeWidth="1.4" />
      </g>
      <ellipse cx="32" cy="34" rx="2.4" ry="13" fill="#1b1b1b" />
      <path d="M31 22 C29 15 26 12 23 11 M33 22 C35 15 38 12 41 11" stroke="#1b1b1b" strokeWidth="1.5" fill="none" />
      <g fill="#fff">
        <circle cx="8" cy="20" r="1.3" /><circle cx="11" cy="26" r="1.1" /><circle cx="56" cy="20" r="1.3" /><circle cx="53" cy="26" r="1.1" />
        <circle cx="16" cy="50" r="1.1" /><circle cx="48" cy="50" r="1.1" />
      </g>
    </svg>
  );
}

/** An entry's tags, as small labels. */
function Tags({ tags }: { tags: string[] }) {
  if (tags.length === 0) return null;
  return <span className="ruta-tags">{tags.map((g) => <span key={g} className="ruta-tag">{g}</span>)}</span>;
}

/** Who wrote an entry (marked when it was an agent), when it changed, and in which cargo. */
function Author({ e }: { e: RutaEntry }) {
  return (
    <span className="small muted">
      {e.byAgent ? <span className="badge ruta-agent" title={t("Written by an agent")}>{t("agent")}</span> : null} {e.author}
      {" · "}{t("updated {when}", { when: timeAgo(e.updatedAt) })}
      {e.cargoId ? <> · {t("cargo #{n}", { n: e.cargoId })}</> : null}
    </span>
  );
}

/** The form to write or change an entry: kind, title, text, tags and, for a to-do, done. */
function EntryForm({ initial, kind, onDone, onCancel }: { initial?: RutaEntry; kind: RutaKind; onDone: () => void; onCancel?: () => void }) {
  const [form, setForm] = useState<RutaInput>({
    kind: initial?.kind ?? kind, title: initial?.title ?? "", body: initial?.body ?? "", tags: initial?.tags ?? [], done: initial?.done ?? false,
  });
  const [tags, setTags] = useState((initial?.tags ?? []).join(", "));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const vivido = form.kind === "vivido";
  const save = () => {
    setBusy(true);
    setErr(undefined);
    const body = { ...form, tags: tags.split(",").map((s) => s.trim()).filter(Boolean) };
    (initial ? api.updateRuta(initial.id, body) : api.addRuta(body)).then(
      () => { setBusy(false); onDone(); },
      (e) => { setBusy(false); setErr(e); },
    );
  };
  return (
    <div className={`card stack ${vivido ? "vivido-form" : ""}`}>
      {!initial && !vivido && (
        <label className="field"><span>{t("Kind")}</span>
          <select value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value as RutaKind })}>
            {kinds.map((k) => <option key={k.id} value={k.id}>{t(kindLabel[k.id])}</option>)}
          </select>
        </label>
      )}
      <label className="field"><span>{t("Title")}</span>
        <input value={form.title} maxLength={200} onChange={(e) => setForm({ ...form, title: e.target.value })}
          placeholder={vivido ? t("e.g. Going to school in Cotija") : ""} />
      </label>
      <label className="field"><span>{vivido ? t("Tell it in your own words") : t("Text")}</span>
        <textarea rows={vivido ? 8 : 6} value={form.body} onChange={(e) => setForm({ ...form, body: e.target.value })} />
      </label>
      <label className="field"><span>{t("Tags (comma-separated)")}</span>
        <input value={tags} onChange={(e) => setTags(e.target.value)} placeholder={vivido ? "cotija, escuela" : "stockpulse, respaldos"} />
      </label>
      {form.kind === "pendiente" && (
        <label className="small row" style={{ gap: 6 }}>
          <input type="checkbox" style={{ width: "auto" }} checked={form.done} onChange={(e) => setForm({ ...form, done: e.target.checked })} /> {t("Done")}
        </label>
      )}
      <p className="small muted" style={{ margin: 0 }}>{t("Never write passwords, tokens or keys here; say where they live instead.")}</p>
      <ErrorBox error={err} />
      <div className="row">
        <button className="primary" disabled={busy || !form.title.trim()} onClick={save}>{initial ? t("Save") : vivido ? t("Keep this story") : t("Add")}</button>
        {onCancel && <button onClick={onCancel}>{t("Cancel")}</button>}
      </div>
    </div>
  );
}

/** One entry, folded to its first lines: open it, edit it, mark a to-do done, or archive it. */
function EntryCard({ e, onChange }: { e: RutaEntry; onChange: () => void }) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  if (editing) return <EntryForm initial={e} kind={e.kind} onDone={() => { setEditing(false); onChange(); }} onCancel={() => setEditing(false)} />;
  const toggleDone = () => api.updateRuta(e.id, { ...e, done: !e.done }).then(onChange, (x) => alert(x.message));
  return (
    <div className={`card stack ruta-entry ${e.done ? "done" : ""}`}>
      <div className="row between" style={{ alignItems: "baseline", flexWrap: "wrap" }}>
        <div className="row" style={{ alignItems: "baseline", flexWrap: "wrap" }}>
          <span className={`badge ruta-kind ${e.kind}`}>{t(kindLabel[e.kind])}</span>
          {e.kind === "pendiente" && <input type="checkbox" style={{ width: "auto" }} checked={e.done} onChange={toggleDone} title={t("Done")} />}
          <a href="#" onClick={(x) => { x.preventDefault(); setOpen(!open); }}><strong>{e.title}</strong></a>
          <Tags tags={e.tags} />
        </div>
        <div className="row">
          <button onClick={() => setEditing(true)}>{t("Edit")}</button>
          <button onClick={() => confirm(t("Archive “{title}”? It disappears from the list but is kept.", { title: e.title })) && api.archiveRuta(e.id).then(onChange, (x) => alert(x.message))}>{t("Archive")}</button>
        </div>
      </div>
      {open && e.body && <div className="ruta-body">{e.body}</div>}
      <Author e={e} />
    </div>
  );
}

/** The Ruta tab: entries by kind and text (all but "lived"), and a button to write one. */
function Entries() {
  const [kind, setKind] = useState<string>("");
  const [q, setQ] = useState("");
  const [adding, setAdding] = useState(false);
  const { data, error, reload } = usePoll(() => api.ruta({ kind, q }), [kind, q], 30000);
  const list = (data ?? []).filter((e) => e.kind !== "vivido");
  return (
    <>
      <div className="row ruta-filters">
        <button className={`chip-btn ${kind === "" ? "on" : ""}`} onClick={() => setKind("")}>{t("All")}</button>
        {kinds.map((k) => <button key={k.id} className={`chip-btn ${kind === k.id ? "on" : ""}`} onClick={() => setKind(k.id)}>{t(k.label)}</button>)}
        <input className="ruta-search" placeholder={t("Search the Ruta…")} value={q} onChange={(e) => setQ(e.target.value)} />
        <button className="primary" onClick={() => setAdding(!adding)}>{adding ? t("Close") : t("New entry")}</button>
      </div>
      {adding && <EntryForm kind={(kind || "nota") as RutaKind} onDone={() => { setAdding(false); reload(); }} onCancel={() => setAdding(false)} />}
      <ErrorBox error={error} />
      {data && list.length === 0 && <div className="card empty"><p>{q ? t("Nothing matches.") : t("Nothing here yet. Agents add decisions, manuals and to-dos as they work; you can too.")}</p></div>}
      <div className="stack">{list.map((e) => <EntryCard key={e.id} e={e} onChange={reload} />)}</div>
    </>
  );
}

/** The "Lived" tab: what people wrote about working here, in their words (agents cannot write these). */
function Vivido() {
  const [adding, setAdding] = useState(false);
  const { data, error, reload } = usePoll(() => api.ruta({ kind: "vivido" }), [], 60000);
  const [editing, setEditing] = useState<number>();
  return (
    <>
      <p className="sub">{t("Stories people lived, in their own words. Agents read them, with respect, but cannot write or change them: what a person lived is not a technical fact, and is not anyone else's to rewrite.")}</p>
      <button className="primary" onClick={() => setAdding(!adding)}>{adding ? t("Close") : t("Tell a story")}</button>
      {adding && <div style={{ marginTop: 12 }}><EntryForm kind="vivido" onDone={() => { setAdding(false); reload(); }} onCancel={() => setAdding(false)} /></div>}
      <ErrorBox error={error} />
      {data && data.length === 0 && !adding && <div className="card empty" style={{ marginTop: 12 }}><p>{t("No stories yet.")}</p></div>}
      <div className="stack" style={{ marginTop: 16 }}>
        {data?.map((e) => editing === e.id
          ? <EntryForm key={e.id} initial={e} kind="vivido" onDone={() => { setEditing(undefined); reload(); }} onCancel={() => setEditing(undefined)} />
          : (
            <figure key={e.id} className="vivido">
              <figcaption><strong>{e.title}</strong> <Tags tags={e.tags} /></figcaption>
              <blockquote>{e.body}</blockquote>
              <div className="row between">
                <span className="small muted">— {e.author}, {new Date(e.createdAt).toLocaleDateString()}</span>
                <span className="row">
                  <button onClick={() => setEditing(e.id)}>{t("Edit")}</button>
                  <button onClick={() => confirm(t("Archive “{title}”? It disappears from the list but is kept.", { title: e.title })) && api.archiveRuta(e.id).then(reload, (x) => alert(x.message))}>{t("Archive")}</button>
                </span>
              </div>
            </figure>
          ))}
      </div>
    </>
  );
}

/**
 * The Cargos tab: each agent's turns of work (purpose, handover, what is left) and, on request, every tool call.
 */
function Cargos() {
  const { data, error } = usePoll(api.rutaActivity, [], 20000);
  const [showActions, setShowActions] = useState(false);
  return (
    <>
      <p className="sub">{t("A cargo is an agent's turn of work: taken with a purpose, handed over with what it did and what is left, so the next one (another session, another model, or a person) knows where things stand.")}</p>
      <ErrorBox error={error} />
      {data && data.cargos.length === 0 && <div className="card empty"><p>{t("No agent has taken a cargo yet.")}</p></div>}
      <ol className="ruta-vuelo">
        {data?.cargos.map((c) => (
          <li key={c.id} className={c.endedAt ? "" : "open"}>
            <div className="card stack">
              <div className="row between" style={{ flexWrap: "wrap" }}>
                <strong>#{c.id} · {c.purpose}</strong>
                <span className={`badge ${c.endedAt ? "ok" : "warn"}`}>{c.endedAt ? t("handed over") : t("in progress")}</span>
              </div>
              <span className="small muted">{c.keyName} · {timeAgo(c.startedAt)}{c.endedAt ? ` → ${timeAgo(c.endedAt)}` : ""}</span>
              {c.entrega && <div><div className="small muted">{t("Handover")}</div><div className="ruta-body">{c.entrega}</div></div>}
              {c.pendientes && <div><div className="small muted">{t("Left to do")}</div><div className="ruta-body">{c.pendientes}</div></div>}
            </div>
          </li>
        ))}
      </ol>
      {data && data.actions.length > 0 && (
        <section style={{ marginTop: 20 }}>
          <a href="#" onClick={(e) => { e.preventDefault(); setShowActions(!showActions); }}>
            {showActions ? t("Hide what agents did") : tn(data.actions.length, "Show the latest agent action", "Show the latest {n} agent actions")}
          </a>
          {showActions && (
            <table className="ruta-actions small">
              <thead><tr><th>{t("When")}</th><th>{t("Key")}</th><th>{t("Tool")}</th><th>{t("Result")}</th></tr></thead>
              <tbody>
                {data.actions.map((a) => (
                  <tr key={a.id} title={a.args}>
                    <td>{timeAgo(a.at)}</td><td>{a.keyName}</td><td className="mono">{a.tool}</td>
                    <td>{a.ok ? "✓" : <span className="ruta-bad">✗ {a.error}</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}
    </>
  );
}

/** The form to make an agent key: its name, whether it can write, and how many days it lasts. */
function NewKey({ onCreated }: { onCreated: (k: CreatedAgentKey) => void }) {
  const [name, setName] = useState("");
  const [canWrite, setCanWrite] = useState(true);
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  return (
    <div className="card stack">
      <div className="form-grid">
        <label className="field"><span>{t("Name")}</span>
          <input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} placeholder={t("e.g. claude on main")} />
        </label>
        <label className="field"><span>{t("Lasts")}</span>
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {[7, 30, 90].map((d) => <option key={d} value={d}>{tn(d, "{n} day", "{n} days")}</option>)}
          </select>
        </label>
        <label className="field"><span>{t("Access")}</span>
          <select value={canWrite ? "w" : "r"} onChange={(e) => setCanWrite(e.target.value === "w")}>
            <option value="w">{t("Read and write (with a cargo)")}</option>
            <option value="r">{t("Read only")}</option>
          </select>
        </label>
      </div>
      <ErrorBox error={err} />
      <div><button className="primary" disabled={busy || !name.trim()} onClick={() => {
        setBusy(true); setErr(undefined);
        api.createAgentKey({ name: name.trim(), canWrite, days }).then((k) => { setBusy(false); setName(""); onCreated(k); }, (e) => { setBusy(false); setErr(e); });
      }}>{t("Create key")}</button></div>
    </div>
  );
}

/**
 * Shown once after making a key: the key itself and how to connect an agent with it (Claude Code's command, or a JSON config).
 */
function Connect({ k, onClose }: { k: CreatedAgentKey; onClose: () => void }) {
  const cli = `claude mcp add --transport http rendimiento ${k.endpoint} --header "Authorization: Bearer ${k.token}"`;
  const json = JSON.stringify({ mcpServers: { rendimiento: { type: "http", url: k.endpoint, headers: { Authorization: "Bearer ${RENDIMIENTO_MCP_TOKEN}" } } } }, null, 2);
  const copy = (s: string) => navigator.clipboard?.writeText(s);
  return (
    <div className="alert info stack" role="status">
      <strong>{t("Copy the key now: it is shown only this once.")}</strong>
      <div className="row"><code className="mono ruta-token">{k.token}</code><button onClick={() => copy(k.token)}>{t("Copy")}</button></div>
      <div className="small">{t("In Claude Code, one command:")}</div>
      <div className="row"><pre className="mono ruta-snippet">{cli}</pre><button onClick={() => copy(cli)}>{t("Copy")}</button></div>
      <div className="small">{t("Or, to share it with anyone who opens a repository, commit this .mcp.json and keep the key in the RENDIMIENTO_MCP_TOKEN environment variable, never in git:")}</div>
      <div className="row"><pre className="mono ruta-snippet">{json}</pre><button onClick={() => copy(json)}>{t("Copy")}</button></div>
      <div><button onClick={onClose}>{t("Done, I saved it")}</button></div>
    </div>
  );
}

/**
 * The Keys tab: the agents' keys with their state (active, expired, revoked), and making or revoking one.
 */
function Keys() {
  const { data, error, reload } = usePoll(api.agentKeys, [], 30000);
  const [created, setCreated] = useState<CreatedAgentKey>();
  const now = Date.now();
  const state = (k: AgentKey) =>
    k.revokedAt ? t("revoked") : new Date(k.expiresAt).getTime() < now ? t("expired") : t("active");
  return (
    <>
      <p className="sub">{t("Agents connect to the Ruta over MCP with a key. A key is shown once, stored only as a hash, always expires, and can be revoked here at any time. Everything an agent does with it is recorded on the Cargos tab.")}</p>
      {created ? <Connect k={created} onClose={() => setCreated(undefined)} /> : <NewKey onCreated={(k) => { setCreated(k); reload(); }} />}
      <ErrorBox error={error} />
      <table className="ruta-actions small" style={{ marginTop: 16 }}>
        <thead><tr><th>{t("Name")}</th><th>{t("Access")}</th><th>{t("State")}</th><th>{t("Expires")}</th><th>{t("Last used")}</th><th /></tr></thead>
        <tbody>
          {data?.map((k) => (
            <tr key={k.id} className={k.revokedAt ? "muted" : ""}>
              <td>{k.name}</td>
              <td>{k.canWrite ? t("read and write") : t("read only")}</td>
              <td>{state(k)}</td>
              <td>{new Date(k.expiresAt).toLocaleDateString()}</td>
              <td>{k.lastUsedAt ? timeAgo(k.lastUsedAt) : t("never")}</td>
              <td>{!k.revokedAt && (
                <button onClick={() => confirm(t("Revoke “{name}”? Agents using it lose access at once.", { name: k.name })) && api.revokeAgentKey(k.id).then(reload, (e) => alert(e.message))}>{t("Revoke")}</button>
              )}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

/** The Ruta page: the platform's memory, kept by people and agents, in four tabs. */
export function Ruta() {
  const [tab, setTab] = useState<Tab>("ruta");
  const tabs: { id: Tab; label: string }[] = [
    { id: "ruta", label: "The Ruta" }, { id: "vivido", label: "Lived" }, { id: "cargos", label: "Cargos" }, { id: "llaves", label: "Keys" },
  ];
  return (
    <>
      <h1 className="row" style={{ gap: 10 }}><Monarca /> {t("La Ruta")}</h1>
      <p className="sub">{t("No monarch makes the whole journey; it takes several generations, and still they find the same forests in Michoacán. Each agent session is one generation: the Ruta keeps the way. Decisions with their reasons, manuals, to-dos, and what each agent handed over, for the next one, whoever it is.")}</p>
      <nav className="tabs">
        {tabs.map((x) => (
          <a key={x.id} href="#" className={tab === x.id ? "active" : ""} onClick={(e) => { e.preventDefault(); setTab(x.id); }}>{t(x.label)}</a>
        ))}
      </nav>
      {tab === "ruta" && <Entries />}
      {tab === "vivido" && <Vivido />}
      {tab === "cargos" && <Cargos />}
      {tab === "llaves" && <Keys />}
    </>
  );
}
