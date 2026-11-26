import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useAdapter, type Adapter } from "../a2ui/adapter";
import { CATALOG, nameOf } from "../a2ui/catalog";
import { wall, when } from "../a2ui/format";
import { Icon } from "../a2ui/icons";
import { InlineYes } from "../a2ui/inline";
import { ListsContext, type Lists } from "../a2ui/lists";
import { inline, Markdown, type Wiki } from "../a2ui/markdown";
import { useMoved, useProposals, type ProposalView } from "../a2ui/proposals";
import { SurfaceView } from "../a2ui/Surfaces";
import type { ListRow, SourceList } from "../data/api";
import { useWords } from "../i18n";
import { Card } from "../page/Card";
import { BlockTitle } from "../a2ui/blocks";
import type { FakeProposals } from "./fake";
import { dayOf, frNumber, onlyActs, parse, plain, split, tokens, unitOf, type Block, type Item, type Parsed, type Problem, type Task, type Tok } from "./parse";
import { objects, type Obj } from "./samples";

// PROTOTYPE (dev server only, ?markdown): a page in Markdown, drawn with
// Eriphe's own components. Each callout, table, quote and code block is an
// A2UI surface of the catalog (Figures, Chart, Table, Agenda, Person,
// Quote, Alert, Code, Asks, Message), fed by what the file says; lists,
// steps and folds are drawn here with the classes Sections and Timeline
// use; cards are page/Card.tsx and a2ui/inline.tsx's InlineYes.

type Ctx = {
  page: string;
  parsed: Parsed;
  year: number;
  toggle(ln: number, to: Task): void;
  removeLines(lns: number[]): void;
  openNode(title: string): void;
  embed(title: string): { md: string; setMd(f: (s: string) => string): void } | undefined;
  ask(text: string, ln: number): void;
  view(o: { target: string; label: string }): void;
  wiki: Wiki;
  lists: Map<string, { list?: SourceList; asks?: { texts: string[]; ln: number } }>;
  depth: number;
};
const MdCtx = createContext<Ctx | null>(null);
const useMd = () => useContext(MdCtx)!;
const AdapterCtx = createContext<Adapter | null>(null);

// resolve: what a reference names, as code would read it from its tool.
export function resolve(parsed: Parsed, id: string): { target: string; obj?: Obj } | undefined {
  const r = parsed.refs.get(id);
  if (!r) return undefined;
  return { target: r.target, obj: objects[r.target] ?? fakeObj(r.target) };
}
// An id the prototype does not know (a pasted page): a made-up thing.
function fakeObj(target: string): Obj | undefined {
  const [scheme] = target.split(":");
  if (scheme === "mail") return { kind: "mail", from: "Expéditeur fictif <inconnu@example.org>", subject: "(identifiant inconnu du prototype)", date: "2026-11-26T08:00:00+01:00", body: `Mail fictif pour ${target}.` };
  if (scheme === "event") return { kind: "event", title: "(événement inconnu du prototype)", start: "2026-11-26T08:00:00+01:00" };
  return undefined;
}
const describe = (o: Obj) => (o.kind === "mail" ? `${nameOf(o.from)} · ${o.subject}` : o.kind === "event" ? o.title : o.kind === "pr" ? `PR : ${o.title}` : o.name);

// mismatch: the link's words say something the thing it names does not.
const norm = (s: string) => s.toLowerCase().normalize("NFD").replace(/\p{M}/gu, "");
export function mismatch(label: string, o: Obj | undefined): boolean {
  if (!o) return false;
  const words = norm(label).split(/[^\p{L}\p{N}]+/u).filter((w) => w.length >= 4);
  if (!words.length) return false;
  const hay = norm(describe(o) + " " + (o.kind === "mail" ? o.from : ""));
  return !words.some((w) => hay.includes(w));
}

// ---- A2UI surfaces ----

let seq = 0;
function A2({ comp, list, asks }: { comp: Record<string, unknown>; list?: SourceList; asks?: { texts: string[]; ln: number } }) {
  const adapter = useContext(AdapterCtx)!;
  const ctx = useMd();
  const sid = useMemo(() => `md-${++seq}`, []);
  const key = JSON.stringify(comp);
  if (list || asks) ctx.lists.set(sid, { list, asks });
  useEffect(() => {
    adapter.applyMessages([{ version: "1.0", createSurface: { surfaceId: sid, catalogId: CATALOG.id, components: [{ ...comp, id: "root" }], dataModel: {} } }]);
    return () => adapter.applyMessages([{ version: "1.0", deleteSurface: { surfaceId: sid } }]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [adapter, sid, key]);
  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  const s = surfaces.find((x) => x.id === sid);
  return <div className="md-a2" data-surface={sid}>{s && <SurfaceView surface={s} />}</div>;
}

// ---- The page ----

export function MdPage({ md, setMd, page, openNode, embed, onProblems }: { md: string; setMd(f: (s: string) => string): void; page: string; openNode(t: string): void; embed: Ctx["embed"]; onProblems?(p: Problem[]): void }) {
  const { lang } = useWords();
  const adapter = useAdapter(lang);
  const [errors, setErrors] = useState<Problem[]>([]);
  useEffect(() => adapter.onError((e) => setErrors((x) => [...x, { ln: -1, what: `composant refusé par le catalogue : ${e.error.message}` }])), [adapter]);
  return (
    <AdapterCtx.Provider value={adapter}>
      <MdBody md={md} setMd={setMd} page={page} openNode={openNode} embed={embed} depth={0} onProblems={onProblems} extra={errors} />
    </AdapterCtx.Provider>
  );
}

function MdBody({ md, setMd, page, openNode, embed, depth, onProblems, extra = [] }: { md: string; setMd(f: (s: string) => string): void; page: string; openNode(t: string): void; embed: Ctx["embed"]; depth: number; onProblems?(p: Problem[]): void; extra?: Problem[] }) {
  const { t } = useWords();
  const parsed = useMemo(() => parse(md), [md]);
  const year = useMemo(() => Number((dayOf(parsed.written?.text ?? "", 0) ?? parsed.front?.date ?? "").slice(0, 4)) || new Date().getFullYear(), [parsed]);
  const [viewing, setViewing] = useState<{ target: string; label: string } | null>(null);
  const [said, setSaid] = useState<{ text: string; line: string } | null>(null);
  const lists = useMemo(() => new Map<string, { list?: SourceList; asks?: { texts: string[]; ln: number } }>(), []);
  const ctx: Ctx = useMemo(
    () => ({
      page,
      parsed,
      year,
      depth,
      toggle: (ln, to) => setMd((s) => setLine(s, ln, to)),
      removeLines: (lns) => setMd((s) => s.split("\n").filter((_, i) => !lns.includes(i)).join("\n")),
      openNode,
      embed,
      ask: (text, ln) => setSaid({ text, line: ln >= 0 ? md.split("\n")[ln] : "" }),
      view: setViewing,
      lists,
      wiki: {
        find: (name) => (embed(name) ? { id: name, title: name, named: true } : undefined),
        open: openNode,
        none: "Aucun node de ce nom dans le prototype",
        archived: (x) => x,
      },
    }),
    [page, parsed, year, depth, setMd, openNode, embed, md, lists],
  );
  const problems = useMemo(() => [...check(parsed), ...extra], [parsed, extra]);
  useEffect(() => onProblems?.(problems), [problems, onProblems]);
  const listsCtx = useMemo<Lists>(
    () => ({
      list: (surface) => lists.get(surface)?.list,
      ask: (surface, comp) => {
        const a = lists.get(surface)?.asks;
        const i = Number(comp.split("#")[1]);
        if (a) ctx.ask(a.texts[i], a.ln);
      },
    }),
    [lists, ctx],
  );
  const main = parsed.blocks.filter((b) => !(b.k === "callout" && b.mods.includes("aside")));
  const aside = parsed.blocks.filter((b) => b.k === "callout" && b.mods.includes("aside"));
  return (
    <MdCtx.Provider value={ctx}>
      <ListsContext.Provider value={listsCtx}>
        {parsed.written && depth === 0 && <p className="md-written">{parsed.written.text}</p>}
        <div className={aside.length ? "md-cols" : "md-one"}>
          <div className="md-main pg-doc">
            <Blocks blocks={main} all={parsed.blocks} />
          </div>
          {aside.length > 0 && (
            <aside className="md-aside">
              <Blocks blocks={aside} all={parsed.blocks} />
            </aside>
          )}
        </div>
        {viewing && <Viewer v={viewing} onClose={() => setViewing(null)} />}
        {said && (
          <div className="md-toast" role="status">
            <b>Envoyé à Eriphe, comme ton message :</b> « {said.text} »{said.line && <small>avec sa ligne : <code>{said.line.trim()}</code></small>}
            <button type="button" className="linkish" onClick={() => setSaid(null)}>
              {t("espace.page.close")}
            </button>
          </div>
        )}
      </ListsContext.Provider>
    </MdCtx.Provider>
  );
}

function setLine(src: string, ln: number, to: Task): string {
  const lines = src.split("\n");
  lines[ln] = lines[ln].replace(/^((?:\s*>)*\s*(?:[-*+]|\d+[.)])\s+)\[[ xX/-]\]/, `$1[${to}]`);
  return lines.join("\n");
}

// check: what code would tell the author after an edit.
export function check(p: Parsed): Problem[] {
  const out: Problem[] = [...p.problems];
  const used = new Set<string>();
  const known = new Set(["figures", "chart", "steps", "agenda", "person", "warning", "info", "success", "error", "danger", "caution", "note", "tip", "abstract", "question", "example"]);
  const walk = (bs: Block[]) => {
    for (const b of bs) {
      const texts = b.k === "p" || b.k === "h" ? [b.text] : b.k === "table" ? [...b.head, ...b.rows.flat()] : b.k === "callout" ? [b.title] : [];
      if (b.k === "list") for (const it of b.items) texts.push(it.text);
      for (const s of texts)
        for (const tk of tokens(s)) {
          if (tk.t === "ref") {
            used.add(tk.id);
            if (!p.refs.has(tk.id)) out.push({ ln: b.ln, what: `référence [${tk.id}] sans cible en bas de page` });
          }
          if (tk.t === "due" && !wall(tk.date)) out.push({ ln: b.ln, what: `date impossible ${tk.date}` });
        }
      if (b.k === "list") for (const it of b.items) walk(it.children);
      if (b.k === "callout") {
        if (!known.has(b.type)) out.push({ ln: b.ln, what: `encadré inconnu [!${b.type}] : dessiné comme une note` });
        if (b.type === "chart") {
          const tb = b.body.find((x) => x.k === "table");
          if (!tb) out.push({ ln: b.ln, what: "graphique sans tableau" });
          else if (tb.k === "table") for (const r of tb.rows) r.slice(1).forEach((c) => frNumber(c) === undefined && out.push({ ln: b.ln, what: `cellule non numérique « ${c} » dans le graphique` }));
        }
        walk(b.body);
      }
    }
  };
  walk(p.blocks);
  for (const [k, r] of p.refs) {
    if (!used.has(k)) out.push({ ln: r.ln, what: `cible [${k}] jamais utilisée` });
    if (!/^(mail|event|file|pr):|^https?:\/\//.test(r.target)) out.push({ ln: r.ln, what: `cible [${k}] de forme inconnue : ${r.target}` });
  }
  return out;
}

// ---- Blocks ----

function Blocks({ blocks, all }: { blocks: Block[]; all: Block[] }) {
  return (
    <>
      {blocks.map((b) => (
        <BlockView key={`${b.k}-${b.ln}`} b={b} all={all} />
      ))}
    </>
  );
}

function BlockView({ b, all }: { b: Block; all: Block[] }) {
  const ctx = useMd();
  switch (b.k) {
    case "h": {
      const n = countUnder(all, b);
      const H = b.level <= 2 ? "h3" : "h4";
      return (
        <H className={`md-h md-h${b.level} a2-sec-head`}>
          <Inl s={b.text} ln={b.ln} />
          {n > 0 && <span className="a2-count">{n}</span>}
        </H>
      );
    }
    case "p": {
      const acts = onlyActs(b.text);
      if (acts) return <Acts acts={acts} ln={b.ln} all={all} />;
      return (
        <p className="md-p">
          <Inl s={b.text} ln={b.ln} />
        </p>
      );
    }
    case "list":
      return <ListView b={b} />;
    case "table":
      return <TableView head={b.head} rows={b.rows} />;
    case "code":
      return <A2 comp={{ component: "Code", text: b.text, ...(b.lang ? { language: b.lang } : {}), mode: b.lang === "diff" ? "diff" : "code" }} />;
    case "quote": {
      const date = b.when ? dayOf(b.when, ctx.year) : undefined;
      return <A2 comp={{ component: "Quote", text: plain(b.text.replace(/\n/g, " ")), ...(b.from ? { from: b.from } : {}), ...(date ? { date } : {}) }} />;
    }
    case "embed":
      return <Embed name={b.name} />;
    case "hr":
      return <hr className="a2-divider" />;
    case "callout":
      return <Callout b={b} all={all} />;
  }
}

// countUnder: the items of the lists under a heading, up to the next of
// its level or higher: the only figure the page computes.
function sectionOf(all: Block[], at: number): Block[] {
  let i = all.findIndex((x) => x.ln === at);
  let level = 7;
  let s = i;
  for (; s >= 0; s--) {
    const x = all[s];
    if (x.k === "h") {
      level = x.level;
      break;
    }
  }
  const out: Block[] = [];
  for (i = Math.max(s, 0) + (s >= 0 ? 1 : 0); i < all.length; i++) {
    const x = all[i];
    if (x.k === "h" && x.level <= level) break;
    out.push(x);
  }
  return out;
}
function countUnder(all: Block[], h: Extract<Block, { k: "h" }>): number {
  return sectionOf(all, h.ln)
    .filter((x): x is Extract<Block, { k: "list" }> => x.k === "list")
    .reduce((n, l) => n + l.items.filter((it) => isThing(it) && it.task !== "x" && it.task !== "-").length, 0);
}

// ---- Inline ----

function Inl({ s, ln }: { s: string; ln: number }) {
  return <Toks toks={tokens(s)} ln={ln} />;
}

function Toks({ toks, ln }: { toks: Tok[]; ln: number }) {
  const ctx = useMd();
  const { t, lang } = useWords();
  return (
    <>
      {toks.map((tk, i) => {
        switch (tk.t) {
          case "text":
            return <span key={i}>{inline(tk.s, { newTab: t("espace.a2.newtab"), wiki: ctx.wiki }, `${ln}.${i}`)}</span>;
          case "due": {
            const w = wall(tk.date);
            return (
              <time key={i} className="md-due a2-due" dateTime={tk.date}>
                <Icon name="calendar" /> {w ? when(lang, w) : tk.date}
              </time>
            );
          }
          case "ref":
            return <RefLink key={i} label={tk.label} id={tk.id} />;
          case "obj":
            return <RefLink key={i} label={tk.label} target={tk.target} />;
          case "act":
            if (tk.scheme === "card") return <InlineYes key={i} refText={tk.target} where={{ surfaceId: ctx.page, componentId: `line-${ln}` }} say />;
            if (tk.scheme === "ask")
              return (
                <button key={i} type="button" className="btn a2-button a2-ask a2-button-default md-ask-inline" onClick={() => ctx.ask(tk.label, ln)}>
                  {tk.label}
                </button>
              );
            return <span key={i} className="md-unplaced">{tk.label}</span>;
        }
      })}
    </>
  );
}

function RefLink({ label, id, target }: { label: string; id?: string; target?: string }) {
  const ctx = useMd();
  const r = id ? resolve(ctx.parsed, id) : target ? { target, obj: objects[target] ?? fakeObj(target) } : undefined;
  if (!r) return <span className="a2-wiki none" title={`[${id}] : pas de cible en bas de page`}>{label}</span>;
  if (/^https?:\/\//.test(r.target))
    return (
      <a href={r.target} target="_blank" rel="noopener noreferrer">
        {label}
      </a>
    );
  const lie = mismatch(label, r.obj);
  return (
    <button type="button" className={`a2-wiki md-ref${lie ? " ext" : ""}`} title={r.obj ? `${describe(r.obj)}${lie ? " ⚠ ne correspond pas aux mots de la page" : ""}` : r.target} onClick={(e) => (e.stopPropagation(), ctx.view({ target: r.target, label }))}>
      {label}
      {lie && <span aria-hidden="true"> ⚠</span>}
    </button>
  );
}

// ---- Lists: a line each, its indented lines its detail ----

// A list of things (tasks, references, dates, cards, details) is drawn as
// rows; any other list (decisions, notes) stays a plain list.
export const isThing = (it: Item) => it.task !== undefined || it.children.length > 0 || tokens(it.text).some((x) => x.t !== "text");
function ListView({ b }: { b: Extract<Block, { k: "list" }> }) {
  const ctx = useMd();
  const { t } = useWords();
  if (!b.items.some(isThing)) {
    const L = b.ordered ? "ol" : "ul";
    return (
      <div className="a2-markdown md-plainlist">
        <L>
          {b.items.map((it) => (
            <li key={it.ln}>{inline(it.text, { newTab: t("espace.a2.newtab"), wiki: ctx.wiki }, String(it.ln))}</li>
          ))}
        </L>
      </div>
    );
  }
  return <ul className="md-list a2-sec-rows">{b.items.map((it) => <ItemView key={it.ln} it={it} />)}</ul>;
}

// cardsOf: the card: links of an item's detail, drawn on its line.
function hoist(it: Item): { cards: { label: string; target: string }[]; rest: Block[] } {
  const cards: { label: string; target: string }[] = [];
  const rest: Block[] = [];
  for (const c of it.children) {
    if (c.k === "list") {
      const items = c.items.filter((x) => {
        const acts = onlyActs(x.text);
        if (acts && acts.every((a) => a.scheme === "card")) {
          cards.push(...acts);
          return false;
        }
        return true;
      });
      if (items.length) rest.push({ ...c, items });
    } else if (c.k === "p" && onlyActs(c.text)?.every((a) => a.scheme === "card")) cards.push(...onlyActs(c.text)!);
    else rest.push(c);
  }
  return { cards, rest };
}

function ItemView({ it }: { it: Item }) {
  const ctx = useMd();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (box.current) box.current.indeterminate = it.task === "/";
  }, [it.task]);
  const toks = tokens(it.text);
  const lineCards = toks.filter((x): x is Extract<Tok, { t: "act" }> => x.t === "act" && x.scheme === "card");
  const shown = toks.filter((x) => !(x.t === "act" && x.scheme === "card") && x.t !== "due");
  const due = toks.find((x): x is Extract<Tok, { t: "due" }> => x.t === "due");
  const { title, comment } = split(shown);
  const { cards, rest } = hoist(it);
  const refs = toks.flatMap((x) => (x.t === "ref" ? [resolve(ctx.parsed, x.id)] : x.t === "obj" ? [{ target: x.target, obj: objects[x.target] }] : [])).filter(Boolean) as { target: string; obj?: Obj }[];
  const mail = refs.find((r) => r.obj?.kind === "mail");
  const openable = rest.length > 0 || !!mail;
  const done = it.task === "x" || it.task === "-";
  return (
    <li className={`md-item${done ? " md-done" : ""}${it.task === "-" ? " md-dropped" : ""}${open ? " open" : ""}`}>
      <div className="a2-sec-row rich md-row" onClick={openable ? () => setOpen(!open) : undefined} role={openable ? "button" : undefined} aria-expanded={openable ? open : undefined}>
        {it.task !== undefined && (
          <input
            ref={box}
            type="checkbox"
            className="a2-tick"
            checked={it.task === "x"}
            disabled={it.task === "-"}
            title={it.task === "/" ? "En cours" : it.task === "-" ? "Abandonnée" : undefined}
            onClick={(e) => e.stopPropagation()}
            onChange={() => ctx.toggle(it.ln, it.task === "x" ? " " : "x")}
          />
        )}
        <div className="a2-sec-main">
          <span className="a2-sec-title md-title">
            <Toks toks={title} ln={it.ln} />
          </span>
          {comment.length > 0 && (
            <span className="a2-sec-sub md-comment">
              <Toks toks={comment} ln={it.ln} />
            </span>
          )}
        </div>
        {due && <Toks toks={[due]} ln={it.ln} />}
        {[...lineCards, ...cards].map((c) => (
          <span key={c.target} onClick={(e) => e.stopPropagation()}>
            <InlineYes refText={c.target} where={{ surfaceId: ctx.page, componentId: `line-${it.ln}` }} />
          </span>
        ))}
        {openable && <span className="a2-sec-chev" aria-hidden="true" />}
      </div>
      {open && (
        <div className="md-detail">
          <Blocks blocks={rest} all={rest} />
          {mail && (
            <button type="button" className="btn md-original" onClick={() => ctx.view({ target: mail.target, label: plain(it.text) })}>
              <Icon name="mail" /> Voir le mail original
            </button>
          )}
        </div>
      )}
    </li>
  );
}

// ---- Actions: do: (a card code composes), card: (one already proposed), ask: ----

function Acts({ acts, ln, all }: { acts: Extract<Tok, { t: "act" }>[]; ln: number; all: Block[] }) {
  const ctx = useMd();
  const asks = acts.filter((a) => a.scheme === "ask");
  return (
    <div className="md-acts">
      {acts.map((a, i) =>
        a.scheme === "do" ? (
          <DoCard key={i} tool={a.target} label={a.label} ln={ln} all={all} />
        ) : a.scheme === "card" ? (
          <div key={i} className="pg-card">
            <Card refText={a.target} surface={ctx.page} />
            <Settled refText={a.target} />
          </div>
        ) : null,
      )}
      {asks.length > 0 && <A2 comp={{ component: "Asks", items: asks.map((a) => a.label) }} asks={{ texts: asks.map((a) => a.label), ln }} />}
    </div>
  );
}

// Settled: a card once answered leaves (Card draws nothing then): the
// outcome, in the words page/Page.tsx's « settled » list uses.
function Settled({ refText, extra }: { refText: string; extra?: string }) {
  const src = useProposals();
  const moved = useMoved(src, [refText]);
  const [p, setP] = useState<ProposalView | null>(null);
  useEffect(() => {
    void src.read(refText).then(setP);
  }, [src, refText, moved]);
  if (!p || p.state === "pending") return null;
  const word = p.state === "rejected" ? "Refusé" : p.exec === "done" ? "Fait" : "En cours…";
  return (
    <section className="pg-settled md-settled">
      <ul>
        <li className={p.state === "rejected" ? "no" : "yes"}>
          <span className="pg-settled-word">{word}</span>
          <span className="pg-settled-text">
            {p.text}
            {extra ? ` · ${extra}` : ""}
          </span>
        </li>
      </ul>
    </section>
  );
}

const VERBS: Record<string, [string, string, string]> = {
  "mail/trash": ["Jeter", "Jeter le mail", "Mettre à la corbeille"],
  "mail/move": ["Archiver", "Archiver le mail", "Archiver"],
};

function DoCard({ tool, label, ln, all }: { tool: string; label: string; ln: number; all: Block[] }) {
  const ctx = useMd();
  const src = useProposals() as FakeProposals;
  const scheme = tool.split("/")[0];
  // The things referenced under this heading, up to the next of its level
  // or higher, still there.
  const section = sectionOf(all, ln);
  const found: { label: string; target: string; obj: Obj; item: Item }[] = [];
  const seen = new Set<string>();
  const walk = (bs: Block[]) => {
    for (const b of bs) {
      if (b.k === "list")
        for (const it of b.items) {
          for (const tk of tokens(it.text)) {
            const r = tk.t === "ref" ? resolve(ctx.parsed, tk.id) : undefined;
            if (tk.t === "ref" && r?.obj && r.target.startsWith(scheme + ":") && !seen.has(r.target)) {
              seen.add(r.target);
              found.push({ label: tk.label, target: r.target, obj: r.obj, item: it });
            }
          }
        }
    }
  };
  walk(section);
  const ref = `do:${ctx.page}:${ln}`;
  const [verb, one, sentence] = VERBS[tool] ?? ["Faire", "Faire", tool];
  const n = found.length;
  const lies = found.filter((f) => mismatch(f.label, f.obj));
  const p: ProposalView | null = n
    ? {
        ref,
        card: 1000 + ln,
        kind: "calls",
        state: "pending",
        verb: n === 1 ? one : `${verb} les ${n}`,
        acts: found.map((_, i) => (i === 0 ? one : `${verb} les ${i + 1}`)),
        text: `${sentence} ${n === 1 ? "ce mail" : `ces ${n} mails`} : ${found.map((f) => (f.obj.kind === "mail" ? f.obj.subject : describe(f.obj))).join(", ")}`,
        context: "compte perso",
        tainted: lies.length > 0,
        choices: found.map((f, i) => ({ id: i + 1, n: 1, state: "proposed", items: [f.target], text: describe(f.obj) })),
        fields: [{ name: tool, value: found.map((f) => f.target.slice(0, 28) + "…").join("\n"), detail: true, raw: true }],
      }
    : null;
  if (p) src.register(p);
  // Done: whoever holds the page rewrites it; here, the lines go.
  const moved = useMoved(src, [ref]);
  useEffect(() => {
    const v = src.peek(ref);
    if (v?.exec !== "done" || !found.length) return;
    const gone = new Set((v.choices ?? []).filter((c) => c.state !== "rejected").flatMap((c) => c.items ?? []));
    const lns: number[] = [];
    for (const f of found)
      if (gone.has(f.target)) {
        lns.push(...linesOf(f.item));
        const def = [...ctx.parsed.refs.values()].find((r) => r.target === f.target);
        if (def) lns.push(def.ln);
      }
    if (lns.length) ctx.removeLines(lns);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [moved]);
  return (
    <div className="md-do">
      {p && <Card refText={ref} surface={ctx.page} />}
      {lies.map((f) => (
        <p key={f.target} className="md-lie" role="note">
          <Icon name="warning" /> La page appelle « {f.label} » un mail de <b>{f.obj.kind === "mail" ? nameOf(f.obj.from) : ""}</b> : « {f.obj.kind === "mail" ? f.obj.subject : describe(f.obj)} ». La carte montre le mail, pas l'étiquette.
        </p>
      ))}
      <Settled refText={ref} extra={src.peek(ref)?.exec === "done" ? "les lignes ont quitté la page" : undefined} />
      {!p && !src.peek(ref) && <p className="md-note">« {label} » : aucun élément référencé sous ce titre, pas de carte.</p>}
    </div>
  );
}

function linesOf(it: Item): number[] {
  const out = [it.ln];
  const walk = (bs: Block[]) => {
    for (const b of bs) {
      out.push(b.ln);
      if (b.k === "list") for (const x of b.items) out.push(...linesOf(x));
      if (b.k === "code") for (let i = 0; i <= b.text.split("\n").length + 1; i++) out.push(b.ln + i);
      if (b.k === "p") for (let i = 0; i < b.text.split("\n").length; i++) out.push(b.ln + i);
    }
  };
  walk(it.children);
  return out;
}

// ---- Tables: the catalog's Table when the cells are plain ----

function TableView({ head, rows, title }: { head: string[]; rows: string[][]; title?: string }) {
  const { t } = useWords();
  const ctx = useMd();
  const rich = [...head, ...rows.flat()].some((c) => /[[*`]|\]\(/.test(c));
  if (rich) {
    const text = [`| ${head.join(" | ")} |`, `|${head.map(() => "---").join("|")}|`, ...rows.map((r) => `| ${r.join(" | ")} |`)].join("\n");
    return <Markdown text={text} newTab={t("espace.a2.newtab")} wiki={ctx.wiki} />;
  }
  const columns = head.map((h, c) => {
    const vals = rows.map((r) => r[c]);
    const nums = vals.map(frNumber);
    const units = new Set(vals.map(unitOf));
    const numeric = c > 0 && nums.every((n) => n !== undefined) && units.size === 1;
    const unit = [...units][0];
    const signed = numeric && unit === "%" && vals.some((v) => /^\s*[+−-]/.test(v));
    return { key: `c${c}`, label: h, numeric, unit: numeric && !signed ? unit : "", kind: signed ? "change" : numeric ? "number" : "text" };
  });
  const data = rows.map((r) => Object.fromEntries(columns.map((col, c) => [col.key, col.numeric ? frNumber(r[c]) : r[c]])));
  const units = Object.fromEntries(columns.filter((c) => c.unit).map((c) => [c.key, c.unit]));
  return <A2 comp={{ component: "Table", ...(title ? { title } : {}), columns: columns.map(({ key, label, kind }) => ({ key, label, kind })), rows: data, ...(Object.keys(units).length ? { units } : {}) }} />;
}

// ---- Callouts ----

function Callout({ b, all }: { b: Extract<Block, { k: "callout" }>; all: Block[] }) {
  const inner = <CalloutBody b={b} all={all} />;
  if (b.fold === "") return inner;
  return <Fold title={b.title || b.type} start={b.fold === "+"}>{inner}</Fold>;
}

function Fold({ title, start, children, count }: { title: ReactNode; start?: boolean; children: ReactNode; count?: number }) {
  const [open, setOpen] = useState(!!start);
  return (
    <div className={`a2-sec-fold md-fold${open ? " open" : ""}`}>
      <div className="a2-sec-fold-line">
        <button type="button" className="a2-sec-fold-head" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="a2-sec-chev" aria-hidden="true" />
          <span className="a2-sec-fold-name">{title}</span>
          {count !== undefined && <span className="a2-count">{count}</span>}
        </button>
      </div>
      {open && <div className="a2-sec-fold-body">{children}</div>}
    </div>
  );
}

const LEVEL: Record<string, string> = { warning: "warning", caution: "warning", attention: "warning", danger: "error", error: "error", failure: "error", bug: "error", success: "success", check: "success", done: "success", info: "info", todo: "info" };

function lines(bs: Block[]): Item[] {
  return bs.flatMap((x) => (x.k === "list" ? x.items : []));
}

function CalloutBody({ b }: { b: Extract<Block, { k: "callout" }>; all: Block[] }) {
  const title = b.title ? plain(b.title) : undefined;
  switch (b.type) {
    case "figures":
      return <A2 comp={{ component: "Figures", ...(title ? { title } : {}), items: lines(b.body).map((it) => figure(it.text)) }} />;
    case "chart": {
      const tb = b.body.find((x): x is Extract<Block, { k: "table" }> => x.k === "table");
      if (!tb) return <p className="md-note">Graphique sans tableau.</p>;
      const kind = b.mods.find((m) => ["line", "bar", "stack"].includes(m)) ?? "line";
      const heads = tb.head.map((h) => /^(.*?)\s*\(([^)]+)\)\s*$/.exec(h));
      const unit = heads.slice(1).find(Boolean)?.[2] ?? (/\(([^)]+)\)\s*$/.exec(b.title)?.[1] || undefined);
      const series = tb.head.slice(1).map((h, c) => ({
        name: heads[c + 1]?.[1] ?? h,
        points: tb.rows.flatMap((r) => {
          const y = frNumber(r[c + 1]);
          return y === undefined ? [] : [{ x: r[0], y }];
        }),
      }));
      return <A2 comp={{ component: "Chart", kind, ...(title ? { title: title.replace(/\s*\([^)]*\)\s*$/, "") } : {}), ...(unit ? { unit } : {}), series }} />;
    }
    case "steps":
      return <Steps title={title} items={lines(b.body)} />;
    case "agenda":
      return <AgendaView title={title} body={b.body} ln={b.ln} />;
    case "person": {
      const facts: { label: string; value: string }[] = [];
      let detail = "";
      for (const it of lines(b.body)) {
        const { title: l, comment: v } = split(tokens(it.text));
        const label = plain(l.map((x) => (x.t === "text" ? x.s : "")).join("")), value = plain(v.map((x) => (x.t === "text" ? x.s : x.t === "ref" || x.t === "obj" || x.t === "act" ? x.label : "")).join(""));
        if (/^r[ôo]le$/i.test(label)) detail = value;
        else if (label) facts.push({ label, value });
      }
      const note = b.body.filter((x) => x.k === "p").map((x) => plain((x as { text: string }).text)).join(" ");
      return <A2 comp={{ component: "Person", name: title ?? "", ...(detail ? { detail } : {}), ...(facts.length ? { facts } : {}), ...(note ? { note } : {}) }} />;
    }
  }
  const level = LEVEL[b.type];
  if (level) {
    const text = b.body.filter((x) => x.k === "p" && !onlyActs(x.text)).map((x) => plain((x as { text: string }).text)).join(" ");
    const rest = b.body.filter((x) => !(x.k === "p" && !onlyActs(x.text)));
    return (
      <>
        <A2 comp={{ component: "Alert", level, ...(title ? { title } : {}), text: text || title || "" }} />
        {rest.length > 0 && <Blocks blocks={rest} all={rest} />}
      </>
    );
  }
  // Any other callout (note, tip, an unknown type): a quiet box of its own
  // Markdown, as Obsidian draws it.
  return (
    <div className={`md-box md-box-${b.type}`}>
      {title && <p className="md-box-title">{title}</p>}
      <Blocks blocks={b.body} all={b.body} />
    </div>
  );
}

// figure: « Label : 18 385 € (▲ 1,8 % cette semaine) ».
export function figure(line: string): Record<string, unknown> {
  const { title, comment } = split(tokens(line));
  const label = plain(title.map((x) => (x.t === "text" ? x.s : x.t === "ref" ? x.label : "")).join(""));
  let rest = plain(comment.map((x) => (x.t === "text" ? x.s : "")).join(""));
  const f: Record<string, unknown> = { label };
  const d = /\(([▲▼+−-])\s*([\d\s ,.]+)\s*([^\s)]*)\s*([^)]*)\)\s*$/.exec(rest);
  let dUnit = "";
  if (d) {
    const n = frNumber(d[2]);
    if (n !== undefined) {
      f.delta = d[1] === "▼" || d[1] === "−" || d[1] === "-" ? -n : n;
      dUnit = d[3];
      f.deltaLabel = d[4].trim();
    }
    rest = rest.slice(0, d.index).trim();
  }
  const n = frNumber(rest);
  const unit = unitOf(rest);
  // Figures draws the delta with the value's unit: when the delta's unit
  // is another (« € » and « % »), the value keeps its unit in its text and
  // the delta's unit goes into its label.
  if (n !== undefined && (!d || !dUnit || dUnit === unit) && /^[^\d]{0,4}$/.test(unit)) {
    f.value = n;
    if (unit) f.unit = unit;
  } else {
    f.value = rest;
    if (dUnit) f.deltaLabel = `${dUnit} ${f.deltaLabel ?? ""}`.trim();
  }
  if (!f.deltaLabel) delete f.deltaLabel;
  return f;
}

// Steps: the catalog's Timeline, its markup copied (Timeline has no fold and
// a plain-text detail): each step's indented lines fold under it.
function Steps({ title, items }: { title?: string; items: Item[] }) {
  const { t, lang } = useWords();
  const state = (it: Item) => (it.task === "x" ? "done" : it.task === "/" ? "current" : it.task === "-" ? "failed" : "pending");
  const done = items.filter((it) => it.task === "x").length;
  const current = items.find((it) => it.task === "/");
  return (
    <div className="md-steps">
      {title && <BlockTitle text={title} />}
      <div className="a2-tl-prog">
        <p className="a2-tl-prog-words">
          <b>{t("espace.a2.progress.of", done, items.length)}</b>
          {current && <span> · {plain(current.text)}</span>}
        </p>
        <span className="a2-bar" role="img">
          {items.map((it, i) => (
            <span key={i} className={it.task === "x" ? "a2-seg-done" : it.task === "/" ? "a2-seg-now" : "a2-seg"} />
          ))}
        </span>
      </div>
      <ol className="a2-timeline">
        {items.map((it) => (
          <Step key={it.ln} it={it} st={state(it)} lang={lang} />
        ))}
      </ol>
    </div>
  );
}

function Step({ it, st }: { it: Item; st: string; lang: string }) {
  const [open, setOpen] = useState(false);
  const toks = tokens(it.text);
  const due = toks.find((x) => x.t === "due");
  const rest = toks.filter((x) => x.t !== "due");
  return (
    <li className={`a2-step a2-step-${st}${it.task === "-" ? " md-dropped" : ""}`}>
      <span className="a2-step-dot" aria-hidden="true">
        {st === "done" ? <Icon name="check" /> : st === "failed" ? <Icon name="close" /> : null}
      </span>
      <span className="a2-step-body">
        <span className={`a2-step-label${it.children.length ? " md-step-open" : ""}`} onClick={() => setOpen(!open)}>
          <Toks toks={rest} ln={it.ln} />
          {it.children.length > 0 && <span className="a2-sec-chev md-chev-s" aria-expanded={open} />}
        </span>
        {due && (
          <span className="a2-step-date">
            {" "}
            <Toks toks={[due]} ln={it.ln} />
          </span>
        )}
        {open && (
          <span className="a2-step-detail md-step-detail">
            <Blocks blocks={it.children} all={it.children} />
          </span>
        )}
      </span>
    </li>
  );
}

// Agenda: « > ### Jeudi 26 novembre » a day, « - 09:00–10:00 Title · place »
// an event; a card: link on an event (or in its detail) is its card.
function AgendaView({ title, body, ln }: { title?: string; body: Block[]; ln: number }) {
  const ctx = useMd();
  const events: Record<string, unknown>[] = [];
  const items: ListRow[] = [];
  let day: string | undefined;
  const days: string[] = [];
  for (const b of body) {
    if (b.k === "h") {
      day = dayOf(b.text, ctx.year);
      if (day) days.push(day);
    }
    if (b.k === "list" && day)
      for (const it of b.items) {
        const m = /^(\d{1,2})[:h](\d{2})(?:\s*[–-]\s*(\d{1,2})[:h](\d{2}))?\s+(.*)$/.exec(it.text.trim());
        const text = m ? m[5] : it.text;
        const toks = tokens(text);
        const ref = toks.find((x): x is Extract<Tok, { t: "ref" }> => x.t === "ref");
        const obj = ref ? resolve(ctx.parsed, ref.id)?.obj : undefined;
        const [name, place] = plain(text).split(/\s+·\s+/);
        const pad = (s: string) => s.padStart(2, "0");
        const ev: Record<string, unknown> = m ? { title: name, start: `${day}T${pad(m[1])}:${m[2]}:00`, ...(m[3] ? { end: `${day}T${pad(m[3])}:${m[4]}:00` } : {}) } : { title: name, start: day, allDay: true };
        if (place) ev.place = place;
        if (obj?.kind === "event" && obj.status) ev.status = obj.status;
        const card = [...toks, ...it.children.flatMap((c) => (c.k === "p" ? tokens(c.text) : c.k === "list" ? c.items.flatMap((x) => tokens(x.text)) : []))].find((x): x is Extract<Tok, { t: "act" }> => x.t === "act" && x.scheme === "card");
        if (card) items.push({ n: events.length, id: `ev${events.length}`, card: card.target });
        events.push(ev);
      }
  }
  if (!days.length) return <p className="md-note">Agenda sans jour lisible.</p>;
  const sorted = [...days].sort();
  const span = Math.round((Date.parse(sorted[sorted.length - 1]) - Date.parse(sorted[0])) / 86400000) + 1;
  return (
    <>
      {title && <BlockTitle text={title} />}
      <A2 comp={{ component: "Agenda", date: sorted[0], days: Math.min(7, span), events, proposals: false }} list={{ component: "root", source: `agenda-${ln}`, items }} />
    </>
  );
}

// ---- Embeds and the reader of a referenced thing ----

function Embed({ name }: { name: string }) {
  const ctx = useMd();
  const e = ctx.embed(name);
  if (!e) return <p className="md-note">![[{name}]] : aucun node de ce nom.</p>;
  if (ctx.depth > 2) return null;
  const open = parse(e.md).blocks.flatMap((b) => (b.k === "list" ? b.items : [])).filter((it) => it.task === " " || it.task === "/").length;
  return (
    <Fold
      title={
        <>
          <Icon name="note" /> {name}
        </>
      }
      count={open}
    >
      <div className="md-embed">
        <MdBody md={e.md} setMd={e.setMd} page={name} openNode={ctx.openNode} embed={ctx.embed} depth={ctx.depth + 1} />
      </div>
      <button type="button" className="linkish md-embed-open" onClick={() => ctx.openNode(name)}>
        Ouvrir {name}
      </button>
    </Fold>
  );
}

function Viewer({ v, onClose }: { v: { target: string; label: string }; onClose(): void }) {
  const o = objects[v.target] ?? fakeObj(v.target);
  const ex = (s: string) => s.split("\n").filter(Boolean).slice(0, 2).join(" ");
  return (
    <div className="md-viewer-back" onClick={onClose}>
      <div className="md-viewer" role="dialog" onClick={(e) => e.stopPropagation()}>
        <p className="md-viewer-head">
          <span>{o?.kind === "mail" ? "Le mail original (fictif)" : o?.kind === "event" ? "L'événement (fictif)" : o?.kind === "pr" ? "La pull request (fictive)" : "Référence"}</span>
          <button type="button" className="pg-x" onClick={onClose} aria-label="Fermer">
            <Icon name="close" />
          </button>
        </p>
        <p className="md-viewer-id">
          La page dit « {v.label} » · <code>{v.target.length > 40 ? v.target.slice(0, 40) + "…" : v.target}</code>
        </p>
        {o?.kind === "mail" && <A2 comp={{ component: "Message", from: o.from, subject: o.subject, date: o.date, excerpt: ex(o.body), body: o.body }} />}
        {o?.kind === "event" && <A2 comp={{ component: "Item", title: o.title, detail: o.place ?? "", note: `${o.start.slice(0, 16).replace("T", " ")}${o.status === "needs-action" ? " · invitation sans réponse" : ""}${o.organizer ? ` · de ${o.organizer}` : ""}` }} />}
        {o?.kind === "pr" && (
          <>
            <A2 comp={{ component: "Item", title: o.title, note: `${o.state} · ${o.checks}` }} />
            <A2 comp={{ component: "Link", label: "Ouvrir la pull request", url: o.url }} />
          </>
        )}
        {!o && <p className="md-note">Rien de connu pour cette cible.</p>}
      </div>
    </div>
  );
}

export { parse };
