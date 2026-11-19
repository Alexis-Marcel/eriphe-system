import type { ReactNode } from "react";

// A small Markdown reader for the catalog's Markdown component and the
// node's page: headings, paragraphs, lists, quotes, fenced code, tables
// (GitHub's), bold, italics, code and links, turned into React elements.
// It follows CommonMark and GFM where agents write them: a code span is
// literal (nothing inside it is read), an underscore inside a word starts
// no emphasis, a lone backtick stays a backtick. No HTML is ever parsed:
// raw HTML stays text, as React writes any string. An image shows its alt
// text (the catalog has none); a link opens only on the user's click, in a
// new tab, and only to http(s) or mailto.
//
// A node's page reads its links to other nodes too, as Obsidian writes
// them: [[Title]], or [[Title|shown words]], never inside code, \[[ only
// brackets (core/nodefile/wikilink.go reads them the same way, both held
// to core/nodefile/testdata/wikilinks.json). The page says which node each
// opens (Wiki): a click opens it beside the page; one archived shows so
// and opens nothing; a name no node has is greyed. The link's real target
// is its tooltip and is in its accessible name, whatever words it shows.
// Without a Wiki (the catalog's Markdown), [[...]] stays as written.

export function safeHref(raw: string): string | null {
  const url = raw.trim();
  if (/^mailto:/i.test(url)) return url;
  try {
    const u = new URL(url);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

// ---- Inline ----

// An inline piece, before React: text, a code span, an emphasis, a link,
// or a run of * or _ not yet paired (a delimiter run, CommonMark 6.2).
type Delim = { t: "delim"; ch: "*" | "_"; n: number; orig: number; open: boolean; close: boolean };
type Inline =
  | { t: "text"; s: string }
  | { t: "code"; s: string }
  | { t: "em" | "strong"; c: Inline[] }
  | { t: "link"; href: string | null; c: Inline[] }
  | { t: "wiki"; name: string; shown: string; raw: string }
  | Delim;

// A node a [[link]] opens, as the page names it: title is its title, or
// the word for someone else's words (named false); archived, it opens
// nothing.
export type WikiTarget = { id: string; title: string; named: boolean; archived?: boolean };
export type Wiki = {
  find: (name: string) => WikiTarget | undefined;
  open: (id: string) => void;
  none: string; // the tooltip of a name no node has
  archived: (title: string) => string; // the tooltip of an archived one
};
export type WikiLink = { name: string; shown?: string };

const PUNCT = /[\p{P}\p{S}]/u;
const SPACE = /\s/u;
const ESCAPABLE = /[!-/:-@[-`{-~]/; // ASCII punctuation

// runLength is the length of the run of ch that starts at i.
function runLength(s: string, i: number, ch: string): number {
  let j = i;
  while (s[j] === ch) j++;
  return j - i;
}

// codeEnd is where the code span opened by the n backticks at i ends
// (just past its closing run), or -1 when no run of exactly n closes it.
function codeEnd(s: string, i: number, n: number): number {
  for (let j = i + n; j < s.length; ) {
    if (s[j] !== "`") {
      j++;
      continue;
    }
    const m = runLength(s, j, "`");
    if (m === n) return j + m;
    j += m;
  }
  return -1;
}

// codeText is a code span's inside as CommonMark shows it: line ends as
// spaces, one space stripped from each side when both have one.
function codeText(raw: string): string {
  const s = raw.replace(/\n/g, " ");
  return s.length > 2 && s[0] === " " && s[s.length - 1] === " " && s.trim() ? s.slice(1, -1) : s;
}

// linkAt reads a link whose [ is at i: its text, its destination and where
// it ends; null when it is not one. Its text skips escapes and code spans,
// and may hold brackets that pair.
function linkAt(s: string, i: number): { text: string; dest: string; end: number } | null {
  let depth = 0;
  let j = i + 1;
  for (; j < s.length; j++) {
    const c = s[j];
    if (c === "\\") j++;
    else if (c === "`") {
      const n = runLength(s, j, "`");
      const e = codeEnd(s, j, n);
      j = (e < 0 ? j + n : e) - 1;
    } else if (c === "[") depth++;
    else if (c === "]") {
      if (depth === 0) break;
      depth--;
    }
  }
  if (j >= s.length) return null;
  const m = /^\(\s*(<[^<>\n]*>|(?:[^\s()]|\([^\s()]*\))*)(?:\s+"[^"]*")?\s*\)/.exec(s.slice(j + 1));
  if (!m) return null;
  const dest = m[1].startsWith("<") ? m[1].slice(1, -1) : m[1];
  return { text: s.slice(i + 1, j), dest, end: j + 1 + m[0].length };
}

// wikiEnd is where the [[...]] that opens at i ends (just past its ]]), or
// -1: it closes on the same line, holds no bracket, and names something.
function wikiEnd(s: string, i: number): number {
  for (let j = i + 2; j + 1 < s.length; j++) {
    const c = s[j];
    if (c === "\n" || c === "[") return -1;
    if (c === "]") return s[j + 1] === "]" && wikiParse(s.slice(i + 2, j)) ? j + 2 : -1;
  }
  return -1;
}

// wikiParse reads a link's inside, name|shown (\| as |); null when it
// names nothing.
function wikiParse(inner: string): WikiLink | null {
  const s = inner.replace(/\\\|/g, "|");
  const bar = s.indexOf("|");
  const name = (bar < 0 ? s : s.slice(0, bar)).trim();
  if (!name) return null;
  const shown = bar < 0 ? "" : s.slice(bar + 1).trim();
  return shown ? { name, shown } : { name };
}

// scan reads text into inline pieces, left to right; emphasis is paired
// afterwards (emphasis).
function scan(s: string): Inline[] {
  const out: Inline[] = [];
  let text = "";
  const push = (p: Inline) => {
    if (text) out.push({ t: "text", s: text });
    text = "";
    out.push(p);
  };
  for (let i = 0; i < s.length; ) {
    const c = s[i];
    let l: ReturnType<typeof linkAt>;
    let w: number;
    if (c === "\\" && i + 1 < s.length && ESCAPABLE.test(s[i + 1])) {
      text += s[i + 1];
      i += 2;
    } else if (c === "`") {
      const n = runLength(s, i, "`");
      const e = codeEnd(s, i, n);
      if (e < 0) {
        // No closing run: the backticks are only backticks.
        text += s.slice(i, i + n);
        i += n;
      } else {
        push({ t: "code", s: codeText(s.slice(i + n, e - n)) });
        i = e;
      }
    } else if (c === "!" && s[i + 1] === "[" && (l = linkAt(s, i + 1))) {
      text += l.text;
      i = l.end;
    } else if (c === "[" && s[i + 1] === "[" && (w = wikiEnd(s, i)) > 0) {
      const raw = s.slice(i, w);
      const p = wikiParse(raw.slice(2, -2))!;
      push({ t: "wiki", name: p.name, shown: p.shown ?? "", raw });
      i = w;
    } else if (c === "[" && (l = linkAt(s, i))) {
      push({ t: "link", href: safeHref(l.dest), c: emphasis(scan(l.text)) });
      i = l.end;
    } else if (c === "*" || c === "_") {
      const n = runLength(s, i, c);
      const before = i > 0 ? s[i - 1] : " ";
      const after = i + n < s.length ? s[i + n] : " ";
      const left = !SPACE.test(after) && (!PUNCT.test(after) || SPACE.test(before) || PUNCT.test(before));
      const right = !SPACE.test(before) && (!PUNCT.test(before) || SPACE.test(after) || PUNCT.test(after));
      // An underscore inside a word (snake_case_name) opens and closes nothing.
      const open = c === "*" ? left : left && (!right || PUNCT.test(before));
      const close = c === "*" ? right : right && (!left || PUNCT.test(after));
      push({ t: "delim", ch: c, n, orig: n, open, close });
      i += n;
    } else {
      text += c;
      i++;
    }
  }
  if (text) out.push({ t: "text", s: text });
  return out;
}

// emphasis pairs the delimiter runs (CommonMark's "process emphasis"):
// each closer with the nearest opener of its kind before it; what is left
// unpaired is text.
function emphasis(items: Inline[]): Inline[] {
  const list = items.slice();
  for (let c = 0; c < list.length; c++) {
    const closer = list[c];
    if (closer.t !== "delim" || !closer.close || closer.n === 0) continue;
    let o = c - 1;
    for (; o >= 0; o--) {
      const op = list[o];
      if (op.t !== "delim" || op.ch !== closer.ch || !op.open || op.n === 0) continue;
      // The rule of 3: a run that may both open and close pairs only when
      // the lengths together are not a multiple of 3.
      if ((op.close || closer.open) && (op.orig + closer.orig) % 3 === 0 && !(op.orig % 3 === 0 && closer.orig % 3 === 0)) continue;
      break;
    }
    if (o < 0) continue;
    const opener = list[o] as Delim;
    const use = opener.n >= 2 && closer.n >= 2 ? 2 : 1;
    opener.n -= use;
    closer.n -= use;
    const inner = list.slice(o + 1, c).map(textOfDelim);
    list.splice(o + 1, c - o - 1, { t: use === 2 ? "strong" : "em", c: inner });
    // The closer now sits just after the new node: read it again (the loop
    // steps on to it), it may have characters left.
    c = o + 1;
  }
  return list.map(textOfDelim).filter((p) => !(p.t === "text" && !p.s));
}

function textOfDelim(p: Inline): Inline {
  return p.t === "delim" ? { t: "text", s: p.ch.repeat(p.n) } : p;
}

// Ctx is what drawing inline pieces needs: the words after a link, the
// page's links to nodes, and whether it draws inside a link (where a
// [[link]] is only its words: no link in a link).
type Ctx = { newTab: string; wiki?: Wiki; inLink?: boolean };

function wikiPiece(p: Extract<Inline, { t: "wiki" }>, ctx: Ctx, k: string): ReactNode {
  const { wiki } = ctx;
  if (!wiki) return p.raw;
  const to = wiki.find(p.name);
  const words = p.shown || (to?.named ? to.title : p.name);
  if (ctx.inLink) return words;
  if (!to)
    return (
      <span key={k} className="a2-wiki none" title={wiki.none}>
        {words}
      </span>
    );
  // The real target, whatever the words: its tooltip, and its name to a
  // screen reader when the words are others.
  const label = words === to.title ? undefined : `${words} (${to.title})`;
  if (to.archived)
    return (
      <span key={k} className="a2-wiki archived" title={wiki.archived(to.title)} aria-label={label && wiki.archived(label)}>
        {words}
      </span>
    );
  return (
    <button key={k} type="button" className="a2-wiki" data-node={to.id} title={to.title} aria-label={label} onClick={() => wiki.open(to.id)}>
      {words}
    </button>
  );
}

function render(items: Inline[], ctx: Ctx, key: string): ReactNode[] {
  return items.map((p, n) => {
    const k = `${key}.${n}`;
    switch (p.t) {
      case "code":
        return <code key={k}>{p.s}</code>;
      case "strong":
        return <strong key={k}>{render(p.c, ctx, k)}</strong>;
      case "em":
        return <em key={k}>{render(p.c, ctx, k)}</em>;
      case "link":
        return p.href ? (
          <a key={k} href={p.href} target="_blank" rel="noopener noreferrer">
            {render(p.c, { ...ctx, inLink: true }, k)}
            <span className="sr-only"> {ctx.newTab}</span>
          </a>
        ) : (
          <span key={k}>{render(p.c, ctx, k)}</span>
        );
      case "wiki":
        return wikiPiece(p, ctx, k);
      case "text":
        return p.s;
      default:
        return null;
    }
  });
}

function inline(text: string, ctx: Ctx, key = "i"): ReactNode[] {
  return render(emphasis(scan(text)), ctx, key);
}

function collect(items: Inline[], out: WikiLink[]) {
  for (const p of items) {
    if (p.t === "wiki") out.push(p.shown ? { name: p.name, shown: p.shown } : { name: p.name });
    else if (p.t === "em" || p.t === "strong" || p.t === "link") collect(p.c, out);
  }
}

// ---- Blocks ----

type Align = "left" | "center" | "right" | undefined;

type Block =
  | { kind: "h"; level: number; text: string }
  | { kind: "p"; text: string }
  | { kind: "quote"; text: string }
  | { kind: "code"; text: string }
  | { kind: "ul" | "ol"; items: string[] }
  | { kind: "table"; align: Align[]; head: string[]; rows: string[][] };

// cells splits a table row on its pipes, as GFM does: a pipe splits the
// row even inside a code span, unless it is escaped (\|), which leaves a
// | in the cell, code span or not; nor one inside a [[name|shown]] link.
// The outer pipes are optional.
export function cells(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  const out: string[] = [];
  let cell = "";
  let ended = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    ended = false;
    if (c === "\\" && s[i + 1] === "|") {
      cell += "|";
      i++;
    } else if (c === "\\" && i + 1 < s.length) {
      cell += c + s[i + 1];
      i++;
    } else if (c === "[" && s[i + 1] === "[" && wikiEnd(s, i) > 0) {
      const e = wikiEnd(s, i);
      cell += s.slice(i, e);
      i = e - 1;
    } else if (c === "|") {
      out.push(cell.trim());
      cell = "";
      ended = true;
    } else cell += c;
  }
  if (!ended || cell.trim()) out.push(cell.trim());
  return out;
}

// delimiterRow reads a table's second row, its columns' alignment; null
// when the line is not one.
function delimiterRow(line: string): Align[] | null {
  if (!line.includes("-") || !/^[\s|:-]+$/.test(line)) return null;
  const cs = cells(line);
  if (!cs.length || !cs.every((c) => /^:?-+:?$/.test(c))) return null;
  if (cs.length === 1 && !line.includes("|")) return null; // a rule, ---
  return cs.map((c) => (c.startsWith(":") && c.endsWith(":") ? "center" : c.endsWith(":") ? "right" : c.startsWith(":") ? "left" : undefined));
}

// A fence opens a code block: three backticks or more (with no backtick
// after them, else it is inline code) or three tildes or more. The block
// ends on a fence of the same character, at least as long, alone on its
// line; with none, at the end of the text.
const FENCE = /^(`{3,})[^`]*$|^(~{3,}).*$/;
const RULE = /^(-{3,}|\*{3,}|_{3,})$/;

const startsBlock = (line: string) => FENCE.test(line) || /^#{1,6}\s/.test(line) || line.startsWith(">") || RULE.test(line);

function closesFence(line: string, fence: string): boolean {
  const n = runLength(line, 0, fence[0]);
  return n >= fence.length && !line.slice(n).trim();
}

function blocks(src: string): Block[] {
  const out: Block[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length) out.push({ kind: "p", text: para.join("\n") });
    para = [];
  };
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  let fence = ""; // the opening fence of a code block under way
  let code: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    const line = raw.trim();
    let m: RegExpExecArray | null;
    let align: Align[] | null;
    if (fence) {
      if (closesFence(line, fence)) {
        out.push({ kind: "code", text: code.join("\n") });
        fence = "";
      } else code.push(raw);
    } else if ((m = FENCE.exec(line))) {
      flush();
      fence = m[1] ?? m[2];
      code = [];
    } else if (!line) {
      flush();
    } else if (line.includes("|") && i + 1 < lines.length && (align = delimiterRow(lines[i + 1].trim())) && cells(line).length === align.length) {
      // A table: its header, its delimiter row, then its rows up to a blank
      // line or another block. A row has the header's cells, no more.
      flush();
      const width = align.length;
      const fit = (cs: string[]) => Array.from({ length: width }, (_, j) => cs[j] ?? "");
      const table: Block = { kind: "table", align, head: fit(cells(line)), rows: [] };
      for (i += 2; i < lines.length && lines[i].trim() && !startsBlock(lines[i].trim()); i++) table.rows.push(fit(cells(lines[i])));
      i--;
      out.push(table);
    } else if ((m = /^(#{1,6})\s+(.*)$/.exec(line))) {
      flush();
      out.push({ kind: "h", level: m[1].length, text: m[2].replace(/\s#+$/, "") });
    } else if (RULE.test(line)) {
      flush();
    } else if ((m = /^[-*+]\s+(.*)$/.exec(line)) || (m = /^\d+[.)]\s+(.*)$/.exec(line))) {
      flush();
      const kind = /^\d/.test(line) ? "ol" : "ul";
      const last = out[out.length - 1];
      if (last && last.kind === kind) last.items.push(m[1]);
      else out.push({ kind, items: [m[1]] });
    } else if ((m = /^>\s?(.*)$/.exec(line))) {
      flush();
      const last = out[out.length - 1];
      if (last && last.kind === "quote") last.text += "\n" + m[1];
      else out.push({ kind: "quote", text: m[1] });
    } else {
      para.push(line);
    }
  }
  flush();
  if (fence) out.push({ kind: "code", text: code.join("\n") });
  return out;
}

const alignClass = (a: Align) => (a ? `a2-md-${a}` : undefined);

// inlineTexts are the pieces of text the blocks read inline.
function inlineTexts(b: Block): string[] {
  switch (b.kind) {
    case "code":
      return [];
    case "ul":
    case "ol":
      return b.items;
    case "table":
      return [...b.head, ...b.rows.flat()];
    default:
      return [b.text];
  }
}

// wikiLinks are the [[links]] of a text, in order, every one: those the
// page draws (and eriphe-core indexes, core/nodefile/wikilink.go).
export function wikiLinks(text: string): WikiLink[] {
  const out: WikiLink[] = [];
  for (const b of blocks(text)) for (const t of inlineTexts(b)) collect(emphasis(scan(t)), out);
  return out;
}

// Markdown renders text; newTab is what a screen reader hears after a link
// ("opens in a new tab"); wiki, a node's page's links to other nodes.
export function Markdown({ text, newTab, wiki }: { text: string; newTab: string; wiki?: Wiki }) {
  const ctx: Ctx = { newTab, wiki };
  return (
    <div className="a2-markdown">
      {blocks(text).map((b, i) => {
        const k = String(i);
        switch (b.kind) {
          case "h": {
            // Inside a block, headings sit under the page's own title.
            const H = (["h3", "h4", "h5", "h6", "h6", "h6"] as const)[b.level - 1];
            return <H key={k}>{inline(b.text, ctx, k)}</H>;
          }
          case "code":
            return (
              <pre key={k}>
                <code>{b.text}</code>
              </pre>
            );
          case "quote":
            return <blockquote key={k}>{inline(b.text, ctx, k)}</blockquote>;
          case "ul":
          case "ol": {
            const L = b.kind;
            return (
              <L key={k}>
                {b.items.map((it, j) => (
                  <li key={j}>{inline(it, ctx, `${k}.${j}`)}</li>
                ))}
              </L>
            );
          }
          case "table":
            // Wider than the page, the table scrolls in its own box.
            return (
              <div key={k} className="a2-md-table">
                <table>
                  <thead>
                    <tr>
                      {b.head.map((c, j) => (
                        <th key={j} className={alignClass(b.align[j])}>
                          {inline(c, ctx, `${k}.h${j}`)}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {b.rows.map((r, ri) => (
                      <tr key={ri}>
                        {r.map((c, j) => (
                          <td key={j} className={alignClass(b.align[j])}>
                            {inline(c, ctx, `${k}.${ri}.${j}`)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          default:
            return <p key={k}>{inline(b.text, ctx, k)}</p>;
        }
      })}
    </div>
  );
}
