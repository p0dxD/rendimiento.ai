// The welcome page: what people see before they sign in. It says what the
// word means (rendir: to yield, to make something go further, to give an
// account), how the platform works, and how it works with AI agents
// (la Ruta). Nothing on it describes this installation: it is public.
import { Cielo, Flor, Guardapolvo, MovimientoSwitch, TemaSwitch, Techo } from "../components/ambiente";
import { LangSwitch, Logo } from "../components/marca";
import { api, timeAgo, type PublicActivity } from "../api";
import { usePoll } from "../components/ui";
import { lang, t } from "../i18n";

export interface BookLinks {
  en?: string;
  es?: string;
}

/** A monarch butterfly, the Ruta's emblem. */
function Monarca({ size = 44 }: { size?: number }) {
  return (
    <svg className="monarca" width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
      <g className="ala izq">
        <path d="M31 30 C24 12 6 8 5 20 C4 30 16 33 31 32 Z" fill="var(--cempasuchil)" stroke="#1B0F0A" strokeWidth="2.4" strokeLinejoin="round" />
        <path d="M31 34 C20 36 10 44 15 52 C20 58 28 48 31 37 Z" fill="var(--cempasuchil-2)" stroke="#1B0F0A" strokeWidth="2.4" strokeLinejoin="round" />
        <path d="M30 30 L12 17 M30 31 L9 26 M30 36 L18 48" stroke="#1B0F0A" strokeWidth="1.6" fill="none" />
      </g>
      <g className="ala der">
        <path d="M33 30 C40 12 58 8 59 20 C60 30 48 33 33 32 Z" fill="var(--cempasuchil)" stroke="#1B0F0A" strokeWidth="2.4" strokeLinejoin="round" />
        <path d="M33 34 C44 36 54 44 49 52 C44 58 36 48 33 37 Z" fill="var(--cempasuchil-2)" stroke="#1B0F0A" strokeWidth="2.4" strokeLinejoin="round" />
        <path d="M34 30 L52 17 M34 31 L55 26 M34 36 L46 48" stroke="#1B0F0A" strokeWidth="1.6" fill="none" />
      </g>
      <rect x="30" y="24" width="4" height="22" rx="2" fill="#1B0F0A" />
      <path d="M31 25 C29 19 27 17 25 16 M33 25 C35 19 37 17 39 16" stroke="#1B0F0A" strokeWidth="1.6" fill="none" strokeLinecap="round" />
    </svg>
  );
}

/** "Right now": what rendimiento is doing, refreshed every 20 seconds. */
function EnEsteMomento() {
  const { data } = usePoll<PublicActivity>(() => api.publicActivity(), [], 20000);
  if (!data) return null;
  const que = (r: PublicActivity["running"][number]) =>
    r.status === "queued" ? t("waiting its turn") : r.deploy ? t("building a new release") : t("checking a proposed change");
  const version = (e: PublicActivity["recent"][number]) =>
    e.automatic ? t("rolled back by itself to release #{n}", { n: e.rollbackOf ?? "" })
      : e.rollbackOf ? t("rolled back to release #{n}", { n: e.rollbackOf })
        : t("release #{n} went live", { n: e.number });
  return (
    <section className="bienv-seccion bienv-ahora" aria-labelledby="ahora">
      <h2 id="ahora">{t("Right now")}</h2>
      <div className="ahora-grid">
        <div className="card">
          <h3>{t("Running")}</h3>
          {data.running.length === 0 ? (
            <p className="muted">{t("All quiet: nothing is building.")}</p>
          ) : (
            <ul className="ahora-lista">
              {data.running.map((r, i) => (
                <li key={i}>
                  <span className={`ahora-punto ${r.status}`} aria-hidden="true" />
                  <b>{r.app}</b> <span className="muted">{que(r)} · {timeAgo(r.since)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="card">
          <h3>{t("Recently")}</h3>
          {data.recent.length === 0 ? (
            <p className="muted">{t("No releases yet.")}</p>
          ) : (
            <ul className="ahora-lista">
              {data.recent.map((e, i) => (
                <li key={i}>
                  <Flor estado={e.rollbackOf ? "marchita" : "viva"} size={18} />
                  <b>{e.app}</b> <span className="muted">{version(e)} · {timeAgo(e.at)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </section>
  );
}

export function Bienvenida({ book, activity }: { book: BookLinks; activity: boolean }) {
  const libro = (lang === "es" ? book.es : book.en) || book.en || book.es;
  const acepciones = [
    { n: 1, def: t("To yield: to give fruit, to produce."), ej: t("“This land yields.”"),
      aqui: t("Every push becomes a tested image and a running app, with a live HTTPS address.") },
    { n: 2, def: t("To make something go further than expected."), ej: t("“Add water to the beans so they go further.”"),
      aqui: t("A few small computers serve many apps: only what changed is rebuilt, and builds share their cache.") },
    { n: 3, def: t("Rendir cuentas: to give an account of what was done."), ej: t("“Every release gives its account.”"),
      aqui: t("Every release is recorded and watched after it goes live; one that breaks what worked is rolled back.") },
  ];
  const pasos = [
    { titulo: t("Connect a repository"), texto: t("rendimiento reads it, works out how to build it and proposes its rendimiento.yaml in a pull request.") },
    { titulo: t("Push code"), texto: t("Tests and builds run on the cluster; GitHub shows the result on every commit and pull request.") },
    { titulo: t("Watch it bloom"), texto: t("Your app goes live. Each app is a cempasúchil: it withers when something fails and revives when it recovers.") },
  ];
  const cosas = [
    { color: "var(--grana)", titulo: t("No Dockerfile needed"), texto: t("A Dockerfile if you have one; Railpack works it out if not.") },
    { color: "var(--anil)", titulo: t("Verified releases"), texto: t("Each release is watched for a while and rolled back by itself if it breaks something.") },
    { color: "var(--nopal)", titulo: t("Databases when you ask"), texto: t("needs: [postgres] or [redis], and rendimiento runs it next to your app.") },
    { color: "var(--rosa)", titulo: t("Visitors and uptime"), texto: t("Visits from Umami and outages from its own checks, on each app's page.") },
    { color: "var(--maiz)", titulo: t("Add-ons from git"), texto: t("The cluster's shared software, installed and kept up to date from a repository.") },
    { color: "var(--teja)", titulo: t("In Spanish and English"), texto: t("Every page, message and email, by day and by night.") },
  ];
  return (
    <div className="bienv">
      <Cielo />
      <Techo />
      <header className="bienv-barra">
        <span className="brand"><Logo /> <span className="brand-name">rendimiento</span></span>
        <span className="spacer" />
        <TemaSwitch />
        <MovimientoSwitch />
        <LangSwitch />
        <a className="btn primary" href="/api/auth/login">{t("Sign in")}</a>
      </header>

      <main className="bienv-main">
        <section className="bienv-heroe">
          <Flor estado="viva" size={88} />
          <p className="bienv-antetitulo">{t("A deployment platform with a Mexican heart")}</p>
          <h1>{t("Make your code yield.")}</h1>
          <p className="bienv-entrada">
            {t("Connect a GitHub repository and press deploy: rendimiento tests it, builds it and gives it a live HTTPS address. Every push after that is a new release, checked, and ready to roll back.")}
          </p>
          <div className="bienv-acciones">
            <a className="btn primary big" href="/api/auth/login">{t("Sign in with GitHub")}</a>
            {libro && <a className="btn big" href={libro}>{t("Read the book")}</a>}
          </div>
        </section>

        {activity && <EnEsteMomento />}

        <section className="bienv-diccionario" aria-labelledby="rendir">
          <div className="dicc-cabeza">
            <h2 id="rendir" lang="es">ren·dir</h2>
            <span className="dicc-fon" lang="es">/ren.ˈdiɾ/</span>
            <span className="dicc-tipo">{t("Spanish verb")}</span>
          </div>
          <ol className="dicc-acepciones">
            {acepciones.map((a) => (
              <li key={a.n}>
                <p className="dicc-def">{a.def}</p>
                <p className="dicc-ej">{a.ej}</p>
                <p className="dicc-aqui"><span className="dicc-flecha" aria-hidden="true">→</span> {a.aqui}</p>
              </li>
            ))}
          </ol>
          <p className="dicc-pie">
            <b lang="es">rendimiento</b> · {t("what something yields: its performance. Here, what your code yields.")}
          </p>
        </section>

        <section className="bienv-seccion">
          <h2>{t("How it works")}</h2>
          <ol className="bienv-pasos">
            {pasos.map((p, i) => (
              <li key={i} className="card">
                <span className="paso-num" aria-hidden="true">{i + 1}</span>
                <h3>{p.titulo}</h3>
                <p className="muted">{p.texto}</p>
              </li>
            ))}
          </ol>
        </section>

        <section className="bienv-seccion bienv-ruta">
          <div className="ruta-vuelo" aria-hidden="true">
            <Monarca size={36} /><Monarca size={48} /><Monarca size={40} />
          </div>
          <h2>{t("Built together with AI agents, remembered in la Ruta")}</h2>
          <p className="bienv-entrada">
            {t("No monarch butterfly makes the whole migration alone: it takes several generations, and still they reach the same forests every autumn. AI agents are like that: a session remembers nothing of the one before. La Ruta is the platform's memory, for people and for agents, so whoever comes next carries on where the last one stopped.")}
          </p>
          <ul className="ruta-lista">
            <li><b>{t("Agents connect over MCP")}</b> {t("with a key you create, read-only or read-write, that expires.")}</li>
            <li><b>{t("Each agent takes a cargo:")}</b> {t("a turn of work with a purpose. It hands it over with what it did and what is left.")}</li>
            <li><b>{t("Decisions, manuals and to-dos")}</b> {t("stay with the platform, not inside one chat.")}</li>
            <li><b>{t("Lo vivido:")}</b> {t("stories only people can write. Agents read them with respect and never change them.")}</li>
            <li><b>{t("rendimiento builds itself:")}</b> {t("its own repository is one more app on it, tested and built on every push.")}</li>
          </ul>
        </section>

        <section className="bienv-seccion">
          <h2>{t("What else it does")}</h2>
          <div className="bienv-cosas">
            {cosas.map((c) => (
              <div key={c.titulo} className="card bienv-cosa" style={{ borderTopColor: c.color }}>
                <h3>{c.titulo}</h3>
                <p className="muted">{c.texto}</p>
              </div>
            ))}
          </div>
        </section>

        <section className="bienv-cierre">
          <h2>{t("Give your code life.")}</h2>
          <div className="bienv-acciones">
            <a className="btn primary big" href="/api/auth/login">{t("Sign in with GitHub")}</a>
            {libro && <a className="btn big" href={libro}>{t("Read the book")}</a>}
          </div>
        </section>
      </main>
      <Guardapolvo />
    </div>
  );
}
