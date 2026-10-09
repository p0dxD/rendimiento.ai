// El Mercado: web pages for people who don't write code. Each page is a
// puesto (a market stall): filled in a form here, rendered and kept by
// rendimiento in its own repository (internal/mercado), deployed like any app.
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { Flor } from "../components/ambiente";
import { ErrorBox, usePoll } from "../components/ui";
import { api, timeAgo, type Pagina, type PaletaPagina, type TipoEnlace, type TipoPagina } from "../api";
import { t } from "../i18n";
import { marchita } from "./Dashboard";

const tipos: { id: TipoPagina; titulo: () => string; texto: () => string }[] = [
  { id: "personal", titulo: () => t("Personal"), texto: () => t("About you, and where to find you.") },
  { id: "negocio", titulo: () => t("Business"), texto: () => t("What you offer, your hours and how to reach you.") },
  { id: "evento", titulo: () => t("Event"), texto: () => t("When and where, and the story behind it.") },
  { id: "portafolio", titulo: () => t("Portfolio"), texto: () => t("Your work, in photos.") },
];

const tipoColor: Record<TipoPagina, string> = {
  personal: "var(--rosa)", negocio: "var(--cempasuchil)", evento: "var(--anil)", portafolio: "var(--nopal)",
};

const paletas: { id: PaletaPagina; nombre: () => string; colores: [string, string] }[] = [
  { id: "cempasuchil", nombre: () => t("Cempasúchil"), colores: ["#E8890C", "#F2C14E"] },
  { id: "grana", nombre: () => t("Grana"), colores: ["#8E1B2C", "#E9B824"] },
  { id: "anil", nombre: () => t("Añil"), colores: ["#23408E", "#E8890C"] },
  { id: "nopal", nombre: () => t("Nopal"), colores: ["#3E7B3A", "#E9B824"] },
  { id: "rosa", nombre: () => t("Rosa mexicano"), colores: ["#D6246E", "#23408E"] },
];

const enlaceTipos: { id: TipoEnlace; nombre: () => string; ejemplo: string }[] = [
  { id: "instagram", nombre: () => "Instagram", ejemplo: "@usuario" },
  { id: "facebook", nombre: () => "Facebook", ejemplo: "usuario" },
  { id: "tiktok", nombre: () => "TikTok", ejemplo: "@usuario" },
  { id: "whatsapp", nombre: () => "WhatsApp", ejemplo: "+52 55 1234 5678" },
  { id: "telefono", nombre: () => t("Phone"), ejemplo: "+52 55 1234 5678" },
  { id: "correo", nombre: () => t("Email"), ejemplo: "nombre@example.com" },
  { id: "web", nombre: () => t("Website"), ejemplo: "example.com" },
];

/** Details each kind of page suggests, as empty rows to fill. */
function sugeridos(tipo: TipoPagina): { etiqueta: string; texto: string }[] {
  switch (tipo) {
    case "negocio": return [{ etiqueta: t("Hours"), texto: "" }, { etiqueta: t("Address"), texto: "" }];
    case "evento": return [{ etiqueta: t("Date"), texto: "" }, { etiqueta: t("Time"), texto: "" }, { etiqueta: t("Place"), texto: "" }];
    default: return [];
  }
}

const tipoNombre = (id: TipoPagina) => tipos.find((x) => x.id === id)?.titulo() ?? id;

/** The Mercado page: every stall (a page made from a form), with its owner, address and state. */
export function Mercado() {
  const { data, error } = usePoll(api.mercado, [], 15000);
  return (
    <div className="stack mercado">
      <header className="mercado-cabeza">
        <div>
          <h1>{t("The Mercado")}</h1>
          <p className="muted">
            {t("Web pages for people who don't write code. Fill in a form and rendimiento makes the page, publishes it and keeps it. Every page is a stall: here are all of them.")}
          </p>
        </div>
        {data?.enabled && <Link to="/mercado/nuevo" className="btn primary big">{t("Open a stall")}</Link>}
      </header>
      <ErrorBox error={error} />
      {data && !data.enabled && (
        <div className="card stack">
          <strong>{t("The Mercado is not set up yet.")}</strong>
          <p className="muted">
            {t("Its pages live in a GitHub organization of their own: set PAGES_ORG to it, install the GitHub App there for all repositories, and give the App the Administration permission (it creates each page's repository).")}
          </p>
        </div>
      )}
      {data?.enabled && data.puestos.length === 0 && (
        <div className="card empty">
          <p>{t("No stalls yet. Yours can be the first.")}</p>
          <Link to="/mercado/nuevo" className="btn primary big">{t("Open a stall")}</Link>
        </div>
      )}
      <div className="puestos">
        {data?.puestos.map((p) => {
          const caida = marchita(p);
          const url = p.status.services?.find((s) => s.url)?.url;
          return (
            <article key={p.appId} className={`puesto-card${caida ? " caida" : ""}`} style={{ ["--toldo" as string]: tipoColor[p.tipo] ?? "var(--grana)" }}>
              <div className="puesto-toldo" aria-hidden="true" />
              <div className="puesto-cuerpo">
                <Flor estado={caida ? "marchita" : "viva"} size={44} label={caida ? t("Withered flower") : t("Blooming flower")} />
                <h3>{p.nombre}</h3>
                <div className="small muted">{tipoNombre(p.tipo)} · {t("stall of @{owner}", { owner: p.owner })}</div>
                {url ? <a href={url} target="_blank" rel="noreferrer" className="small">{url}</a>
                  : <span className="small muted">{t("Being built…")}</span>}
                <div className="small muted">{t("changed {when}", { when: timeAgo(p.updatedAt) })}</div>
                <div className="row" style={{ justifyContent: "center" }}>
                  <Link to={`/mercado/${p.app}`} className="btn">{t("Change it")}</Link>
                  <Link to={`/apps/${p.app}`} className="btn ghost">{t("Technical details")}</Link>
                </div>
              </div>
            </article>
          );
        })}
      </div>
    </div>
  );
}

/** An address word from a name: "Pastelería Ana" → "pasteleria-ana". */
function palabra(s: string): string {
  let w = s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40).replace(/-+$/, "");
  if (w && !/^[a-z]/.test(w)) w = "p-" + w;
  return w;
}

/** Shrinks a photo in the browser, so pages stay light: at most 1600 px, as JPEG. */
async function achicar(file: File): Promise<string> {
  const img = await createImageBitmap(file);
  const scale = Math.min(1, 1600 / Math.max(img.width, img.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(img.width * scale);
  canvas.height = Math.round(img.height * scale);
  const ctx = canvas.getContext("2d")!;
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
  return canvas.toDataURL("image/jpeg", 0.85);
}

const vacia: Pagina = { tipo: "personal", idioma: "es", nombre: "", paleta: "cempasuchil", detalles: [], enlaces: [{ tipo: "instagram", valor: "" }], galeria: [] };

/** The stall form: opening a new one or changing one, with a live preview of the page. */
export function PuestoEditor() {
  const { app } = useParams();
  const nuevo = !app;
  const nav = useNavigate();
  const [params] = useSearchParams();
  const [pagina, setPagina] = useState<Pagina>(vacia);
  const [cargada, setCargada] = useState(nuevo);
  const [dominio, setDominio] = useState("");
  const [sub, setSub] = useState("");
  const [subTocado, setSubTocado] = useState(false);
  const [zona, setZona] = useState("");
  const [zonas, setZonas] = useState<string[]>([]);
  const [error, setError] = useState<unknown>();
  const [guardando, setGuardando] = useState(false);
  const [guardado, setGuardado] = useState(false);

  useEffect(() => {
    if (nuevo) {
      api.zones().then((z) => { setZonas(z); setZona(z[0] ?? ""); }).catch(setError);
      return;
    }
    api.puesto(app!).then((r) => { setPagina({ ...vacia, ...r.pagina }); setDominio(r.dominio); setCargada(true); }).catch(setError);
  }, [app, nuevo]);

  const direccion = nuevo ? (sub && zona ? `${sub}.${zona}` : "") : dominio;
  const set = (patch: Partial<Pagina>) => { setPagina((p) => ({ ...p, ...patch })); setGuardado(false); };
  const ponerNombre = (nombre: string) => {
    set({ nombre });
    if (nuevo && !subTocado) setSub(palabra(nombre));
  };
  const ponerTipo = (tipo: TipoPagina) => {
    const vacios = (pagina.detalles ?? []).every((d) => !d.texto.trim());
    set({ tipo, ...(vacios ? { detalles: sugeridos(tipo) } : {}) });
  };

  // The preview: the same HTML the page will have, a moment after each change.
  const [html, setHtml] = useState("");
  const [errorVista, setErrorVista] = useState("");
  useEffect(() => {
    if (!cargada) return;
    const timer = setTimeout(() => {
      api.previewPagina(pagina, nuevo ? undefined : dominio)
        .then((r) => { setHtml(r.html); setErrorVista(""); })
        .catch((e) => setErrorVista(e instanceof Error ? e.message : String(e)));
    }, 500);
    return () => clearTimeout(timer);
  }, [pagina, cargada, dominio, nuevo]);

  const fotoSrc = (f: string) => (f.startsWith("data:") ? f : `https://${dominio}/${f}`);

  const publicar = async () => {
    setGuardando(true);
    setError(undefined);
    try {
      if (nuevo) {
        const a = await api.openPuesto(pagina, direccion);
        nav(`/mercado/${a.name}?nuevo=1`);
      } else {
        await api.savePuesto(app!, pagina);
        setGuardado(true);
      }
    } catch (e) {
      setError(e);
    } finally {
      setGuardando(false);
    }
  };

  const detalles = pagina.detalles ?? [];
  const enlaces = pagina.enlaces ?? [];
  const galeria = pagina.galeria ?? [];
  const fotos = (pagina.foto ? 1 : 0) + galeria.length;

  return (
    <div className="stack">
      <div className="row between">
        <h1>{nuevo ? t("Open a stall") : pagina.nombre || t("Your stall")}</h1>
        <Link to="/mercado" className="btn ghost">{t("Back to the Mercado")}</Link>
      </div>
      {params.get("nuevo") && (
        <div className="card aviso-ok">
          <strong>{t("Your stall is open.")}</strong>{" "}
          {t("rendimiento is building it: in a few minutes it will be at {url}. You can keep changing it here; every save publishes it again.", { url: `https://${dominio}` })}
        </div>
      )}
      <ErrorBox error={error} />
      <div className="editor-puesto">
        <div className="stack editor-form">
          <fieldset className="card stack">
            <legend>{t("What kind of page?")}</legend>
            <div className="tipos-pagina">
              {tipos.map((x) => (
                <label key={x.id} className={`tipo-pagina${pagina.tipo === x.id ? " elegido" : ""}`} style={{ ["--toldo" as string]: tipoColor[x.id] }}>
                  <input type="radio" name="tipo" checked={pagina.tipo === x.id} onChange={() => ponerTipo(x.id)} />
                  <strong>{x.titulo()}</strong>
                  <span className="small muted">{x.texto()}</span>
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("Its name")}</legend>
            <label className="campo">{t("Name")}
              <input value={pagina.nombre} maxLength={80} placeholder={t("e.g. Pastelería Ana")} onChange={(e) => ponerNombre(e.target.value)} />
            </label>
            <label className="campo">{t("A line under the name (optional)")}
              <input value={pagina.lema ?? ""} maxLength={160} placeholder={t("e.g. Cakes made by hand, since 1998")} onChange={(e) => set({ lema: e.target.value })} />
            </label>
            {nuevo ? (
              <label className="campo">{t("Its address")}
                <div className="domain-input">
                  <input value={sub} placeholder="pasteleria-ana" onChange={(e) => { setSub(palabra(e.target.value) || e.target.value.toLowerCase()); setSubTocado(true); }} />
                  <span>.</span>
                  <select value={zona} onChange={(e) => setZona(e.target.value)}>
                    {zonas.map((z) => <option key={z} value={z}>{z}</option>)}
                  </select>
                </div>
                <span className="small muted">{direccion ? `https://${direccion}` : t("One word: letters, numbers and hyphens.")}</span>
              </label>
            ) : (
              <div className="small">{t("Address")}: <a href={`https://${dominio}`} target="_blank" rel="noreferrer">https://{dominio}</a></div>
            )}
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("Colors")}</legend>
            <div className="paletas">
              {paletas.map((x) => (
                <label key={x.id} className={`paleta${pagina.paleta === x.id ? " elegido" : ""}`}>
                  <input type="radio" name="paleta" checked={pagina.paleta === x.id} onChange={() => set({ paleta: x.id })} />
                  <span className="paleta-muestra" style={{ background: `linear-gradient(135deg, ${x.colores[0]} 55%, ${x.colores[1]} 55%)` }} aria-hidden="true" />
                  <span className="small">{x.nombre()}</span>
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("Main photo")}</legend>
            <div className="row">
              {pagina.foto && <img className="foto-mini redonda" src={fotoSrc(pagina.foto)} alt="" />}
              <label className="btn">
                {pagina.foto ? t("Change the photo") : t("Choose a photo")}
                <input type="file" accept="image/*" hidden onChange={async (e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (f) set({ foto: await achicar(f) });
                }} />
              </label>
              {pagina.foto && <button className="btn ghost" onClick={() => set({ foto: undefined })}>{t("Remove")}</button>}
            </div>
            <span className="small muted">{t("A photo of you, your logo or your place. It shows round, at the top.")}</span>
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("Tell its story")}</legend>
            <textarea rows={7} maxLength={4000} value={pagina.sobre ?? ""} onChange={(e) => set({ sobre: e.target.value })}
              placeholder={t("Who you are, what you do, what makes it special. Leave a blank line between paragraphs.")} />
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("Details")}</legend>
            {detalles.map((d, i) => (
              <div key={i} className="fila-editor">
                <input value={d.etiqueta} maxLength={40} placeholder={t("e.g. Hours")} onChange={(e) => set({ detalles: detalles.map((x, j) => (j === i ? { ...x, etiqueta: e.target.value } : x)) })} />
                <input value={d.texto} maxLength={300} placeholder={t("e.g. Monday to Saturday, 9 to 6")} onChange={(e) => set({ detalles: detalles.map((x, j) => (j === i ? { ...x, texto: e.target.value } : x)) })} />
                <button className="btn ghost" aria-label={t("Remove")} onClick={() => set({ detalles: detalles.filter((_, j) => j !== i) })}>✕</button>
              </div>
            ))}
            {detalles.length < 8 && <button className="btn" onClick={() => set({ detalles: [...detalles, { etiqueta: "", texto: "" }] })}>{t("Add a detail")}</button>}
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("Where to find you")}</legend>
            {enlaces.map((e, i) => (
              <div key={i} className="fila-editor">
                <select value={e.tipo} onChange={(ev) => set({ enlaces: enlaces.map((x, j) => (j === i ? { ...x, tipo: ev.target.value as TipoEnlace } : x)) })}>
                  {enlaceTipos.map((x) => <option key={x.id} value={x.id}>{x.nombre()}</option>)}
                </select>
                <input value={e.valor} placeholder={enlaceTipos.find((x) => x.id === e.tipo)?.ejemplo}
                  onChange={(ev) => set({ enlaces: enlaces.map((x, j) => (j === i ? { ...x, valor: ev.target.value } : x)) })} />
                <button className="btn ghost" aria-label={t("Remove")} onClick={() => set({ enlaces: enlaces.filter((_, j) => j !== i) })}>✕</button>
              </div>
            ))}
            {enlaces.length < 8 && <button className="btn" onClick={() => set({ enlaces: [...enlaces, { tipo: "whatsapp", valor: "" }] })}>{t("Add a way to find you")}</button>}
          </fieldset>

          <fieldset className="card stack">
            <legend>{pagina.tipo === "portafolio" ? t("Your work") : t("More photos")}</legend>
            <div className="galeria-editor">
              {galeria.map((f, i) => (
                <div key={i} className="foto-editor">
                  <img className="foto-mini" src={fotoSrc(f)} alt="" />
                  <button className="btn ghost small" onClick={() => set({ galeria: galeria.filter((_, j) => j !== i) })}>{t("Remove")}</button>
                </div>
              ))}
            </div>
            {fotos < 9 && (
              <label className="btn">
                {t("Add photos")}
                <input type="file" accept="image/*" multiple hidden onChange={async (e) => {
                  const files = Array.from(e.target.files ?? []).slice(0, 9 - fotos);
                  e.target.value = "";
                  const nuevas = await Promise.all(files.map(achicar));
                  setPagina((p) => ({ ...p, galeria: [...(p.galeria ?? []), ...nuevas] }));
                  setGuardado(false);
                }} />
              </label>
            )}
            <span className="small muted">{t("Up to nine photos in all, the main one included.")}</span>
          </fieldset>

          <fieldset className="card stack">
            <legend>{t("The page's language")}</legend>
            <select value={pagina.idioma} onChange={(e) => set({ idioma: e.target.value as Pagina["idioma"] })}>
              <option value="es">Español</option>
              <option value="en">English</option>
            </select>
            <span className="small muted">{t("The language of the page's own words, like its headings. What you write stays as you wrote it.")}</span>
          </fieldset>

          <div className="row">
            <button className="btn primary big" disabled={guardando || !pagina.nombre.trim() || (nuevo && !direccion)} onClick={publicar}>
              {guardando ? t("Saving…") : nuevo ? t("Open my stall") : t("Save and publish")}
            </button>
            {guardado && <span className="small" style={{ color: "var(--ok)" }}>{t("Saved: the new version will be live in a couple of minutes.")}</span>}
          </div>
        </div>

        <div className="editor-vista">
          <div className="vista-marco">
            <div className="vista-barra" aria-hidden="true"><span /><span /><span /><em>{direccion || "…"}</em></div>
            {html ? <iframe title={t("Preview")} sandbox="" srcDoc={html} /> : <div className="empty">{t("The preview shows here.")}</div>}
          </div>
          {errorVista && <div className="small" style={{ color: "var(--bad)" }}>{errorVista}</div>}
        </div>
      </div>
    </div>
  );
}
