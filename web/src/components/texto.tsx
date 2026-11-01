// Text that agents write (diagnoses, Ruta entries, handovers, encargos)
// comes in a little Markdown: paragraphs, lists, **bold**, `code` and fenced
// code blocks. TextoConFormato shows it formatted, building React elements
// from the text itself, so nothing in it is ever read as HTML.
import type { ReactNode } from "react";

/** Inline formatting: **bold** and `code`; everything else is plain text. */
function enLinea(text: string, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let last = 0;
  let n = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const t = m[0];
    out.push(t.startsWith("**")
      ? <strong key={`${key}-${n++}`}>{t.slice(2, -2)}</strong>
      : <code key={`${key}-${n++}`}>{t.slice(1, -1)}</code>);
    last = m.index + t.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

type Bloque =
  | { kind: "p"; lines: string[] }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; items: string[] }
  | { kind: "pre"; lines: string[] };

/** Splits the text into paragraphs, lists and code blocks. */
function bloques(text: string): Bloque[] {
  const out: Bloque[] = [];
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  let cur: Bloque | null = null;
  const close = () => { if (cur) out.push(cur); cur = null; };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.trimStart().startsWith("```")) {
      close();
      const code: string[] = [];
      for (i++; i < lines.length && !lines[i].trimStart().startsWith("```"); i++) code.push(lines[i]);
      out.push({ kind: "pre", lines: code });
      continue;
    }
    const ul = /^\s*[-*]\s+(.*)$/.exec(line);
    const ol = /^\s*\d+[.)]\s+(.*)$/.exec(line);
    if (ul || ol) {
      const kind: "ul" | "ol" = ul ? "ul" : "ol";
      const item = (ul ?? ol)![1];
      if (cur && cur.kind === kind) cur.items.push(item);
      else { close(); cur = kind === "ul" ? { kind: "ul", items: [item] } : { kind: "ol", items: [item] }; }
      continue;
    }
    if (line.trim() === "") { close(); continue; }
    if (cur && cur.kind === "p") cur.lines.push(line);
    else if (cur && (cur.kind === "ul" || cur.kind === "ol") && /^\s+\S/.test(line)) cur.items[cur.items.length - 1] += " " + line.trim();
    else { close(); cur = { kind: "p", lines: [line] }; }
  }
  close();
  return out;
}

/** Text written by an agent or a person, with its little Markdown shown as formatting. */
export function TextoConFormato({ text, className }: { text: string; className?: string }) {
  return (
    <div className={`texto${className ? " " + className : ""}`}>
      {bloques(text).map((b, i) => {
        const k = String(i);
        if (b.kind === "pre") return <pre key={k} className="texto-codigo">{b.lines.join("\n")}</pre>;
        if (b.kind === "ul") return <ul key={k}>{b.items.map((it, j) => <li key={j}>{enLinea(it, `${k}-${j}`)}</li>)}</ul>;
        if (b.kind === "ol") return <ol key={k}>{b.items.map((it, j) => <li key={j}>{enLinea(it, `${k}-${j}`)}</li>)}</ol>;
        return <p key={k}>{b.lines.flatMap((l, j) => j ? [<br key={`br${j}`} />, ...enLinea(l, `${k}-${j}`)] : enLinea(l, `${k}-${j}`))}</p>;
      })}
    </div>
  );
}
