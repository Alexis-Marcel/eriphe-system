import { tokens, type LinkKind } from "./read";

// What a line of a page holds that the page draws itself, read as
// eriphe-core reads it (read.ts's tokens: references and Eriphe's links),
// with its 📅 dates; the text between is ordinary Markdown, drawn by the
// workspace's inline reader (a2ui/markdown.tsx).

export type Piece =
  | { t: "text"; s: string }
  | { t: "ref"; text: string; label: string } // [text][m1]
  | { t: "link"; kind: LinkKind; text: string; target: string; n: number } // [text](do:…): n, its place among its line's links of its kind
  | { t: "due"; date: string }; // 📅 2026-11-27

const DUE = /📅[\t\n\f\r ]*(\d{4}-\d{2}-\d{2})/g;

// pieces cuts a line into its pieces.
export function pieces(s: string): Piece[] {
  const out: Piece[] = [];
  const text = (x: string) => {
    if (!x) return;
    // Its dates, outside code spans.
    const plainAt = x.replace(/(`+)([\s\S]*?)\1/g, (m) => " ".repeat(m.length));
    let at = 0;
    for (const m of plainAt.matchAll(DUE)) {
      if (m.index! > at) out.push({ t: "text", s: x.slice(at, m.index) });
      out.push({ t: "due", date: m[1] });
      at = m.index! + m[0].length;
    }
    if (at < x.length) out.push({ t: "text", s: x.slice(at) });
  };
  const seen: Record<string, number> = {};
  let at = 0;
  for (const k of tokens(s)) {
    text(s.slice(at, k.start));
    if (k.ref) out.push({ t: "ref", text: k.text, label: k.label! });
    else {
      const n = seen[k.kind!] ?? 0;
      seen[k.kind!] = n + 1;
      out.push({ t: "link", kind: k.kind!, text: k.text, target: k.target!, n });
    }
    at = k.end;
  }
  text(s.slice(at));
  return out;
}

// split cuts a line's pieces at its first « : » outside a reference: its
// title, then the comment of whoever keeps the page. « 18:00 » or a
// URL's « https: » is no separator: a space follows the colon.
export function split(ps: Piece[]): { title: Piece[]; comment: Piece[] } {
  for (let i = 0; i < ps.length; i++) {
    const p = ps[i];
    if (p.t !== "text") continue;
    const m = /:\s/.exec(p.s);
    if (!m) continue;
    const before = p.s.slice(0, m.index).trimEnd(), after = p.s.slice(m.index + m[0].length);
    if (!before && i === 0) continue;
    return {
      title: [...ps.slice(0, i), ...(before ? [{ t: "text", s: before } as Piece] : [])],
      comment: [...(after.trim() ? [{ t: "text", s: after } as Piece] : []), ...ps.slice(i + 1)],
    };
  }
  return { title: ps, comment: [] };
}

// plain is a line's words, as a component that takes a string shows them:
// its links' words, no date, no Markdown marks.
export function plain(s: string | Piece[]): string {
  const ps = typeof s === "string" ? pieces(s) : s;
  return ps
    .map((p) => (p.t === "text" ? p.s : p.t === "due" ? "" : p.text))
    .join("")
    .replace(/\[\[([^\]|]+)\|?([^\]]*)\]\]/g, (_, a, b) => b || a)
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/[*_`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

// text is pieces written back as a line (a part of one).
export const text = (ps: Piece[]) =>
  ps.map((p) => (p.t === "text" ? p.s : p.t === "due" ? `📅 ${p.date}` : p.t === "ref" ? `[${p.text}][${p.label}]` : `[${p.text}](${p.kind}:${p.target})`)).join("");

// frNumber reads a number as people write it: « 18 385 », « 1,8 », « −2 ».
export function frNumber(s: string): number | undefined {
  const m = /^\s*([+−-]?)\s*(\d[\d\s  ]*(?:[,.]\d+)?)\s*$/.exec(s);
  if (!m) return undefined;
  const n = Number(m[2].replace(/[\s  ]/g, "").replace(",", "."));
  if (!Number.isFinite(n)) return undefined;
  return m[1] === "-" || m[1] === "−" ? -n : n;
}

// amount reads « 18 385 € » or « 38,2 Mo »: its number and its unit (a
// short word, or a sign), none when it is no amount.
export function amount(s: string): { n: number; unit: string } | undefined {
  const m = /^\s*([+−-]?\s*\d[\d\s  ]*(?:[,.]\d+)?)\s*([^\d\s][^\d\s]{0,3})?\s*$/.exec(s);
  if (!m) return undefined;
  const n = frNumber(m[1]);
  return n === undefined ? undefined : { n, unit: m[2] ?? "" };
}

const MONTHS = ["janvier", "fevrier", "mars", "avril", "mai", "juin", "juillet", "aout", "septembre", "octobre", "novembre", "decembre"];
const MONTHS_EN = ["january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"];
const fold = (s: string) => s.toLowerCase().normalize("NFD").replace(/\p{M}/gu, "");

// dayOf reads a day as a page writes it (a heading, a quote's « — Sam,
// 24/11 »): ISO, 2026-11-26 anywhere in it; « 26 novembre 2026 » or
// « 26/11/2026 »; without its year (« Jeudi 26 novembre », « 24/11 »),
// the year given (the page's own, from its « Écrit le … » line), never a
// year guessed: with none, it reads as no day.
export function dayOf(s: string, year?: number): string | undefined {
  const pad = (n: number) => String(n).padStart(2, "0");
  const ok = (y: number, mo: number, d: number) => {
    const t = new Date(Date.UTC(y, mo - 1, d));
    return t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d ? `${y}-${pad(mo)}-${pad(d)}` : undefined;
  };
  let m = /(\d{4})-(\d{2})-(\d{2})/.exec(s);
  if (m) return ok(Number(m[1]), Number(m[2]), Number(m[3]));
  m = /(\d{1,2})(?:er)?\s+([\p{L}]+)(?:\s+(\d{4}))?/u.exec(s);
  if (m) {
    const word = fold(m[2]);
    const i = MONTHS.indexOf(word) >= 0 ? MONTHS.indexOf(word) : MONTHS_EN.indexOf(word);
    const y = m[3] ? Number(m[3]) : year;
    if (i >= 0 && y) return ok(y, i + 1, Number(m[1]));
  }
  m = /(\d{1,2})\/(\d{1,2})(?:\/(\d{4}))?/.exec(s);
  if (m) {
    const y = m[3] ? Number(m[3]) : year;
    if (y) return ok(y, Number(m[2]), Number(m[1]));
  }
  return undefined;
}

// A figure of [!figures] as FiguresView takes it, from its line
// « Label : 18 385 € (▲ 1,8 % cette semaine) »: its label, its value (an
// amount, else its words), its change, in its own unit when it has one.
export type Figure = { label: Piece[]; value: Piece[]; amount?: { n: number; unit: string }; delta?: number; deltaUnit?: string; deltaLabel?: string };

export function figure(line: string): Figure {
  const { title, comment } = split(pieces(line));
  const f: Figure = { label: title, value: comment };
  // Its change, at its end in brackets.
  const last = comment[comment.length - 1];
  if (last?.t === "text") {
    const d = /\(\s*([▲▼+−-])\s*(\d[\d\s  ]*(?:[,.]\d+)?)\s*(?:([^\s)\d][^\s)\d]{0,3})(?=[\s)]))?\s*([^)]*)\)\s*$/.exec(last.s);
    const n = d ? frNumber(d[2]) : undefined;
    if (d && n !== undefined) {
      f.delta = d[1] === "▼" || d[1] === "−" || d[1] === "-" ? -n : n;
      if (d[3]) f.deltaUnit = d[3];
      if (d[4].trim()) f.deltaLabel = d[4].trim();
      const rest = last.s.slice(0, d.index).trimEnd();
      f.value = [...comment.slice(0, -1), ...(rest ? [{ t: "text", s: rest } as Piece] : [])];
    }
  }
  const a = amount(plain(f.value));
  if (a && f.value.every((p) => p.t === "text" || (p.t === "link" && p.kind === "on"))) f.amount = a;
  return f;
}
