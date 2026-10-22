import { useEffect, useState } from "react";
import { api, dollars, session, timeAgo, type AIProvider, type IAAnswer, type IAView } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t } from "../i18n";

const providerLabel: Record<AIProvider, string> = { local: "local model", own: "own key", platform: "the platform's" };

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
      {data && <Settings v={data} onSaved={reload} />}
      {data && <Spending v={data} />}
      <Try onDone={reload} />
      {data && <Calls v={data} />}
      {session.operator && <Operator />}
    </>
  );
}

function Settings({ v, onSaved }: { v: IAView; onSaved: () => void }) {
  const admin = session.role === "admin";
  const [provider, setProvider] = useState<AIProvider>(v.settings.provider);
  const [kind, setKind] = useState(v.settings.kind ?? "anthropic");
  const [baseUrl, setBaseUrl] = useState(v.settings.baseUrl ?? "");
  const [model, setModel] = useState(v.settings.model ?? "");
  const [cap, setCap] = useState(v.settings.capMicros / 1e6);
  const [key, setKey] = useState("");
  const [auto, setAuto] = useState(v.settings.autoDiagnose);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  useEffect(() => {
    setProvider(v.settings.provider); setKind(v.settings.kind ?? "anthropic"); setBaseUrl(v.settings.baseUrl ?? "");
    setModel(v.settings.model ?? ""); setCap(v.settings.capMicros / 1e6); setAuto(v.settings.autoDiagnose);
  }, [v.settings]);
  const save = (extra: { removeKey?: boolean } = {}) => {
    setBusy(true); setErr(undefined);
    api.saveIA({ provider, kind, baseUrl, model, capDollars: cap, key: key || undefined, autoDiagnose: auto, ...extra })
      .then(() => { setBusy(false); setKey(""); onSaved(); }, (e) => { setBusy(false); setErr(e); });
  };
  return (
    <div className="card stack">
      <h2 style={{ margin: 0 }}>{t("Who answers")}</h2>
      <label className="row ia-choice"><input type="radio" disabled={!admin} checked={provider === "local"} onChange={() => setProvider("local")} />
        <span><strong>{t("The local model")}</strong> {v.localModel ? <span className="muted">({v.localModel}) · {t("free, on the platform's own hardware")}</span> : <span className="muted">{t("not set up on this platform")}</span>}</span>
      </label>
      <label className="row ia-choice"><input type="radio" disabled={!admin} checked={provider === "own"} onChange={() => setProvider("own")} />
        <span><strong>{t("Your own key")}</strong> <span className="muted">{t("Anthropic, or any OpenAI-compatible service; you pay your provider")}</span></span>
      </label>
      {provider === "own" && (
        <div className="form-grid ia-own">
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
      <label className="row ia-choice"><input type="radio" disabled={!admin || !v.platformAvailable} checked={provider === "platform"} onChange={() => setProvider("platform")} />
        <span><strong>{t("The platform's")}</strong> {v.platformAvailable
          ? <span className="muted">({v.platformModel}) · {t("up to {allowance} a month, the allowance the platform's operators give you", { allowance: dollars(v.settings.allowanceMicros) })}</span>
          : <span className="muted">{t("not offered on this platform")}</span>}</span>
      </label>
      <label className="row ia-choice"><input type="checkbox" disabled={!admin} checked={auto} onChange={(e) => setAuto(e.target.checked)} />
        <span><strong>{t("Explain every failed run")}</strong> <span className="muted">{t("the diagnostician agent reads the failed steps and their logs, and says why and how to fix it, on the run's page")}</span></span>
      </label>
      <label className="field" style={{ maxWidth: 260 }}><span>{t("Monthly cap (dollars)")}</span>
        <input disabled={!admin} type="number" min={0} max={1000} step={0.5} value={cap} onChange={(e) => setCap(Number(e.target.value))} />
      </label>
      <ErrorBox error={err} />
      {admin ? (
        <div className="row" style={{ gap: 8 }}>
          <button className="primary" disabled={busy} onClick={() => save()}>{t("Save")}</button>
          {v.keyHint && <button disabled={busy} onClick={() => confirm(t("Remove your key?")) && save({ removeKey: true })}>{t("Remove the key")}</button>}
        </div>
      ) : <p className="small muted" style={{ margin: 0 }}>{t("Only administrators change this.")}</p>}
    </div>
  );
}

function Spending({ v }: { v: IAView }) {
  const s = v.settings;
  const platformLimit = Math.min(s.capMicros, s.allowanceMicros);
  return (
    <div className="row ia-spend" style={{ gap: 16, marginTop: 16, flexWrap: "wrap" }}>
      <div className="card"><div className="small muted">{t("Your key, this month")}</div><strong>{dollars(v.spentOwnMicros)}</strong> <span className="muted small">{t("of {cap}", { cap: dollars(s.capMicros) })}</span></div>
      <div className="card"><div className="small muted">{t("The platform's, this month")}</div><strong>{dollars(v.spentPlatformMicros)}</strong> <span className="muted small">{t("of {cap}", { cap: dollars(platformLimit) })}</span></div>
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
            {t(providerLabel[answer.provider])} · {answer.model} · {t("{in} tokens in, {out} out", { in: answer.inputTokens, out: answer.outputTokens })} · {dollars(answer.costMicros)}
            {answer.note && <> · <strong>{answer.note}</strong></>}
          </p>
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
      <table className="ruta-actions small">
        <thead><tr><th>{t("When")}</th><th>{t("Who asked")}</th><th>{t("Model")}</th><th>{t("Tokens")}</th><th>{t("Cost")}</th><th /></tr></thead>
        <tbody>
          {v.usage.map((u) => (
            <tr key={u.id} className={u.ok ? "" : "muted"}>
              <td>{timeAgo(u.at)}</td>
              <td>{u.source}</td>
              <td>{u.model} <span className="muted">({t(providerLabel[u.provider])})</span></td>
              <td>{u.inputTokens} / {u.outputTokens}</td>
              <td>{dollars(u.costMicros)}</td>
              <td className="muted">{u.note}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

/** For the platform's operators: each organization's allowance of the platform's AI, and the bill. */
function Operator() {
  const { data, error, reload } = usePoll(api.iaOrgs, [], 60000);
  const [err, setErr] = useState<unknown>();
  return (
    <>
      <h2 style={{ marginTop: 28 }}>{t("The platform's AI (operators)")}</h2>
      <ErrorBox error={error ?? err} />
      {data && (
        <>
          <p className="sub">{data.platformKeySet
            ? t("Spent of the platform's key this month: {spent} of {cap} (AI_PLATFORM_CAP).", { spent: dollars(data.spentMicros), cap: dollars(data.capMicros) })
            : t("The platform has no AI key yet (the rendimiento-ia secret, AI_PLATFORM_KEY): organizations can use the local model or their own key.")}</p>
          <table className="ruta-actions small">
            <thead><tr><th>{t("Organization")}</th><th>{t("Uses")}</th><th>{t("Spent of the platform's")}</th><th>{t("Allowance a month (dollars)")}</th></tr></thead>
            <tbody>
              {data.orgs.map((o) => (
                <tr key={o.org.id}>
                  <td>{o.org.name}</td>
                  <td>{t(providerLabel[o.settings.provider])}</td>
                  <td>{dollars(o.spentPlatformMicros)}</td>
                  <td><input type="number" min={0} max={1000} step={0.5} defaultValue={o.settings.allowanceMicros / 1e6} style={{ width: 90 }}
                    onBlur={(e) => { setErr(undefined); api.setIAAllowance(o.org.id, Number(e.target.value)).then(reload, setErr); }} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  );
}
