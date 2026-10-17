// The Caminos tab of an app: its last two weeks as roads (see caminos.ts),
// with a panel that tells each branch's and each run's story.
import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, type Caminos, type CaminosBranch, type CaminosRun } from "../api";
import { locale, t, tn } from "../i18n";
import { CaminosMap, Layout, type CaminosSel, type PlayState } from "./caminos";
import { ErrorBox, usePoll } from "./ui";

const fmtWhen = new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
const when = (iso?: string) => (iso ? fmtWhen.format(new Date(iso)) : "—");
const secs = (a?: string, b?: string) => (a && b ? (new Date(b).getTime() - new Date(a).getTime()) / 1000 : 0);

function dur(s: number): string {
  if (!(s > 0)) return "—";
  if (s < 90) return t("{n} s", { n: Math.round(s) });
  if (s < 3600) return t("{n} min", { n: Math.round(s / 60) });
  if (s < 2 * 86400) { const h = Math.floor(s / 3600), m = Math.round((s % 3600) / 60); return m ? t("{h} h {m} min", { h, m }) : t("{h} h", { h }); }
  return tn(Math.round(s / 86400), "{n} day", "{n} days");
}

const STATUS: Record<string, { cls: string; label: string }> = {
  succeeded: { cls: "ok", label: "Passed" },
  failed: { cls: "bad", label: "Failed" },
  cancelled: { cls: "", label: "Cancelled" },
  running: { cls: "warn", label: "On the way" },
  queued: { cls: "warn", label: "On the way" },
};
const statusOf = (s: string) => STATUS[s] ?? { cls: "", label: s };
const KIND: Record<string, string> = { test: "tests", build: "image", task: "task" };
/** "platform:test" → "platform: tests". */
const stepLabel = (id: string) => { const [svc, kind = ""] = id.split(":"); return `${svc}: ${t(KIND[kind] ?? kind)}`; };

const PACES = [
  { s: 180, label: "Stroll · 3 min" },
  { s: 90, label: "Trot · 1½ min" },
  { s: 45, label: "Gallop · 45 s" },
];

/** The Caminos tab. */
export function CaminosTab({ name }: { name: string }) {
  const { data, error } = usePoll(() => api.caminos(name), [name], 30000);
  if (error) return <ErrorBox error={error} />;
  if (!data) return <p className="muted">{t("Loading…")}</p>;
  if (data.main.length === 0 && data.branches.length === 0) return <p className="muted">{t("No runs in the last two weeks yet: the road starts with the first push.")}</p>;
  return <CaminosView name={name} data={data} />;
}

function CaminosView({ name, data }: { name: string; data: Caminos }) {
  const canvas = useRef<HTMLCanvasElement>(null), mini = useRef<HTMLCanvasElement>(null);
  const engine = useRef<CaminosMap>(null);
  const [sel, setSel] = useState<CaminosSel | null>(null);
  const [hint, setHint] = useState("");
  const [play, setPlay] = useState<PlayState>("stopped");
  const [pace, setPace] = useState(180);
  const layout = useMemo(() => new Layout(data), [data]);

  useEffect(() => {
    if (!canvas.current || !mini.current) return;
    const e = new CaminosMap(canvas.current, mini.current, data, { onSelect: setSel, onHover: setHint, onPlay: setPlay });
    engine.current = e;
    return () => { e.destroy(); engine.current = null; };
    // the engine is made once per app; new data arrives through setData
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);
  useEffect(() => { engine.current?.setData(data); }, [data]);
  useEffect(() => { engine.current?.setSpeed(pace); }, [pace]);
  const choose = (s: CaminosSel | null) => { setSel(s); engine.current?.select(s, true); };

  const branchNames = [...data.branches].sort((a, b) => lastAt(b).localeCompare(lastAt(a)));
  return (
    <div className="caminos">
      <div className="row between" style={{ marginBottom: 10 }}>
        <div className="row">
          <label className="small muted" htmlFor="caminos-branch">{t("Go to a branch")}</label>
          <select id="caminos-branch" value={sel?.kind === "branch" ? sel.name : ""} onChange={(e) => choose(e.target.value ? { kind: "branch", name: e.target.value } : null)}>
            <option value="">{t("All branches")}</option>
            {branchNames.map((b) => <option key={b.name} value={b.name}>{b.name}{b.pr ? ` (#${b.pr})` : ""}</option>)}
          </select>
        </div>
        <div className="row">
          <label className="small muted" htmlFor="caminos-pace">{t("Pace")}</label>
          <select id="caminos-pace" value={pace} onChange={(e) => setPace(+e.target.value)}>
            {PACES.map((p) => <option key={p.s} value={p.s}>{t(p.label)}</option>)}
          </select>
          <button className="primary" onClick={() => engine.current?.togglePlay()}>
            {play === "stopped" ? t("▶ Tell the story") : play === "playing" ? t("⏸ Pause") : t("▶ Resume")}
          </button>
          {play !== "stopped" && <button onClick={() => engine.current?.stop()}>{t("⏹ Finish")}</button>}
        </div>
      </div>
      {data.partial && <div className="alert info" style={{ marginBottom: 10 }}>{t("GitHub didn't answer everything: some branches may lack their pull request or the commit they started from.")}</div>}
      <div className="caminos-main">
        <div className="caminos-col">
          <div className="caminos-stage">
            <div className="caminos-picado" aria-hidden="true">{Array.from({ length: 16 }, (_, i) => <i key={i} />)}</div>
            <canvas ref={canvas} aria-label={t("Map of the branches as roads that leave the default branch and come back to it")} />
            <div className="caminos-tools">
              <button onClick={() => engine.current?.zoom(1.4)} aria-label={t("Zoom in")}>+</button>
              <button onClick={() => engine.current?.zoom(1 / 1.4)} aria-label={t("Zoom out")}>−</button>
              <button onClick={() => engine.current?.today()} aria-label={t("Go to the most recent")}>⇥</button>
            </div>
            <div className="caminos-hint"><span>{hint || t("Drag to travel the road · Ctrl + wheel or the buttons to zoom · tap a stop or a branch")}</span></div>
          </div>
          <div className="caminos-mini"><canvas ref={mini} aria-label={t("The whole two weeks; tap to go to that date")} /></div>
        </div>
        <aside className="card caminos-panel" aria-live="polite">
          <Panel name={name} data={data} layout={layout} sel={sel} choose={choose} />
        </aside>
      </div>
      <details className="card caminos-legend">
        <summary>{t("How to read it")}</summary>
        <ul>
          <li>{t("The camino real is the default branch ({branch}). It runs left to right, oldest to newest; the stone markers are days.", { branch: data.defaultBranch })}</li>
          <li>{t("Each vereda is a branch: it leaves the camino real at the commit it started from and, once merged, comes back by its pull request. A stacked pull request leaves its parent branch instead.")}</li>
          <li>{t("Each stop is a run. A whole crate passed; a broken crate with its maize spilled failed, and the panel says at which step; a grey one was cancelled.")}</li>
          <li>{t("The market stalls on the camino real are the releases that were deployed.")}</li>
          <li>{t("A vereda that ends at a sign was not merged: waiting at the gate if it ran recently or its pull request is open, left on the way if nobody came back for it.")}</li>
          <li>{t("Tell the story walks the two weeks: a burro carries each branch's load, stumbles where a run failed, and the caravan reaches the market with each release.")}</li>
        </ul>
      </details>
    </div>
  );
}

const lastAt = (b: CaminosBranch) => b.runs[b.runs.length - 1]?.at ?? "";

function Panel({ name, data, layout, sel, choose }: { name: string; data: Caminos; layout: Layout; sel: CaminosSel | null; choose: (s: CaminosSel | null) => void }) {
  if (sel?.kind === "branch") {
    const b = data.branches.find((x) => x.name === sel.name);
    if (b) return <BranchPanel b={b} choose={choose} />;
  }
  if (sel?.kind === "run") {
    const s = layout.all.find((x) => x.r.id === sel.id);
    if (s) return <RunPanel name={name} r={s.r} b={s.b?.b} def={data.defaultBranch} choose={choose} />;
  }
  return <Summary data={data} layout={layout} choose={choose} />;
}

function Summary({ data, layout, choose }: { data: Caminos; layout: Layout; choose: (s: CaminosSel | null) => void }) {
  const all = layout.all.map((s) => s.r);
  const n = all.length, ok = all.filter((r) => r.status === "succeeded").length;
  const fails = layout.all.filter((s) => s.r.status === "failed").sort((a, b) => b.at - a.at);
  const merged = data.branches.filter((b) => b.state === "merged");
  const lives = merged.map((b) => secs(b.fork?.at ?? b.runs[0].at, b.mergedAt)).filter((x) => x > 0).sort((a, b) => a - b);
  const firstTry = merged.filter((b) => b.runs.find((r) => r.status !== "cancelled")?.status === "succeeded").length;
  const open = data.branches.filter((b) => b.state !== "merged");
  const stumbles = (b: CaminosBranch) => b.runs.filter((r) => r.status === "failed").length;
  const worst = [...data.branches].sort((a, b) => stumbles(b) - stumbles(a))[0];
  return (
    <>
      <div>
        <h2>{t("Two weeks on the road")}</h2>
        <p className="muted small">{t("{branches} branches left the camino real and {merged} came back with their load; {releases} releases reached the market.", { branches: data.branches.length, merged: merged.length, releases: layout.releases.length })}</p>
      </div>
      <div className="rel-tiles">
        <Tile label={t("Trips")} value={String(n)} />
        <Tile label={t("Arrived whole")} value={n ? `${Math.round((ok / n) * 100)}%` : "—"} />
        <Tile label={t("Typical branch life")} value={lives.length ? dur(lives[Math.floor(lives.length / 2)]) : "—"} />
        <Tile label={t("Passed on the first trip")} value={merged.length ? t("{n} of {m}", { n: firstTry, m: merged.length }) : "—"} />
      </div>
      {open.length > 0 && (
        <div>
          <h3>{t("Branches not merged")}</h3>
          <div className="caminos-list">
            {open.map((b) => (
              <button key={b.name} className="caminos-item" onClick={() => choose({ kind: "branch", name: b.name })}>
                <span className={`badge ${b.state === "open" ? "warn" : "bad"}`} />
                <span>{b.name}<small>{b.state === "open" ? t("Waiting at the gate") : t("Left on the way")} · {when(lastAt(b))}</small></span>
                <span className="muted small">{tn(b.runs.length, "{n} trip", "{n} trips")}</span>
              </button>
            ))}
          </div>
        </div>
      )}
      <div>
        <h3>{t("Stumbles ({n})", { n: fails.length })}</h3>
        {fails.length === 0 ? <p className="muted small">{t("Not one: every load arrived whole.")}</p> : (
          <div className="caminos-list">
            {fails.slice(0, 8).map((s) => <RunItem key={s.r.id} r={s.r} where={s.b?.b.name ?? data.defaultBranch} choose={choose} />)}
          </div>
        )}
        {fails.length > 8 && <p className="muted small">{t("And {n} more, in red on the map below.", { n: fails.length - 8 })}</p>}
      </div>
      {worst && stumbles(worst) > 1 && <p className="muted small">{t("The branch that stumbled most was {name}: {n} times.", { name: worst.name, n: stumbles(worst) })}</p>}
    </>
  );
}

function RunItem({ r, where, choose }: { r: CaminosRun; where: string; choose: (s: CaminosSel | null) => void }) {
  const st = statusOf(r.status);
  return (
    <button className="caminos-item" onClick={() => choose({ kind: "run", id: r.id })}>
      <span className={`badge ${st.cls}`} />
      <span>{r.release ? t("Release #{n} reached the market", { n: r.release }) : (r.failed?.length ? t("Stumbled at {step}", { step: r.failed.map(stepLabel).join(", ") }) : t(st.label))}<small>{where} · {when(r.at)}</small></span>
      <span className="muted small">{dur(secs(r.at, r.end))}</span>
    </button>
  );
}

function BranchPanel({ b, choose }: { b: CaminosBranch; choose: (s: CaminosSel | null) => void }) {
  const fails = b.runs.filter((r) => r.status === "failed").length;
  const state = b.state === "merged" ? { cls: "ok", label: b.pr ? t("Merged by pull request #{n}", { n: b.pr }) : t("Merged") }
    : b.state === "open" ? { cls: "warn", label: t("Waiting at the gate") } : { cls: "bad", label: t("Left on the way") };
  const start = b.fork?.at ?? b.runs[0].at, end = b.mergedAt ?? b.runs[b.runs.length - 1].end ?? b.runs[b.runs.length - 1].at;
  return (
    <>
      <div>
        <span className={`badge ${state.cls}`}>{state.label}</span>
        <h2>{b.name}</h2>
        {b.title && <p className="small">{b.title}</p>}
        <p className="muted small">
          {b.parent ? t("Left the branch {parent} on {when}", { parent: b.parent, when: when(start) }) : t("Left the camino real on {when}", { when: when(start) })}
          {b.fork && <> · <code>{b.fork.sha.slice(0, 7)}</code></>}
        </p>
      </div>
      <div className="rel-tiles">
        <Tile label={t("Trips")} value={String(b.runs.length)} />
        <Tile label={t("Stumbles")} value={String(fails)} />
        <Tile label={b.state === "merged" ? t("From leaving to merging") : t("On the way")} value={dur(secs(start, end))} />
        <Tile label={t("Merge commit")} value={b.mergeSha ? b.mergeSha.slice(0, 7) : "—"} />
      </div>
      <div>
        <h3>{t("Its trips")}</h3>
        <div className="caminos-list">{[...b.runs].reverse().map((r) => <RunItem key={r.id} r={r} where={b.name} choose={choose} />)}</div>
      </div>
      <div className="row"><button onClick={() => choose(null)}>{t("← The whole road")}</button></div>
    </>
  );
}

function RunPanel({ name, r, b, def, choose }: { name: string; r: CaminosRun; b?: CaminosBranch; def: string; choose: (s: CaminosSel | null) => void }) {
  const st = statusOf(r.status);
  return (
    <>
      <div>
        <span className={`badge ${r.release ? "ok" : st.cls}`}>{r.release ? t("Reached the market") : t(st.label)}</span>
        <h2>{r.release ? t("Release #{n}", { n: r.release }) : r.failed?.length ? t("Stumbled at {step}", { step: r.failed.map(stepLabel).join(", ") }) : t("Run {id}", { id: r.id })}</h2>
        <p className="muted small">{b ? t("Branch {name}", { name: b.name }) : t("Camino real ({branch})", { branch: def })} · {t("run {id}", { id: r.id })}</p>
      </div>
      <dl className="caminos-facts">
        <dt>{t("Commit")}</dt><dd><code>{r.sha.slice(0, 7)}</code></dd>
        <dt>{t("Left")}</dt><dd>{when(r.at)}</dd>
        <dt>{t("Took")}</dt><dd>{dur(secs(r.at, r.end))}</dd>
        {b?.title && <><dt>{t("Load")}</dt><dd>{b.title}</dd></>}
      </dl>
      {r.message && !r.release && <pre className="caminos-msg">{r.message}</pre>}
      <div className="row">
        <Link to={`/apps/${name}/runs/${r.id}`}>{t("Open the run")}</Link>
        {b && <button onClick={() => choose({ kind: "branch", name: b.name })}>{t("See the branch")}</button>}
        <button onClick={() => choose(null)}>{t("← The whole road")}</button>
      </div>
    </>
  );
}

function Tile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rel-tile">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
    </div>
  );
}
