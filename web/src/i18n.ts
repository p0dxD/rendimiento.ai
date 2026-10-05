// Translation: every phrase in the UI is written in English and passed
// through t(); in Spanish, t() looks it up in i18n/es.ts (falling back to
// the English text). The language follows the browser until the user picks
// one with the switch in the top bar; the choice is kept in this browser.
// The API is told the language too (Accept-Language), for server messages.
import { es } from "./i18n/es";

export type Lang = "en" | "es";

const storageKey = "rendimiento.lang";

function initial(): Lang {
  try {
    const saved = localStorage.getItem(storageKey);
    if (saved === "en" || saved === "es") return saved;
  } catch {
    /* storage blocked: follow the browser */
  }
  return navigator.language?.toLowerCase().startsWith("es") ? "es" : "en";
}

export const lang: Lang = initial();

/** The locale for dates and numbers. */
export const locale = lang === "es" ? "es-US" : "en-US";

document.documentElement.lang = lang;

/** Switches the language and reloads, so every page renders in it. */
export function setLang(l: Lang) {
  try {
    localStorage.setItem(storageKey, l);
  } catch {
    /* not remembered; still switch for this load */
  }
  location.reload();
}

/** The phrase in the current language, with {name} placeholders filled in. */
export function t(s: string, vars?: Record<string, string | number>): string {
  let out = lang === "es" ? (es[s] ?? s) : s;
  if (vars) out = out.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
  return out;
}

/** t() for a count: `one` when n is 1, else `other`; {n} is filled in. */
export function tn(n: number, one: string, other: string, vars?: Record<string, string | number>): string {
  return t(n === 1 ? one : other, { n, ...vars });
}
