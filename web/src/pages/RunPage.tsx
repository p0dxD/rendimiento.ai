import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, dollars, duration, session, subscribe, timeAgo, type Diagnosis, type Run, type Step } from "../api";
import { ErrorBox, RunBadge } from "../components/ui";
import { t } from "../i18n";

/** A run's page: its graph of steps, live as they go, and the log of the step chosen. */
export function RunPage() {
  const { name = "", id = "" } = useParams();
  const runId = Number(id);
  const [run, setRun] = useState<Run>();
  const [error, setError] = useState<unknown>();
  const [selected, setSelected] = useState<string>();
  const [logs, setLogs] = useState<Record<string, string>>({});
  const [diagnosis, setDiagnosis] = useState<Diagnosis | null>();
  const logRef = useRef<HTMLDivElement>(null);
  const follow = useRef(true);

  // Initial state, then live updates over SSE.
  useEffect(() => {
    api.run(runId).then((r) => {
      setRun(r);
      const first = r.steps?.find((s) => s.status === "running" || s.status === "failed") ?? r.steps?.[0];
      setSelected((cur) => cur ?? first?.id);
    }, setError);
    return subscribe(`/api/runs/${runId}/events`, {
      run: (r: Run) => setRun(r),
      step: (s: { id: string; status: string; digest?: string; message?: string }) =>
        setRun((cur) => cur && { ...cur, steps: cur.steps?.map((x) => (x.id === s.id ? { ...x, ...s } as typeof x : x)) }),
      log: (l: { step: string; text: string }) => setLogs((cur) => ({ ...cur, [l.step]: (cur[l.step] ?? "") + l.text })),
    });
  }, [runId]);

  // Stored log for the selected step (covers finished steps and reconnects).
  useEffect(() => {
    if (!selected) return;
    api.stepLog(runId, selected).then((text) => setLogs((cur) => (text.length >= (cur[selected]?.length ?? 0) ? { ...cur, [selected]: text } : cur)), () => {});
  }, [runId, selected, run?.steps?.find((s) => s.id === selected)?.status]);

  useEffect(() => {
    const el = logRef.current;
    if (el && follow.current) el.scrollTop = el.scrollHeight;
  }, [logs, selected]);

  if (error) return <ErrorBox error={error} />;
  if (!run) return <p className="muted">{t("Loading…")}</p>;
  const step = run.steps?.find((s) => s.id === selected);

  return (
    <>
      <p className="small"><Link to={`/apps/${name}/runs`}>{t("← {name} runs", { name })}</Link></p>
      <div className="row between">
        <div className="row">
          <h1>{t("Run #{n}", { n: run.id })}</h1>
          <RunBadge status={run.status} />
        </div>
        {(run.status === "running" || run.status === "queued") && (
          <button className="danger" onClick={() => api.cancelRun(run.id).catch((e) => alert(e.message))}>{t("Cancel")}</button>
        )}
        {run.status === "failed" && <AskAgent run={run} app={name} diagnosis={diagnosis?.status === "done" ? diagnosis.text : undefined} />}
      </div>
      <p className="sub">
        <span className="mono">{run.sha.slice(0, 7)}</span> {t("on")} <strong>{run.branch}</strong> · {t(run.event)}
        {run.deploy ? ` · ${t("deploys on success")}` : ` · ${t("build only")}`} · {timeAgo(run.createdAt)}
        {run.startedAt && ` · ${duration(run.startedAt, run.finishedAt)}`}
        {run.message && ` · ${run.message}`}
      </p>

      {run.status === "failed" && <WhyFailed run={run} d={diagnosis} setD={setDiagnosis} />}

      {run.steps?.length ? (
        <div className="ejecucion">
          <Pasos run={run} selected={selected} onSelect={setSelected} />
          {step && (
            <div className="ejecucion-registro">
              <div className="row">
                <strong>{step.id}</strong>
                <RunBadge status={step.status} />
                <span className="small muted">{duration(step.startedAt, step.finishedAt)}</span>
                {step.digest && <span className="mono small muted" title={step.digest}>{step.digest.slice(0, 19)}…</span>}
                <span style={{ flex: 1 }} />
                {firstError(logs[step.id]) >= 0 && (
                  <button className="small" onClick={() => { follow.current = false; logRef.current?.querySelector(".err")?.scrollIntoView({ block: "center", behavior: "smooth" }); }}>{t("Jump to the error")}</button>
                )}
              </div>
              {step.message && step.status !== "succeeded" && step.status !== "reused" && <div className="alert">{step.message}</div>}
              <Registro text={logs[step.id]} innerRef={logRef} onScroll={(near) => { follow.current = near; }}
                empty={step.status === "pending" ? t("Waiting to start…") : step.status === "skipped" ? t("Skipped.") : step.status === "reused" ? t("Nothing in this service changed, so it was not rebuilt: ") + (step.message ?? "") : t("No output yet.")} />
            </div>
          )}
        </div>
      ) : run.status === "failed" && run.message ? (
        <div className="alert" role="alert">
          <strong>{t("Nothing was built.")}</strong> {run.message}
          <div className="small" style={{ marginTop: 6 }}>{t("Fix the file and push again; the app keeps running its current release.")}</div>
        </div>
      ) : (
        <p className="muted">{t("Waiting to start…")}</p>
      )}
    </>
  );
}

const pasoColor: Record<Step["status"], string> = {
  succeeded: "var(--ok)", failed: "var(--bad)", running: "var(--warn)", pending: "var(--muted)", skipped: "var(--muted)", reused: "var(--anil)",
};
const pasoIcono: Record<Step["status"], string> = {
  succeeded: "M7 12.5l3 3 7-7", failed: "M8.5 8.5l7 7M15.5 8.5l-7 7", running: "M8 12h.01M12 12h.01M16 12h.01",
  pending: "", skipped: "M8 12h8", reused: "M8 10a4 4 0 0 1 7-1M16 14a4 4 0 0 1-7 1",
};

/**
 * The steps as a list, each with a bar for when it ran within the run: bars that overlap ran at the same time («a la par»),
 * so what ran in parallel shows at a glance.
 */
function Pasos({ run, selected, onSelect }: { run: Run; selected?: string; onSelect: (id: string) => void }) {
  const ms = (x?: string) => (x ? new Date(x).getTime() : undefined);
  const steps = run.steps ?? [];
  const now = Date.now();
  const starts = steps.map((s) => ms(s.startedAt)).filter((x): x is number => x !== undefined);
  const t0 = starts.length ? Math.min(...starts) : now;
  const t1 = Math.max(t0 + 1000, ...steps.map((s) => ms(s.finishedAt) ?? (s.startedAt ? now : 0)));
  const span = (s: Step): [number, number] | undefined => (s.startedAt ? [ms(s.startedAt)!, ms(s.finishedAt) ?? now] : undefined);
  const total = duration(new Date(t0).toISOString(), new Date(t1).toISOString());
  return (
    <div className="pasos" role="list" aria-label={t("Steps")}>
      <div className="pasos-escala small muted" aria-hidden="true"><span>0 s</span><span>{total}</span></div>
      {steps.map((s, i) => {
        const r = span(s);
        const con = r ? steps.filter((o) => o.id !== s.id && span(o) && span(o)![0] < r[1] && span(o)![1] > r[0]).map((o) => o.id) : [];
        const x = r ? ((r[0] - t0) / (t1 - t0)) * 100 : 0;
        const w = r ? Math.max(1.5, ((r[1] - r[0]) / (t1 - t0)) * 100) : 0;
        return (
          <button key={s.id} role="listitem" className={`paso${s.id === selected ? " on" : ""}${s.status === "running" ? " corre" : ""}`} onClick={() => onSelect(s.id)} aria-current={s.id === selected}>
            <svg width="24" height="24" viewBox="0 0 24 24" aria-hidden="true">
              <circle cx="12" cy="12" r="10" fill={pasoColor[s.status]} opacity={s.status === "pending" ? 0.35 : 1} />
              {pasoIcono[s.status] && <path d={pasoIcono[s.status]} stroke="#fff" strokeWidth="2.6" fill="none" strokeLinecap="round" strokeLinejoin="round" />}
            </svg>
            <span style={{ minWidth: 0 }}>
              <strong>{s.id}</strong>
              {con.length > 0 && <span className="par" title={t("Ran at the same time as {steps}", { steps: con.join(", ") })}>{t("side by side")}</span>}
              <br />
              <span className="small muted">{t(s.kind)}{s.dependsOn.length > 0 ? ` · ${t("after {steps}", { steps: s.dependsOn.join(", ") })}` : ""}</span>
              <span className="pista">{r && <b style={{ left: `${x}%`, width: `${Math.min(w, 100 - x)}%`, ["--c" as string]: pasoColor[s.status], ["--d" as string]: `${i * 50}ms` }} />}</span>
            </span>
            <span className="small muted">{s.startedAt ? duration(s.startedAt, s.finishedAt) : ""}</span>
          </button>
        );
      })}
    </div>
  );
}

const errLine = /\b(error|errors|failed|fail|failure|panic|fatal)\b|^--- FAIL|^FAIL\b/i;
const notErr = /\b(0|no) (errors|failures)\b/i;
const isErr = (l: string) => errLine.test(l) && !notErr.test(l);

/** The line of the first error in a log, or -1. */
function firstError(text?: string) {
  return formatLog(text).split("\n").findIndex(isErr);
}

/** A step's log with line numbers, its error lines marked and its groups as headings. */
function Registro({ text, empty, innerRef, onScroll }: { text?: string; empty: string; innerRef: React.RefObject<HTMLDivElement | null>; onScroll: (nearBottom: boolean) => void }) {
  const lines = formatLog(text).replace(/\n$/, "").split("\n");
  return (
    <div className="reg" role="log" aria-label={t("Step log")} ref={innerRef}
      onScroll={(e) => { const el = e.currentTarget; onScroll(el.scrollHeight - el.scrollTop - el.clientHeight < 40); }}>
      {!text ? <div className="reg-vacio">{empty}</div> : lines.map((l, i) => (
        <div key={i} className={isErr(l) ? "err" : l.startsWith("── ") ? "grp" : undefined}><span className="n">{i + 1}</span>{l}</div>
      ))}
    </div>
  );
}

/** Drops the ::group:: markers the executor uses to separate clone and step output. */
function formatLog(s?: string) {
  if (!s) return "";
  return s.replace(/^::group::(.*)$/gm, "── $1 ──").replace(/^::endgroup::$\n?/gm, "");
}

/**
 * On a failed run: hand it to agents as an encargo, with the failed steps
 * and the run's message as its detail, then go to the encargos.
 */
function AskAgent({ run, app, diagnosis }: { run: Run; app: string; diagnosis?: string }) {
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const failed = run.steps?.filter((s) => s.status === "failed") ?? [];
  const body = [
    t("Run #{n} of {app} failed on {branch} ({sha}).", { n: run.id, app, branch: run.branch, sha: run.sha.slice(0, 7) }),
    ...failed.map((s) => `- ${s.id}: ${s.message || t("failed")}`),
    run.message && !failed.length ? run.message : "",
    diagnosis ? t("The diagnostician's explanation:") + "\n" + diagnosis : "",
    t("Find out why (ejecucion_leer, paso_registro), fix it on a branch, test it there and hand it over with the link."),
  ].filter(Boolean).join("\n");
  return (
    <button disabled={busy} title={t("Creates an encargo for an agent to take")} onClick={() => {
      setBusy(true);
      api.createEncargo({ title: t("Fix run #{n} of {app}", { n: run.id, app }), body, app, runId: run.id })
        .then(() => navigate("/ruta?tab=encargos"), (e) => { setBusy(false); alert(e.message); });
    }}>{t("Ask an agent")}</button>
  );
}

/**
 * «Why did it fail?»: the diagnostician agent's explanation of a failed
 * run, kept fresh while it works; a collaborator asks for it (again).
 */
function WhyFailed({ run, d, setD }: { run: Run; d: Diagnosis | null | undefined; setD: (d: Diagnosis | null) => void }) {
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    let live = true;
    const load = () => api.diagnosis(run.id).then((x) => live && setD(x), () => live && setD(null));
    load();
    const every = setInterval(load, 8000);
    return () => { live = false; clearInterval(every); };
  }, [run.id, setD]);
  const ask = () => { setAsking(true); api.askDiagnosis(run.id).then(() => { setAsking(false); setD({ runId: run.id, status: "pending", costMicros: 0, updatedAt: new Date().toISOString() }); }, (e) => { setAsking(false); alert(e.message); }); };
  const canAsk = session.role !== "guest";
  return (
    <div className="card why-failed">
      <div className="row between">
        <h2 style={{ margin: 0 }}>{t("Why did it fail?")}</h2>
        {canAsk && d?.status !== "pending" && <button className="link small" disabled={asking} onClick={ask}>{d ? t("Explain again") : t("Explain")}</button>}
      </div>
      {d === undefined && <p className="muted small">{t("Loading…")}</p>}
      {d === null && <p className="muted small">{t("No explanation yet. If automatic diagnosis is on (AI page), the diagnostician agent writes one within a minute of the failure.")}</p>}
      {d?.status === "pending" && <p className="muted small">{t("The diagnostician agent is reading the logs…")}</p>}
      {d?.status === "done" && <p style={{ whiteSpace: "pre-wrap", margin: "8px 0" }}>{d.text}</p>}
      {d?.status === "failed" && <p className="small">{t("It could not explain it: {why}", { why: d.note ?? "" })}</p>}
      {d?.status === "done" && <Judge run={run} d={d} setD={setD} />}
      {d && d.status !== "pending" && d.model && (
        <p className="small muted" style={{ margin: 0 }}>{t("By the diagnostician agent")} · {d.model} · {dollars(d.costMicros)} · {timeAgo(d.updatedAt)}{d.note && d.status === "done" && <> · {d.note}</>}</p>
      )}
    </div>
  );
}

/**
 * Did the explanation help? Either answer becomes a known failure in the
 * Ruta, which the diagnostician reads next time a failure looks alike: that
 * is how it learns.
 */
function Judge({ run, d, setD }: { run: Run; d: Diagnosis; setD: (d: Diagnosis) => void }) {
  const [wrong, setWrong] = useState(false);
  const [cause, setCause] = useState("");
  const [busy, setBusy] = useState(false);
  if (session.role === "guest") return null;
  const send = (useful: boolean) => {
    setBusy(true);
    api.judgeDiagnosis(run.id, useful, cause.trim()).then((r) => {
      setBusy(false); setWrong(false);
      setD({ ...d, feedback: useful ? "useful" : "wrong", feedbackBy: session.role, rutaEntry: r.rutaEntry });
    }, (e) => { setBusy(false); alert(e.message); });
  };
  if (d.feedback && !wrong) {
    return (
      <p className="small judge">
        {d.feedback === "useful" ? t("Marked useful.") : t("Corrected.")}{" "}
        {d.rutaEntry && <Link to="/ruta">{t("Kept in the Ruta as a known failure (#{n}): the diagnostician reads it next time.", { n: d.rutaEntry })}</Link>}{" "}
        <button className="link small" onClick={() => setWrong(true)}>{t("Change")}</button>
      </p>
    );
  }
  return (
    <div className="judge stack">
      {!wrong ? (
        <div className="row" style={{ gap: 8 }}>
          <span className="small muted">{t("Did it help?")}</span>
          <button className="small" disabled={busy} onClick={() => send(true)}>{t("Useful")}</button>
          <button className="small" disabled={busy} onClick={() => setWrong(true)}>{t("Not useful…")}</button>
        </div>
      ) : (
        <>
          <label className="field"><span>{t("What was the real cause, and the fix?")}</span>
            <textarea rows={3} value={cause} maxLength={4000} onChange={(e) => setCause(e.target.value)}
              placeholder={t("e.g. The test's etcd is slow on a busy node: it is transient, run it again. Fixed for good with unsafe-no-fsync.")} />
          </label>
          <div className="row" style={{ gap: 8 }}>
            <button className="primary small" disabled={busy || !cause.trim()} onClick={() => send(false)}>{t("Teach the diagnostician")}</button>
            <button className="link small" onClick={() => setWrong(false)}>{t("Cancel")}</button>
          </div>
        </>
      )}
    </div>
  );
}
