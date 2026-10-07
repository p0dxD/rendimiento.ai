import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Link, NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api, ApiError } from "./api";
import { t } from "./i18n";
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
import { Ruta } from "./pages/Ruta";
import { Cielo, Guardapolvo, MovimientoSwitch, TemaSwitch, Techo } from "./components/ambiente";
import { LangSwitch, Logo } from "./components/marca";
import { Bienvenida, type BookLinks } from "./pages/Bienvenida";
import "./styles.css";

function Shell() {
  const [state, setState] = useState<{ login?: string; configured?: boolean; book?: BookLinks; activity?: boolean; loading: boolean }>({ loading: true });

  useEffect(() => {
    (async () => {
      const setup = await api.setupStatus().catch(() => ({ configured: false, book: undefined, activity: false }));
      if (!setup.configured) return setState({ configured: false, loading: false });
      try {
        const me = await api.me();
        setState({ login: me.login, configured: true, loading: false });
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) setState({ configured: true, book: setup.book, activity: setup.activity, loading: false });
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

  if (!state.login) return <Bienvenida book={state.book ?? {}} activity={!!state.activity} />;

  return (
    <>
      <Cielo />
      <Techo />
      <header className="topbar">
        <Link to="/" className="brand"><Logo /> <span className="brand-name">rendimiento</span></Link>
        <NavLink to="/" end className="nav-link">{t("Apps")}</NavLink>
        <NavLink to="/services" className="nav-link">{t("Services")}</NavLink>
        <NavLink to="/addons" className="nav-link">{t("Add-ons")}</NavLink>
        <NavLink to="/environment" className="nav-link">{t("Environment")}</NavLink>
        <NavLink to="/ruta" className="nav-link">{t("Ruta")}</NavLink>
        <ProblemsNavLink />
        <span className="spacer" />
        <Link to="/new" className="btn primary">{t("New app")}</Link>
        <span className="muted small hide-sm">{state.login}</span>
        <TemaSwitch />
        <MovimientoSwitch />
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
          <Route path="/ruta" element={<Ruta />} />
          <Route path="/addons/:name" element={<AddonDetail />} />
          <Route path="/services/:ns/:name" element={<Services />} />
          <Route path="/apps/:name" element={<AppPage />} />
          <Route path="/apps/:name/:tab" element={<AppPage />} />
          <Route path="/apps/:name/runs/:id" element={<RunPage />} />
          <Route path="/setup" element={<Navigate to="/" />} />
          <Route path="*" element={<div className="empty">{t("Page not found.")} <Link to="/">{t("Go home")}</Link></div>} />
        </Routes>
      </main>
      <Guardapolvo />
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
