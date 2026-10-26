import { useEffect, useState, type ReactNode } from "react";
import { api, dollars, session, timeAgo, type AIProvider, type IAAnswer, type IAView } from "../api";
import { ErrorBox, Switch, usePoll } from "../components/ui";
import { t } from "../i18n";

const providerLabel: Record<AIProvider, string> = { local: "local model", own: "own key", platform: "the platform's" };
const providerName: Record<AIProvider, string> = { local: "The local model", own: "Your own key", platform: "The platform's" };
const escalateName: Record<"" | AIProvider, string> = { "": "Do not escalate", local: "Do not escalate", own: "Your own key", platform: "The platform's" };

/**
 * The organization's AI: which model answers (the local one, free; its own
 * key; or the platform's, within the operator's allowance), its monthly cap,
 * what it spent, and every call. Administrators change it; collaborators
 * try it.
 */
export function IA() {
  const { data, error, reload } = usePoll(api.ia, [], 30000);
  return (
    <>
      <h1>{t("AI")}</h1>
      <p className="sub">{t("Every call the platform makes to a model goes through here: the agents inside the platform, and the explanations of what failed. It stays within the cap you set, and when a cap is reached the local model answers instead.")}</p>
      <ErrorBox error={error} />
      {data && <Summary v={data} />}
      {data && <Settings v={data} onSaved={reload} />}
      <Try onDone={reload} />
      {data && <Calls v={data} />}
      {session.operator && <Operator />}
    </>
  );
}

/** The model a provider would use, as the gateway picks it. */
function modelOf(v: IAView, p: AIProvider): string {
  if (p === "local") return v.localModel ?? "";
  if (p === "platform") return v.platformModel ?? "";
  return v.settings.model || (v.settings.kind === "openai" ? "" : "claude-haiku-5-5");
}

/** Why a provider cannot answer now ("" when it can): the gateway then falls back to the local model. */
function blocked(v: IAView, p: AIProvider | ""): string {
  if (p === "own" && !v.keyHint) return t("your key is not set");
  if (p === "platform" && !v.platformAvailable) return t("the platform does not offer it");
  if (p === "platform" && Math.min(v.settings.allowanceMicros, v.settings.capMicros) <= 0) return t("your allowance of the platform's is $0");
  if (p === "local" && !v.localModel) return t("there is no local model");
  return "";
}

/** How things stand now, saved: who answers, failures, and what was spent. */
function Summary({ v }: { v: IAView }) {
  const s = v.settings;
  const why = blocked(v, s.provider);
  const escalate = s.autoDiagnose ? s.escalateTo : "";
  const escWhy = escalate ? blocked(v, escalate) : "";
  const answers: AIProvider = why ? "local" : s.provider;
  const platformLimit = Math.min(s.capMicros, s.allowanceMicros);
  const spent = v.spentOwnMicros + v.spentPlatformMicros;
  // One notice for what is missing, said once even when both choices miss the same thing.
  const missing = [...new Set([why, escWhy].filter(Boolean))];
  return (
    <div className="card ia-summary">
      {missing.length > 0 && (
        <div className="ia-alert small">
          <strong>{t("Something is missing:")}</strong> {t("{why}, so the local model answers for now.", { why: missing.join("; ") })}{" "}
          {missing.some((m) => m === blocked(v, "platform")) && (session.operator
            ? t("Give your organization an allowance below, under the platform's AI.")
            : t("Ask the platform's operators for an allowance."))}
        </div>
      )}
      <div className="ia-fact">
        <span className="small muted">{t("Answers now")}</span>
        <strong>{t(providerName[answers])}</strong>
        <span className="small muted">{modelOf(v, answers)} · {t(answers === "local" ? "free" : answers === "own" ? "paid by you" : "paid by the platform")}</span>
      </div>
      <div className="ia-fact">
        <span className="small muted">{t("Failed runs")}</span>
        <strong>{t(s.autoDiagnose ? "Explained" : "Not explained")}</strong>
        {s.autoDiagnose && (
          <span className="small muted">{escalate && !escWhy
            ? t("New ones with {who}: {n} of {limit} this month", { who: t(providerLabel[escalate]), n: v.escalations, limit: s.escalateLimit })
            : t("All with the usual model")}</span>
        )}
      </div>
      <div className="ia-fact">
        <span className="small muted">{t("Spent this month")}</span>
        <strong>{dollars(spent)}</strong>
        {s.provider === "own" || v.spentOwnMicros > 0
          ? <Meter label={t("Your key")} used={v.spentOwnMicros} total={s.capMicros} fmt={dollars} /> : null}
        {platformLimit > 0 && <Meter label={t("The platform's")} used={v.spentPlatformMicros} total={platformLimit} fmt={dollars} />}
        {!spent && s.provider === "local" && <span className="small muted">{t("everything on the local model, free")}</span>}
      </div>
      {s.updatedBy && <p className="small muted ia-last">{t("Last changed by {who}, {when}.", { who: s.updatedBy, when: timeAgo(s.updatedAt) })}</p>}
    </div>
  );
}

function Meter({ label, used, total, fmt }: { label: string; used: number; total: number; fmt: (n: number) => string }) {
  const pct = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 100;
  const color = pct >= 90 ? "var(--bad)" : pct >= 75 ? "var(--warn)" : "var(--ok)";
  return (
    <div className="ia-meter">
      <div className="row between small"><span>{label}</span><span className="muted">{t("{used} of {total}", { used: fmt(used), total: fmt(total) })}</span></div>
      <div className="meter" role="meter" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} aria-label={label}>
        <div style={{ width: `${pct}%`, background: color }} />
      </div>
    </div>
  );
}

/** One choice of a group, as a card: what it is, what it costs, and whether it can be used. */
function Tile({ name, checked, disabled, onPick, title, cost, status }: {
  name: string; checked: boolean; disabled?: boolean; onPick: () => void; title: string; cost: string; status: ReactNode;
}) {
  return (
    <label className={`ia-tile${checked ? " on" : ""}${disabled ? " off" : ""}`}>
      <input type="radio" name={name} checked={checked} disabled={disabled} onChange={onPick} />
      <strong>{title}</strong>
      <span className="small muted">{cost}</span>
      <span className="small">{status}</span>
    </label>
  );
}

function Settings({ v, onSaved }: { v: IAView; onSaved: () => void }) {
  const admin = session.role === "admin";
  const s = v.settings;
  const [provider, setProvider] = useState<AIProvider>(s.provider);
  const [kind, setKind] = useState(s.kind ?? "anthropic");
  const [baseUrl, setBaseUrl] = useState(s.baseUrl ?? "");
  const [model, setModel] = useState(s.model ?? "");
  const [cap, setCap] = useState(s.capMicros / 1e6);
  const [key, setKey] = useState("");
  const [auto, setAuto] = useState(s.autoDiagnose);
  const [escalateTo, setEscalateTo] = useState(s.escalateTo);
  const [escalateLimit, setEscalateLimit] = useState(s.escalateLimit);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<unknown>();
  const reset = () => {
    setProvider(s.provider); setKind(s.kind ?? "anthropic"); setBaseUrl(s.baseUrl ?? "");
    setModel(s.model ?? ""); setCap(s.capMicros / 1e6); setAuto(s.autoDiagnose);
    setEscalateTo(s.escalateTo); setEscalateLimit(s.escalateLimit); setKey("");
  };
  // Polling brings a new object every time: reset only when the saved settings change.
  useEffect(reset, [JSON.stringify(s)]);
  useEffect(() => {
    if (!saved) return;
    const id = setTimeout(() => setSaved(false), 4000);
    return () => clearTimeout(id);
  }, [saved]);

  // What would change, in words, before saving.
  const onOff = (b: boolean) => t(b ? "On" : "Off");
  const changes: [string, string, string][] = [];
  if (provider !== s.provider) changes.push([t("Who answers"), t(providerName[s.provider]), t(providerName[provider])]);
  if (provider === "own" && kind !== (s.kind ?? "anthropic")) changes.push([t("Provider"), s.kind ?? "anthropic", kind]);
  if (provider === "own" && baseUrl !== (s.baseUrl ?? "")) changes.push([t("Address"), s.baseUrl || "—", baseUrl || "—"]);
  if (provider === "own" && model !== (s.model ?? "")) changes.push([t("Model"), s.model || "—", model || "—"]);
  if (key) changes.push([t("Key"), v.keyHint ?? "—", t("a new one")]);
  if (auto !== s.autoDiagnose) changes.push([t("Explain failed runs"), onOff(s.autoDiagnose), onOff(auto)]);
  if (auto && escalateTo !== s.escalateTo) changes.push([t("New failures"), t(escalateName[s.escalateTo]), t(escalateName[escalateTo])]);
  if (auto && escalateTo && escalateLimit !== s.escalateLimit) changes.push([t("Times a month"), String(s.escalateLimit), String(escalateLimit)]);
  if (Math.round(cap * 1e6) !== s.capMicros) changes.push([t("Monthly cap"), dollars(s.capMicros), dollars(Math.round(cap * 1e6))]);

  const save = (extra: { removeKey?: boolean } = {}) => {
    setBusy(true); setErr(undefined);
    api.saveIA({ provider, kind, baseUrl, model, capDollars: cap, key: key || undefined, autoDiagnose: auto, escalateTo, escalateLimit, ...extra })
      .then(() => { setBusy(false); setKey(""); setSaved(true); onSaved(); }, (e) => { setBusy(false); setErr(e); });
  };
  const allowance = Math.min(s.allowanceMicros, s.capMicros);
  const status = (p: AIProvider) => {
    const why = blocked(v, p);
    if (why) return <span className="badge warn">{why}</span>;
    if (p === "own") return <span className="badge ok">{t("key set ({hint})", { hint: v.keyHint ?? "" })}</span>;
    return <span className="badge ok">{t("ready")}</span>;
  };

  return (
    <div className="card stack ia-settings">
      <section className="stack">
        <h2>{t("1. Who answers")}</h2>
        <p className="small muted">{t("The model that answers every question: the agents, and the explanations of known failures.")}</p>
        <div className="ia-tiles">
          <Tile name="provider" title={t("The local model")} checked={provider === "local"} disabled={!admin} onPick={() => setProvider("local")}
            cost={`${v.localModel ?? "—"} · ${t("free, on the platform's own hardware")}`} status={status("local")} />
          <Tile name="provider" title={t("Your own key")} checked={provider === "own"} disabled={!admin} onPick={() => setProvider("own")}
            cost={t("Anthropic, or any OpenAI-compatible service; you pay your provider")} status={status("own")} />
          <Tile name="provider" title={t("The platform's")} checked={provider === "platform"} disabled={!admin || !v.platformAvailable} onPick={() => setProvider("platform")}
            cost={v.platformAvailable ? `${v.platformModel} · ${t("up to {allowance} a month, the allowance the platform's operators give you", { allowance: dollars(allowance) })}` : t("not offered on this platform")}
            status={status("platform")} />
        </div>
        {provider === "own" && (
          <div className="form-grid">
            <label className="field"><span>{t("Provider")}</span>
              <select disabled={!admin} value={kind} onChange={(e) => setKind(e.target.value as "anthropic" | "openai")}>
                <option value="anthropic">Anthropic</option>
                <option value="openai">{t("OpenAI-compatible")}</option>
              </select>
            </label>
            {kind === "openai" && (
              <label className="field"><span>{t("Address")}</span>
                <input disabled={!admin} value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://api.openai.com/v1" />
              </label>
            )}
            <label className="field"><span>{t("Model")}</span>
              <input disabled={!admin} value={model} onChange={(e) => setModel(e.target.value)} placeholder={kind === "anthropic" ? "claude-haiku-5-5" : "gpt-…"} />
            </label>
            <label className="field"><span>{t("Key")}</span>
              <input disabled={!admin} type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)}
                placeholder={v.keyHint ? t("set ({hint}); write a new one to replace it", { hint: v.keyHint }) : t("paste it here; it is kept as a secret and never shown again")} />
            </label>
          </div>
        )}
      </section>

      <section className="stack">
        <div className="row between">
          <h2>{t("2. Explain failed runs")}</h2>
          <Switch checked={auto} disabled={!admin} onChange={setAuto} label={t("Explain every failed run")} />
        </div>
        <p className="small muted">{t("the diagnostician agent reads the failed steps and their logs, and says why and how to fix it, on the run's page")}</p>
        {auto && (
          <>
            <p className="small" style={{ margin: 0 }}><strong>{t("New failures")}</strong> <span className="muted">{t("(nothing alike in the Ruta yet): who answers them?")}</span></p>
            <div className="ia-tiles">
              <Tile name="escalate" title={t("Do not escalate")} checked={escalateTo === ""} disabled={!admin} onPick={() => setEscalateTo("")}
                cost={t("the usual model answers them too")} status={<span className="badge ok">{t("$0 more")}</span>} />
              <Tile name="escalate" title={t("Your own key")} checked={escalateTo === "own"} disabled={!admin} onPick={() => setEscalateTo("own")}
                cost={t("a stronger model, paid by you")} status={status("own")} />
              <Tile name="escalate" title={t("The platform's")} checked={escalateTo === "platform"} disabled={!admin || !v.platformAvailable} onPick={() => setEscalateTo("platform")}
                cost={v.platformAvailable ? `${v.platformModel} · ${t("within your allowance")}` : t("not offered on this platform")} status={status("platform")} />
            </div>
            {escalateTo && (
              <label className="field ia-narrow"><span>{t("At most, times a month")}</span>
                <input disabled={!admin} type="number" min={0} max={1000} value={escalateLimit} onChange={(e) => setEscalateLimit(Number(e.target.value))} />
              </label>
            )}
            <p className="small muted" style={{ margin: 0 }}>{escalateTo
              ? t("What the stronger model answers is kept in the Ruta as an unconfirmed known failure, so the local model knows it next time.")
              : t("Known failures always go to the usual model, which reads what the Ruta knows.")}</p>
          </>
        )}
      </section>

      <section className="stack">
        <h2>{t("3. Monthly cap")}</h2>
        <p className="small muted">{t("The most this organization spends on paid models in a month; past it, the local model answers.")}</p>
        <label className="field ia-narrow"><span>{t("Dollars")}</span>
          <input disabled={!admin} type="number" min={0} max={1000} step={0.5} value={cap} onChange={(e) => setCap(Number(e.target.value))} />
        </label>
      </section>

      <ErrorBox error={err} />
      {!admin ? <p className="small muted" style={{ margin: 0 }}>{t("Only administrators change this.")}</p> : changes.length ? (
        <div className="ia-savebar">
          <div className="small">
            <strong>{t("Not saved yet:")}</strong>
            <ul>{changes.map(([what, from, to]) => <li key={what}>{what}: <span className="muted">{from}</span> → <strong>{to}</strong></li>)}</ul>
          </div>
          <div className="row" style={{ gap: 8 }}>
            <button className="primary" disabled={busy} onClick={() => save()}>{busy ? t("Saving…") : t("Save")}</button>
            <button disabled={busy} onClick={reset}>{t("Discard")}</button>
          </div>
        </div>
      ) : (
        <div className="row" style={{ gap: 8 }}>
          {saved ? <span className="badge ok">{t("Saved")}</span> : <span className="small muted">{t("No changes.")}</span>}
          {v.keyHint && <button disabled={busy} onClick={() => confirm(t("Remove your key?")) && save({ removeKey: true })}>{t("Remove the key")}</button>}
        </div>
      )}
    </div>
  );
}

function Try({ onDone }: { onDone: () => void }) {
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [answer, setAnswer] = useState<IAAnswer>();
  const [err, setErr] = useState<unknown>();
  if (session.role === "guest") return null;
  return (
    <div className="card stack" style={{ marginTop: 16 }}>
      <h2 style={{ margin: 0 }}>{t("Try it")}</h2>
      <p className="small muted" style={{ margin: 0 }}>{t("Asks whoever answers now (saved settings), and shows who did and what it cost.")}</p>
      <textarea rows={2} value={prompt} maxLength={2000} onChange={(e) => setPrompt(e.target.value)} placeholder={t("e.g. In one sentence, what is a release rollback?")} />
      <div><button className="primary" disabled={busy || !prompt.trim()} onClick={() => {
        setBusy(true); setErr(undefined); setAnswer(undefined);
        api.testIA(prompt.trim()).then((a) => { setBusy(false); setAnswer(a); onDone(); }, (e) => { setBusy(false); setErr(e); });
      }}>{busy ? t("Asking…") : t("Ask")}</button></div>
      <ErrorBox error={err} />
      {answer && (
        <div className="ia-answer">
          <p style={{ whiteSpace: "pre-wrap", margin: 0 }}>{answer.text}</p>
          <p className="small muted" style={{ margin: "6px 0 0" }}>
            <span className={`badge ia-${answer.provider}`}>{t(providerLabel[answer.provider])}</span> {answer.model} · {t("{in} tokens in, {out} out", { in: answer.inputTokens, out: answer.outputTokens })} · {dollars(answer.costMicros)}
          </p>
          {answer.note && <p className="small" style={{ margin: "6px 0 0" }}><span className="badge warn">{answer.note}</span></p>}
        </div>
      )}
    </div>
  );
}

function Calls({ v }: { v: IAView }) {
  if (!v.usage.length) return <p className="muted" style={{ marginTop: 16 }}>{t("No calls yet.")}</p>;
  return (
    <>
      <h2 style={{ marginTop: 24 }}>{t("Latest calls")}</h2>
      <div className="table-wrap">
        <table className="ruta-actions small">
          <thead><tr><th>{t("When")}</th><th>{t("Who asked")}</th><th>{t("Who answered")}</th><th>{t("Tokens")}</th><th>{t("Cost")}</th><th /></tr></thead>
          <tbody>
            {v.usage.map((u) => (
              <tr key={u.id}>
                <td>{timeAgo(u.at)}</td>
                <td>{u.source}</td>
                <td><span className={`badge ia-${u.provider}`}>{t(providerLabel[u.provider])}</span> <span className="muted">{u.model}</span></td>
                <td>{u.inputTokens} / {u.outputTokens}</td>
                <td>{dollars(u.costMicros)}</td>
                <td>{!u.ok ? <span className="badge bad">{u.note || t("failed")}</span> : u.note && <span className="muted">{u.note}</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

/** For the platform's operators: each organization's allowance of the platform's AI, and the bill. */
function Operator() {
  const { data, error, reload } = usePoll(api.iaOrgs, [], 60000);
  return (
    <>
      <h2 style={{ marginTop: 28 }}>{t("The platform's AI (operators)")}</h2>
      <ErrorBox error={error} />
      {data && (
        <>
          {data.platformKeySet
            ? <Meter label={t("The platform's key this month (AI_PLATFORM_CAP)")} used={data.spentMicros} total={data.capMicros} fmt={dollars} />
            : <p className="sub">{t("The platform has no AI key yet (the rendimiento-ia secret, AI_PLATFORM_KEY): organizations can use the local model or their own key.")}</p>}
          <p className="small muted">{t("An organization can use the platform's key only up to the allowance you give it here.")}</p>
          <div className="table-wrap">
            <table className="ruta-actions small">
              <thead><tr><th>{t("Organization")}</th><th>{t("Uses")}</th><th>{t("Spent of the platform's")}</th><th>{t("Allowance a month (dollars)")}</th></tr></thead>
              <tbody>
                {data.orgs.map((o) => <AllowanceRow key={o.org.id} org={o.org.id} name={o.org.name} uses={o.settings.provider}
                  spent={o.spentPlatformMicros} allowance={o.settings.allowanceMicros} onSaved={reload} />)}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}

function AllowanceRow({ org, name, uses, spent, allowance, onSaved }: { org: number; name: string; uses: AIProvider; spent: number; allowance: number; onSaved: () => void }) {
  const [value, setValue] = useState(allowance / 1e6);
  const [state, setState] = useState<"" | "busy" | "saved">("");
  const [err, setErr] = useState<unknown>();
  useEffect(() => setValue(allowance / 1e6), [allowance]);
  const dirty = Math.round(value * 1e6) !== allowance;
  return (
    <tr>
      <td>{name}</td>
      <td>{t(providerLabel[uses])}</td>
      <td>{dollars(spent)}</td>
      <td>
        <div className="row" style={{ gap: 6 }}>
          <input type="number" min={0} max={1000} step={0.5} value={value} style={{ width: 90 }} onChange={(e) => { setValue(Number(e.target.value)); setState(""); }} />
          {dirty && <button className="primary" disabled={state === "busy"} onClick={() => {
            setState("busy"); setErr(undefined);
            api.setIAAllowance(org, value).then(() => { setState("saved"); onSaved(); }, (e) => { setState(""); setErr(e); });
          }}>{t("Save")}</button>}
          {!dirty && state === "saved" && <span className="badge ok">{t("Saved")}</span>}
        </div>
        <ErrorBox error={err} />
      </td>
    </tr>
  );
}
