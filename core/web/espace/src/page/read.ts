import { cells as tableCells } from "../a2ui/markdown";

// A node's body read as a Markdown page, as eriphe-core reads it
// (core/nodefile/md.go, which is authoritative): its blocks, its
// references and their targets, and Eriphe's own links (do:, card:, ask:,
// on:). The page draws what this gives (Body.tsx); the two readers are
// held to the same pages (core/nodefile/testdata/pages: each page and its
// tree as JSON), field for field, so a line here is the line eriphe-core
// changes when the user ticks it. Lines are counted from 1, in the body.
//
// Go's \s and \S are ASCII (space, tab, line ends) where JavaScript's are
// Unicode: a French page holds non-breaking spaces, so the patterns below
// spell them out.

export type Task = " " | "x" | "/" | "-";

export type MdItem = {
  line: number;
  end: number;
  task?: Task;
  text: string;
  due?: string;
  detail?: MdBlock[];
};

export type MdBlock = {
  kind: "heading" | "paragraph" | "list" | "table" | "code" | "callout" | "quote" | "embed" | "rule";
  line: number;
  end: number;
  level?: number;
  text?: string;
  ordered?: boolean;
  items?: MdItem[];
  head?: string[];
  rows?: string[][];
  lang?: string;
  type?: string;
  opts?: string[];
  fold?: "-" | "+";
  title?: string;
  body?: MdBlock[];
  from?: string;
  when?: string;
  name?: string;
};

export type RefDef = { label: string; target: string; scheme?: string; id?: string; line: number };
export type RefUse = { label: string; text: string; line: number; item?: number; task?: string };
export type LinkKind = "do" | "card" | "ask" | "on";
export type PageLink = { kind: LinkKind; label: string; target: string; line: number; alone?: boolean; item?: number; callout?: number; heading?: number };

export type MdPage = {
  written?: { text: string; line: number };
  blocks: MdBlock[];
  refs: RefDef[];
  uses: RefUse[];
  links: PageLink[];
};

type Info = { code: boolean; def: boolean; item: number; task: string; callout: number; heading: number };
type Line = { t: string; ln: number };

const S = "[\\t\\n\\f\\r ]"; // Go's \s
const NS = "[^\\t\\n\\f\\r ]"; // Go's \S
const FENCE = new RegExp(`^${S}*(\`{3,}|~{3,})${S}*(${NS}*)`);
const ITEM = new RegExp(`^(${S}*)([-*+]|\\d+[.)])${S}+(.*)$`);
const DEF = new RegExp(`^ {0,3}\\[([^\\]^][^\\]]*)\\]:${S}*(${NS}+)`);
const CALLOUT = new RegExp(`^\\[!([\\w-]+)((?:\\|[^\\]|]*)*)\\]([-+]?)${S}*(.*)$`);
const TASK = new RegExp(`^\\[([ xX/-])\\](?:${S}+(.*))?$`);
const HEAD = new RegExp(`^(#{1,6})${S}+(.*)$`);
const EMBED = /^!\[\[([^\]]+)\]\]$/;
const RULE = /^(-{3,}|\*{3,}|_{3,})$/;
const WRITTEN = /^(Écrit le|Ecrit le|Mis à jour le|Written on|Updated on)\b/i;
const DUE = new RegExp(`📅${S}*(\\d{4}-\\d{2}-\\d{2})`);
const QUOTE_BY = new RegExp(`^${S}*[—–]${S}*(.+?)(?:,${S}*([^,]+))?${S}*$`);
const SCHEME = /^[A-Za-z][A-Za-z0-9+.-]*$/;
const LINK = new RegExp(`^\\[([^\\]]+)\\](?:\\[([^\\]]*)\\]|\\(([^)\\t\\n\\f\\r ]*)\\))`);
const DELIM = /^[\t\n\f\r |:-]+$/;
const ALIGN = /^:?-+:?$/;

export const blank = (s: string) => s.trim() === "";

// A label as references match it: without case, its spaces one each.
export const refLabel = (s: string) => s.toLowerCase().split(/\s+/).filter(Boolean).join(" ");

// indent is a line's indent: a tab counts four.
function indent(s: string): number {
  let n = 0;
  for (const c of s) {
    if (c === " ") n++;
    else if (c === "\t") n += 4;
    else return n;
  }
  return n;
}

// dedent takes n columns of indent off s, a tab counting four.
function dedent(s: string, n: number): string {
  let col = 0;
  for (let i = 0; i < s.length; i++) {
    if (col >= n) return s.slice(i);
    const c = s[i];
    if (c === " ") col++;
    else if (c === "\t") {
      col += 4;
      if (col > n) return " ".repeat(col - n) + s.slice(i + 1);
    } else return s.slice(i);
  }
  return "";
}

// closes tells whether t closes a fence opened by f.
function closes(t: string, f: string): boolean {
  const s = t.trim();
  if (!s.startsWith(f[0].repeat(f.length))) return false;
  return s.split(f[0]).join("") === "";
}

function startsBlock(t: string): boolean {
  const s = t.trim();
  return FENCE.test(t) || HEAD.test(s) || s.startsWith(">") || ITEM.test(t) || EMBED.test(s) || RULE.test(s);
}

function tableDelim(t: string): boolean {
  const s = t.trim();
  if (!s.includes("-") || !s.includes("|") || !DELIM.test(s)) return false;
  return tableCells(s).every((c) => ALIGN.test(c.trim()));
}

function trimClosingHashes(raw: string): string {
  const s = raw.trim();
  const t = s.replace(/#+$/, "");
  if (t !== s && (t === "" || t.endsWith(" "))) return t.trim();
  return s;
}

function quoteStrip(t: string): string {
  let s = t.replace(/^[ \t]+/, "");
  if (s.startsWith(">")) s = s.slice(1);
  return s.startsWith(" ") ? s.slice(1) : s;
}

// block builds a block with only the fields Go writes (its omitempty).
function block(kind: MdBlock["kind"], line: number, end: number, f: Partial<MdBlock> = {}): MdBlock {
  const b: MdBlock = { kind, line, end };
  for (const [k, v] of Object.entries(f)) {
    if (v === undefined || v === "" || v === 0 || v === false || (Array.isArray(v) && v.length === 0)) continue;
    (b as Record<string, unknown>)[k] = v;
  }
  return b;
}

class Reader {
  info: Info[];
  constructor(n: number) {
    this.info = Array.from({ length: n }, () => ({ code: false, def: false, item: 0, task: "", callout: 0, heading: 0 }));
  }

  blocks(ls: Line[], callout: number): MdBlock[] {
    const out: MdBlock[] = [];
    for (let i = 0; i < ls.length; ) {
      const l = ls[i];
      const s = l.t.trim();
      let m: RegExpExecArray | null;
      if (blank(l.t)) {
        i++;
      } else if ((m = FENCE.exec(l.t))) {
        const code: string[] = [];
        let j = i + 1;
        for (; j < ls.length && !closes(ls[j].t, m[1]); j++) code.push(ls[j].t);
        let end = l.ln;
        if (j < ls.length) end = ls[j].ln;
        else if (ls.length > i + 1) end = ls[ls.length - 1].ln;
        for (let ln = l.ln; ln <= end; ln++) this.info[ln - 1].code = true;
        out.push(block("code", l.ln, end, { lang: m[2], text: code.join("\n") }));
        i = j + 1;
      } else if ((m = HEAD.exec(s))) {
        out.push(block("heading", l.ln, l.ln, { level: m[1].length, text: trimClosingHashes(m[2]) }));
        i++;
      } else if ((m = EMBED.exec(s))) {
        out.push(block("embed", l.ln, l.ln, { name: m[1].split("|")[0].trim() }));
        i++;
      } else if (RULE.test(s)) {
        out.push(block("rule", l.ln, l.ln));
        i++;
      } else if (s.startsWith(">")) {
        const inner: Line[] = [];
        for (; i < ls.length && ls[i].t.trim().startsWith(">"); i++) inner.push({ t: quoteStrip(ls[i].t), ln: ls[i].ln });
        out.push(this.quote(inner));
      } else if (ITEM.test(l.t)) {
        const [b, next] = this.list(ls, i, callout);
        out.push(b);
        i = next;
      } else if (s.includes("|") && i + 1 < ls.length && tableDelim(ls[i + 1].t)) {
        const head = tableCells(s).map((c) => c.trim());
        const rows: string[][] = [];
        let j = i + 2;
        for (; j < ls.length && ls[j].t.includes("|") && !blank(ls[j].t) && !startsBlock(ls[j].t); j++) {
          const row = tableCells(ls[j].t).map((c) => c.trim());
          rows.push(head.map((_, c) => row[c] ?? ""));
        }
        out.push(block("table", l.ln, ls[j - 1].ln, { head, rows }));
        i = j;
      } else {
        const para = [s];
        let j = i + 1;
        for (; j < ls.length && !blank(ls[j].t) && !startsBlock(ls[j].t); j++) para.push(ls[j].t.trim());
        out.push(block("paragraph", l.ln, ls[j - 1].ln, { text: para.join("\n") }));
        i = j;
      }
    }
    return out;
  }

  quote(inner: Line[]): MdBlock {
    const first = inner[0].ln, last = inner[inner.length - 1].ln;
    const m = CALLOUT.exec(inner[0].t.trim());
    if (m) {
      const opts = m[2].split("|").map((o) => o.trim().toLowerCase()).filter(Boolean);
      for (let ln = first; ln <= last; ln++) this.info[ln - 1].callout = first;
      return block("callout", first, last, {
        type: m[1].toLowerCase(),
        opts,
        fold: (m[3] || undefined) as MdBlock["fold"],
        title: m[4].trim(),
        body: this.blocks(inner.slice(1), first),
      });
    }
    const text = inner.map((l) => l.t);
    while (text.length && blank(text[text.length - 1])) text.pop();
    if (text.length) {
      const by = QUOTE_BY.exec(text[text.length - 1]);
      if (by) return block("quote", first, last, { text: text.slice(0, -1).join("\n").trim(), from: by[1], when: (by[2] ?? "").trim() });
    }
    return block("quote", first, last, { text: text.join("\n").trim() });
  }

  list(ls: Line[], i: number, callout: number): [MdBlock, number] {
    const first = ITEM.exec(ls[i].t)!;
    const base = indent(ls[i].t);
    const start = ls[i].ln;
    const ordered = first[2][0] >= "0" && first[2][0] <= "9";
    const items: MdItem[] = [];
    let end = start;
    while (i < ls.length) {
      const m = ITEM.exec(ls[i].t);
      if (!m || indent(ls[i].t) !== base) break;
      const it: MdItem = { line: ls[i].ln, end: ls[i].ln, text: m[3] };
      const t = TASK.exec(m[3]);
      if (t) {
        it.task = t[1].toLowerCase() as Task;
        it.text = t[2] ?? "";
      }
      const d = DUE.exec(it.text);
      if (d) it.due = d[1];
      const kids: Line[] = [];
      let j = i + 1;
      for (; j < ls.length; j++) {
        const c = ls[j].t;
        if (blank(c)) {
          // A blank line keeps the item open only if an indented line follows.
          let k = j + 1;
          while (k < ls.length && blank(ls[k].t)) k++;
          if (k < ls.length && indent(ls[k].t) > base) {
            kids.push(ls[j]);
            continue;
          }
          break;
        }
        if (indent(c) <= base) break;
        kids.push(ls[j]);
      }
      if (kids.length) it.end = kids[kids.length - 1].ln;
      for (let ln = it.line; ln <= it.end; ln++) {
        this.info[ln - 1].item = it.line;
        this.info[ln - 1].task = it.task ?? "";
      }
      let cut = -1;
      for (const k of kids) if (!blank(k.t) && (cut < 0 || indent(k.t) < cut)) cut = indent(k.t);
      const detail = this.blocks(
        kids.map((k) => ({ t: dedent(k.t, Math.max(cut, 0)), ln: k.ln })),
        callout,
      );
      if (detail.length) it.detail = detail;
      items.push(it);
      end = it.end;
      i = j;
      let k = i;
      while (k < ls.length && blank(ls[k].t)) k++;
      if (k > i && k < ls.length && ITEM.test(ls[k].t) && indent(ls[k].t) === base) i = k;
    }
    return [block("list", start, end, { ordered, items }), i];
  }
}

// A reference or one of Eriphe's links in a line: start and end its
// characters.
export type Token = { start: number; end: number; text: string; ref?: boolean; label?: string; kind?: LinkKind; target?: string };

const isLinkKind = (k: string): k is LinkKind => k === "do" || k === "card" || k === "ask" || k === "on";

// tokens are a line's references and Eriphe's links, in order. A code span
// is read whole: nothing in it is a link; nor is an image, a [[wikilink]]
// or an escaped bracket.
export function tokens(s: string): Token[] {
  const out: Token[] = [];
  for (let i = 0; i < s.length; ) {
    const c = s[i];
    if (c === "`") {
      let n = 0;
      while (s[i + n] === "`") n++;
      const close = s.indexOf("`".repeat(n), i + n);
      i = close >= 0 ? close + n : i + n;
      continue;
    }
    if (c === "\\") {
      i += 2;
      continue;
    }
    if (c === "[" && s[i + 1] !== "[" && (i === 0 || (s[i - 1] !== "!" && s[i - 1] !== "["))) {
      const m = LINK.exec(s.slice(i));
      if (m) {
        const t: Token = { start: i, end: i + m[0].length, text: m[1] };
        if (m[2] !== undefined) {
          t.ref = true;
          t.label = refLabel(m[2].trim() === "" ? m[1] : m[2]);
        } else {
          const dest = m[3] ?? "";
          const at = dest.indexOf(":");
          const kind = at < 0 ? "" : dest.slice(0, at);
          if (!isLinkKind(kind)) {
            i += m[0].length;
            continue;
          }
          t.kind = kind;
          t.target = dest.slice(at + 1);
        }
        out.push(t);
        i += m[0].length;
        continue;
      }
    }
    i++;
  }
  return out;
}

// alone tells whether a line holds only links of Eriphe's, separated by
// spaces, « · », | or commas (a callout's > before them).
export function alone(s: string, toks: Token[]): boolean {
  if (!toks.length) return false;
  const gap = (g: string) => /^[ \t·|,>]*$/.test(g);
  let at = 0;
  for (const t of toks) {
    if (!t.kind || !gap(s.slice(at, t.start))) return false;
    at = t.end;
  }
  return gap(s.slice(at));
}

// readPage reads a body as a page. It never fails: what it cannot read is
// text.
export function readPage(body: string): MdPage {
  const raw = body.replace(/\r\n/g, "\n").split("\n");
  if (raw.length && raw[raw.length - 1] === "") raw.pop();
  const r = new Reader(raw.length);
  const page: MdPage = { blocks: [], refs: [], uses: [], links: [] };

  // Reference targets, outside code fences, are not blocks.
  let rest: Line[] = [];
  let fence = "";
  raw.forEach((t, i) => {
    const ln = i + 1;
    if (fence) {
      r.info[i].code = true;
      if (closes(t, fence)) fence = "";
      rest.push({ t, ln });
      return;
    }
    const f = FENCE.exec(t);
    if (f) {
      fence = f[1];
      r.info[i].code = true;
      rest.push({ t, ln });
      return;
    }
    const d = DEF.exec(t);
    if (d) {
      const def: RefDef = { label: refLabel(d[1]), target: d[2], line: ln };
      const at = def.target.indexOf(":");
      if (at >= 0 && SCHEME.test(def.target.slice(0, at))) {
        def.scheme = def.target.slice(0, at);
        if (at + 1 < def.target.length) def.id = def.target.slice(at + 1);
      }
      page.refs.push(def);
      r.info[i].def = true;
      return;
    }
    rest.push({ t, ln });
  });
  // « Écrit le … » on the first line of text.
  const k = rest.findIndex((l) => !blank(l.t));
  if (k >= 0) {
    const s = rest[k].t.trim();
    if (WRITTEN.test(s) && !r.info[rest[k].ln - 1].code) {
      page.written = { text: s.replace(/\.$/, ""), line: rest[k].ln };
      rest = [...rest.slice(0, k), ...rest.slice(k + 1)];
    }
  }
  page.blocks = r.blocks(rest, 0);
  // Each line's section: the last heading of the page's above it.
  const heads = new Set(page.blocks.filter((b) => b.kind === "heading").map((b) => b.line));
  let cur = 0;
  r.info.forEach((inf, i) => {
    if (heads.has(i + 1)) cur = i + 1;
    inf.heading = cur;
  });
  // Each line's references and links, outside code.
  raw.forEach((t, i) => {
    const inf = r.info[i];
    if (inf.code || inf.def) return;
    const ln = i + 1;
    const toks = tokens(t);
    const only = alone(t, toks);
    for (const k of toks) {
      if (k.ref) {
        const u: RefUse = { label: k.label!, text: k.text, line: ln };
        if (inf.item) u.item = inf.item;
        if (inf.task) u.task = inf.task;
        page.uses.push(u);
      } else if (k.kind) {
        const l: PageLink = { kind: k.kind, label: k.text, target: k.target!, line: ln };
        if (only) l.alone = true;
        if (inf.item) l.item = inf.item;
        if (inf.callout) l.callout = inf.callout;
        if (inf.heading) l.heading = inf.heading;
        page.links.push(l);
      }
    }
  });
  return page;
}
