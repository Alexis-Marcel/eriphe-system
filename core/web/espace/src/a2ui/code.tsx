import { createContext, useContext } from "react";
import { useWords } from "../i18n";

// Code: text in a fixed-width font, as it is, one block per line so that a
// tile with too little room hides its last lines whole (place.ts, FOLDABLE)
// and says the rest is there, open. A diff is a unified diff, read line by
// line: its files' headers, its hunks (@@), the lines added, removed and
// kept, each a class (espace.css), never a style.
//
// A tile on the grid never scrolls: its long lines wrap. The surface open
// (OnStage) keeps them as they are and scrolls sideways; it shows every
// line, where a tile draws GRID_LINES at most and says how many more.

// OnStage tells a component whether its surface is open on stage (or alone,
// as in the gallery), not a tile among the others on the grid.
export const OnStage = createContext(true);

export const GRID_LINES = 60;

export type LineKind = "code" | "file" | "hunk" | "add" | "del" | "ctx" | "note";
export type Line = { kind: LineKind; text: string };

// lines splits a text in lines, without the empty one after a final newline.
function lines(text: string): string[] {
  const out = text.replace(/\r\n?/g, "\n").split("\n");
  if (out.length > 1 && out[out.length - 1] === "") out.pop();
  return out;
}

const HEADER = /^(diff |index |--- |\+\+\+ |new file mode|deleted file mode|old mode|new mode|similarity index|dissimilarity index|rename from|rename to|copy from|copy to|Binary files)/;
const HUNK = /^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@/;

// diffLines reads a unified diff. Inside a hunk its header's counts say
// how many lines it holds: a removed line that reads "-- x" ("--- x" in the
// diff) stays a removed line, not a file's header. Outside, a line is a
// header, a hunk, or read by its first character.
export function diffLines(text: string): Line[] {
  const out: Line[] = [];
  let old = 0, now = 0; // the lines left in the hunk, on each side
  for (const l of lines(text)) {
    if (old > 0 || now > 0) {
      const c = l[0];
      if (c === "+" && now > 0) {
        now--;
        out.push({ kind: "add", text: l });
        continue;
      }
      if (c === "-" && old > 0) {
        old--;
        out.push({ kind: "del", text: l });
        continue;
      }
      if ((c === " " || l === "") && old > 0 && now > 0) {
        old--;
        now--;
        out.push({ kind: "ctx", text: l });
        continue;
      }
      if (c === "\\") {
        out.push({ kind: "note", text: l });
        continue;
      }
      old = now = 0; // the hunk ended early
    }
    const h = HUNK.exec(l);
    if (h) {
      old = h[1] === undefined ? 1 : Number(h[1]);
      now = h[2] === undefined ? 1 : Number(h[2]);
      out.push({ kind: "hunk", text: l });
    } else if (HEADER.test(l)) out.push({ kind: "file", text: l });
    else if (l.startsWith("\\")) out.push({ kind: "note", text: l });
    else if (l.startsWith("+")) out.push({ kind: "add", text: l });
    else if (l.startsWith("-")) out.push({ kind: "del", text: l });
    else out.push({ kind: "ctx", text: l });
  }
  return out;
}

export function codeLines(text: string, mode: "code" | "diff"): Line[] {
  return mode === "diff" ? diffLines(text) : lines(text).map((l) => ({ kind: "code", text: l }));
}

export function Code({ text, language, mode }: { text: string; language: string; mode: "code" | "diff" }) {
  const { t } = useWords();
  const stage = useContext(OnStage);
  const all = codeLines(text, mode);
  const shown = stage ? all : all.slice(0, GRID_LINES);
  const rest = all.length - shown.length;
  return (
    <figure className={`a2-code a2-code-${mode}`}>
      {language && <figcaption className="a2-code-lang">{language}</figcaption>}
      <pre className="a2-code-body">
        <code>
          {shown.map((l, i) => (
            <span key={i} className={l.kind === "code" ? "a2-code-line" : `a2-code-line a2-diff-${l.kind}`}>
              {l.text}
            </span>
          ))}
        </code>
      </pre>
      {rest > 0 && <p className="a2-code-rest">{t("espace.a2.code.rest", rest)}</p>}
    </figure>
  );
}
