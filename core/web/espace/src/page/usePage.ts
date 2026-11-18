import { useEffect, useState } from "react";
import * as api from "../data/api";
import { useGraph } from "../data/store";
import { sample } from "../data/sample";

// usePage reads a node's page at a version (0: the last), and again at each
// new version eriphe-core says it has (the stream's "doc"); the sample's on
// the dev server.
export function usePage(id: string, version: number, live: boolean): { page: api.NodePage | null; failed: boolean } {
  const bump = useGraph((s) => s.docs[id] ?? 0);
  const [got, setGot] = useState<{ key: string; page: api.NodePage | null; failed: boolean }>({ key: "", page: null, failed: false });
  const key = `${id}@${version}`;
  useEffect(() => {
    let on = true;
    const read = live ? api.nodePage(id, version) : Promise.resolve(sample.page(id, version));
    read.then(
      (page) => on && setGot({ key, page, failed: page === null }),
      () => on && setGot((g) => ({ ...g, key, failed: true })),
    );
    return () => {
      on = false;
    };
  }, [id, version, live, bump, key]);
  // Another version asked: the one shown stays until it is read.
  return { page: got.page && got.page.node.id === id ? got.page : null, failed: got.key === key && got.failed };
}

// useTrace reads a node's trace while it shows, and again as it grows.
export function useTrace(id: string, on: boolean, live: boolean): api.TraceEntry[] | null {
  const bump = useGraph((s) => s.traces[id] ?? 0);
  const [got, setGot] = useState<api.TraceEntry[] | null>(null);
  useEffect(() => {
    if (!on) return;
    let ok = true;
    (live ? api.nodeTrace(id) : Promise.resolve(sample.trace(id))).then(
      (t) => ok && setGot(t),
      () => ok && setGot([]),
    );
    return () => {
      ok = false;
    };
  }, [id, on, live, bump]);
  return got;
}

// useFile reads a node's file as it is, at a version.
export function useFile(id: string, version: number, on: boolean, live: boolean): api.NodeFile | null {
  const bump = useGraph((s) => s.docs[id] ?? 0);
  const [got, setGot] = useState<api.NodeFile | null>(null);
  useEffect(() => {
    if (!on) return;
    let ok = true;
    (live ? api.nodeFile(id, version) : Promise.resolve(sample.file(id))).then(
      (f) => ok && setGot(f),
      () => ok && setGot(null),
    );
    return () => {
      ok = false;
    };
  }, [id, version, on, live, bump]);
  return got;
}
