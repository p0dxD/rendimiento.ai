import { useEffect, useState } from "react";
import type { AppStatus, ResourceNode, Run, Step } from "../api";
import { t } from "../i18n";

type Tone = "ok" | "warn" | "bad" | "idle";

const phaseTone: Record<string, Tone> = {
  Healthy: "ok",
  Progressing: "warn",
  WaitingForBuild: "idle",
  Degraded: "bad",
  Error: "bad",
  Suspended: "idle",
};
const phaseLabel: Record<string, string> = { WaitingForBuild: "Waiting for first build" };

/** A status word from the API (a phase, a run or step status, a health), in the UI's language. */
export function statusText(s: string): string {
  return t(phaseLabel[s] ?? s);
}

/** An app's state as a colored label (its message as the tooltip). */
export function PhaseBadge({ status }: { status: AppStatus }) {
  const phase = status.phase ?? "Pending";
  const tone = phaseTone[phase] ?? "idle";
  return (
    <span className={`badge ${tone} ${phase === "Progressing" ? "live" : ""}`} title={status.message}>
      {statusText(phase)}
    </span>
  );
}

const runTone: Record<string, Tone> = { succeeded: "ok", running: "warn", queued: "idle", failed: "bad", cancelled: "idle", reused: "idle" };

/** A run's or step's state as a colored label, pulsing while it runs. */
export function RunBadge({ status }: { status: Run["status"] | Step["status"] }) {
  const tone = runTone[status] ?? (status === "skipped" || status === "pending" ? "idle" : "idle");
  return <span className={`badge ${tone} ${status === "running" ? "live" : ""}`}>{statusText(status)}</span>;
}

/** A resource's health as a colored label. */
export function HealthBadge({ health }: { health: ResourceNode["health"] }) {
  const tone: Tone = health === "Healthy" ? "ok" : health === "Degraded" ? "bad" : health === "Missing" ? "idle" : "warn";
  return <span className={`badge ${tone}`}>{statusText(health)}</span>;
}

/** An error as a red box, or nothing. */
export function ErrorBox({ error }: { error: unknown }) {
  if (!error) return null;
  return <div className="alert">{error instanceof Error ? error.message : String(error)}</div>;
}

/** Loads data and refreshes it every `intervalMs` (0 = once). */
export function usePoll<T>(load: () => Promise<T>, deps: unknown[], intervalMs = 0) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    const go = () =>
      load().then(
        (d) => alive && (setData(d), setError(undefined)),
        (e) => alive && setError(e),
      );
    go();
    const t = intervalMs ? setInterval(go, intervalMs) : undefined;
    return () => {
      alive = false;
      if (t) clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  return { data, error, reload: () => setTick((n) => n + 1), setData };
}

/** The tree of an app's resources in the cluster, each with its health. */
export function ResourceTree({ node }: { node: ResourceNode }) {
  return (
    <ul className="tree">
      <TreeNode node={node} />
    </ul>
  );
}

/** One resource in the tree, with what is under it. */
function TreeNode({ node }: { node: ResourceNode }) {
  return (
    <li>
      <div className="node">
        <span className="kind">{node.kind}</span>
        <strong>{node.name}</strong>
        <HealthBadge health={node.health} />
        {node.info?.map((i) =>
          i.startsWith("https://") ? (
            <a key={i} href={i} target="_blank" rel="noreferrer" className="small">
              {i}
            </a>
          ) : (
            <span key={i} className="small muted">
              {i}
            </span>
          ),
        )}
      </div>
      {node.children && node.children.length > 0 && (
        <ul className="tree">
          {node.children.map((c) => (
            <TreeNode key={c.kind + c.name} node={c} />
          ))}
        </ul>
      )}
    </li>
  );
}

const stepColors: Record<string, string> = {
  succeeded: "var(--ok)",
  running: "var(--warn)",
  failed: "var(--bad)",
  skipped: "var(--idle)",
  pending: "var(--idle)",
  reused: "var(--border)",
};

/**
 * Pipeline DAG: steps are laid out in columns by dependency depth, one row
 * per service, with edges from each dependency. Deploy is drawn as the final
 * node for default-branch runs.
 */
export function PipelineGraph({ run, selected, onSelect }: { run: Run; selected?: string; onSelect: (id: string) => void }) {
  const steps = run.steps ?? [];
  const depth = new Map<string, number>();
  const byId = new Map(steps.map((s) => [s.id, s]));
  const d = (id: string): number => {
    if (depth.has(id)) return depth.get(id)!;
    const s = byId.get(id);
    const v = s && s.dependsOn.length ? 1 + Math.max(...s.dependsOn.map(d)) : 0;
    depth.set(id, v);
    return v;
  };
  steps.forEach((s) => d(s.id));
  const services = [...new Set(steps.map((s) => s.service))];
  const maxDepth = Math.max(0, ...steps.map((s) => d(s.id)));
  const W = 170, H = 54, GX = 60, GY = 22, PAD = 8;
  const pos = (s: Step) => ({ x: PAD + d(s.id) * (W + GX), y: PAD + services.indexOf(s.service) * (H + GY) });
  const deployCol = maxDepth + 1;
  const width = PAD * 2 + (deployCol + (run.deploy ? 1 : 0)) * (W + GX) - GX;
  const height = PAD * 2 + services.length * (H + GY) - GY;
  const deployX = PAD + deployCol * (W + GX);
  const deployY = height / 2 - H / 2;
  const deployStatus =
    run.status === "succeeded" ? "succeeded" : run.status === "failed" || run.status === "cancelled" ? "skipped" : "pending";

  return (
    <div className="pipeline">
      <svg width={Math.max(width, 200)} height={Math.max(height, H + PAD * 2)} role="img" aria-label={t("Pipeline steps")}>
        {steps.flatMap((s) =>
          s.dependsOn.map((dep) => {
            const a = pos(byId.get(dep)!), b = pos(s);
            return (
              <path key={dep + s.id} d={`M${a.x + W},${a.y + H / 2} C${a.x + W + GX / 2},${a.y + H / 2} ${b.x - GX / 2},${b.y + H / 2} ${b.x},${b.y + H / 2}`}
                fill="none" stroke="var(--border)" strokeWidth={2} />
            );
          }),
        )}
        {run.deploy &&
          steps
            .filter((s) => s.kind === "build")
            .map((s) => {
              const a = pos(s);
              return (
                <path key={"deploy" + s.id} d={`M${a.x + W},${a.y + H / 2} C${a.x + W + GX / 2},${a.y + H / 2} ${deployX - GX / 2},${deployY + H / 2} ${deployX},${deployY + H / 2}`}
                  fill="none" stroke="var(--border)" strokeWidth={2} />
              );
            })}
        {steps.map((s) => {
          const p = pos(s);
          const color = stepColors[s.status];
          return (
            <g key={s.id} className="stepbox" transform={`translate(${p.x},${p.y})`} onClick={() => onSelect(s.id)} opacity={s.status === "reused" ? 0.55 : 1}
              tabIndex={0} role="button" aria-label={`${s.id} ${statusText(s.status)}`} onKeyDown={(e) => e.key === "Enter" && onSelect(s.id)}>
              <rect width={W} height={H} rx={9} fill="var(--surface)" stroke={selected === s.id ? "var(--accent)" : "var(--border)"} strokeWidth={selected === s.id ? 2 : 1} />
              <rect width={5} height={H - 16} x={9} y={8} rx={2.5} fill={color}>
                {s.status === "running" && <animate attributeName="opacity" values="1;0.3;1" dur="1.2s" repeatCount="indefinite" />}
              </rect>
              <text x={24} y={22} fontSize={13} fontWeight={600} fill="var(--text)">{s.service}</text>
              <text x={24} y={40} fontSize={12} fill="var(--muted)">{t(s.kind)} · {statusText(s.status)}</text>
            </g>
          );
        })}
        {run.deploy && (
          <g transform={`translate(${deployX},${deployY})`}>
            <rect width={W} height={H} rx={9} fill="var(--surface)" stroke="var(--border)" />
            <rect width={5} height={H - 16} x={9} y={8} rx={2.5} fill={stepColors[deployStatus]} />
            <text x={24} y={22} fontSize={13} fontWeight={600} fill="var(--text)">{t("deploy")}</text>
            <text x={24} y={40} fontSize={12} fill="var(--muted)">{run.status === "succeeded" ? run.message : statusText(deployStatus)}</text>
          </g>
        )}
      </svg>
    </div>
  );
}

/** An on/off switch (a button with role="switch"). */
export function Switch({ checked, onChange, disabled, label }: { checked: boolean; onChange: (on: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} title={label} disabled={disabled}
      className={`switch ${checked ? "on" : ""}`} onClick={() => onChange(!checked)}>
      <span />
    </button>
  );
}
