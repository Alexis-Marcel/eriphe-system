import { cells } from "../a2ui/markdown";

// PROTOTYPE (dev server only, ?markdown): a node's page as plain Markdown
// with Obsidian's conventions, read into blocks that Eriphe draws with its
// own components (render.tsx). Every block keeps the line it starts on in
// the file (ln, from 0), so that a tick writes back into the very line.

export type Line = { t: string; ln: number };
export type Task = " " | "x" | "/" | "-";
export type Item = { ln: number; task?: Task; text: string; children: Block[] };
export type Block =
  | { k: "h"; level: number; text: string; ln: number }
  | { k: "p"; text: string; ln: number }
  | { k: "list"; ordered: boolean; items: Item[]; ln: number }
  | { k: "table"; head: string[]; rows: string[][]; ln: number }
  | { k: "code"; lang: string; text: string; ln: number }
  | { k: "callout"; type: string; mods: string[]; fold: "" | "-" | "+"; title: string; body: Block[]; ln: number }
  | { k: "quote"; text: string; from?: string; when?: string; ln: number }
  | { k: "embed"; name: string; ln: number }
  | { k: "hr"; ln: number };

export type Ref = { target: string; ln: number };
export type Problem = { ln: number; what: string };
export type Parsed = { front: Record<string, string> | null; written?: { text: string; ln: number }; refs: Map<string, Ref>; blocks: Block[]; problems: Problem[] };

const FENCE = /^\s*(`{3,}|~{3,})\s*([\w+-]*)/;
const ITEM = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;
const DEF = /^ {0,3}\[([^\]^][^\]]*)\]:\s*(\S+)/;
const CALLOUT = /^\[!([\w-]+)((?:\|[^\]|]*)*)\]([-+]?)\s*(.*)$/;
const TASK = /^\[([ xX/-])\]\s+(.*)$/;

// Tabs (Obsidian indents nested lists with tabs by default) count four.
const indent = (s: string) => /^[ \t]*/.exec(s)![0].replace(/\t/g, "    ").length;
const blank = (s: string) => !s.trim();

export function parse(src: string): Parsed {
  const all: Line[] = src.replace(/\r\n?/g, "\n").split("\n").map((t, ln) => ({ t, ln }));
  const problems: Problem[] = [];
  let front: Record<string, string> | null = null;
  let start = 0;
  if (all[0]?.t.trim() === "---") {
    const end = all.findIndex((l, i) => i > 0 && l.t.trim() === "---");
    if (end > 0) {
      front = {};
      for (const l of all.slice(1, end)) {
        const m = /^([\w-]+):\s*(.*)$/.exec(l.t);
        if (m) front[m[1]] = m[2].replace(/^"|"$/g, "");
      }
      start = end + 1;
    }
  }
  // Reference definitions, outside code fences: the target is the first
  // word after the colon (the format's example adds a comment after it).
  const refs = new Map<string, Ref>();
  const body: Line[] = [];
  let fence = "";
  for (const l of all.slice(start)) {
    const f = FENCE.exec(l.t);
    if (fence) {
      if (f && f[1][0] === fence[0] && f[1].length >= fence.length && !l.t.trim().slice(f[1].length)) fence = "";
      body.push(l);
      continue;
    }
    if (f) {
      fence = f[1];
      body.push(l);
      continue;
    }
    const d = DEF.exec(l.t);
    if (d) {
      const label = d[1].trim().toLowerCase();
      if (refs.has(label)) problems.push({ ln: l.ln, what: `étiquette « ${d[1]} » définie deux fois` });
      refs.set(label, { target: d[2], ln: l.ln });
      continue;
    }
    body.push(l);
  }
  // « Écrit le … » on the first line of text: when the page was written.
  let written: Parsed["written"];
  const first = body.findIndex((l) => !blank(l.t));
  if (first >= 0 && /^(Écrit|Ecrit|Mis à jour) le\b/i.test(body[first].t.trim())) {
    written = { text: body[first].t.trim().replace(/\.$/, ""), ln: body[first].ln };
    body.splice(first, 1);
  }
  return { front, written, refs, blocks: blocks(body, problems), problems };
}

// startsBlock: a line that ends a paragraph.
function startsBlock(l: string): boolean {
  const s = l.trim();
  return FENCE.test(l) || /^#{1,6}\s/.test(s) || s.startsWith(">") || ITEM.test(l) || /^!\[\[[^\]]+\]\]$/.test(s) || /^(-{3,}|\*{3,}|_{3,})$/.test(s);
}

function delimiterRow(line: string): boolean {
  const s = line.trim();
  if (!s.includes("-") || !/^[\s|:-]+$/.test(s) || !s.includes("|")) return false;
  return cells(s).every((c) => /^:?-+:?$/.test(c));
}

export function blocks(lines: Line[], problems: Problem[]): Block[] {
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const l = lines[i];
    const s = l.t.trim();
    let m: RegExpExecArray | null;
    if (blank(l.t)) {
      i++;
    } else if ((m = FENCE.exec(l.t))) {
      const f = m[1];
      const code: string[] = [];
      let j = i + 1;
      for (; j < lines.length; j++) {
        const c = lines[j].t.trim();
        if (c.startsWith(f[0].repeat(f.length)) && !c.replace(new RegExp(`^\\${f[0]}+`), "").trim()) break;
        code.push(lines[j].t);
      }
      if (j >= lines.length) problems.push({ ln: l.ln, what: "bloc de code jamais fermé" });
      out.push({ k: "code", lang: m[2] ?? "", text: code.join("\n"), ln: l.ln });
      i = j + 1;
    } else if ((m = /^(#{1,6})\s+(.*)$/.exec(s))) {
      out.push({ k: "h", level: m[1].length, text: m[2].replace(/\s#+$/, ""), ln: l.ln });
      i++;
    } else if ((m = /^!\[\[([^\]]+)\]\]$/.exec(s))) {
      out.push({ k: "embed", name: m[1].split("|")[0].trim(), ln: l.ln });
      i++;
    } else if (/^(-{3,}|\*{3,}|_{3,})$/.test(s)) {
      out.push({ k: "hr", ln: l.ln });
      i++;
    } else if (s.startsWith(">")) {
      const inner: Line[] = [];
      for (; i < lines.length && lines[i].t.trim().startsWith(">"); i++) inner.push({ t: lines[i].t.replace(/^\s*> ?/, ""), ln: lines[i].ln });
      out.push(quoteBlock(inner, problems));
    } else if (ITEM.test(l.t)) {
      i = list(lines, i, out, problems);
    } else if (s.includes("|") && i + 1 < lines.length && delimiterRow(lines[i + 1].t)) {
      const head = cells(s);
      const rows: string[][] = [];
      let j = i + 2;
      for (; j < lines.length && lines[j].t.trim().includes("|") && !startsBlock(lines[j].t); j++) rows.push(cells(lines[j].t.trim()));
      out.push({ k: "table", head, rows: rows.map((r) => head.map((_, c) => r[c] ?? "")), ln: l.ln });
      i = j;
    } else {
      const para: string[] = [s];
      let j = i + 1;
      for (; j < lines.length && !blank(lines[j].t) && !startsBlock(lines[j].t); j++) para.push(lines[j].t.trim());
      out.push({ k: "p", text: para.join("\n"), ln: l.ln });
      i = j;
    }
  }
  return out;
}

// list reads the items that start at i, at that line's indent: an item's
// lines indented further are its detail (its children, read again as
// blocks). It returns where the list ends.
function list(lines: Line[], i: number, out: Block[], problems: Problem[]): number {
  const first = ITEM.exec(lines[i].t)!;
  const base = indent(lines[i].t);
  const ordered = /\d/.test(first[2]);
  const items: Item[] = [];
  const startLn = lines[i].ln;
  while (i < lines.length) {
    const m = ITEM.exec(lines[i].t);
    if (!m || indent(lines[i].t) !== base) break;
    let text = m[3];
    let task: Task | undefined;
    const t = TASK.exec(text);
    if (t) {
      task = (t[1] === "X" ? "x" : t[1]) as Task;
      text = t[2];
    }
    const ln = lines[i].ln;
    const kids: Line[] = [];
    let j = i + 1;
    for (; j < lines.length; j++) {
      const c = lines[j].t;
      if (blank(c)) {
        // A blank line keeps the item open only if an indented line follows.
        const next = lines.slice(j + 1).find((x) => !blank(x.t));
        if (next && indent(next.t) > base) {
          kids.push(lines[j]);
          continue;
        }
        break;
      }
      if (indent(c) <= base) break;
      kids.push(lines[j]);
    }
    const cut = Math.min(...kids.filter((k) => !blank(k.t)).map((k) => indent(k.t)));
    const dedented = kids.map((k) => ({ t: k.t.replace(/\t/g, "    ").slice(Number.isFinite(cut) ? cut : 0), ln: k.ln }));
    items.push({ ln, task, text, children: blocks(dedented, problems) });
    i = j;
    while (i < lines.length && blank(lines[i].t)) {
      const next = lines[i + 1];
      if (next && ITEM.test(next.t) && indent(next.t) === base) i++;
      else break;
    }
  }
  out.push({ k: "list", ordered, items, ln: startLn });
  return i;
}

// quoteBlock: a callout (> [!type|opt|opt]- Title) or a quote, its last
// line « — Who, when » naming who said it.
function quoteBlock(inner: Line[], problems: Problem[]): Block {
  const head = CALLOUT.exec(inner[0].t.trim());
  if (head) {
    const mods = head[2].split("|").map((x) => x.trim().toLowerCase()).filter(Boolean);
    return { k: "callout", type: head[1].toLowerCase(), mods, fold: head[3] as "" | "-" | "+", title: head[4].trim(), body: blocks(inner.slice(1), problems), ln: inner[0].ln };
  }
  const text = inner.map((l) => l.t);
  while (text.length && blank(text[text.length - 1])) text.pop();
  const last = text[text.length - 1] ?? "";
  const by = /^\s*[—–]\s*(.+?)(?:,\s*([^,]+))?\s*$/.exec(last);
  if (by) return { k: "quote", text: text.slice(0, -1).join("\n").trim(), from: by[1], when: by[2], ln: inner[0].ln };
  return { k: "quote", text: text.join("\n").trim(), ln: inner[0].ln };
}

// ---- Inline tokens: what a line holds that Eriphe draws itself ----

export type Tok =
  | { t: "text"; s: string }
  | { t: "ref"; label: string; id: string } // [label][id]
  | { t: "act"; label: string; scheme: "do" | "card" | "ask"; target: string } // [label](do:…)
  | { t: "obj"; label: string; target: string } // [label](mail:…) written inline
  | { t: "due"; date: string }; // 📅 2026-11-27

const SCHEMES = /^(do|card|ask|mail|event|file|pr):/;

// tokens splits a line on its references, actions and dates; the text
// between is ordinary Markdown (markdown.tsx's inline reader). Code spans
// are skipped whole: nothing inside them is read.
export function tokens(s: string): Tok[] {
  const out: Tok[] = [];
  let text = "";
  const push = (t: Tok) => {
    if (text) out.push({ t: "text", s: text });
    text = "";
    out.push(t);
  };
  for (let i = 0; i < s.length; ) {
    const c = s[i];
    if (c === "`") {
      let n = 0;
      while (s[i + n] === "`") n++;
      const close = s.indexOf("`".repeat(n), i + n);
      const end = close < 0 ? i + n : close + n;
      text += s.slice(i, end);
      i = end;
      continue;
    }
    if (c === "[" && s[i + 1] !== "[" && s[i - 1] !== "!" && s[i - 1] !== "[") {
      const m = /^\[([^\]]+)\](?:\[([^\]]+)\]|\(([^)\s]*)\))/.exec(s.slice(i));
      if (m && m[2]) {
        push({ t: "ref", label: m[1], id: m[2].toLowerCase() });
        i += m[0].length;
        continue;
      }
      if (m && m[3] !== undefined && SCHEMES.test(m[3])) {
        const scheme = m[3].split(":")[0];
        const target = m[3].slice(scheme.length + 1);
        if (scheme === "do" || scheme === "card" || scheme === "ask") push({ t: "act", label: m[1], scheme, target });
        else push({ t: "obj", label: m[1], target: m[3] });
        i += m[0].length;
        continue;
      }
    }
    const d = /^📅\s*(\d{4}-\d{2}-\d{2})/u.exec(s.slice(i));
    if (d) {
      push({ t: "due", date: d[1] });
      i += d[0].length;
      continue;
    }
    text += c;
    i++;
  }
  if (text) out.push({ t: "text", s: text });
  return out;
}

// split cuts a line's tokens at its first « : » outside a reference: the
// title, then the comment of whoever keeps the page.
export function split(toks: Tok[]): { title: Tok[]; comment: Tok[] } {
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    if (t.t !== "text") continue;
    const m = /\s:\s|:\s/.exec(t.s);
    // « 18:00 » or a URL's « https: » is no separator: a space must follow.
    if (!m) continue;
    const before = t.s.slice(0, m.index).trimEnd(), after = t.s.slice(m.index + m[0].length);
    if (!before && i === 0) continue;
    const title = [...toks.slice(0, i), ...(before ? [{ t: "text", s: before } as Tok] : [])];
    const comment = [...(after.trim() ? [{ t: "text", s: after } as Tok] : []), ...toks.slice(i + 1)];
    return { title, comment };
  }
  return { title: toks, comment: [] };
}

// onlyActs: a paragraph that is nothing but actions (do:, card:, ask:),
// separated by « · » or spaces.
export function onlyActs(text: string): Extract<Tok, { t: "act" }>[] | null {
  const toks = tokens(text);
  const acts = toks.filter((t): t is Extract<Tok, { t: "act" }> => t.t === "act");
  if (!acts.length) return null;
  return toks.every((t) => t.t === "act" || (t.t === "text" && /^[\s·|,]*$/.test(t.s))) ? acts : null;
}

// setTask writes a task's new state into its line of the file.
export function setTask(src: string, ln: number, to: Task): string {
  const lines = src.split("\n");
  lines[ln] = lines[ln].replace(/^(\s*(?:>\s?)*\s*(?:[-*+]|\d+[.)])\s+)\[[ xX/-]\]/, `$1[${to}]`);
  return lines.join("\n");
}

// Plain words of a line, for the components that take a string.
export function plain(s: string): string {
  return tokens(s)
    .map((t) => (t.t === "text" ? t.s : t.t === "due" ? "" : t.label))
    .join("")
    .replace(/\[\[([^\]|]+)\|?([^\]]*)\]\]/g, (_, a, b) => b || a)
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/[*_`]/g, "")
    .trim();
}

// French numbers as people write them: « 18 385 », « 1,8 », « −2 % ».
export function frNumber(s: string): number | undefined {
  const m = /^\s*([+−-]?)\s*([\d\s\u00a0\u202f]+(?:[,.]\d+)?)\s*(\S*)\s*$/.exec(s);
  if (!m) return undefined;
  const n = Number(m[2].replace(/[\s\u00a0\u202f]/g, "").replace(",", "."));
  if (!Number.isFinite(n)) return undefined;
  return m[1] === "-" || m[1] === "−" ? -n : n;
}
export const unitOf = (s: string) => /^\s*[+−-]?\s*[\d\s\u00a0\u202f]+(?:[,.]\d+)?\s*(.*)$/.exec(s)?.[1]?.trim() ?? "";

const MONTHS = ["janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"];

// dayOf reads a day as a heading or a quote writes it: 2026-11-26,
// « Jeudi 26 novembre » or « 26/11 », in year (the page's).
export function dayOf(s: string, year: number): string | undefined {
  let m = /(\d{4}-\d{2}-\d{2})/.exec(s);
  if (m) return m[1];
  const pad = (n: number) => String(n).padStart(2, "0");
  m = /(\d{1,2})\s+(janvier|février|fevrier|mars|avril|mai|juin|juillet|août|aout|septembre|octobre|novembre|décembre|decembre)(?:\s+(\d{4}))?/i.exec(s);
  if (m) {
    const mo = MONTHS.findIndex((x) => x.normalize("NFD").replace(/\p{M}/gu, "") === m![2].toLowerCase().normalize("NFD").replace(/\p{M}/gu, "")) + 1;
    return `${m[3] ?? year}-${pad(mo)}-${pad(Number(m[1]))}`;
  }
  m = /(\d{1,2})\/(\d{1,2})(?:\/(\d{4}))?/.exec(s);
  if (m) return `${m[3] ?? year}-${pad(Number(m[2]))}-${pad(Number(m[1]))}`;
  return undefined;
}
