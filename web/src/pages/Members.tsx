import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api, session, timeAgo, type MemberRole, type Org } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t } from "../i18n";

export const memberRoleLabel: Record<MemberRole, string> = { guest: "Guest", collaborator: "Collaborator", admin: "Administrator" };

const roleHelp: Record<MemberRole, string> = {
  guest: "sees everything: apps, runs, logs, the Universe and the Ruta",
  collaborator: "also runs CI, writes the Ruta and asks agents for things",
  admin: "also deploys, rolls back, and manages secrets, keys, add-ons and people",
};

// What each role may do, from the least; a role may do its line and every line above it.
const permisos: { min: MemberRole; text: string }[] = [
  { min: "guest", text: "See apps, runs, logs, the Universe and the Ruta" },
  { min: "collaborator", text: "Build and start runs" },
  { min: "collaborator", text: "Write in the Ruta and ask agents for encargos" },
  { min: "admin", text: "Deploy and roll back to an earlier release" },
  { min: "admin", text: "Manage secrets, keys and add-ons" },
  { min: "admin", text: "Add and remove people" },
];
const nivel: Record<MemberRole, number> = { guest: 0, collaborator: 1, admin: 2 };
const grupoLabel: Record<MemberRole, string> = { admin: "Administrators", collaborator: "Collaborators", guest: "Guests" };
const colores = ["var(--grana)", "var(--anil)", "var(--nopal)", "var(--rosa)", "var(--cempasuchil)", "var(--teja)"];
const colorDe = (login: string) => colores[[...login].reduce((n, c) => n + c.charCodeAt(0), 0) % colores.length];

/** A person's initial in a colored circle. */
function Inicial({ login, size = 34 }: { login: string; size?: number }) {
  return <span className="miembro-inicial" style={{ width: size, height: size, fontSize: size * 0.42, background: colorDe(login) }} aria-hidden="true">{login[0]?.toUpperCase()}</span>;
}

/**
 * The people in the organization, by role, on the left; on the right the one chosen, with their role and what it
 * lets them do. An administrator adds someone by their GitHub username (they can sign in from then on), changes a
 * role, or removes them; there is always at least one administrator.
 */
export function Members({ me, role, org }: { me: string; role: MemberRole; org?: Org }) {
  const { data, error, reload } = usePoll(api.members, [], 0);
  const [params, setParams] = useSearchParams();
  const [err, setErr] = useState<unknown>();
  const admin = role === "admin";
  const act = (p: Promise<unknown>) => { setErr(undefined); p.then(reload, setErr); };
  const agregando = admin && params.has("agregar");
  const elegido = agregando ? undefined : data?.find((m) => m.login === params.get("persona")) ?? data?.find((m) => m.login.toLowerCase() === me.toLowerCase()) ?? data?.[0];
  const yo = (login: string) => login.toLowerCase() === me.toLowerCase();
  return (
    <>
      <h1>{t("Members")}</h1>
      <p className="sub" style={{ marginBottom: 0 }}>{t("People who can sign in here, and what each may do. You are {role}.", { role: t(memberRoleLabel[role]).toLowerCase() })}</p>
      <ErrorBox error={err ?? error} />
      <div className="lista-detalle">
        <aside className="ld-lista">
          {admin && <button className="primary" onClick={() => setParams({ agregar: "1" }, { replace: true })}>{t("Add someone")}</button>}
          {(["admin", "collaborator", "guest"] as MemberRole[]).map((r) => {
            const items = (data ?? []).filter((m) => m.role === r);
            if (items.length === 0) return null;
            return (
              <div key={r}>
                <div className="ld-grupo">{t(grupoLabel[r])} · {items.length}</div>
                {items.map((m) => (
                  <button key={m.login} className={`ld-item${elegido?.login === m.login ? " on" : ""}`} style={{ ["--c" as string]: colorDe(m.login) }}
                    aria-current={elegido?.login === m.login} onClick={() => setParams({ persona: m.login }, { replace: true })}>
                    <Inicial login={m.login} />
                    <span className="ld-nombre">@{m.login}{yo(m.login) && <span className="muted" style={{ fontWeight: 400 }}> ({t("you")})</span>}</span>
                  </button>
                ))}
              </div>
            );
          })}
        </aside>
        {agregando && <AddMember onDone={(login) => { reload(); setParams({ persona: login }, { replace: true }); }} onCancel={() => setParams({}, { replace: true })} />}
        {elegido && (
          <main className="ld-detalle" key={elegido.login}>
            <div className="ld-cabeza">
              <Inicial login={elegido.login} size={60} />
              <div style={{ minWidth: 0 }}>
                <h2 className="ld-titulo">@{elegido.login}{yo(elegido.login) && <span className="muted" style={{ fontSize: 16, fontWeight: 400 }}> ({t("you")})</span>}</h2>
                <span className="small muted">{t("added by {who} · {when}", { who: elegido.addedBy, when: timeAgo(elegido.createdAt) })}</span>
              </div>
            </div>
            <div className="roles-elegir" role="radiogroup" aria-label={t("Role")}>
              {(["guest", "collaborator", "admin"] as MemberRole[]).map((r) => (
                <button key={r} role="radio" aria-checked={elegido.role === r} className={`rol-tarjeta${elegido.role === r ? " on" : ""}`} disabled={!admin}
                  onClick={() => elegido.role !== r && act(api.setMember(elegido.login, r))}>
                  <strong>{t(memberRoleLabel[r])}</strong>
                  <span className="small muted">{t(roleHelp[r])}</span>
                </button>
              ))}
            </div>
            <div>
              <h3 className="ld-h3">{t("What they can do")}</h3>
              <ul className="permisos">
                {permisos.map((p) => {
                  const si = nivel[elegido.role] >= nivel[p.min];
                  return <li key={p.text} className={si ? "si" : "no"}><span role="img" aria-label={si ? t("Allowed") : t("Not allowed")}>{si ? "✓" : "—"}</span>{t(p.text)}</li>;
                })}
              </ul>
            </div>
            {admin && (
              <button style={{ alignSelf: "flex-start" }} onClick={() => confirm(t("Remove {login}? They lose access at once.", { login: elegido.login })) && act(api.removeMember(elegido.login).then(() => setParams({}, { replace: true })))}>
                {t("Remove @{login}", { login: elegido.login })}
              </button>
            )}
          </main>
        )}
      </div>
      {admin && <DemoLinks org={org} />}
      <Orgs />
    </>
  );
}

/**
 * Demo links: a secret address for someone without GitHub here (a reviewer, an investor) to look around this
 * organization, read-only, until the link expires or is turned off. The address is shown once. The platform's own
 * organization has none, since its guests see the cluster: they are made in another one, such as a "Demo".
 */
function DemoLinks({ org }: { org?: Org }) {
  const platformOrg = org?.id === 1;
  const { data, error, reload } = usePoll(() => (platformOrg ? Promise.resolve([]) : api.demoLinks()), [org?.id], 0);
  const [note, setNote] = useState("");
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const [nueva, setNueva] = useState<string>();
  const [copiada, setCopiada] = useState(false);
  return (
    <>
      <h2 style={{ marginTop: 28 }}>{t("Demo links")}</h2>
      <p className="sub">{t("A secret address that lets someone without GitHub here look around this organization, read-only: apps, runs, logs, encargos and the Ruta. They change nothing and never see the cluster or your other organizations.")}</p>
      {platformOrg ? (
        <p className="small muted">{t("The platform's own organization has no demo links, since its guests see the cluster. Create an organization for the demo below, switch to it, and make the link there.")}</p>
      ) : (
        <div className="card stack">
          <ErrorBox error={error ?? err} />
          {nueva && (
            <div className="stack demo-nueva">
              <strong>{t("Here is the link. It is shown only this once:")}</strong>
              <div className="row" style={{ gap: 8 }}>
                <input readOnly value={nueva} onFocus={(e) => e.target.select()} aria-label={t("Demo link")} />
                <button onClick={() => navigator.clipboard?.writeText(nueva).then(() => setCopiada(true), () => setCopiada(false))}>{copiada ? t("Copied") : t("Copy")}</button>
              </div>
            </div>
          )}
          {(data ?? []).length > 0 && (
            <ul className="demo-ligas">
              {data!.map((d) => (
                <li key={d.id}>
                  <div style={{ minWidth: 0 }}>
                    <strong>{d.note || t("Demo link #{n}", { n: d.id })}</strong>
                    <div className="small muted">
                      {t("by {who} · expires {when}", { who: d.createdBy, when: new Date(d.expiresAt).toLocaleDateString() })}
                      {" · "}{d.uses === 0 ? t("not opened yet") : t("opened {n} times, last {when}", { n: d.uses, when: timeAgo(d.lastUsedAt!) })}
                    </div>
                  </div>
                  <button onClick={() => confirm(t("Turn off this demo link? Whoever opened it loses access at once.")) &&
                    api.deleteDemoLink(d.id).then(() => { setNueva(undefined); reload(); }, setErr)}>{t("Turn off")}</button>
                </li>
              ))}
            </ul>
          )}
          <div className="form-grid">
            <label className="field"><span>{t("Who it is for (a note for you)")}</span>
              <input value={note} maxLength={120} onChange={(e) => setNote(e.target.value)} placeholder={t("e.g. Y Combinator reviewers")} />
            </label>
            <label className="field"><span>{t("Lasts")}</span>
              <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
                {[7, 30, 60, 90].map((n) => <option key={n} value={n}>{t("{n} days", { n })}</option>)}
              </select>
            </label>
          </div>
          <div><button className="primary" disabled={busy} onClick={() => {
            setBusy(true); setErr(undefined); setCopiada(false);
            api.createDemoLink(note.trim(), days).then((d) => { setBusy(false); setNote(""); setNueva(d.url); reload(); }, (e) => { setBusy(false); setErr(e); });
          }}>{t("Make a demo link")}</button></div>
        </div>
      )}
    </>
  );
}

/** The form to add someone by their GitHub username, with the role to give them. */
function AddMember({ onDone, onCancel }: { onDone: (login: string) => void; onCancel: () => void }) {
  const [login, setLogin] = useState("");
  const [newRole, setNewRole] = useState<MemberRole>("guest");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  return (
    <main className="ld-detalle">
      <h2 className="ld-titulo">{t("Add someone")}</h2>
      <label className="field"><span>{t("GitHub username")}</span>
        <input value={login} autoFocus onChange={(e) => setLogin(e.target.value.trim())} placeholder="octocat" />
      </label>
      <div className="roles-elegir" role="radiogroup" aria-label={t("Role")}>
        {(["guest", "collaborator", "admin"] as MemberRole[]).map((r) => (
          <button key={r} role="radio" aria-checked={newRole === r} className={`rol-tarjeta${newRole === r ? " on" : ""}`} onClick={() => setNewRole(r)}>
            <strong>{t(memberRoleLabel[r])}</strong>
            <span className="small muted">{t(roleHelp[r])}</span>
          </button>
        ))}
      </div>
      <ErrorBox error={err} />
      <div className="row">
        <button className="primary" disabled={busy || !login} onClick={() => {
          setBusy(true); setErr(undefined);
          api.setMember(login, newRole).then(() => { setBusy(false); onDone(login); }, (e) => { setBusy(false); setErr(e); });
        }}>{t("Add")}</button>
        <button onClick={onCancel}>{t("Cancel")}</button>
      </div>
      <p className="small muted" style={{ margin: 0 }}>{t("They sign in with GitHub at this same address.")}</p>
    </main>
  );
}

/**
 * The organization the person works in, as a choice in the top bar when
 * they belong to more than one; choosing another reloads the page in it.
 */
export function OrgSwitch({ current }: { current?: Org }) {
  const { data } = usePoll(api.orgs, [], 0);
  if (!current || !data || data.length < 2) return null;
  return (
    <select className="org-switch" aria-label={t("Organization")} value={current.id} onChange={(e) => {
      api.switchOrg(Number(e.target.value)).then(() => location.assign("/"), (err: Error) => alert(err.message));
    }}>
      {data.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
    </select>
  );
}

/**
 * The organizations the person belongs to; the platform's operators make
 * new ones (by invitation, for now), and become their first administrator.
 */
export function Orgs() {
  const { data, error, reload } = usePoll(api.orgs, [], 0);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const toSlug = (v: string) => v.toLowerCase().normalize("NFD").replace(/[\u0300-\u036f]/g, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
  return (
    <>
      <h2 style={{ marginTop: 28 }}>{t("Organizations")}</h2>
      <p className="sub">{t("Each organization has its own apps, Ruta, encargos, agent keys and people. Switch between yours at the top.")}</p>
      <ErrorBox error={error ?? err} />
      <ul className="small">
        {data?.map((o) => <li key={o.id}><strong>{o.name}</strong> <span className="muted">({o.slug}) · {t(memberRoleLabel[o.role ?? "guest"])}</span></li>)}
      </ul>
      {session.operator && (
        <div className="card stack">
          <div className="form-grid">
            <label className="field"><span>{t("Name")}</span>
              <input value={name} maxLength={80} onChange={(e) => { setName(e.target.value); setSlug(toSlug(e.target.value)); }} placeholder={t("e.g. Panadería Ana")} />
            </label>
            <label className="field"><span>{t("Address")}</span>
              <input value={slug} maxLength={40} onChange={(e) => setSlug(toSlug(e.target.value))} placeholder="panaderia-ana" />
            </label>
          </div>
          <div><button className="primary" disabled={busy || !name.trim() || slug.length < 2} onClick={() => {
            setBusy(true); setErr(undefined);
            api.createOrg(slug, name.trim()).then(() => { setBusy(false); setName(""); setSlug(""); reload(); }, (e) => { setBusy(false); setErr(e); });
          }}>{t("Create organization")}</button></div>
          <p className="small muted" style={{ margin: 0 }}>{t("You become its first administrator; switch to it at the top and add its people under Members.")}</p>
        </div>
      )}
    </>
  );
}
