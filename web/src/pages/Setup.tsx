import { useState } from "react";

/** First-run page: creates the GitHub App through GitHub's manifest flow. */
export function Setup() {
  const [token, setToken] = useState(new URLSearchParams(location.search).get("token") ?? "");
  return (
    <main style={{ maxWidth: 560 }}>
      <h1>Set up rendimiento.ai</h1>
      <p className="sub">One-time step: create the GitHub App that lets rendimiento read your repos, run CI on every push and open onboarding PRs.</p>
      <div className="card stack">
        <ol className="stack" style={{ paddingLeft: 18, margin: 0 }}>
          <li>Paste the setup token. Get it with:
            <pre className="file">kubectl get secret rendimiento-setup -n rendimiento-system -o jsonpath='{"{.data.token}"}' | base64 -d</pre>
          </li>
          <li>GitHub opens and asks you to create the app. The name and permissions are already filled in.</li>
          <li>Then pick which repos the app can see. You can change this later.</li>
        </ol>
        <label className="field">
          <span>Setup token</span>
          <input value={token} onChange={(e) => setToken(e.target.value)} placeholder="paste token" autoComplete="off" />
        </label>
        <a className={`btn primary big ${token ? "" : "disabled"}`} aria-disabled={!token}
          href={token ? `/api/setup/github?token=${encodeURIComponent(token)}` : undefined}>
          Create GitHub App
        </a>
      </div>
    </main>
  );
}
