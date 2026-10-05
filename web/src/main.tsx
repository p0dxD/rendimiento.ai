import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Link, NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api, ApiError } from "./api";
import { lang, setLang, t } from "./i18n";
import { Dashboard } from "./pages/Dashboard";
import { NewApp } from "./pages/NewApp";
import { AppPage } from "./pages/AppPage";
import { RunPage } from "./pages/RunPage";
import { Setup } from "./pages/Setup";
import { Environment } from "./pages/Environment";
import { Services } from "./pages/Services";
import { Addons } from "./pages/Addons";
import { AddonDetail } from "./pages/Installed";
import { Problems, ProblemsNavLink } from "./pages/Problems";
import "./styles.css";

function Logo() {
  return (
    <span className="logo" aria-hidden>
      <svg width="16" height="16" viewBox="0 0 32 32">
        <path d="M5 24 L13 9 L19 18 L27 11" stroke="white" strokeWidth="4" fill="none" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </span>
  );
}

/** EN/ES: shows the other language, in that language. */
function LangSwitch() {
  const other = lang === "es" ? "en" : "es";
  return (
    <button className="lang-switch" onClick={() => setLang(other)} title={other === "es" ? "Cambiar a español" : "Switch to English"}
      aria-label={other === "es" ? "Cambiar a español" : "Switch to English"}>
      {other.toUpperCase()}
    </button>
  );
}

function Shell() {
  const [state, setState] = useState<{ login?: string; configured?: boolean; loading: boolean }>({ loading: true });

  useEffect(() => {
    (async () => {
      const setup = await api.setupStatus().catch(() => ({ configured: false }));
      if (!setup.configured) return setState({ configured: false, loading: false });
      try {
        const me = await api.me();
        setState({ login: me.login, configured: true, loading: false });
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) setState({ configured: true, loading: false });
        else throw e;
      }
    })();
  }, []);

  if (state.loading) return <div className="center muted">{t("Loading…")}</div>;

  if (!state.configured) {
    return (
      <Routes>
        <Route path="*" element={<Setup />} />
      </Routes>
    );
  }

  if (!state.login) {
    return (
      <div className="center">
        <div className="stack">
          <div className="row" style={{ justifyContent: "center" }}>
            <span className="brand" style={{ fontSize: 22 }}><Logo /> rendimiento.ai</span>
          </div>
          <p className="muted">{t("Connect a repo, press deploy, get a live HTTPS URL.")}</p>
          <a className="btn primary big" href="/api/auth/login">{t("Sign in with GitHub")}</a>
          <div className="row" style={{ justifyContent: "center" }}><LangSwitch /></div>
        </div>
      </div>
    );
  }

  return (
    <>
      <header className="topbar">
        <Link to="/" className="brand"><Logo /> rendimiento.ai</Link>
        <NavLink to="/" end className="nav-link">{t("Apps")}</NavLink>
        <NavLink to="/services" className="nav-link">{t("Services")}</NavLink>
        <NavLink to="/addons" className="nav-link">{t("Add-ons")}</NavLink>
        <NavLink to="/environment" className="nav-link">{t("Environment")}</NavLink>
        <ProblemsNavLink />
        <span className="spacer" />
        <Link to="/new" className="btn primary">{t("New app")}</Link>
        <span className="muted small hide-sm">{state.login}</span>
        <LangSwitch />
        <button onClick={() => api.logout().then(() => location.assign("/"))}>{t("Sign out")}</button>
      </header>
      <main>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/new" element={<NewApp />} />
          <Route path="/environment" element={<Environment />} />
          <Route path="/services" element={<Services />} />
          <Route path="/addons" element={<Addons />} />
          <Route path="/problems" element={<Problems />} />
          <Route path="/addons/:name" element={<AddonDetail />} />
          <Route path="/services/:ns/:name" element={<Services />} />
          <Route path="/apps/:name" element={<AppPage />} />
          <Route path="/apps/:name/:tab" element={<AppPage />} />
          <Route path="/apps/:name/runs/:id" element={<RunPage />} />
          <Route path="/setup" element={<Navigate to="/" />} />
          <Route path="*" element={<div className="empty">{t("Page not found.")} <Link to="/">{t("Go home")}</Link></div>} />
        </Routes>
      </main>
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <Shell />
    </BrowserRouter>
  </StrictMode>,
);
