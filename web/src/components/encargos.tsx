import { useState } from "react";
import { Link } from "react-router-dom";
import { api, dollars, timeAgo, type Encargo, type EncargoStatus } from "../api";
import { ErrorBox, usePoll } from "./ui";
import { t } from "../i18n";
import { TextoConFormato } from "./texto";

const statusLabel: Record<EncargoStatus, string> = {
  pending: "Pending", taken: "Taken", waiting: "Waiting", delivered: "Delivered", reviewed: "Reviewed", cancelled: "Dropped",
};
const statusClass: Record<EncargoStatus, string> = {
  pending: "", taken: "live", waiting: "warn live", delivered: "warn", reviewed: "ok", cancelled: "",
};
const eventLabel: Record<string, string> = {
  created: "created", taken: "taken", waiting: "waiting", resumed: "done waiting", delivered: "delivered",
  reviewed: "reviewed", routed: "routed", returned: "sent back", released: "let go", cancelled: "dropped",
};

const routeLabel = { platform: "For the platform's AI", local: "For your own agent" };

/**
 * What the estimator said of a pending encargo (how hard, what the
 * platform's AI would spend) and who should do it, with the choice to
 * change it: past the amount set on the AI page, it is for the person's own
 * agent, paid by the month rather than by the call.
 */
function Estimate({ e, act }: { e: Encargo; act: (p: Promise<unknown>) => void }) {
  if (e.status !== "pending") return null;
  if (!e.difficulty) {
    return <p className="small muted encargo-estimate">{e.estimateReason ? t("No estimate: {why}", { why: e.estimateReason }) : t("Estimating…")}</p>;
  }
  return (
    <div className="small encargo-estimate">
      <span className={`badge ${e.route === "platform" ? "ok" : ""}`}>{t(routeLabel[e.route ?? "local"])}</span>{" "}
      <span>{t("difficulty {d}/10 · about {cost} with the platform's AI", { d: e.difficulty, cost: dollars(e.estimateMicros ?? 0) })}</span>
      {e.estimateReason && <span className="muted"> · {e.estimateReason}</span>}{" "}
      {e.route === "platform"
        ? <button className="link small" onClick={() => act(api.routeEncargo(e.id, "local"))}>{t("Leave it to my agent")}</button>
        : <button className="link small" onClick={() => act(api.routeEncargo(e.id, "platform"))}>{t("Do it with AI anyway")}</button>}
    </div>
  );
}

/** The form to ask agents for something: what, the detail, and the app. */
function NewEncargo({ onCreated }: { onCreated: () => void }) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [app, setApp] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const { data: apps } = usePoll(api.apps, [], 0);
  return (
    <div className="card stack">
      <div className="form-grid">
        <label className="field"><span>{t("What to do")}</span>
          <input value={title} maxLength={200} onChange={(e) => setTitle(e.target.value)} placeholder={t("e.g. Find out why analizar-accion fails")} />
        </label>
        <label className="field"><span>{t("App")}</span>
          <select value={app} onChange={(e) => setApp(e.target.value)}>
            <option value="">{t("none")}</option>
            {apps?.map((a) => <option key={a.name} value={a.name}>{a.name}</option>)}
          </select>
        </label>
      </div>
      <label className="field"><span>{t("Detail")}</span>
        <textarea rows={3} value={body} onChange={(e) => setBody(e.target.value)} placeholder={t("What you know, where to look, and how to tell it is done.")} />
      </label>
      <ErrorBox error={err} />
      <div><button className="primary" disabled={busy || !title.trim()} onClick={() => {
        setBusy(true); setErr(undefined);
        api.createEncargo({ title: title.trim(), body, app: app || undefined }).then(() => {
          setBusy(false); setTitle(""); setBody(""); setApp(""); onCreated();
        }, (e) => { setBusy(false); setErr(e); });
      }}>{t("Ask agents")}</button></div>
    </div>
  );
}

/** One encargo: what was asked, who has it, what it waits on, what came back, and its history. */
function EncargoCard({ e, onChange }: { e: Encargo; onChange: () => void }) {
  const [open, setOpen] = useState(false);
  const [full, setFull] = useState<Encargo>();
  const act = (p: Promise<unknown>) => p.then(onChange, (err: Error) => alert(err.message));
  const toggle = () => {
    if (!open && !full) api.encargo(e.id).then(setFull, () => undefined);
    setOpen(!open);
  };
  return (
    <div className="card encargo">
      <div className="row between">
        <div className="row" style={{ gap: 8, minWidth: 0 }}>
          <span className={`badge ${statusClass[e.status]}`}>{t(statusLabel[e.status])}</span>
          <strong className="encargo-title">{e.title}</strong>
        </div>
        <span className="small muted">{e.app && <Link to={`/apps/${e.app}`}>{e.app}</Link>} · {timeAgo(e.updatedAt)}</span>
      </div>
      <p className="small muted encargo-who">
        {e.keyName ? t("{agent} has it", { agent: e.keyName }) : t("asked by {who}", { who: e.createdBy })}
        {e.waitingRun && <> · {t("waiting on")} <Link to={`/apps/${e.app}/runs/${e.waitingRun}`}>{t("run #{n}", { n: e.waitingRun })}</Link></>}
        {e.runId && <> · {t("about")} <Link to={`/apps/${e.app}/runs/${e.runId}`}>{t("run #{n}", { n: e.runId })}</Link></>}
      </p>
      <Estimate e={e} act={act} />
      {e.result && <div className="encargo-result"><TextoConFormato text={e.result} />
        {e.links.map((l) => <div key={l}><a className="small" href={l} target="_blank" rel="noreferrer">{l}</a></div>)}</div>}
      {e.comment && e.status === "pending" && <p className="small"><strong>{t("Sent back:")}</strong> {e.comment}</p>}
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        {e.status === "delivered" && <>
          <button className="primary" onClick={() => act(api.reviewEncargo(e.id, true))}>{t("Reviewed ✓")}</button>
          <button onClick={() => {
            const c = prompt(t("What is missing? The next agent reads it."));
            if (c?.trim()) act(api.reviewEncargo(e.id, false, c.trim()));
          }}>{t("Send back")}</button>
        </>}
        {e.status !== "reviewed" && e.status !== "cancelled" && (
          <button className="link small" onClick={() => confirm(t("Drop this encargo?")) && act(api.cancelEncargo(e.id))}>{t("Drop")}</button>
        )}
        <button className="link small" onClick={toggle}>{open ? t("Hide history") : t("History")}</button>
      </div>
      {open && (
        <div className="small encargo-log">
          {full?.body && <TextoConFormato text={full.body} />}
          {full?.log?.map((l, i) => (
            <div key={i}><span className="muted">{new Date(l.at).toLocaleString()}</span> · {l.who} · {t(eventLabel[l.what] ?? l.what)}{l.note && ` · ${l.what === "routed" ? t(routeLabel[l.note as "platform" | "local"] ?? l.note) : l.note}`}</div>
          ))}
        </div>
      )}
    </div>
  );
}

/**
 * The Encargos tab: work asked of agents. A person (or an agent) asks; an
 * agent takes it, may wait on a run, and hands it over; the person reviews
 * it or sends it back with a comment.
 */
export function Encargos() {
  const [all, setAll] = useState(false);
  const { data, error, reload } = usePoll(() => api.encargos(all), [all], 15000);
  return (
    <>
      <p className="sub">{t("Work for agents. An agent connected over MCP sees the pending ones when it starts, takes one (nobody else can then), waits on its runs without asking over and over, and hands it over with what it did. You review it, or send it back with what is missing.")}</p>
      <NewEncargo onCreated={reload} />
      <div className="row between" style={{ marginTop: 16 }}>
        <h2 style={{ margin: 0 }}>{all ? t("All encargos") : t("Open encargos")}</h2>
        <label className="small"><input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} /> {t("Show reviewed and dropped")}</label>
      </div>
      <ErrorBox error={error} />
      {data && !data.length && <p className="muted">{t("No encargos. Ask agents for something above, or from a failed run.")}</p>}
      <div className="stack" style={{ marginTop: 8 }}>
        {data?.map((e) => <EncargoCard key={e.id} e={e} onChange={reload} />)}
      </div>
    </>
  );
}
