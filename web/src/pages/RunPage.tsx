import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, duration, subscribe, timeAgo, type Run } from "../api";
import { ErrorBox, PipelineGraph, RunBadge } from "../components/ui";

export function RunPage() {
  const { name = "", id = "" } = useParams();
  const runId = Number(id);
  const [run, setRun] = useState<Run>();
  const [error, setError] = useState<unknown>();
  const [selected, setSelected] = useState<string>();
  const [logs, setLogs] = useState<Record<string, string>>({});
  const logRef = useRef<HTMLPreElement>(null);
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
  if (!run) return <p className="muted">Loading…</p>;
  const step = run.steps?.find((s) => s.id === selected);

  return (
    <>
      <p className="small"><Link to={`/apps/${name}/runs`}>← {name} runs</Link></p>
      <div className="row between">
        <div className="row">
          <h1>Run #{run.id}</h1>
          <RunBadge status={run.status} />
        </div>
        {(run.status === "running" || run.status === "queued") && (
          <button className="danger" onClick={() => api.cancelRun(run.id).catch((e) => alert(e.message))}>Cancel</button>
        )}
      </div>
      <p className="sub">
        <span className="mono">{run.sha.slice(0, 7)}</span> on <strong>{run.branch}</strong> · {run.event}
        {run.deploy ? " · deploys on success" : " · build only"} · {timeAgo(run.createdAt)}
        {run.startedAt && ` · ${duration(run.startedAt, run.finishedAt)}`}
        {run.message && ` · ${run.message}`}
      </p>

      {run.steps?.length ? (
        <div className="card">
          <PipelineGraph run={run} selected={selected} onSelect={setSelected} />
        </div>
      ) : run.status === "failed" && run.message ? (
        <div className="alert" role="alert">
          <strong>Nothing was built.</strong> {run.message}
          <div className="small" style={{ marginTop: 6 }}>Fix the file and push again; the app keeps running its current release.</div>
        </div>
      ) : (
        <div className="card">
          <PipelineGraph run={run} selected={selected} onSelect={setSelected} />
        </div>
      )}

      {step && (
        <>
          <div className="row between" style={{ margin: "20px 0 8px" }}>
            <div className="row">
              <strong>{step.id}</strong>
              <RunBadge status={step.status} />
              <span className="small muted">{duration(step.startedAt, step.finishedAt)}</span>
            </div>
            {step.digest && <span className="mono small muted" title={step.digest}>{step.digest.slice(0, 19)}…</span>}
          </div>
          {step.message && step.status !== "succeeded" && step.status !== "reused" && <div className="alert" style={{ marginBottom: 8 }}>{step.message}</div>}
          <pre className="log" ref={logRef}
            onScroll={(e) => { const el = e.currentTarget; follow.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40; }}>
            {formatLog(logs[step.id]) || (step.status === "pending" ? "Waiting to start…" : step.status === "skipped" ? "Skipped." : step.status === "reused" ? "Nothing in this service changed, so it was not rebuilt: " + (step.message ?? "") : "No output yet.")}
          </pre>
        </>
      )}
    </>
  );
}

/** Drops the ::group:: markers the executor uses to separate clone and step output. */
function formatLog(s?: string) {
  if (!s) return "";
  return s.replace(/^::group::(.*)$/gm, "── $1 ──").replace(/^::endgroup::$\n?/gm, "");
}
