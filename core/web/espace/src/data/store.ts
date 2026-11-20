import { createStore, type StoreApi } from "zustand/vanilla";
import { useStore } from "zustand";
import type { BarLine, Exchange, GraphNode, Moved, Note, PickItem, Place, Theme, View } from "./api";

// The workspace's state: the graph as eriphe-core streams it (stream.ts),
// and what the user has open. The graph is core's: the page never changes a
// node by itself, it shows what the stream says. What is open is the
// page's: the pages open in the graph, and the one the console talks to
// (talk), the home when none; where the user dropped bubbles (places) and
// the theme. eriphe-core keeps those too (web_gestures.go): the boot gives
// them back (restore), on any of the user's screens.
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
  // the rows a List picks in, of the sources an action acts on, by node,
  // block and name (list_actions.go)
  rows: Record<string, PickItem[]>;
  open: string[];
  talk: string | null;
  shown: number; // the last show applied
  exchange: Exchange | null;
  notes: Note[];
  moved: Moved & { n: number }; // the proposals that moved last, n growing each time
  live: boolean; // the stream is open
  places: Record<string, Place>; // where the user dropped a bubble: a root's spot, a child's offset from its parent
  theme: Theme; // "" follows the system
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
  exchange: null,
  notes: [],
  moved: { n: 0, refs: [] },
  live: false,
  places: {},
  theme: "",
});

// restore is the workspace as the user left it: its pages open (kept when
// their nodes come), the one the console talked to, the last show taken
// (one the user saw is not shown again), the places, the theme.
export function restore(s: State, v: View | undefined, places: Record<string, Place> | undefined): State {
  const open = Array.isArray(v?.open) ? v.open.filter((id) => typeof id === "string") : [];
  const talk = v?.talk && open.includes(v.talk) ? v.talk : null;
  const theme: Theme = v?.theme === "light" || v?.theme === "dark" ? v.theme : "";
  const shown = typeof v?.shown === "number" && v.shown > 0 ? v.shown : s.shown;
  return { ...s, open, talk, theme, shown, places: places && typeof places === "object" ? { ...places } : {} };
}

// viewOf is what eriphe-core keeps of what is open.
export const viewOf = (s: State): View => ({ open: s.open, talk: s.talk ?? undefined, shown: s.shown || undefined, theme: s.theme });

// placeAt keeps where the user dropped a bubble; null forgets it.
export function placeAt(s: State, id: string, p: Place | null): State {
  const places = { ...s.places };
  if (p) places[id] = p;
  else delete places[id];
  return { ...s, places };
}

export function setTheme(s: State, theme: Theme): State {
  return theme === s.theme ? s : { ...s, theme };
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
  // The page the console talked to left: it talks to the last one open.
  const talk = s.talk === null || open.includes(s.talk) ? s.talk : (open.at(-1) ?? null);
  return { ...s, nodes, open, talk };
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

export type SourceEvent = { node: string; block: string; name: string; value: unknown; items?: PickItem[] };

export function applySource(s: State, ev: SourceEvent): State {
  if (!ev || typeof ev.node !== "string" || typeof ev.block !== "string" || typeof ev.name !== "string") return s;
  const key = sourceKey(ev.node, ev.block, ev.name);
  const rows = Array.isArray(ev.items) ? { ...s.rows, [key]: ev.items } : s.rows;
  return { ...s, sources: { ...s.sources, [key]: ev.value }, rows };
}

// SHOW_FRESH is how old a show may be and still open a page when the stream
// (re)connects: the last one comes at once, and an old one is not news.
export const SHOW_FRESH = 60_000;

// The "show" event: Eriphe opens a node's page for the user, beside what is
// open. The console still talks to whom it talked to: only the user's own
// gesture (a click on a page or a bubble) moves it, so Eriphe's answer stays
// where it was asked. One exception: a subject Eriphe opened for what the
// user asked at the home (talk) takes the console with it, when it still
// talks to the home: the exchange goes on in that page. Each show once.
export function applyShow(s: State, ev: Shown, now = Date.now()): State {
  if (!ev || typeof ev.node !== "string" || !(ev.seq > s.shown)) return s;
  const first = s.shown === 0;
  const seen = { ...s, shown: ev.seq };
  if (first && ev.at && now - Date.parse(ev.at) > SHOW_FRESH) return seen;
  if (!s.nodes[ev.node]) return seen;
  if (ev.talk && s.talk === null) return openPage(seen, ev.node, true);
  if (s.open.includes(ev.node)) return seen;
  return { ...seen, open: [...s.open, ev.node] };
}

// The "exchange" event: the exchange of the node the console talks to; one
// of another (the stream not yet moved) is not shown.
export function applyExchange(s: State, x: Exchange): State {
  if (!x || typeof x !== "object") return s;
  if ((x.node ?? null) !== s.talk) return s;
  const line = (l: unknown) => (l && typeof l === "object" && typeof (l as BarLine).seq === "number" && typeof (l as BarLine).text === "string" ? (l as BarLine) : undefined);
  const cards = Array.isArray(x.cards) ? x.cards.filter((t) => typeof t === "string") : undefined;
  const waiting = Array.isArray(x.waiting) ? x.waiting.filter((w) => !!line(w)) : undefined;
  return { ...s, exchange: { ...x, asked: line(x.asked), said: line(x.said), draft: typeof x.draft === "string" ? x.draft : undefined, cards, waiting } };
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
  return { ...s, moved: { n: s.moved.n + 1, refs: Array.isArray(m.refs) ? m.refs : [], all: Boolean(m.all) } };
}

// openPage opens a node's page: alone, or beside those open (a ⌘-click,
// Eriphe's show). The console then talks to it.
export function openPage(s: State, id: string, beside = false): State {
  if (!s.nodes[id]) return s;
  const open = beside ? (s.open.includes(id) ? s.open : [...s.open, id]) : [id];
  return { ...s, open, talk: id, exchange: s.talk === id ? s.exchange : null };
}

// closePage closes one page; the console talks to the one it talked to if
// it is still open (the home too), else to the last one open, else to the
// home.
export function closePage(s: State, id: string): State {
  const open = s.open.filter((x) => x !== id);
  const talk = s.talk === null || (s.talk !== id && open.includes(s.talk)) ? s.talk : (open.at(-1) ?? null);
  return { ...s, open, talk, exchange: talk === s.talk ? s.exchange : null };
}

// talkTo makes an open page the one the console talks to.
export function talkTo(s: State, id: string | null): State {
  if (id === s.talk || (id !== null && !s.open.includes(id))) return s;
  return { ...s, talk: id, exchange: null };
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

// childrenOf, rootOf and pathOf read the graph's parents.
export function rootOf(nodes: Record<string, GraphNode>, id: string): string {
  let c = id;
  for (let i = 0; i < 64 && nodes[c]?.parent && nodes[nodes[c].parent!]; i++) c = nodes[c].parent!;
  return c;
}

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
