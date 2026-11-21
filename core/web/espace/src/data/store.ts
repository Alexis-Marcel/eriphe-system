import { createStore, type StoreApi } from "zustand/vanilla";
import { useStore } from "zustand";
import type { GraphNode, Moved, Note, OrbFeed, OrbWords, PickItem, Theme, View, Waiting } from "./api";

// The workspace's state: the graph as eriphe-core streams it (stream.ts),
// and what the user has open. The graph is core's: the page never changes a
// node by itself, it shows what the stream says. What is open is the
// page's: the pages open in the graph, and the one the bar talks to
// (talk), the home when none; and the theme. eriphe-core keeps those too (web_gestures.go): the boot gives
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
  // the rows of the Lists the page must know (those that filter a
  // source's items, those the user picks in), by node, block and the
  // List's component id (list_actions.go, list_sections.go)
  rows: Record<string, PickItem[]>;
  open: string[];
  talk: string | null;
  shown: number; // the last show applied
  orb: OrbFeed | null; // what Eriphe's orb shows (orb/)
  sent: Sent | null; // what the user just sent, until eriphe-core has it
  call: { id: string; n: number; after: number } | null; // the last node that began to wait for the user, the n-th, after the orb's words of that seq (the orb says it)
  notes: Note[];
  moved: Moved & { n: number }; // the proposals that moved last, n growing each time
  live: boolean; // the stream is open
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
  orb: null,
  sent: null,
  call: null,
  notes: [],
  moved: { n: 0, refs: [] },
  live: false,
  theme: "",
});

// restore is the workspace as the user left it: its pages open (kept when
// their nodes come), the one the bar talked to, the last show taken
// (one the user saw is not shown again), the theme.
export function restore(s: State, v: View | undefined): State {
  const open = Array.isArray(v?.open) ? v.open.filter((id) => typeof id === "string") : [];
  const talk = v?.talk && open.includes(v.talk) ? v.talk : null;
  const theme: Theme = v?.theme === "light" || v?.theme === "dark" ? v.theme : "";
  const shown = typeof v?.shown === "number" && v.shown > 0 ? v.shown : s.shown;
  return { ...s, open, talk, theme, shown };
}

// viewOf is what eriphe-core keeps of what is open.
export const viewOf = (s: State): View => ({ open: s.open, talk: s.talk ?? undefined, shown: s.shown || undefined, theme: s.theme });

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
  // The page the bar talked to left: it talks to the last one open.
  const talk = s.talk === null || open.includes(s.talk) ? s.talk : (open.at(-1) ?? null);
  const began = newlyWaiting(s.nodes, nodes, ev.reset);
  const after = Math.max(s.orb?.answer?.seq ?? 0, s.orb?.said?.seq ?? 0);
  const call = began.length ? { id: began[began.length - 1], n: (s.call?.n ?? 0) + 1, after } : s.call;
  return { ...s, nodes, open, talk, call };
}

// newlyWaiting are the nodes that began to wait for the user (a card of
// theirs, their state) from one graph to the next: none in the first
// graph, nor in a whole graph sent again (reset: the stream came back).
export function newlyWaiting(before: Record<string, GraphNode>, after: Record<string, GraphNode>, reset?: boolean): string[] {
  const waits = (x: GraphNode | undefined) => !!x && (!!x.waits || x.state === "waiting");
  if (reset || Object.keys(before).length === 0) return [];
  return Object.values(after)
    .filter((x) => waits(x) && !waits(before[x.id]))
    .map((x) => x.id);
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

export type SourceEvent = { node: string; block: string; name: string; value: unknown; lists?: { component: string; items: PickItem[] }[] };

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
  const orb: OrbFeed = { asked: typeof x.asked === "number" ? x.asked : undefined, work, answer: words(x.answer), said: words(x.said), failed, wait, cards, waiting };
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
  return { ...s, moved: { n: s.moved.n + 1, refs: Array.isArray(m.refs) ? m.refs : [], all: Boolean(m.all) } };
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
