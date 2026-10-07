// The brand mark and the language switch, shared by the top bar and the welcome page.
import { lang, setLang } from "../i18n";

export function Logo() {
  return (
    <span className="logo" aria-hidden>
      <svg width="20" height="20" viewBox="0 0 32 32">
        <path d="M3 17 L10 17 L13 9 L18 25 L21 17 L29 17" stroke="var(--accent-text)" strokeWidth="3.4" fill="none" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </span>
  );
}

/** EN/ES: shows the other language, in that language. */
export function LangSwitch() {
  const other = lang === "es" ? "en" : "es";
  return (
    <button className="lang-switch" onClick={() => setLang(other)} title={other === "es" ? "Cambiar a español" : "Switch to English"}
      aria-label={other === "es" ? "Cambiar a español" : "Switch to English"}>
      {other.toUpperCase()}
    </button>
  );
}
