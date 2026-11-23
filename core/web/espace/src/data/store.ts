import { createStore, type StoreApi } from "zustand/vanilla";
import { useStore } from "zustand";
import type { GraphNode, Moved, Note, OrbClosed, OrbFeed, OrbWords, ListRow, Theme, View, Waiting } from "./api";
import { DEFAULT_FORCES, forcesFrom, type Forces } from "../graph/layout";

// The workspace's state: the graph as eriphe-core streams it (stream.ts),
// and what the user has open. The graph is core's: the page never changes a
// node by itself, it shows what the stream says. What is open is the
// page's: the pages open in the graph, and the one the bar talks to
// (talk), the home when none; the theme, the graph's forces and whether
// the open node is its centre (the settings, hud/Settings.tsx).
// eriphe-core keeps those too (web_gestures.go): the boot gives them back
// (restore), on any of the user's screens.
//
// Each event of the stream is a pure step (applyX), tested on its own;
// the store only holds the result.

export type Progress = { text: string; at: string };
export type Shown = { node: string; by?: string; at?: string; seq: number; talk?: boolean };

export type State = {
  nodes: Record<string, GraphNode>;
  // bumped at each new version (docs) or each trace that grew (traces): a
  // page shown reads its node again
  docs: Record<string, number>;
  traces: Record<string, number>;
  progress: Record<string, Progress>;
  // the sources' last values, by node, block and name
  sources: Record<string, unknown>;
  // the rows of the Lists the page must know (those that filter a
  // source's items, open them, a batch folds, a card waits on), by node, block and the
  // List's component id (list_rows.go, list_sections.go)
  rows: Record<string, ListRow[]>;
  open: string[];
  talk: string | null;
  shown: number; // the last show applied
  pending: Shown | null; // a show of a node the graph does not have yet, applied when it comes
  orb: OrbFeed | null; // what Eriphe's orb shows (orb/)
  sent: Sent | null; // what the user just sent, until eriphe-core has it
  notes: Note[];
  moved: Moved & { n: number }; // the proposals that moved last, n growing each time
  live: boolean; // the stream is open
  theme: Theme; // "" follows the system
  closed: OrbClosed; // what the user closed of the orb's
  forces: Forces; // the graph's forces (graph/layout.ts): the defaults and what the user changed
  centring: boolean; // the open node becomes the graph's centre
};

export const empty = (): State => ({
  nodes: {},
  docs: {},
  traces: {},
  progress: {},
  sources: {},
  rows: {},
  open: [],
  talk: null,
  shown: 0,
  pending: null,
  orb: null,
  sent: null,
  notes: [],
  moved: { n: 0, refs: [] },
  live: false,
  theme: "",
  closed: { answer: 0, seen: 0, wait: 0, failed: 0 },
  forces: { ...DEFAULT_FORCES },
  centring: true,
});

// restore is the workspace as the user left it: its pages open (kept when
// their nodes come), the one the bar talked to, the last show taken
// (one the user saw is not shown again), the theme, what the user closed
// of the orb's (not shown again either), the graph's forces (each within
// its range, forcesFrom) and its centring.
export function restore(s: State, v: View | undefined): State {
  const open = Array.isArray(v?.open) ? v.open.filter((id) => typeof id === "string") : [];
  const talk = v?.talk && open.includes(v.talk) ? v.talk : null;
  const theme: Theme = v?.theme === "light" || v?.theme === "dark" ? v.theme : "";
  const shown = typeof v?.shown === "number" && v.shown > 0 ? v.shown : s.shown;
  const n = (k: keyof OrbClosed) => (typeof v?.orb?.[k] === "number" && v.orb[k] > 0 ? v.orb[k] : 0);
  const closed = { answer: n("answer"), seen: n("seen"), wait: n("wait"), failed: n("failed") };
  return { ...s, open, talk, theme, shown, closed, forces: forcesFrom(v?.forces), centring: v?.centring !== false };
}

// viewOf is what eriphe-core keeps of what is open, and of the settings:
// only the forces the user changed, the centring only when it is off.
export const viewOf = (s: State): View => ({
  open: s.open,
  talk: s.talk ?? undefined,
  shown: s.shown || undefined,
  theme: s.theme,
  orb: s.closed,
  forces: changedForces(s.forces),
  centring: s.centring ? undefined : false,
});

const FORCE_NAMES = Object.keys(DEFAULT_FORCES) as (keyof Forces)[];

// changedForces are the forces that are not the defaults; none when all are.
function changedForces(f: Forces): Partial<Forces> | undefined {
  const out = Object.fromEntries(FORCE_NAMES.filter((k) => f[k] !== DEFAULT_FORCES[k]).map((k) => [k, f[k]]));
  return Object.keys(out).length ? out : undefined;
}

// setGraphForces sets the graph's forces (the settings' sliders), each
// within its range.
export function setGraphForces(s: State, f: Partial<Forces>): State {
  const forces = forcesFrom({ ...s.forces, ...f });
  return FORCE_NAMES.every((k) => forces[k] === s.forces[k]) ? s : { ...s, forces };
}

// setGraphCentring says whether the open node becomes the graph's centre.
export function setGraphCentring(s: State, on: boolean): State {
  return on === s.centring ? s : { ...s, centring: on };
}

export function setTheme(s: State, theme: Theme): State {
  return theme === s.theme ? s : { ...s, theme };
}

// setClosed notes what the user closed of the orb's (orb/model.ts: close).
export function setClosed(s: State, closed: OrbClosed): State {
  return closed === s.closed ? s : { ...s, closed };
}

export const sourceKey = (node: string, block: string, name: string) => `${node}\u0000${block}\u0000${name}`;

// The "node" event: all the rows (reset), or those that changed and the
// ids of those that left (gone: archived). A page open on a node that left
// closes.
export type NodeEvent = { reset?: boolean; nodes?: GraphNode[]; gone?: string[] };

export function applyNodes(s: State, ev: NodeEvent): State {
  const nodes = ev.reset ? {} : { ...s.nodes };
  for (const n of ev.nodes ?? []) if (n && typeof n.id === "string") nodes[n.id] = n;
  for (const id of ev.gone ?? []) delete nodes[id];
  const open = s.open.filter((id) => nodes[id]);
  // The page the bar talked to left: it talks to the last one open.
  const talk = s.talk === null || open.includes(s.talk) ? s.talk : (open.at(-1) ?? null);
  const next = { ...s, nodes, open, talk };
  // A show that came before its node: the node is here, it opens now.
  return s.pending && nodes[s.pending.node] ? showNode({ ...next, pending: null }, s.pending) : next;
}

// The "doc" event: a new version ({id, version}) or a trace that grew
// ({id, trace}).
export type DocEvent = { id: string; version?: number; trace?: boolean };

export function applyDoc(s: State, ev: DocEvent): State {
  if (!ev || typeof ev.id !== "string") return s;
  if (ev.trace) return { ...s, traces: { ...s.traces, [ev.id]: (s.traces[ev.id] ?? 0) + 1 } };
  return { ...s, docs: { ...s.docs, [ev.id]: (s.docs[ev.id] ?? 0) + 1 } };
}

export type ProgressEvent = { node: string; text: string; at: string };

export function applyProgress(s: State, ev: ProgressEvent): State {
  if (!ev || typeof ev.node !== "string" || typeof ev.text !== "string") return s;
  return { ...s, progress: { ...s.progress, [ev.node]: { text: ev.text, at: ev.at } } };
}

export type SourceEvent = { node: string; block: string; name: string; value: unknown; lists?: { component: string; items: ListRow[] }[] };

export function applySource(s: State, ev: SourceEvent): State {
  if (!ev || typeof ev.node !== "string" || typeof ev.block !== "string" || typeof ev.name !== "string") return s;
  const key = sourceKey(ev.node, ev.block, ev.name);
  let rows = s.rows;
  for (const l of Array.isArray(ev.lists) ? ev.lists : []) {
    if (l && typeof l.component === "string" && Array.isArray(l.items)) rows = { ...rows, [sourceKey(ev.node, ev.block, l.component)]: l.items };
  }
  return { ...s, sources: { ...s.sources, [key]: ev.value }, rows };
}

// SHOW_FRESH is how old a show may be and still open a page when the stream
// (re)connects: the last one comes at once, and an old one is not news.
export const SHOW_FRESH = 60_000;

// The "show" event: Eriphe opens a node's page for the user, beside what is
// open. The bar still talks to whom it talked to: only the user's own
// gesture (a click on a page or a bubble) moves it, so Eriphe's answer stays
// where it was asked. One exception: a subject Eriphe opened for what the
// user asked at the home (talk) takes the bar with it, when it still
// talks to the home: the exchange goes on in that page. Each show once. A
// show of a node the graph does not have yet (its row comes after) waits
// for it (pending), the last show only: applyNodes opens it; a newer show
// drops it, so that shows open in their order.
export function applyShow(s: State, ev: Shown, now = Date.now()): State {
  if (!ev || typeof ev.node !== "string" || !(ev.seq > s.shown)) return s;
  const first = s.shown === 0;
  const seen = { ...s, shown: ev.seq, pending: null };
  if (first && ev.at && now - Date.parse(ev.at) > SHOW_FRESH) return seen;
  if (!s.nodes[ev.node]) return { ...seen, pending: ev };
  return showNode(seen, ev);
}

// showNode opens the page of a show whose node the graph has.
function showNode(s: State, ev: Shown): State {
  if (ev.talk && s.talk === null) return openPage(s, ev.node, true);
  if (s.open.includes(ev.node)) return s;
  return { ...s, open: [...s.open, ev.node] };
}

// Sent is a message the user just wrote: after is the orb's asked seq
// when it was sent, to the node the bar talked to then.
export type Sent = { text: string; after: number; to: string | null };

// The "orb" event: what Eriphe's orb shows, whatever page the bar talks
// to (orb/). What is not what it says it is is dropped, never drawn.
export function applyOrb(s: State, x: OrbFeed): State {
  if (!x || typeof x !== "object") return s;
  const words = (w: unknown) => (w && typeof w === "object" && typeof (w as OrbWords).seq === "number" && typeof (w as OrbWords).text === "string" ? (w as OrbWords) : undefined);
  const str = (v: unknown) => (typeof v === "string" ? v : undefined);
  const work = x.work && typeof x.work === "object" ? { node: str(x.work.node), draft: str(x.work.draft) } : undefined;
  const failed = x.failed && typeof x.failed.seq === "number" ? { seq: x.failed.seq, node: str(x.failed.node) } : undefined;
  const wait = x.wait && typeof x.wait.card === "number" && typeof x.wait.node === "string" ? x.wait : undefined;
  const cards = Array.isArray(x.cards) ? x.cards.filter((t) => typeof t === "string") : undefined;
  const waiting = Array.isArray(x.waiting) ? x.waiting.filter((w): w is Waiting => !!words(w)) : undefined;
  const orb: OrbFeed = { asked: typeof x.asked === "number" ? x.asked : undefined, work, answer: words(x.answer), failed, wait, cards, waiting };
  return { ...s, orb, sent: landed(orb, s.sent) ? null : s.sent };
}

// landed tells whether the message sent is in the orb's feed: a message
// of the user's newer than the feed was when it was sent, read by Eriphe
// or waiting for it.
export function landed(x: OrbFeed | null, sent: Sent | null): boolean {
  return !!sent && ((x?.asked ?? 0) > sent.after || (x?.waiting ?? []).some((w) => w.seq > sent.after));
}

// lastSeq is the newest message of the user's the feed knows.
export function lastSeq(x: OrbFeed | null): number {
  return Math.max(x?.asked ?? 0, ...(x?.waiting ?? []).map((w) => w.seq));
}

// send notes the message the user just sent (null: it could not go).
export function setSent(s: State, sent: Sent | null): State {
  return { ...s, sent };
}

// NOTES_KEPT is how many notices show at once.
export const NOTES_KEPT = 4;

export function applyNotes(s: State, ns: Note[]): State {
  if (!Array.isArray(ns) || ns.length === 0) return s;
  const fresh = ns.filter((n) => !s.notes.some((o) => o.id === n.id));
  return { ...s, notes: [...s.notes, ...fresh].slice(-NOTES_KEPT) };
}

export function dropNote(s: State, id: number): State {
  return { ...s, notes: s.notes.filter((n) => n.id !== id) };
}

export function applyMoved(s: State, m: Moved): State {
  if (!m || !(m.all || m.refs?.length)) return s;
  const nodes = Array.isArray(m.nodes) ? m.nodes.filter((id) => typeof id === "string") : [];
  return { ...s, moved: { n: s.moved.n + 1, refs: Array.isArray(m.refs) ? m.refs : [], nodes, all: Boolean(m.all) } };
}

// openPage opens a node's page: alone, or beside those open (a ⌘-click,
// Eriphe's show). The bar then talks to it.
export function openPage(s: State, id: string, beside = false): State {
  if (!s.nodes[id]) return s;
  const open = beside ? (s.open.includes(id) ? s.open : [...s.open, id]) : [id];
  return { ...s, open, talk: id };
}

// closePage closes one page; the bar talks to the one it talked to if
// it is still open (the home too), else to the last one open, else to the
// home.
export function closePage(s: State, id: string): State {
  const open = s.open.filter((x) => x !== id);
  const talk = s.talk === null || (s.talk !== id && open.includes(s.talk)) ? s.talk : (open.at(-1) ?? null);
  return { ...s, open, talk };
}

// talkTo makes an open page the one the bar talks to.
export function talkTo(s: State, id: string | null): State {
  if (id === s.talk || (id !== null && !s.open.includes(id))) return s;
  return { ...s, talk: id };
}

export type Store = StoreApi<State>;

export function makeStore(init: Partial<State> = {}): Store {
  return createStore<State>(() => ({ ...empty(), ...init }));
}

// step applies a pure step to the store.
export function step<A extends unknown[]>(store: Store, f: (s: State, ...a: A) => State, ...a: A) {
  const before = store.getState();
  const after = f(before, ...a);
  if (after !== before) store.setState(after, true);
}

// The page's own store.
export const store = makeStore();

export function useGraph<T>(select: (s: State) => T): T {
  return useStore(store, select);
}

// pathOf and depthOf read the graph's parents.
export function pathOf(nodes: Record<string, GraphNode>, id: string): string[] {
  const out: string[] = [];
  for (let c: string | undefined = id; c && nodes[c] && out.length < 64; c = nodes[c].parent) out.unshift(c);
  return out;
}

export function depthOf(nodes: Record<string, GraphNode>, id: string): number {
  return pathOf(nodes, id).length - 1;
}

// subtree is a node and every node under it.
export function subtree(nodes: Record<string, GraphNode>, id: string): string[] {
  const kids = new Map<string, string[]>();
  for (const n of Object.values(nodes)) if (n.parent) kids.set(n.parent, [...(kids.get(n.parent) ?? []), n.id]);
  const out: string[] = [];
  const walk = (x: string) => {
    if (out.includes(x)) return;
    out.push(x);
    for (const k of kids.get(x) ?? []) walk(k);
  };
  walk(id);
  return out;
}
