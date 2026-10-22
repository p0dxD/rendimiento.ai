import { useState } from "react";
import { api, timeAgo, type Member, type MemberRole } from "../api";
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
    </>
  );
}
