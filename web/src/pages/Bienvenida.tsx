// The welcome page: what people see before they sign in. It says what the
// word means (rendir: to yield, to make something go further, to give an
// account), how the platform works, and how it works with AI agents
// (la Ruta). Nothing on it describes this installation: it is public.
import { useEffect, useState } from "react";
import { Cielo, Guardapolvo, MovimientoSwitch, TemaSwitch, Techo } from "../components/ambiente";
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

/** "Right now in the town": what rendimiento is doing, pinned up as a bando, refreshed every 20 seconds. */
function Bando() {
  const { data } = usePoll<PublicActivity>(() => api.publicActivity(), [], 20000);
  if (!data) return null;
  const que = (r: PublicActivity["running"][number]) =>
    r.status === "queued" ? t("waiting its turn") : r.deploy ? t("building a new release") : t("checking a proposed change");
  const version = (e: PublicActivity["recent"][number]) =>
    e.automatic ? t("rolled back by itself to release #{n}", { n: e.rollbackOf ?? "" })
      : e.rollbackOf ? t("rolled back to release #{n}", { n: e.rollbackOf })
        : t("release #{n} went live", { n: e.number });
  const lineas = [
    ...data.running.map((r) => ({ app: r.app, que: que(r), cuando: r.since, tone: "var(--warn)", late: true })),
    ...data.recent.map((e) => ({ app: e.app, que: version(e), cuando: e.at, tone: e.rollbackOf ? "var(--bad)" : "var(--ok)", late: false })),
  ].slice(0, 6);
  return (
    <section className="bienv-bando" aria-labelledby="ahora">
      <h2 id="ahora" className="bienv-bando-titulo">{t("Right now in the town")}</h2>
      {lineas.length === 0 && <p className="muted" style={{ margin: "6px 0 0" }}>{t("All quiet: nothing is building.")}</p>}
      {lineas.map((l, i) => (
        <div key={i} className="bienv-bando-linea" style={{ ["--d" as string]: `${i * 90}ms` }}>
          <i className={l.late ? "late" : undefined} style={{ background: l.tone }} aria-hidden="true" />
          <strong>{l.app}</strong>
          <span className="bienv-bando-que">{l.que}</span>
          <span className="bienv-bando-cuando">{timeAgo(l.cuando)}</span>
        </div>
      ))}
    </section>
  );
}

/** The drawing beside each sense of "rendir": a flower that grows, a pot of beans, a book of accounts. */
function Ilustracion({ n }: { n: number }) {
  const label = [t("A flower that grows"), t("A pot of beans"), t("A book of accounts")][n - 1];
  return (
    <svg className="dicc-ilus" width="340" height="340" viewBox="0 0 340 340" role="img" aria-label={label}>
      <circle cx="170" cy="170" r="160" fill="var(--surface-2)" />
      {n === 1 && (
        <g key="1" className="ilus-entra">
          <path d="M170 300 C 166 250, 176 210, 170 170" stroke="#3E7B3A" strokeWidth="9" fill="none" strokeLinecap="round" />
          <path d="M171 245 C 200 225, 225 232, 236 218 C 214 252, 190 254, 171 250 Z" fill="#3E7B3A" />
          <g className="ilus-cabeza">
            <circle cx="170" cy="96" r="30" fill="#E8890C" /><circle cx="208" cy="124" r="30" fill="#F2A93B" /><circle cx="194" cy="166" r="30" fill="#E8890C" />
            <circle cx="146" cy="166" r="30" fill="#F2A93B" /><circle cx="132" cy="124" r="30" fill="#E8890C" /><circle cx="170" cy="134" r="21" fill="#7A3E0B" />
          </g>
        </g>
      )}
      {n === 2 && (
        <g key="2" className="ilus-entra">
          {[0, 0.8, 1.6].map((d, i) => (
            <path key={i} className="ilus-vapor" style={{ animationDelay: `${d}s` }} d={`M${140 + i * 30} ${i === 1 ? 116 : 120} q-10 -14 0 -28 q10 -14 0 -28`} stroke="#C9B79F" strokeWidth="5" fill="none" strokeLinecap="round" />
          ))}
          <path d="M80 150 h180 v20 q0 90 -90 90 q-90 0 -90 -90 z" fill="#B5532A" />
          <rect x="70" y="140" width="200" height="18" rx="9" fill="#8A3C1E" />
          <circle cx="140" cy="148" r="7" fill="#5E3A1E" /><circle cx="170" cy="146" r="7" fill="#5E3A1E" /><circle cx="200" cy="148" r="7" fill="#5E3A1E" />
          <path d="M110 200 h120" stroke="#E9B824" strokeWidth="5" strokeDasharray="10 8" />
        </g>
      )}
      {n === 3 && (
        <g key="3" className="ilus-entra">
          <rect x="80" y="60" width="180" height="230" rx="8" fill="#FFFDF8" stroke="#C9B79F" strokeWidth="3" />
          {[[41, "#2F6B2C"], [40, "#2F6B2C"], [39, "#B3261E"], [38, "#2F6B2C"], [37, "#2F6B2C"]].map(([v, c], i) => (
            <g key={i} className="ilus-renglon" style={{ animationDelay: `${i * 150}ms` }}>
              <text x="100" y={100 + i * 40} fontFamily="Fraunces, Georgia, serif" fontSize="17" fill="#2B1B12">#{v}</text>
              <path d={`M140 ${95 + i * 40} h${60 + (i % 3) * 14}`} stroke="#C9B79F" strokeWidth="4" strokeLinecap="round" />
              <circle cx="236" cy={95 + i * 40} r="8" fill={c as string} />
            </g>
          ))}
        </g>
      )}
    </svg>
  );
}

/**
 * The welcome page, before signing in: "rendir" as a dictionary entry whose senses take turns with a drawing each, how
 * rendimiento works, la Ruta, what else it does on hanging signs, what is happening now on a bando, and the book.
 */
export function Bienvenida({ book, activity }: { book: BookLinks; activity: boolean }) {
  const libro = (lang === "es" ? book.es : book.en) || book.en || book.es;
  // The senses take turns every few seconds, until one is chosen by hand.
  const [sentido, setSentido] = useState(1);
  const [quieto, setQuieto] = useState(false);
  useEffect(() => {
    if (quieto || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const id = setInterval(() => setSentido((n) => (n % 3) + 1), 6000);
    return () => clearInterval(id);
  }, [quieto]);
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
        <section className="bienv-heroe dicc" aria-labelledby="rendir">
          <div className="dicc-texto">
            <div className="dicc-cabeza">
              <h1 id="rendir" lang="es">ren·dir</h1>
              <span className="dicc-fon" lang="es">/ren.ˈdiɾ/</span>
              <span className="dicc-tipo">{t("Spanish verb")}</span>
            </div>
            <p className="bienv-entrada">
              <b>{t("Make your code yield.")}</b> {t("Connect a GitHub repository and press deploy: rendimiento tests it, builds it and gives it a live HTTPS address. Every push after that is a new release, checked, and ready to roll back.")}
            </p>
            <div className="dicc-acepciones" role="group" aria-label={t("The senses of rendir")}>
              {acepciones.map((a, i) => (
                <button key={a.n} className={`acepcion${sentido === a.n ? " on" : ""}`} aria-pressed={sentido === a.n} style={{ ["--d" as string]: `${i * 150}ms` }}
                  onClick={() => { setQuieto(true); setSentido(a.n); }}>
                  <span className="acepcion-num" aria-hidden="true">{a.n}</span>
                  <span>
                    <span className="dicc-def">{a.def}</span>
                    <span className="dicc-ej">{a.ej}</span>
                    <span className="dicc-aqui"><b>{t("Here:")}</b> {a.aqui}</span>
                  </span>
                </button>
              ))}
            </div>
            <p className="dicc-pie">
              <b lang="es">rendimiento</b> · {t("what something yields: its performance. Here, what your code yields.")}
            </p>
            <div className="bienv-acciones">
              <a className="btn primary big" href="/api/auth/login">{t("Sign in with GitHub")}</a>
              {libro && <a className="btn big" href={libro}>{t("Read the book")}</a>}
            </div>
          </div>
          <div className="dicc-dibujo"><Ilustracion n={sentido} /></div>
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
          <div className="bienv-letreros">
            {cosas.map((c, i) => (
              <div key={c.titulo} className="bienv-letrero">
                <span className="cuerdas" aria-hidden="true" />
                <div className="tabla" style={{ borderTopColor: c.color, ["--d" as string]: `${i * -600}ms` }}>
                  <h3>{c.titulo}</h3>
                  <p>{c.texto}</p>
                </div>
              </div>
            ))}
          </div>
        </section>

        {activity && <Bando />}

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
