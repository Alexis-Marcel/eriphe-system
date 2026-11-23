import { motion } from "motion/react";
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Markdown, type Wiki } from "../a2ui/markdown";
import { KINDS, PAIRS, Skeleton, type Ctx, type Feed, type Row } from "./blocks";
import { articleNotes, articles, type After, type Scenario } from "./scenarios";

// The page as the prototype draws it: the node's text and its blocks, one
// component each. Layout and hierarchy come from the order and kind of
// the blocks, never from the block: the first paragraph is the lead, a
// short Markdown list becomes chips, a first Figures is the hero, two
// small blocks in a row sit side by side on a wide page. Blocks come in a
// soft stagger that lands within 250 ms; nothing waits for it.

export type State = "live" | "loading" | "empty" | "error";

const EASE = [0.2, 0, 0, 1] as const;
const wiki: Wiki = {
  find: (name) => ({ id: name, title: name, named: true }),
  open: () => {},
  none: "",
  archived: (t) => t,
  external: "",
};

// Text: the page's own Markdown renderer; a list of short items (a fridge,
// a packing list) is drawn as chips, by the renderer alone.
function Text({ text }: { text: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelectorAll("ul").forEach((ul) => {
      const lis = [...ul.children];
      if (lis.length >= 4 && lis.every((li) => (li.textContent ?? "").length <= 24)) ul.classList.add("bv-chiplist");
    });
  }, [text]);
  return (
    <div ref={ref} className="bv-text">
      <Markdown text={text} newTab="nouvel onglet" wiki={wiki} />
    </div>
  );
}

// feedOf: what a block's first source gives, as code would hand it: its
// items with their note's fields and the card waiting on them; or a node
// list; or a state the prototype is asked to show.
function feedOf(s: Scenario, b: After, state: State, done: Set<string>, extra: Row[], values: Record<string, unknown>): Feed | undefined {
  const [name, src] = Object.entries(b.sources ?? {})[0] ?? [];
  if (!src) return undefined;
  const base: Feed = { every: src.every, items: src.items };
  if (state === "loading") return { ...base, loading: true };
  const v = values[name] as Record<string, unknown> | undefined;
  let rows: Row[] = [];
  if (src.items) {
    const list = ((v?.[src.items.path] as Row[]) ?? []).concat(name === "mails" ? extra : []);
    rows = list.map((it) => {
      const id = String(it[src.items!.id]);
      const n = s.notes[id] ?? articleNotes[id] ?? (it._note ? { note: String(it._note), group: String(it._group), state: String(it._state) } : {});
      const card = s.cards.find((c) => c.items?.includes(id));
      return { ...it, _note: n.note ?? "", _group: n.group ?? "", _state: done.has(id) ? "done" : (n.state ?? ""), _card: (it._card as string) ?? card?.ref ?? "", _cardText: card?.text ?? (it._card ? "Envoyer la réponse à Léa · compte perso" : "") };
    });
  } else rows = (v?.nodes as Row[]) ?? [];
  if (state === "empty") return { ...base, rows: [] };
  if (state === "error") return rows.length ? { ...base, rows, error: "Lecture échouée il y a 3 min : voici la dernière." } : { ...base, error: `Je n'arrive pas à lire « ${name} » pour l'instant.` };
  return { ...base, rows };
}

// What the dock's « simulate » buttons change.
export type Sim = { bump: number; arrived: boolean; step: number; first: boolean };

function figuresMoved(p: Row, bump: number): Row {
  if (!bump || !Array.isArray(p.items)) return p;
  const k = [0.011, -0.007, 0.004][bump % 3];
  return {
    ...p,
    items: (p.items as Row[]).map((f, i) =>
      typeof f.value === "number" && f.unit === "€" && i < 3 ? { ...f, value: Math.round((f.value as number) * (1 + k * (i === 2 ? 0.2 : 1))), delta: Math.round(((f.delta as number) ?? 0) + (f.value as number) * k) } : f,
    ),
  };
}

function stepped(p: Row, step: number): Row {
  if (!step || !Array.isArray(p.items)) return p;
  const items = p.items as { state: string }[];
  const cur = items.findIndex((s) => s.state === "current");
  const at = Math.min(items.length, cur + step);
  return { ...p, items: items.map((s, i) => ({ ...s, state: i < at ? "done" : i === at ? "current" : "pending" })) };
}

const newMail: Row = { id: "n1", account: "perso", from: "Léa <lea@example.org>", subject: "Dîner samedi ?", date: new Date().toISOString(), _note: "Répondre à Léa — brouillon prêt", _group: "À traiter", _state: "act", _card: "calls:199" };

export function AfterPage({ s, state, sim, replay }: { s: Scenario; state: State; sim: Sim; replay: number }) {
  const [done, setDone] = useState<Set<string>>(() => new Set());
  useEffect(() => setDone(new Set()), [s.id, replay]);
  const values = useMemo(() => (s.id === "premiere" && sim.first ? { lectures: { articles } } : s.values), [s, sim.first]);
  const onDone = (ids: string[]) => setDone((d) => new Set([...d, ...ids]));
  const blocks = s.after;
  const n = blocks.length;
  const step = n > 1 ? Math.min(0.03, 0.09 / (n - 1)) : 0;
  const firstBlock = blocks.findIndex((b) => typeof b !== "string");

  const draw = (b: string | After, i: number): ReactNode => {
    if (typeof b === "string") return <Text text={b} />;
    const kind = String(b.components.component);
    const Comp = KINDS[kind];
    let p = b.components as Row;
    if (kind === "Figures") p = figuresMoved(p, sim.bump);
    if (kind === "Timeline") p = stepped(p, sim.step);
    const ctx: Ctx = { feed: feedOf(s, b, state, done, sim.arrived ? [newMail] : [], values), onDone, hero: kind === "Figures" && i === firstBlock };
    // a block with no source shows its loading skeleton too: the page is read as a whole
    const body = state === "loading" && !b.sources && kind !== "Asks" ? <Skeleton kind={kind} /> : Comp ? <Comp p={p} ctx={ctx} /> : null;
    return (
      <section className={`bv-block k-${kind}${b.external ? " ext" : ""}`} data-block={b.name}>
        {b.external && <span className="bv-ext" title="Des mots d'un autre" aria-label="Des mots d'un autre" />}
        {body}
      </section>
    );
  };

  // two small blocks in a row sit side by side (CSS decides when it is wide enough)
  const groups: { at: number; items: (string | After)[] }[] = [];
  blocks.forEach((b, i) => {
    const prev = groups.at(-1);
    const small = typeof b !== "string" && PAIRS.has(String(b.components.component));
    const prevSmall = prev && prev.items.length === 1 && typeof prev.items[0] !== "string" && PAIRS.has(String((prev.items[0] as After).components.component));
    if (small && prevSmall && prev.at === i - 1) prev.items.push(b);
    else groups.push({ at: i, items: [b] });
  });

  return (
    <div className="bv-doc" key={`${s.id}-${replay}`}>
      {groups.map((g) => {
        const inner = g.items.map((b, k) => (
          <motion.div
            key={g.at + k}
            className="bv-piece"
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: (g.at + k) * step, duration: 0.16, ease: EASE }}
          >
            {draw(b, g.at + k)}
          </motion.div>
        ));
        return g.items.length > 1 ? (
          <div className="bv-pair" key={g.at}>
            {inner}
          </div>
        ) : (
          <Fragment key={g.at}>{inner}</Fragment>
        );
      })}
    </div>
  );
}
