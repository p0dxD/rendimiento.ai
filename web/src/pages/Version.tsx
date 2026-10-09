import { useEffect, useState } from "react";
import { Link, NavLink } from "react-router-dom";
import { envApi, timeAgo, type PlatformRelease } from "../api";
import { usePoll } from "../components/ui";
import { t } from "../i18n";

// The Environment link, marked when a newer rendimiento is ready.
export function EnvironmentNavLink() {
  const { data } = usePoll(() => envApi.version().catch(() => undefined), [], 5 * 60000);
  const ready = data?.status?.available && data.status.latest;
  return (
    <NavLink to="/environment" className="nav-link" title={ready ? t("A new version of rendimiento is ready") : undefined}>
      {t("Environment")}{ready && <span className="nav-count">{t("new")}</span>}
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
    <div className={`card env-hero ${st.available ? "warn" : "ok"}`} style={{ marginTop: 12 }}>
      <strong>{t("rendimiento")}</strong>
      <span className="small">
        {st.current ? t("runs {release}", { release: describe(st.current) }) : t("runs an image built by hand, not from a release")}
      </span>
      {!st.available && <span className="badge ok">{t("Up to date")}</span>}
      {st.available && st.latest && (
        <>
          <span className="small">{t("{release} is ready", { release: describe(st.latest) })}</span>
          {target ? (
            <span className="badge warn">{t("Updating… the page reloads when the new version answers")}</span>
          ) : (
            <button className="primary" onClick={() => update(st.latest!.release)}>{t("Update")}</button>
          )}
        </>
      )}
      <Link className="small" to={`/apps/${st.app}/releases`}>{t("Releases")}</Link>
      {failed && <span className="small" style={{ color: "var(--bad)" }}>{failed}</span>}
    </div>
  );
}
