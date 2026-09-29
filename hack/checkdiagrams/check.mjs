// Parses every Mermaid diagram in the book with the real Mermaid parser, so
// a syntax error fails here instead of showing up as a broken diagram in the
// browser (mkdocs build --strict does not look inside diagrams).
// Usage: make docs-check
import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
const dom = new JSDOM("<!doctype html><html><body></body></html>");
globalThis.window = dom.window; globalThis.document = dom.window.document;
globalThis.DOMParser = dom.window.DOMParser; globalThis.Element = dom.window.Element;
const { default: mermaid } = await import("mermaid");
mermaid.initialize({ startOnLoad: false });
const root = process.argv[2];
const files = fs.readdirSync(root, { recursive: true }).filter((f) => f.endsWith(".md"));
let bad = 0, total = 0;
for (const f of files) {
  const lines = fs.readFileSync(path.join(root, f), "utf8").split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].trim() !== "```mermaid") continue;
    const start = i + 1; const body = [];
    for (i++; i < lines.length && lines[i].trim() !== "```"; i++) body.push(lines[i]);
    total++;
    try { await mermaid.parse(body.join("\n")); }
    catch (e) { bad++; console.log(`${f}:${start}: ${String(e.message || e).split("\n").slice(0, 3).join(" | ")}`); }
  }
}
console.log(`${total} diagrams, ${bad} with errors`);
process.exit(bad ? 1 : 0);
