import { stillAsks, type ListRow, type Moved, type Opened, type PageBlock, type PagePart, type ProposalView, type SourceList, type Tick, type TickAction } from "./api";

// A small make-believe eriphe-core for the dev server (sample.ts): a few
// view nodes kept as their author writes them in the node's file (text and
// eriphe:block JSON: components and sources; their sources' values
// and their items' notes as code keeps them), and what code does with
// them, done here the way eriphe-core does it in Go:
//   - the block's data: each source's value at /<name>, each item with its
//     note's fields (_note, _state, _group) and, while a card waits on it,
//     _card (item_cards.go);
//   - the Lists the page must know (list_rows.go, list_sections.go):
//     their filter applied, their template path unfiltered, their rows,
//     each with the card that waits on it;
//   - an item opened in place (item_open.go);
//   - a card answered (one item's, or a batch's over many, its rows
//     unticked left out): its items settle and leave their section, and
//     the view's skill writes its bilan again.
// Nothing leaves the page. No real name, address or tool in it.

export type Src = {
  tool: string;
  args?: Record<string, unknown>;
  items: { path: string; id: string; title: string; detail?: string; open?: { tool: string; id: string } };
};
export type Authored = { name: string; external?: boolean; components: Record<string, unknown>[]; data?: Record<string, unknown>; sources?: Record<string, Src> };
export type Note = { group?: string; note?: string; state?: string };
export type SampleView = {
  id: string;
  // the node's body: its text (the skill's bilan, its digest) and blocks
  parts: (string | Authored)[];
  // the sources' last values, by name
  values: Record<string, Record<string, unknown>>;
  notes: Record<string, Note>;
  // what opening an item reads
  bodies: Record<string, Opened>;
  // the cards: those on items (items), the others the node's
  cards: SampleCard[];
  // the view's skill writing its bilan again, from what is left
  bilan?: (v: SampleView) => string;
  // Prototype « Eriphe coche »: the rows Eriphe ticked, by item, and the
  // actions its Lists name (list_rows.go would tell them, as it tells cards).
  ticks?: Record<string, Tick>;
  actions?: Record<string, TickAction>;
};
export type SampleCard = ProposalView & { items?: string[]; settled?: boolean };

const followers = new Set<(m: Moved) => void>();
const bumpers = new Set<(id: string) => void>();
let views: SampleView[] = [];

export function setViews(vs: SampleView[]) {
  views = vs;
}

export const viewOf = (id: string) => views.find((v) => v.id === id);

// onBump calls back when a view changed (eriphe-core's "doc" event).
export function onBump(f: (id: string) => void): () => void {
  bumpers.add(f);
  return () => bumpers.delete(f);
}
const bump = (id: string) => bumpers.forEach((f) => f(id));
const moved = (refs: string[]) => followers.forEach((f) => f({ refs }));

// A filter of a List's path: [field=value] or [field!=value], one or more.
type Filter = { field: string; value: string; not: boolean };
function parsePath(path: string): { base: string; filters: Filter[] } {
  const filters: Filter[] = [];
  const base = path.replace(/\[([A-Za-z0-9_]+)(!?=)([^\]]*)\]/g, (_, field: string, op: string, value: string) => {
    filters.push({ field, value, not: op === "!=" });
    return "";
  });
  return { base, filters };
}
const keeps = (it: Record<string, unknown>, fs: Filter[]) => fs.every((f) => (String(it[f.field] ?? "") === f.value) !== f.not);

const at = (v: unknown, path: string): unknown => path.split("/").filter(Boolean).reduce<unknown>((o, k) => (o && typeof o === "object" ? (o as Record<string, unknown>)[k] : undefined), v);

// itemsOf are a source's items, each with its note's fields and its card.
function itemsOf(v: SampleView, name: string, src: Src): Record<string, unknown>[] {
  const list = (at(v.values[name], src.items.path) as Record<string, unknown>[] | undefined) ?? [];
  return list.map((it) => {
    const id = String(it[src.items.id]);
    const n = v.notes[id] ?? {};
    const card = v.cards.find((c) => c.items?.includes(id) && !c.settled);
    return { ...it, _note: n.note ?? "", _state: n.state ?? "", _group: n.group ?? "", _card: card?.ref ?? "" };
  });
}

function block(v: SampleView, b: Authored): PageBlock {
  const data: Record<string, unknown> = { ...(b.data ?? {}) };
  const comps = b.components.map((c) => ({ ...c }));
  const folded = new Set(comps.filter((c) => c.component === "Fold" && typeof c.list === "string").map((c) => String(c.list)));
  const lists: SourceList[] = [];
  for (const [name, src] of Object.entries(b.sources ?? {})) {
    const items = itemsOf(v, name, src);
    const value = structuredClone(v.values[name]);
    const holder = at(value, src.items.path.split("/").slice(0, -1).join("/")) ?? value;
    (holder as Record<string, unknown>)[src.items.path.split("/").at(-1)!] = items;
    data[name] = value;
    for (const c of comps) {
      // An Agenda bound to the source's items: its events' rows, each with
      // the card that waits on it (an invitation's), as for a List.
      const evs = c.events as { "@path"?: string } | undefined;
      if (c.component === "Agenda" && evs?.["@path"] === `/${name}/${src.items.path}`) {
        const rows: ListRow[] = items.map((it, n) => ({ n, id: String(it[src.items.id]), title: String(it[src.items.title] ?? ""), card: (it._card as string) || undefined }));
        lists.push({ component: String(c.id), source: name, items: rows });
        continue;
      }
      const ch = c.children as { path?: string; componentId?: string } | undefined;
      if (c.component !== "List" || !ch?.path) continue;
      const { base, filters } = parsePath(ch.path);
      if (base !== `/${name}/${src.items.path}`) continue;
      // code filters, the page draws the List over all the source's items
      c.children = { ...ch, path: base };
      // a tick, like a card, makes the List one the page is told of
      const carded = items.some((it) => it._card || tickOf(v, String(it[src.items.id]), it));
      if (!filters.length && !folded.has(String(c.id)) && !carded && !src.items.open) continue;
      const rows: ListRow[] = [];
      items.forEach((it, n) => {
        if (!keeps(it, filters)) return;
        const id = String(it[src.items.id]);
        rows.push({ n, id, title: String(it[src.items.title] ?? ""), detail: src.items.detail ? String(it[src.items.detail] ?? "") : undefined, card: (it._card as string) || undefined, tick: tickOf(v, id, it) });
      });
      lists.push({ component: String(c.id), source: name, filtered: filters.length > 0, open: !!src.items.open, items: rows, actions: rows.some((r) => r.tick) ? v.actions : undefined });
    }
  }
  return { name: b.name, external: b.external, components: comps, data, lists };
}

export function samplePage(id: string): { parts: PagePart[]; cards: ProposalView[]; settled: ProposalView[] } | null {
  const v = viewOf(id);
  if (!v) return null;
  // the node's own cards, as eriphe-core splits them: those that still ask
  // at the foot, the decided ones for the « ⋯ » menu; those on items show
  // on their lines, and a dated one that still asks in the page's Agenda,
  // as a ghost with its Oui (eriphe-core checks the Agenda draws its day;
  // the samples' cards are all within the week)
  const agenda = v.parts.some((p) => typeof p !== "string" && p.components.some((c) => c.component === "Agenda"));
  const own = v.cards.filter((c) => !c.items && !(agenda && c.when?.length && stillAsks(c)));
  return {
    parts: v.parts.map((p) => (typeof p === "string" ? { text: p } : { block: block(v, p) })),
    cards: own.filter(stillAsks),
    settled: own.filter((c) => !stillAsks(c)),
  };
}

export async function sampleOpen(id: string, item: string): Promise<Opened> {
  await new Promise((r) => setTimeout(r, 250));
  const v = viewOf(id);
  const o = v?.bodies[item];
  const n = v?.notes[item];
  return o ? { ...o, note: n ? { group: n.group, note: n.note, external: true } : undefined } : { error: "Introuvable." };
}

// later: the view's skill runs again a moment after (the monitor saw the
// change) and writes its bilan from what is left.
function later(v: SampleView) {
  if (!v.bilan) return;
  setTimeout(() => {
    const i = v.parts.findIndex((p) => typeof p === "string");
    if (i >= 0) v.parts[i] = v.bilan!(v);
    bump(v.id);
  }, 1200);
}

// The views' cards as proposals: answered, a card on items runs (then is
// done), and its items settle: code notes them done (those of the calls
// left, unticked, stay), and the List that keeps what is not done no
// longer shows them.
export function sampleCards(): {
  read(ref: string): ProposalView | null;
  answer(ref: string, yes: boolean, only?: number[]): boolean;
  day(date: string): ProposalView[];
  follow(on: (m: Moved) => void): () => void;
} {
  const find = (ref: string) => {
    for (const v of views) {
      const c = v.cards.find((x) => x.ref === ref);
      if (c) return { v, c };
    }
    return null;
  };
  return {
    read: (ref) => {
      const f = find(ref);
      return f ? { ...f.c } : null;
    },
    answer: (ref, yes, only) => {
      const f = find(ref);
      if (!f || f.c.state !== "pending") return false;
      f.c.state = yes ? "approved" : "rejected";
      if (yes) f.c.exec = "running";
      const going = only && f.c.choices ? f.c.choices.filter((ch) => only.includes(ch.id)).flatMap((ch) => ch.items ?? []) : (f.c.items ?? []);
      moved([ref]);
      setTimeout(() => {
        if (yes) f.c.exec = "done";
        f.c.settled = true;
        if (yes) for (const it of going) f.v.notes[it] = { ...(f.v.notes[it] ?? {}), state: "done" };
        moved([ref]);
        bump(f.v.id);
        later(f.v);
      }, 900);
      return true;
    },
    // the cards dated on a day that still wait: an Agenda's ghosts
    day: (date) => {
      const dated = views.flatMap((v) => v.cards).filter((c) => c.state === "pending" && c.when?.some((w) => w.start.startsWith(date)));
      return dated.filter((c, i) => dated.findIndex((d) => d.ref === c.ref) === i).map((c) => ({ ...c }));
    },
    follow: (on) => {
      followers.add(on);
      return () => followers.delete(on);
    },
  };
}

// --- Prototype « Eriphe coche, tu cliques » (proto/eriphe-coche) ---------

// tickOf is the tick Eriphe left on an item still there, code's: told of
// the row (ListRow.tick), never put in the block's data.
function tickOf(v: SampleView, id: string, it: Record<string, unknown>): Tick | undefined {
  return it._state === "done" ? undefined : v.ticks?.[id];
}

export type SourceEv = { node: string; block: string; name: string; value: unknown; lists: SourceList[] };
const sourcers = new Set<(ev: SourceEv) => void>();
// onSource calls back with a source's new value and its Lists' rows, as
// eriphe-core's "source" event: the page changes them in place.
export function onSource(f: (ev: SourceEv) => void): () => void {
  sourcers.add(f);
  return () => sourcers.delete(f);
}

// pushSources sends every source of a view again (a new mail, a tick, a
// run): the page draws them without being read again.
export function pushSources(v: SampleView) {
  for (const p of v.parts) {
    if (typeof p === "string" || !p.sources) continue;
    const b = block(v, p);
    for (const name of Object.keys(p.sources)) {
      const ev = { node: v.id, block: b.name, name, value: b.data?.[name], lists: (b.lists ?? []).filter((l) => l.source === name) };
      sourcers.forEach((f) => f(ev));
    }
  }
}

// bumpView has a view read again (eriphe-core's "doc" event).
export const bumpView = (id: string) => bump(id);
// movedCards says cards moved (made, folded), as the stream does.
export const movedCards = (refs: string[]) => moved(refs);

// sampleRun is an action's calls, one per account: their items are done
// (they leave the Lists that keep what is not), their ticks go; the
// view's skill writes its bilan again a moment later.
export async function sampleRun(node: string, _action: string, calls: { account: string; ids: string[] }[]): Promise<number> {
  const v = viewOf(node);
  if (!v?.ticks) return -1;
  const ticks = v.ticks;
  await new Promise((r) => setTimeout(r, 700));
  let n = 0;
  for (const c of calls)
    for (const id of c.ids) {
      if (!ticks[id]) continue;
      v.notes[id] = { ...(v.notes[id] ?? {}), state: "done" };
      delete ticks[id];
      n++;
    }
  pushSources(v);
  later(v);
  return n;
}
