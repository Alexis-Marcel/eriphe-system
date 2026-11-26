import { useEffect, useRef, useState } from "react";
import * as api from "../data/api";
import { store, useGraph, type State } from "../data/store";

// usePage reads a node's page at a version (0: the last), and again at each
// new version eriphe-core says it has (the stream's "doc") and each time a
// proposal it shows, or one of its node's cards, moves ("proposals":
// concerns); a proposal of another page reads nothing again.
export function usePage(id: string, version: number): { page: api.NodePage | null; failed: boolean } {
  const doc = useGraph((s) => s.docs[id] ?? 0);
  const [moved, setMoved] = useState(0);
  const [got, setGot] = useState<{ key: string; page: api.NodePage | null; failed: boolean }>({ key: "", page: null, failed: false });
  const shown = useRef<api.NodePage | null>(null);
  shown.current = got.page;
  useEffect(
    () =>
      store.subscribe((s, before) => {
        if (s.moved.n !== before.moved.n && concerns(s.moved, id, shown.current)) setMoved((n) => n + 1);
      }),
    [id],
  );
  const key = `${id}@${version}`;
  useEffect(() => {
    let on = true;
    reading(api.nodePage(id, version)).then(
      (page) => on && setGot({ key, page, failed: page === null }),
      () => on && setGot((g) => ({ ...g, key, failed: true })),
    );
    return () => {
      on = false;
    };
  }, [id, version, doc, moved, key]);
  // Another version asked: the one shown stays until it is read.
  return { page: got.page && got.page.node.id === id ? got.page : null, failed: got.key === key && got.failed };
}

// concerns tells whether proposals that moved change the page of node id
// (page: as it was last read): all of them, a card of its node's, or a
// proposal it shows (at its foot, in its menu, on a row, on a line).
export function concerns(m: State["moved"], id: string, page: api.NodePage | null): boolean {
  if (m.all || m.nodes?.includes(id)) return true;
  if (!page) return false;
  const refs = new Set<string>();
  for (const c of [...page.cards, ...(page.settled ?? [])]) refs.add(c.ref);
  for (const r of page.placed ?? []) refs.add(r);
  for (const it of page.items ?? []) if (it.card) refs.add(it.card);
  return m.refs.some((r) => refs.has(r));
}

// useTrace reads a node's trace while it shows, and again as it grows.
export function useTrace(id: string, on: boolean): api.TraceEntry[] | null {
  const bump = useGraph((s) => s.traces[id] ?? 0);
  const [got, setGot] = useState<api.TraceEntry[] | null>(null);
  useEffect(() => {
    if (!on) return;
    let ok = true;
    reading(api.nodeTrace(id)).then(
      (t) => ok && setGot(t),
      () => ok && setGot([]),
    );
    return () => {
      ok = false;
    };
  }, [id, on, bump]);
  return got;
}

// useFile reads a node's file as it is, at a version.
export function useFile(id: string, version: number, on: boolean): api.NodeFile | null {
  const bump = useGraph((s) => s.docs[id] ?? 0);
  const [got, setGot] = useState<api.NodeFile | null>(null);
  useEffect(() => {
    if (!on) return;
    let ok = true;
    reading(api.nodeFile(id, version)).then(
      (f) => ok && setGot(f),
      () => ok && setGot(null),
    );
    return () => {
      ok = false;
    };
  }, [id, version, on, bump]);
  return got;
}

// The reads of pages under way: the graph is not at rest while one is
// (Graph.tsx, data-still), its page about to change its size.
let reads = 0;
const watchers = new Set<() => void>();

export const readsUnderWay = () => reads;

export function onReads(f: () => void): () => void {
  watchers.add(f);
  return () => watchers.delete(f);
}

function reading<T>(p: Promise<T>): Promise<T> {
  reads++;
  watchers.forEach((f) => f());
  const done = () => {
    reads--;
    watchers.forEach((f) => f());
  };
  p.then(done, done);
  return p;
}
