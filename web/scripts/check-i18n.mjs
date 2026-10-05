// Checks the Spanish catalog (src/i18n/es.ts) against the UI: every phrase
// passed to t()/tn() as a literal must have a translation, with the same
// {placeholders}. Phrases passed through variables (tab labels, statuses)
// are not seen here; they are listed in the catalog by hand.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const src = new URL("../src/", import.meta.url).pathname;
const files = [];
const walk = (d) =>
  readdirSync(d).forEach((f) => {
    const p = join(d, f);
    if (statSync(p).isDirectory()) f !== "i18n" && walk(p);
    else if (/\.tsx?$/.test(f) && f !== "i18n.ts") files.push(p);
  });
walk(src);

const lit = String.raw`"((?:[^"\\]|\\.)*)"`;
const reT = new RegExp(String.raw`\bt\(\s*` + lit, "g");
const reTn = new RegExp(String.raw`\btn\(\s*[^,]+,\s*` + lit + String.raw`\s*,\s*` + lit, "g");
const used = new Map(); // phrase → file
for (const f of files) {
  const s = readFileSync(f, "utf8");
  for (const m of s.matchAll(reT)) used.set(JSON.parse(`"${m[1]}"`), f);
  for (const m of s.matchAll(reTn)) [m[1], m[2]].forEach((k) => used.set(JSON.parse(`"${k}"`), f));
}

const es = new Map();
for (const m of readFileSync(join(src, "i18n/es.ts"), "utf8").matchAll(new RegExp(String.raw`^\s*` + lit + String.raw`:\s*` + lit + ",?$", "gm"))) {
  es.set(JSON.parse(`"${m[1]}"`), JSON.parse(`"${m[2]}"`));
}

const holes = (s) => (s.match(/\{\w+\}/g) ?? []).sort().join(" ");
const errors = [];
for (const [k, f] of used) {
  if (!es.has(k)) errors.push(`missing Spanish for ${JSON.stringify(k)} (${f.slice(src.length)})`);
  else if (holes(k) !== holes(es.get(k))) errors.push(`placeholders differ for ${JSON.stringify(k)}: ${JSON.stringify(es.get(k))}`);
}
if (errors.length) {
  console.error(errors.join("\n"));
  process.exit(1);
}
console.log(`i18n: ${used.size} phrases, all translated`);
