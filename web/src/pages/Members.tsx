import { useState } from "react";
import { api, session, timeAgo, type Member, type MemberRole, type Org } from "../api";
import { ErrorBox, usePoll } from "../components/ui";
import { t } from "../i18n";

export const memberRoleLabel: Record<MemberRole, string> = { guest: "Guest", collaborator: "Collaborator", admin: "Administrator" };

const roleHelp: Record<MemberRole, string> = {
  guest: "sees everything: apps, runs, logs, the Universe and the Ruta",
  collaborator: "also runs CI, writes the Ruta and asks agents for things",
  admin: "also deploys, rolls back, and manages secrets, keys, add-ons and people",
};

/** The roles as choices, with what each allows. */
function RoleSelect({ value, onChange, disabled }: { value: MemberRole; onChange: (r: MemberRole) => void; disabled?: boolean }) {
  return (
    <select value={value} disabled={disabled} onChange={(e) => onChange(e.target.value as MemberRole)}>
      {(["guest", "collaborator", "admin"] as MemberRole[]).map((r) => <option key={r} value={r}>{t(memberRoleLabel[r])}</option>)}
    </select>
  );
}

/**
 * The people in the organization and their roles. An administrator adds
 * someone by their GitHub username (they can sign in from then on), changes
 * a role, or removes them; there is always at least one administrator.
 */
export function Members({ me, role }: { me: string; role: MemberRole }) {
  const { data, error, reload } = usePoll(api.members, [], 0);
  const [login, setLogin] = useState("");
  const [newRole, setNewRole] = useState<MemberRole>("guest");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const admin = role === "admin";
  const act = (p: Promise<unknown>) => { setErr(undefined); p.then(reload, setErr); };
  return (
    <>
      <h1>{t("Members")}</h1>
      <p className="sub">{t("People who can sign in here, and what each may do. You are {role}.", { role: t(memberRoleLabel[role]).toLowerCase() })}</p>
      <ul className="small muted">
        {(["guest", "collaborator", "admin"] as MemberRole[]).map((r) => <li key={r}><strong>{t(memberRoleLabel[r])}</strong>: {t(roleHelp[r])}</li>)}
      </ul>
      {admin && (
        <div className="card stack">
          <div className="form-grid">
            <label className="field"><span>{t("GitHub username")}</span>
              <input value={login} onChange={(e) => setLogin(e.target.value.trim())} placeholder="octocat" />
            </label>
            <label className="field"><span>{t("Role")}</span><RoleSelect value={newRole} onChange={setNewRole} /></label>
          </div>
          <div><button className="primary" disabled={busy || !login} onClick={() => {
            setBusy(true); setErr(undefined);
            api.setMember(login, newRole).then(() => { setBusy(false); setLogin(""); reload(); }, (e) => { setBusy(false); setErr(e); });
          }}>{t("Add")}</button></div>
          <p className="small muted" style={{ margin: 0 }}>{t("They sign in with GitHub at this same address.")}</p>
        </div>
      )}
      <ErrorBox error={err ?? error} />
      <table className="ruta-actions small" style={{ marginTop: 16 }}>
        <thead><tr><th>{t("Person")}</th><th>{t("Role")}</th><th>{t("Added by")}</th><th /></tr></thead>
        <tbody>
          {data?.map((m: Member) => (
            <tr key={m.login}>
              <td>{m.login}{m.login.toLowerCase() === me.toLowerCase() && <span className="muted"> ({t("you")})</span>}</td>
              <td><RoleSelect value={m.role} disabled={!admin} onChange={(r) => act(api.setMember(m.login, r))} /></td>
              <td className="muted">{m.addedBy} · {timeAgo(m.createdAt)}</td>
              <td>{admin && <button className="link small" onClick={() => confirm(t("Remove {login}? They lose access at once.", { login: m.login })) && act(api.removeMember(m.login))}>{t("Remove")}</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <Orgs />
    </>
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
