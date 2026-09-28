import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Link, NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api, ApiError } from "./api";
import { Dashboard } from "./pages/Dashboard";
import { NewApp } from "./pages/NewApp";
import { AppPage } from "./pages/AppPage";
import { RunPage } from "./pages/RunPage";
import { Setup } from "./pages/Setup";
import { Environment } from "./pages/Environment";
import { Services } from "./pages/Services";
import { Addons } from "./pages/Addons";
import { AddonDetail } from "./pages/Installed";
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

  if (state.loading) return <div className="center muted">Loading…</div>;

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
          <p className="muted">Connect a repo, press deploy, get a live HTTPS URL.</p>
          <a className="btn primary big" href="/api/auth/login">Sign in with GitHub</a>
        </div>
      </div>
    );
  }

  return (
    <>
      <header className="topbar">
        <Link to="/" className="brand"><Logo /> rendimiento.ai</Link>
        <NavLink to="/" end className="nav-link">Apps</NavLink>
        <NavLink to="/services" className="nav-link">Services</NavLink>
        <NavLink to="/addons" className="nav-link">Add-ons</NavLink>
        <NavLink to="/environment" className="nav-link">Environment</NavLink>
        <span className="spacer" />
        <Link to="/new" className="btn primary">New app</Link>
        <span className="muted small hide-sm">{state.login}</span>
        <button onClick={() => api.logout().then(() => location.assign("/"))}>Sign out</button>
      </header>
      <main>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/new" element={<NewApp />} />
          <Route path="/environment" element={<Environment />} />
          <Route path="/services" element={<Services />} />
          <Route path="/addons" element={<Addons />} />
          <Route path="/addons/:name" element={<AddonDetail />} />
          <Route path="/services/:ns/:name" element={<Services />} />
          <Route path="/apps/:name" element={<AppPage />} />
          <Route path="/apps/:name/:tab" element={<AppPage />} />
          <Route path="/apps/:name/runs/:id" element={<RunPage />} />
          <Route path="/setup" element={<Navigate to="/" />} />
          <Route path="*" element={<div className="empty">Page not found. <Link to="/">Go home</Link></div>} />
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
