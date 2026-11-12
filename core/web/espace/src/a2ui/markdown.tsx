import type { ReactNode } from "react";

// A small Markdown reader for the catalog's Markdown component: headings,
// paragraphs, lists, quotes, bold, italics, code and links, turned into
// React elements. No HTML is ever parsed: raw HTML stays text, as React
// writes any string. An image shows its alt text (the catalog has none); a
// link opens only on the user's click, in a new tab, and only to http(s)
// or mailto.

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

// Inline: the first of these that starts earliest wins; its inside is read
// again, but for code.
const INLINE = /`([^`]+)`|!\[([^\]]*)\]\(([^)\s]*)\)|\[([^\]]+)\]\(([^)\s]+)\)|\*\*(.+?)\*\*|__(.+?)__|\*([^*\s][^*]*?)\*|_([^_\s][^_]*?)_/;

function inline(text: string, newTab: string, key = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  let rest = text;
  let n = 0;
  while (rest) {
    const m = INLINE.exec(rest);
    if (!m) {
      out.push(rest);
      break;
    }
    if (m.index > 0) out.push(rest.slice(0, m.index));
    const k = `${key}.${n++}`;
    if (m[1] !== undefined) out.push(<code key={k}>{m[1]}</code>);
    else if (m[2] !== undefined || m[3] !== undefined) out.push(m[2] || "");
    else if (m[4] !== undefined) {
      const href = safeHref(m[5]);
      out.push(
        href ? (
          <a key={k} href={href} target="_blank" rel="noopener noreferrer">
            {inline(m[4], newTab, k)}
            <span className="sr-only"> {newTab}</span>
          </a>
        ) : (
          <span key={k}>{inline(m[4], newTab, k)}</span>
        ),
      );
    } else if (m[6] !== undefined || m[7] !== undefined) out.push(<strong key={k}>{inline(m[6] ?? m[7], newTab, k)}</strong>);
    else out.push(<em key={k}>{inline(m[8] ?? m[9], newTab, k)}</em>);
    rest = rest.slice(m.index + m[0].length);
  }
  return out;
}

type Block =
  | { kind: "h"; level: number; text: string }
  | { kind: "p"; text: string }
  | { kind: "quote"; text: string }
  | { kind: "ul" | "ol"; items: string[] };

function blocks(src: string): Block[] {
  const out: Block[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length) out.push({ kind: "p", text: para.join(" ") });
    para = [];
  };
  for (const raw of src.replace(/\r\n?/g, "\n").split("\n")) {
    const line = raw.trim();
    let m: RegExpExecArray | null;
    if (!line) {
      flush();
    } else if ((m = /^(#{1,6})\s+(.*)$/.exec(line))) {
      flush();
      out.push({ kind: "h", level: m[1].length, text: m[2].replace(/\s#+$/, "") });
    } else if ((m = /^[-*+]\s+(.*)$/.exec(line)) || (m = /^\d+[.)]\s+(.*)$/.exec(line))) {
      flush();
      const kind = /^\d/.test(line) ? "ol" : "ul";
      const last = out[out.length - 1];
      if (last && last.kind === kind) last.items.push(m[1]);
      else out.push({ kind, items: [m[1]] });
    } else if ((m = /^>\s?(.*)$/.exec(line))) {
      flush();
      const last = out[out.length - 1];
      if (last && last.kind === "quote") last.text += " " + m[1];
      else out.push({ kind: "quote", text: m[1] });
    } else if (/^(-{3,}|\*{3,}|_{3,})$/.test(line)) {
      flush();
    } else {
      para.push(line);
    }
  }
  flush();
  return out;
}

// Markdown renders text; newTab is what a screen reader hears after a link
// ("opens in a new tab").
export function Markdown({ text, newTab }: { text: string; newTab: string }) {
  return (
    <div className="a2-markdown">
      {blocks(text).map((b, i) => {
        const k = String(i);
        switch (b.kind) {
          case "h": {
            // Inside a tile, headings sit under the tile's own title.
            const H = (["h3", "h4", "h5", "h6", "h6", "h6"] as const)[b.level - 1];
            return <H key={k}>{inline(b.text, newTab, k)}</H>;
          }
          case "quote":
            return <blockquote key={k}>{inline(b.text, newTab, k)}</blockquote>;
          case "ul":
          case "ol": {
            const L = b.kind;
            return (
              <L key={k}>
                {b.items.map((it, j) => (
                  <li key={j}>{inline(it, newTab, `${k}.${j}`)}</li>
                ))}
              </L>
            );
          }
          default:
            return <p key={k}>{inline(b.text, newTab, k)}</p>;
        }
      })}
    </div>
  );
}
