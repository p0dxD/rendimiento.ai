import { useState } from "react";
import { Link } from "react-router-dom";
import { api, dollars, timeAgo, type CodeAgent, type Encargo, type EncargoStatus } from "../api";
import { ErrorBox, usePoll } from "./ui";
import { t, tn } from "../i18n";
import { TextoConFormato } from "./texto";

const statusLabel: Record<EncargoStatus, string> = {
  pending: "Pending", taken: "Taken", waiting: "Waiting", delivered: "Delivered", reviewed: "Reviewed", cancelled: "Dropped",
};
const statusClass: Record<EncargoStatus, string> = {
  pending: "", taken: "live", waiting: "warn live", delivered: "warn", reviewed: "ok", cancelled: "",
};
const eventLabel: Record<string, string> = {
  created: "created", taken: "taken", waiting: "waiting", resumed: "done waiting", delivered: "delivered",
  reviewed: "reviewed", routed: "routed", estimate: "asked to estimate it again", sent: "sent it to the coding agent", returned: "sent back", released: "let go", cancelled: "dropped",
};

const routeLabel = { platform: "For the platform's AI", local: "For your own agent" };

/** A difficulty's color, from green (1, cheap) to red (10, expensive), in either theme. */
const difficultyColor = (d: number) => `color-mix(in oklab, var(--bad) ${Math.round(((d - 1) / 9) * 100)}%, var(--ok))`;

/** A difficulty as a chip in its color. */
function DifficultyChip({ d }: { d: number }) {
  return <span className="dificultad" style={{ ["--c" as string]: difficultyColor(d) }}>{d}/10</span>;
}

/**
 * What the estimator said of an encargo (how hard, what the platform's AI
 * would spend) and, while it is pending, who should do it, with the choice
 * to change it: past the amount set on the AI page, it is for the person's
 * own agent, paid by the month rather than by the call.
 */
function Estimate({ e, act }: { e: Encargo; act: (p: Promise<unknown>) => void }) {
  if (e.status === "cancelled") return null;
  const again = <button className="link small" onClick={() => act(api.reestimateEncargo(e.id))}>{t("Estimate again")}</button>;
  if (!e.difficulty) {
    return <p className="small muted encargo-estimate">{e.estimateReason ? <>{t("No estimate: {why}", { why: e.estimateReason })} {again}</> : t("Estimating…")}</p>;
  }
  const pending = e.status === "pending";
  return (
    <div className="small encargo-estimate">
      <DifficultyChip d={e.difficulty} />
      {pending && <span className={`badge ${e.route === "platform" ? "ok" : ""}`}>{t(routeLabel[e.route ?? "local"])}</span>}
      <span>{t("about {cost} with the platform's AI", { cost: dollars(e.estimateMicros ?? 0) })}</span>
      {e.estimateReason && <span className="muted"> · {e.estimateReason}</span>}
      {pending && (e.route === "platform"
        ? <button className="link small" onClick={() => act(api.routeEncargo(e.id, "local"))}>{t("Leave it to my agent")}</button>
        : <button className="link small" onClick={() => act(api.routeEncargo(e.id, "platform"))}>{t("Do it with AI anyway")}</button>)}
      {again}
    </div>
  );
}

/**
 * What the encargos would cost with the platform's AI: the open ones (all,
 * and only the easy ones, under the amount), and what the done ones were
 * worth, with the open ones by difficulty from green to red.
 */
function CostSummary({ list, codeAgent, chosen, onDo }: { list: Encargo[]; codeAgent?: CodeAgent; chosen: number[]; onDo: (ids: number[]) => void }) {
  const estimated = list.filter((e) => e.difficulty && e.status !== "cancelled");
  if (!estimated.length) return null;
  const open = estimated.filter((e) => e.status !== "delivered" && e.status !== "reviewed");
  const easy = open.filter((e) => e.route === "platform");
  const done = estimated.filter((e) => e.status === "delivered" || e.status === "reviewed");
  const sum = (l: Encargo[]) => dollars(l.reduce((n, e) => n + (e.estimateMicros ?? 0), 0));
  const waiting = list.filter((e) => !e.difficulty && e.status !== "cancelled").length;
  const byDifficulty = Array.from({ length: 10 }, (_, i) => open.filter((e) => e.difficulty === i + 1));
  const spent = list.reduce((n, e) => n + (e.spentMicros ?? 0), 0);
  // What can be sent to the coding agent: pending, of an app, not sent yet.
  const sendable = open.filter((e) => e.status === "pending" && e.app && !e.doNow);
  const easyToSend = sendable.filter((e) => e.route === "platform");
  const hardToSend = sendable.filter((e) => e.route !== "platform");
  const chosenList = list.filter((e) => chosen.includes(e.id));
  const send = (l: Encargo[], ask: boolean) => {
    if (!l.length) return;
    if (ask && !confirm(t("Send {n} encargos to the platform's coding agent? It would spend about {cost}, at most twice that.", { n: l.length, cost: sum(l) }))) return;
    onDo(l.map((e) => e.id));
  };
  return (
    <div className="card stack encargo-costos">
      <strong>{t("What the encargos would cost with the platform's AI")}</strong>
      <div className="encargo-cifras">
        <div><span className="cifra">{sum(open)}</span><span className="small muted">{tn(open.length, "to do the open one", "to do the {n} open ones")}</span></div>
        <div><span className="cifra">{sum(easy)}</span><span className="small muted">{tn(easy.length, "to do only the easy one (under your amount)", "to do only the {n} easy ones (under your amount)")}</span></div>
        <div><span className="cifra">{sum(done)}</span><span className="small muted">{tn(done.length, "what the done one was worth", "what the {n} done ones were worth")}</span></div>
        <div><span className="cifra">{dollars(spent)}</span><span className="small muted">{t("what the platform's coding agent spent on them; the rest was done by your own agent, paid by the month")}</span></div>
      </div>
      {codeAgent && codeAgent !== "off" ? (
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <button disabled={!easyToSend.length} onClick={() => send(easyToSend, false)}>{t("Do the easy ones ({n} · {cost})", { n: easyToSend.length, cost: sum(easyToSend) })}</button>
          <button disabled={!hardToSend.length} onClick={() => send(hardToSend, true)}>{t("Do the hard ones ({n} · {cost})", { n: hardToSend.length, cost: sum(hardToSend) })}</button>
          <button className="primary" disabled={!chosenList.length} onClick={() => send(chosenList, true)}>{t("Do the chosen ones ({n} · {cost})", { n: chosenList.length, cost: sum(chosenList) })}</button>
        </div>
      ) : codeAgent === "off" && <span className="small muted">{t("To have the platform's AI do them, an administrator turns on the coding agent on the AI page.")}</span>}
      {open.length > 0 && (
        <div className="stack" style={{ gap: 4 }}>
          <div className="dificultad-barra" role="img" aria-label={t("Open encargos by difficulty")}>
            {byDifficulty.map((l, i) => l.length > 0 && (
              <span key={i} style={{ flexGrow: l.length, background: difficultyColor(i + 1) }} title={t("{n} of difficulty {d}", { n: l.length, d: i + 1 })}>{i + 1}</span>
            ))}
          </div>
          <div className="row between small muted"><span>{t("cheap")}</span><span>{t("expensive")}</span></div>
        </div>
      )}
      {waiting > 0 && <span className="small muted">{tn(waiting, "{n} still to estimate", "{n} still to estimate")}</span>}
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
function EncargoCard({ e, onChange, choosable, chosen, onChoose }: { e: Encargo; onChange: () => void; choosable: boolean; chosen: boolean; onChoose: (on: boolean) => void }) {
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
          {choosable && <input type="checkbox" checked={chosen} onChange={(ev) => onChoose(ev.target.checked)} aria-label={t("Choose it")} />}
          <span className={`badge ${statusClass[e.status]}`}>{t(statusLabel[e.status])}</span>
          {e.doNow && e.status === "pending" && <span className="badge warn live">{t("in the coding agent's queue")}</span>}
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
      {!!e.spentMicros && <p className="small muted encargo-estimate">{t("The coding agent spent {cost} on it.", { cost: dollars(e.spentMicros) })}</p>}
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
  const { data, error, reload: reloadList } = usePoll(() => api.encargos(all), [all], 15000);
  const { data: todos, reload: reloadAll } = usePoll(() => api.encargos(true), [], 15000);
  const reload = () => { reloadList(); reloadAll(); };
  const { data: ia } = usePoll(api.ia, [], 60000);
  const codeAgent = ia?.settings.codeAgent;
  const [chosen, setChosen] = useState<number[]>([]);
  const [sent, setSent] = useState<string>();
  const doThem = (ids: number[]) => api.doEncargos(ids).then((r) => {
    setChosen([]); setSent(tn(r.sent, "Sent {n} encargo to the coding agent.", "Sent {n} encargos to the coding agent.")); reload();
  }, (err: Error) => alert(err.message));
  return (
    <>
      <p className="sub">{t("Work for agents. An agent connected over MCP sees the pending ones when it starts, takes one (nobody else can then), waits on its runs without asking over and over, and hands it over with what it did. You review it, or send it back with what is missing.")}</p>
      <NewEncargo onCreated={reload} />
      {todos && <CostSummary list={todos} codeAgent={codeAgent} chosen={chosen} onDo={doThem} />}
      {sent && <p className="small" role="status">{sent}</p>}
      <div className="row between" style={{ marginTop: 16 }}>
        <h2 style={{ margin: 0 }}>{all ? t("All encargos") : t("Open encargos")}</h2>
        <label className="small"><input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} /> {t("Show reviewed and dropped")}</label>
      </div>
      <ErrorBox error={error} />
      {data && !data.length && <p className="muted">{t("No encargos. Ask agents for something above, or from a failed run.")}</p>}
      <div className="stack" style={{ marginTop: 8 }}>
        {data?.map((e) => (
          <EncargoCard key={e.id} e={e} onChange={reload} chosen={chosen.includes(e.id)}
            choosable={!!codeAgent && codeAgent !== "off" && e.status === "pending" && !!e.app && !e.doNow}
            onChoose={(on) => setChosen(on ? [...chosen, e.id] : chosen.filter((id) => id !== e.id))} />
        ))}
      </div>
    </>
  );
}
