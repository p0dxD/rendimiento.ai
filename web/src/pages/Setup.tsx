import { useState } from "react";
import { t } from "../i18n";

const pasos = [
  "Paste the setup token",
  "Create the GitHub App",
  "Choose its repositories",
];

/**
 * First-run page, in the style of New app: the three steps on the left, and on the right the one at hand, which
 * creates the GitHub App through GitHub's manifest flow.
 */
export function Setup() {
  const [token, setToken] = useState(new URLSearchParams(location.search).get("token") ?? "");
  const actual = token ? 1 : 0;
  return (
    <main className="setup">
      <h1>{t("Set up rendimiento.ai")}</h1>
      <p className="sub" style={{ marginBottom: 0 }}>{t("One-time step: create the GitHub App that lets rendimiento read your repos, run CI on every push and open onboarding PRs.")}</p>
      <div className="lista-detalle">
        <aside className="ld-lista">
          {pasos.map((p, i) => (
            <div key={p} className={`ld-item setup-paso${i === actual ? " on" : ""}${i < actual ? " hecho" : ""}`} aria-current={i === actual ? "step" : undefined}>
              <span className="setup-num" aria-hidden="true">{i < actual ? "✓" : i + 1}</span>
              <span className="ld-nombre">{t(p)}</span>
            </div>
          ))}
        </aside>
        <section className="ld-detalle">
          <div>
            <h3 className="ld-h3">{t("1. The setup token")}</h3>
            <p style={{ margin: "0 0 8px" }}>{t("Paste the setup token. Get it with:")}</p>
            <pre className="file">kubectl get secret rendimiento-setup -n rendimiento-system -o jsonpath='{"{.data.token}"}' | base64 -d</pre>
            <label className="field" style={{ marginTop: 10 }}>
              <span>{t("Setup token")}</span>
              <input value={token} onChange={(e) => setToken(e.target.value)} placeholder={t("paste token")} autoComplete="off" />
            </label>
          </div>
          <div className={token ? undefined : "setup-espera"}>
            <h3 className="ld-h3">{t("2. The GitHub App")}</h3>
            <p style={{ margin: 0 }}>{t("GitHub opens and asks you to create the app. The name and permissions are already filled in.")}</p>
          </div>
          <div className={token ? undefined : "setup-espera"}>
            <h3 className="ld-h3">{t("3. Its repositories")}</h3>
            <p style={{ margin: 0 }}>{t("Then pick which repos the app can see. You can change this later.")}</p>
          </div>
          <div className="setup-pie">
            <a className={`btn primary big ${token ? "" : "disabled"}`} aria-disabled={!token}
              href={token ? `/api/setup/github?token=${encodeURIComponent(token)}` : undefined}>
              {t("Create GitHub App")}
            </a>
          </div>
        </section>
      </div>
    </main>
  );
}
