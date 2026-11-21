import type { Moved, Opened, PageBlock, PagePart, PickAction, PickItem, PickList, ProposalView } from "./api";

// A small make-believe eriphe-core for the dev server (sample.ts): a few
// view nodes kept as their author writes them in the node's file (text and
// eriphe:block JSON: components, sources, actions; their sources' values
// and their items' notes as code keeps them), and what code does with
// them, done here the way eriphe-core does it in Go:
//   - the block's data: each source's value at /<name>, each item with its
//     note's fields (_note, _state, _group) and, while a card waits on it,
//     _card (item_cards.go);
//   - the Lists the page must know (list_actions.go, list_sections.go):
//     their filter applied, their template path unfiltered, their rows,
//     the actions on them in code's words;
//   - an action run on picked ids (web_list_actions.go): one call per
//     account when the action takes an argument from each item;
//   - an item opened in place (item_open.go);
//   - a card answered: its item settles and leaves its section, and the
//     view's skill writes its bilan again.
// Nothing leaves the page. No real name, address or tool in it.

export type Src = {
  tool: string;
  args?: Record<string, unknown>;
  items: { path: string; id: string; title: string; detail?: string; open?: { tool: string; id: string } };
};
export type Act = { label?: string; tool: string; args?: Record<string, unknown>; items: string; source: string; list?: string };
export type Authored = { name: string; external?: boolean; components: Record<string, unknown>[]; data?: Record<string, unknown>; sources?: Record<string, Src>; actions?: Record<string, Act> };
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
  // the cards: those on an item (item), the others the node's
  cards: (ProposalView & { item?: string; settled?: boolean })[];
  // the view's skill writing its bilan again, from what is left
  bilan?: (v: SampleView) => string;
  // code's words for an action's buttons, by the action's tool
  words: Record<string, { one: string; many: string; all: string; done: (n: number) => string; confirm?: boolean }>;
};

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
    const card = v.cards.find((c) => c.item === id && !c.settled);
    return { ...it, _note: n.note ?? "", _state: n.state ?? "", _group: n.group ?? "", _card: card?.ref ?? "" };
  });
}

function block(v: SampleView, b: Authored): PageBlock {
  const data: Record<string, unknown> = { ...(b.data ?? {}), marks: {} };
  const comps = b.components.map((c) => ({ ...c }));
  const lists: PickList[] = [];
  for (const [name, src] of Object.entries(b.sources ?? {})) {
    const items = itemsOf(v, name, src);
    const value = structuredClone(v.values[name]);
    const holder = at(value, src.items.path.split("/").slice(0, -1).join("/")) ?? value;
    (holder as Record<string, unknown>)[src.items.path.split("/").at(-1)!] = items;
    data[name] = value;
    for (const c of comps) {
      const ch = c.children as { path?: string; componentId?: string } | undefined;
      if (c.component !== "List" || !ch?.path) continue;
      const { base, filters } = parsePath(ch.path);
      if (base !== `/${name}/${src.items.path}`) continue;
      // code filters, the page draws the List over all the source's items
      c.children = { ...ch, path: base };
      const actions = Object.entries(b.actions ?? {})
        .filter(([, a]) => a.source === name && (!a.list || a.list === c.id))
        .map(([an, a]): PickAction => {
          const w = v.words[a.tool];
          return { name: an, one: w.one, many: w.many, all: w.all, tool: a.tool, args: argLines(a, items), confirm: w.confirm };
        });
      if (!filters.length && !actions.length && !src.items.open) continue;
      const rows: PickItem[] = [];
      items.forEach((it, n) => {
        if (!keeps(it, filters)) return;
        const id = String(it[src.items.id]);
        rows.push({ n, id, title: String(it[src.items.title] ?? ""), detail: src.items.detail ? String(it[src.items.detail] ?? "") : undefined, card: (it._card as string) || undefined });
      });
      lists.push({ component: String(c.id), source: name, filtered: filters.length > 0, open: !!src.items.open, items: rows, actions });
    }
  }
  return { name: b.name, external: b.external, components: comps, data, lists };
}

// argLines are an action's fixed args as the confirm says them; one taken
// from each item says which values the items have.
function argLines(a: Act, items: Record<string, unknown>[]): string[] {
  return Object.entries(a.args ?? {}).map(([k, val]) => {
    const field = (val as { "@item"?: string } | null)?.["@item"];
    if (!field) return `${k} : ${String(val)}`;
    const count = new Map<string, number>();
    for (const it of items) count.set(String(it[field]), (count.get(String(it[field])) ?? 0) + 1);
    return `${k} : ${[...count.keys()].join(", ")} (${count.size === 1 ? "un appel" : `un appel par ${k}`})`;
  });
}

// stillAsks tells whether a card still asks something of the user on its
// page (pendingAsks, web_graph.go): it waits, or its yes runs or failed.
const stillAsks = (c: ProposalView) => c.state === "pending" || (c.state === "approved" && (c.exec === "running" || c.exec === "failed"));

export function samplePage(id: string): { parts: PagePart[]; cards: ProposalView[]; settled: ProposalView[] } | null {
  const v = viewOf(id);
  if (!v) return null;
  // the node's own cards, as eriphe-core splits them: those that still ask
  // at the foot, the decided ones for the « ⋯ » menu; those on an item show
  // on its line
  const own = v.cards.filter((c) => !c.item);
  return {
    parts: v.parts.map((p) => (typeof p === "string" ? { text: p } : { block: block(v, p) })),
    cards: own.filter(stillAsks),
    settled: own.filter((c) => !stillAsks(c)),
  };
}

function findList(v: SampleView, blockName: string, list: string): { b: Authored; src: Src; name: string } | null {
  const b = v.parts.find((p): p is Authored => typeof p !== "string" && p.name === blockName);
  if (!b) return null;
  const c = b.components.find((x) => x.id === list) as { children?: { path?: string } } | undefined;
  const { base } = parsePath(c?.children?.path ?? "");
  for (const [name, src] of Object.entries(b.sources ?? {})) if (base === `/${name}/${src.items.path}`) return { b, src, name };
  return null;
}

// act runs an action on the ids the user picked, as web_list_actions.go:
// each id one the List shows now, one call per value of an argument taken
// from each item; the items go from the source (a mail put in the trash,
// an alert acknowledged) and the view's skill writes its bilan again.
export async function sampleAct(id: string, blockName: string, list: string, action: string, ids: string[]): Promise<{ ok: boolean; text: string }> {
  const v = viewOf(id);
  const found = v && findList(v, blockName, list);
  const a = found?.b.actions?.[action];
  if (!v || !found || !a) return { ok: false, text: "Cette liste n'a pas cette action." };
  await new Promise((r) => setTimeout(r, 450));
  const holder = at(v.values[found.name], found.src.items.path.split("/").slice(0, -1).join("/")) ?? v.values[found.name];
  const key = found.src.items.path.split("/").at(-1)!;
  const all = (holder as Record<string, Record<string, unknown>[]>)[key];
  (holder as Record<string, unknown>)[key] = all.filter((it) => !ids.includes(String(it[found.src.items.id])));
  bump(id);
  later(v);
  return { ok: true, text: v.words[a.tool].done(ids.length) };
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

// The views' cards as proposals: answered, a card on an item runs (then is
// done), and its item settles: code marks it done, and the List that
// keeps what is not done no longer shows it.
export function sampleCards(): { read(ref: string): ProposalView | null; answer(ref: string, yes: boolean): boolean; follow(on: (m: Moved) => void): () => void } {
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
    answer: (ref, yes) => {
      const f = find(ref);
      if (!f || f.c.state !== "pending") return false;
      f.c.state = yes ? "approved" : "rejected";
      if (yes) f.c.exec = "running";
      moved([ref]);
      setTimeout(() => {
        if (yes) f.c.exec = "done";
        f.c.settled = true;
        if (f.c.item) f.v.notes[f.c.item] = { ...(f.v.notes[f.c.item] ?? {}), state: "done" };
        moved([ref]);
        bump(f.v.id);
        later(f.v);
      }, 900);
      return true;
    },
    follow: (on) => {
      followers.add(on);
      return () => followers.delete(on);
    },
  };
}
