import { useEffect, useState } from "react";
import { Link, NavLink } from "react-router-dom";
import { envApi, timeAgo, type PlatformRelease } from "../api";
import { usePoll } from "../components/ui";
import { t } from "../i18n";

// The Environment link, marked when a newer rendimiento is ready.
export function EnvironmentNavLink() {
  const { data } = usePoll(() => envApi.version().catch(() => undefined), [], 5 * 60000);
  const ready = data?.status?.available && data.status.latest;
  const deploy = !!data?.status?.manifests?.changed?.length;
  return (
    <NavLink to="/environment" className="nav-link" title={ready ? t("A new version of rendimiento is ready") : deploy ? t("make deploy is needed") : undefined}>
      {t("Environment")}{ready ? <span className="nav-count">{t("new")}</span> : deploy && <span className="nav-count">make deploy</span>}
    </NavLink>
  );
}

const describe = (r: PlatformRelease) => t("release #{n} ({sha}, {when})", { n: r.release, sha: r.sha.slice(0, 7), when: timeAgo(r.at) });

// The version of rendimiento that runs, and the button that moves it to the
// newest image its own repository built and tested.
export function PlatformVersion() {
  const { data, error, setData } = usePoll(envApi.version, [], 60000);
  const [target, setTarget] = useState<number>();
  const [failed, setFailed] = useState<string>();

  // While it restarts the API stops answering; once it answers with the new
  // release, reload the page so the new UI loads too.
  useEffect(() => {
    if (!target) return;
    const id = setInterval(() => {
      envApi.version().then((v) => {
        if (v.status?.current?.release === target) location.reload();
        else setData(v);
      }, () => undefined);
    }, 5000);
    return () => clearInterval(id);
  }, [target, setData]);

  const st = data?.status;
  if ((error && !target) || !data?.enabled || !st) return null;
  const manifests = st.manifests;
  const update = async (n: number) => {
    if (!confirm(t("Update rendimiento to release #{n}? It restarts: for about a minute the page and webhooks do not answer, and runs in progress start again.", { n }))) return;
    setFailed(undefined);
    try {
      await envApi.update(n);
      setTarget(n);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className={`bando ${st.available ? "warn" : "ok"}`}>
      <span className="bando-titulo">{t("Bando")}</span>
      <strong>{t("rendimiento")}</strong>
      <span className="small">
        {st.current ? t("runs {release}", { release: describe(st.current) }) : t("runs an image built by hand, not from a release")}
      </span>
      {!st.available && <span className="sello puesto">{t("Up to date")} ✓</span>}
      {st.available && st.latest && (
        <>
          <span className="small">{t("{release} is ready", { release: describe(st.latest) })}</span>
          {target ? (
            <span className="badge warn">{t("Updating… the page reloads when the new version answers")}</span>
          ) : (
            <button className="sello" onClick={() => update(st.latest!.release)}>{t("Update")}</button>
          )}
        </>
      )}
      <Link className="small" to={`/apps/${st.app}/releases`}>{t("Releases")}</Link>
      {failed && <span className="small" style={{ color: "var(--bad)" }}>{failed}</span>}
      {manifests?.changed?.length ? (
        <div className="small" style={{ flexBasis: "100%" }}>
          <p style={{ margin: "4px 0" }}>
            <strong>{t("make deploy is needed")}</strong>{" "}
            {st.available
              ? t("This version also changes the platform's manifests, which the Update button does not apply. After updating, run in your copy of rendimiento:")
              : t("The platform's manifests changed since the last make deploy ({sha}), and the Update button does not apply them. Run in your copy of rendimiento:", { sha: (manifests.applied ?? "").slice(0, 7) })}
          </p>
          <pre className="file">git pull && make deploy</pre>
          <span className="muted">{t("Changed:")} <span className="mono">{manifests.changed.join(", ")}</span></span>
        </div>
      ) : manifests && !manifests.applied ? (
        <span className="small muted" style={{ flexBasis: "100%" }}>{t("rendimiento cannot tell yet whether make deploy is needed: it learns which commit was applied the next time make deploy runs.")}</span>
      ) : null}
    </div>
  );
}
