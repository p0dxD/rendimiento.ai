import { useState } from "react";
import { t } from "../i18n";

/** First-run page: creates the GitHub App through GitHub's manifest flow. */
export function Setup() {
  const [token, setToken] = useState(new URLSearchParams(location.search).get("token") ?? "");
  return (
    <main style={{ maxWidth: 560 }}>
      <h1>{t("Set up rendimiento.ai")}</h1>
      <p className="sub">{t("One-time step: create the GitHub App that lets rendimiento read your repos, run CI on every push and open onboarding PRs.")}</p>
      <div className="card stack">
        <ol className="stack" style={{ paddingLeft: 18, margin: 0 }}>
          <li>{t("Paste the setup token. Get it with:")}
            <pre className="file">kubectl get secret rendimiento-setup -n rendimiento-system -o jsonpath='{"{.data.token}"}' | base64 -d</pre>
          </li>
          <li>{t("GitHub opens and asks you to create the app. The name and permissions are already filled in.")}</li>
          <li>{t("Then pick which repos the app can see. You can change this later.")}</li>
        </ol>
        <label className="field">
          <span>{t("Setup token")}</span>
          <input value={token} onChange={(e) => setToken(e.target.value)} placeholder={t("paste token")} autoComplete="off" />
        </label>
        <a className={`btn primary big ${token ? "" : "disabled"}`} aria-disabled={!token}
          href={token ? `/api/setup/github?token=${encodeURIComponent(token)}` : undefined}>
          {t("Create GitHub App")}
        </a>
      </div>
    </main>
  );
}
