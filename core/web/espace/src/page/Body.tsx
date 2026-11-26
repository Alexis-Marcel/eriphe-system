import { createContext, Fragment, useContext, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { Code as CodeView } from "../a2ui/code";
import { wall, when } from "../a2ui/format";
import { Icon } from "../a2ui/icons";
import { inline, safeHref, type Wiki } from "../a2ui/markdown";
import { QuoteView } from "../a2ui/catalog";
import { useWords } from "../i18n";
import { Callout } from "./callouts";
import { amount, dayOf, frNumber, pieces, split, type Piece } from "./line";
import { readPage, type MdBlock, type MdItem, type MdPage, type PageLink, type RefDef, type Task } from "./read";

// A node's page, drawn from its Markdown (docs/node-format.md, "The
// page"): what its file says is on the page, nothing else, nothing less.
// The body is read as eriphe-core reads it (read.ts); each block is drawn
// with the workspace's own components: a list's items as rows, their
// indented lines their detail, folded (a click opens it), a task's box
// the user ticks (eriphe-core writes the line: tick); a date as a chip;
// callouts by the catalog's views (callouts.tsx), those marked aside in
// a side column; a reference [text][m1] by its words, its target in its
// tooltip; an ask: line as buttons that ask Eriphe (ask, which
// eriphe-core reads again in the stored page).
//
// Where the page will carry cards and actions (Slots): a line's cards at
// the end of its row (item), and a do: or card: link where it is written
// (link): until then a do: or card: link is drawn as its words, quiet.
//
// A legacy block, in its fence (```eriphe:block <name>), is drawn as it
// was, by the page that knows its surface (legacy): until node format 3
// converts it.

export type Slots = {
  // The cards an item's line carries (its references'), drawn at the end
  // of its row, before its chevron.
  item?: (it: MdItem) => ReactNode;
  // A do: (an action on its heading's references) or card: link (a card
  // placed), drawn where it is written; undefined: drawn as its words.
  link?: (l: PageLink) => ReactNode | undefined;
};

export type BodyProps = {
  body: string;
  wiki?: Wiki;
  // tick writes a task's new state into its line; none on an old version
  // or a page the user cannot change. false when it did not go.
  tick?: (it: MdItem, line: string, to: Task) => Promise<boolean>;
  // ask sends the nth ask: link of a line; none where nothing is sent.
  ask?: (line: number, n: number) => void;
  legacy?: (name: string) => ReactNode;
  slots?: Slots;
};

type Ctx = {
  page: MdPage;
  lines: string[];
  refs: Map<string, RefDef>;
  year?: number;
  newTab: string;
  wiki?: Wiki;
  tick?: BodyProps["tick"];
  ask?: BodyProps["ask"];
  legacy?: BodyProps["legacy"];
  slots: Slots;
};

const MdCtx = createContext<Ctx | null>(null);
export const useMd = () => useContext(MdCtx)!;

export function PageBody({ body, wiki, tick, ask, legacy, slots = {} }: BodyProps) {
  const { t } = useWords();
  const page = useMemo(() => readPage(body), [body]);
  const ctx = useMemo<Ctx>(() => {
    const refs = new Map<string, RefDef>();
    for (const d of page.refs) if (!refs.has(d.label)) refs.set(d.label, d);
    // A day without its year takes the page's, from its « Écrit le … ».
    const year = Number(dayOf(page.written?.text ?? "")?.slice(0, 4)) || undefined;
    return { page, lines: body.replace(/\r\n/g, "\n").split("\n"), refs, year, newTab: t("espace.a2.newtab"), wiki, tick, ask, legacy, slots };
  }, [page, body, t, wiki, tick, ask, legacy, slots]);
  const aside = (b: MdBlock) => b.kind === "callout" && !!b.opts?.includes("aside");
  const main = page.blocks.filter((b) => !aside(b));
  const side = page.blocks.filter(aside);
  return (
    <MdCtx.Provider value={ctx}>
      <div className="md-page">
        {page.written && <p className="md-written">{page.written.text}</p>}
        <div className={side.length ? "md-cols" : "md-one"}>
          <div className="pg-doc md-main">
            <Blocks blocks={main} top />
          </div>
          {side.length > 0 && (
            <aside className="md-aside">
              <Blocks blocks={side} />
            </aside>
          )}
        </div>
      </div>
    </MdCtx.Provider>
  );
}

// Blocks draws blocks in their order; top: the page's own (a heading
// counts what is left to do under it).
export function Blocks({ blocks, top }: { blocks: MdBlock[]; top?: boolean }) {
  return (
    <>
      {blocks.map((b, i) => (
        <Block key={`${b.kind}-${b.line}`} b={b} after={top ? blocks.slice(i + 1) : undefined} />
      ))}
    </>
  );
}

function Block({ b, after }: { b: MdBlock; after?: MdBlock[] }) {
  const ctx = useMd();
  switch (b.kind) {
    case "heading": {
      const n = after ? openUnder(after, b.level!) : 0;
      const H = b.level! <= 2 ? "h3" : "h4";
      return (
        <H className={`md-h md-h${b.level}`}>
          <Line s={b.text!} ln={b.line} />
          {n > 0 && <span className="a2-count">{n}</span>}
        </H>
      );
    }
    case "paragraph":
      return <Paragraph b={b} />;
    case "list":
      return <List b={b} />;
    case "table":
      return <Table b={b} />;
    case "code": {
      const name = ctx.lines[b.line - 1]?.trim().split(/\s+/)[1];
      if (b.lang === "eriphe:block" && name && ctx.legacy) return <>{ctx.legacy(name)}</>;
      return <CodeView text={b.text ?? ""} language={b.lang ?? ""} mode={b.lang === "diff" ? "diff" : "code"} />;
    }
    case "callout":
      return <Callout b={b} />;
    case "quote": {
      const date = b.when ? dayOf(b.when, ctx.year) : undefined;
      return <QuoteView text={<Lines text={b.text ?? ""} ln={b.line} />} from={b.from} date={date} written={date ? undefined : b.when} />;
    }
    case "embed":
      return (
        <p className="md-embed">
          <Icon name="note" />
          {inline(`[[${b.name}]]`, { newTab: ctx.newTab, wiki: ctx.wiki }, `e${b.line}`)}
        </p>
      );
    case "rule":
      return <hr className="a2-divider" />;
  }
}

// openUnder counts the tasks still open in the lists of a heading's
// section: what the page computes, a count.
function openUnder(after: MdBlock[], level: number): number {
  let n = 0;
  for (const x of after) {
    if (x.kind === "heading" && x.level! <= level) break;
    if (x.kind === "list") n += x.items!.filter((it) => it.task === " " || it.task === "/").length;
  }
  return n;
}

// ---- Lines ----

// Line draws one line of the page (its number ln): its Markdown, its
// references, links and dates.
export function Line({ s, ln }: { s: string; ln: number }) {
  return <Pieces ps={pieces(s)} ln={ln} />;
}

// Lines draws lines that follow one another from ln (a paragraph, a
// quote): a soft break between them.
export function Lines({ text, ln }: { text: string; ln: number }) {
  return (
    <>
      {text.split("\n").map((s, i) => (
        <Fragment key={i}>
          {i > 0 && "\n"}
          <Line s={s} ln={ln + i} />
        </Fragment>
      ))}
    </>
  );
}

export function Pieces({ ps, ln }: { ps: Piece[]; ln: number }) {
  const ctx = useMd();
  const { lang } = useWords();
  return (
    <>
      {ps.map((p, i) => {
        switch (p.t) {
          case "text":
            return <Fragment key={i}>{inline(p.s, { newTab: ctx.newTab, wiki: ctx.wiki }, `${ln}.${i}`)}</Fragment>;
          case "due": {
            const w = wall(p.date);
            return (
              <time key={i} className="md-due a2-due" dateTime={p.date}>
                <Icon name="calendar" /> {w ? when(lang, w) : p.date}
              </time>
            );
          }
          case "ref":
            return <Ref key={i} text={p.text} label={p.label} />;
          case "link":
            if (p.kind === "ask")
              return (
                <button key={i} type="button" className="btn a2-button a2-ask a2-button-default md-ask-inline" disabled={!ctx.ask} onClick={() => ctx.ask?.(ln, p.n)}>
                  {p.text}
                </button>
              );
            if (p.kind === "on") return <Fragment key={i}>{inline(p.text, { newTab: ctx.newTab, wiki: ctx.wiki }, `${ln}.${i}`)}</Fragment>;
            return <Link key={i} kind={p.kind} n={p.n} text={p.text} ln={ln} />;
        }
      })}
    </>
  );
}

// Ref is a reference's words: a web address opens in a new tab; any other
// target (a mail, an event, as its tool gave it) is the thing a card will
// act on, its target in its tooltip; a reference with no target is
// greyed, as a link to no node is.
function Ref({ text, label }: { text: string; label: string }) {
  const ctx = useMd();
  const { t } = useWords();
  const d = ctx.refs.get(label);
  const words = inline(text, { newTab: ctx.newTab }, `r.${label}`);
  if (!d)
    return (
      <span className="a2-wiki none" title={t("espace.md.ref.none")}>
        {words}
      </span>
    );
  const href = /^https?:\/\//i.test(d.target) ? safeHref(d.target) : null;
  if (href)
    return (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {words}
        <span className="sr-only"> {ctx.newTab}</span>
      </a>
    );
  return (
    <span className="md-ref" data-ref={d.label} title={d.target}>
      {words}
    </span>
  );
}

// linkAt is one of the page's links, as eriphe-core reads it: the nth of
// its kind on line ln.
function linkAt(page: MdPage, ln: number, kind: PageLink["kind"], n: number): PageLink | undefined {
  return page.links.filter((l) => l.line === ln && l.kind === kind)[n];
}

// Link is a do: or card: link where it is written: what the page's slots
// draw there, else its words, quiet.
function Link({ kind, n, text, ln }: { kind: PageLink["kind"]; n: number; text: string; ln: number }) {
  const ctx = useMd();
  const l = linkAt(ctx.page, ln, kind, n);
  const drawn = l && ctx.slots.link?.(l);
  if (drawn !== undefined && drawn !== null) return <>{drawn}</>;
  return (
    <span className={`md-link md-link-${kind}`} data-line={ln}>
      {inline(text, { newTab: ctx.newTab }, `l.${ln}.${n}`)}
    </span>
  );
}

// ---- Paragraphs ----

// A line of nothing but Eriphe's links: ask: links are buttons, the first
// the default; a do: or card: link is drawn where it is (Link).
function Paragraph({ b }: { b: MdBlock }) {
  const ctx = useMd();
  const rows = b.text!.split("\n").map((s, i) => ({ s, ln: b.line + i }));
  const acts = rows.every((r) => ctx.page.links.some((l) => l.line === r.ln && l.alone));
  if (!acts)
    return (
      <p className="md-p">
        <Lines text={b.text!} ln={b.line} />
      </p>
    );
  return (
    <div className="md-acts">
      {rows.map((r) => (
        <Acts key={r.ln} s={r.s} ln={r.ln} />
      ))}
    </div>
  );
}

function Acts({ s, ln }: { s: string; ln: number }) {
  const ctx = useMd();
  const [sent, setSent] = useState(-1);
  const ps = pieces(s).filter((p): p is Extract<Piece, { t: "link" }> => p.t === "link");
  const asks = ps.filter((p) => p.kind === "ask");
  return (
    <>
      {ps
        .filter((p) => p.kind !== "ask")
        .map((p, i) => (p.kind === "on" ? <span key={i}>{p.text}</span> : <Link key={i} kind={p.kind} n={p.n} text={p.text} ln={ln} />))}
      {asks.length > 0 && (
        <div className="a2-asks">
          {asks.map((p, i) => (
            <button
              key={i}
              type="button"
              className={`btn a2-button a2-ask a2-button-${i === 0 ? "primary" : "default"}${sent === i ? " sent" : ""}`}
              disabled={!ctx.ask}
              onClick={() => {
                setSent(i);
                ctx.ask?.(ln, p.n);
              }}
            >
              {p.text}
            </button>
          ))}
        </div>
      )}
    </>
  );
}

// ---- Lists ----

// A list of things (tasks, references, dates, links, details) is drawn as
// rows; any other list (decisions, notes) stays a plain list.
export const isThing = (it: MdItem) => it.task !== undefined || !!it.detail || pieces(it.text).some((p) => p.t !== "text");

function List({ b }: { b: MdBlock }) {
  const items = b.items!;
  if (!items.some(isThing)) {
    const L = b.ordered ? "ol" : "ul";
    return (
      <div className="a2-markdown md-plainlist">
        <L>
          {items.map((it) => (
            <li key={it.line}>
              <Line s={it.text} ln={it.line} />
            </li>
          ))}
        </L>
      </div>
    );
  }
  return (
    <ul className="md-list a2-sec-rows">
      {items.map((it) => (
        <Item key={it.line} it={it} />
      ))}
    </ul>
  );
}

// interactive tells a click on a button, a link or a box of a row: it is
// theirs, not the row's.
const interactive = (e: MouseEvent) => !!(e.target as HTMLElement).closest("button, a, input, label");

function Item({ it }: { it: MdItem }) {
  const ctx = useMd();
  const { t } = useWords();
  const [open, setOpen] = useState(false);
  // The state the user just gave, until the page reads it back.
  const [want, setWant] = useState<Task | null>(null);
  useEffect(() => setWant(null), [it.task, it.line]);
  const task = want ?? it.task;
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (box.current) box.current.indeterminate = task === "/";
  }, [task]);
  const ps = pieces(it.text);
  const due = ps.find((p) => p.t === "due");
  const { title, comment } = split(ps.filter((p) => p.t !== "due"));
  const openable = !!it.detail;
  const done = task === "x" || task === "-";
  const tick = ctx.tick;
  const flip = () => {
    if (!tick) return;
    const to: Task = task === "x" ? " " : "x";
    setWant(to);
    void tick(it, ctx.lines[it.line - 1] ?? it.text, to).then((ok) => !ok && setWant(null));
  };
  return (
    <li className={`md-item${done ? " md-done" : ""}${task === "-" ? " md-dropped" : ""}${open ? " open" : ""}`} data-line={it.line}>
      <div
        className="a2-sec-row rich md-row"
        role={openable ? "button" : undefined}
        tabIndex={openable ? 0 : undefined}
        aria-expanded={openable ? open : undefined}
        onClick={openable ? (e) => !interactive(e) && setOpen(!open) : undefined}
        onKeyDown={openable ? (e) => (e.key === "Enter" || e.key === " ") && e.target === e.currentTarget && (e.preventDefault(), setOpen(!open)) : undefined}
      >
        {task !== undefined && (
          <input
            ref={box}
            type="checkbox"
            className="a2-tick"
            checked={task === "x"}
            disabled={!tick || task === "-"}
            title={task === "/" ? t("espace.md.task.doing") : task === "-" ? t("espace.md.task.dropped") : undefined}
            aria-label={plainOf(title)}
            onChange={flip}
          />
        )}
        <div className="a2-sec-main">
          <span className="a2-sec-title md-title">
            <Pieces ps={title} ln={it.line} />
          </span>
          {comment.length > 0 && (
            <span className="a2-sec-sub md-comment">
              <Pieces ps={comment} ln={it.line} />
            </span>
          )}
        </div>
        {due && <Pieces ps={[due]} ln={it.line} />}
        {ctx.slots.item?.(it)}
        {openable && <span className="a2-sec-chev" aria-hidden="true" />}
      </div>
      {open && it.detail && (
        <div className="md-detail">
          <Blocks blocks={it.detail} />
        </div>
      )}
    </li>
  );
}

const plainOf = (ps: Piece[]) =>
  ps
    .map((p) => (p.t === "text" ? p.s : p.t === "due" ? "" : p.text))
    .join("")
    .replace(/[*_`[\]]/g, "")
    .trim();

// ---- Tables ----

// A table, its cells' bold and links kept; a column of numbers (an
// amount, a share) is set right.
export function Table({ b, title }: { b: MdBlock; title?: ReactNode }) {
  const head = b.head!, rows = b.rows ?? [];
  const numeric = head.map((_, c) => rows.length > 0 && rows.every((r) => !r[c] || frNumber(r[c]) !== undefined || amount(r[c]) !== undefined));
  const cls = (c: number) => (c > 0 && numeric[c] ? "a2-col-number" : "a2-col-text");
  return (
    <>
      {title}
      <div className="a2-table-wrap">
        <table className="a2-table md-table">
          <thead>
            <tr>
              {head.map((h, c) => (
                <th key={c} scope="col" className={cls(c)}>
                  <Line s={h} ln={b.line} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                {r.map((cell, c) => (
                  <td key={c} className={cls(c)}>
                    <Line s={cell} ln={b.line + 2 + i} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
