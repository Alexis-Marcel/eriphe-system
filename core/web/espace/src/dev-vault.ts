import type { GraphNode } from "./data/api";

// The ?leger page's real shape (dev-leger.tsx): a vault of notes as a
// small export describes it, read in the browser from a file on the
// machine, never kept, never sent, never in the repository: each note its
// title, its folder, the titles it links to, when it last changed. Its
// graph: one root per folder, titled by it; each note a child of its
// folder's root, linked to the notes it names; changed when its file
// changed (so asleep when it did not for weeks: graph/life.ts); idle.
// Nothing waits or runs in a vault: some are made to, on demand, to see
// the signals on the real shape. Only on the dev server.

export type VaultNote = { title: string; folder: string; links: string[]; mtime: string };

// isVault tells whether what was read is a vault's export.
export function isVault(x: unknown): x is VaultNote[] {
  return Array.isArray(x) && x.every((n) => n && typeof n === "object" && typeof n.title === "string" && typeof n.folder === "string" && Array.isArray(n.links) && typeof n.mtime === "string");
}

// A small seeded generator: the same notes marked for the same seed.
function rng(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const EPOCH = "1970-01-01T00:00:00.000Z";
const iso = (s: string) => {
  const t = Date.parse(s);
  return t === t ? new Date(t).toISOString() : EPOCH;
};

// vaultGraph is the graph of a vault's notes; waits notes (not roots)
// marked as waiting for the user, runs as running, picked by seed. The
// ids are by position (vf: a folder, vn: a note), never by title: a title
// is the user's words, kept to the page. A link to a title no note has,
// or to itself, is left out; two notes of one title, the first one is
// linked to.
export function vaultGraph(notes: readonly VaultNote[], o: { waits?: number; runs?: number; seed?: number } = {}): GraphNode[] {
  const byTitle = new Map<string, number>();
  notes.forEach((n, i) => byTitle.has(n.title) || byTitle.set(n.title, i));
  // the links, resolved and once each way
  const links = notes.map((n, i) => {
    const out = new Set<number>();
    for (const t of n.links) {
      const j = byTitle.get(t);
      if (j !== undefined && j !== i) out.add(j);
    }
    return [...out];
  });
  const degree = new Array<number>(notes.length).fill(1);
  links.forEach((ls, i) => {
    degree[i] += ls.length;
    for (const j of ls) degree[j]++;
  });
  // the folders, in the order they come
  const folders = new Map<string, { id: string; first: string; last: string }>();
  notes.forEach((n) => {
    if (!n.folder) return;
    const t = iso(n.mtime);
    const f = folders.get(n.folder);
    if (!f) folders.set(n.folder, { id: `vf${folders.size}`, first: t, last: t });
    else {
      if (t < f.first) f.first = t;
      if (t > f.last) f.last = t;
    }
  });
  const out: GraphNode[] = [];
  for (const [title, f] of folders) out.push({ id: f.id, title, weight: 0.8, status: "idle", created: f.first, updated: f.last, version: 1 });
  notes.forEach((n, i) => {
    const t = iso(n.mtime);
    const x: GraphNode = {
      id: `vn${i}`,
      title: n.title,
      weight: 0.2 + 0.6 * Math.min(1, Math.log1p(degree[i]) / Math.log1p(30)),
      status: "idle",
      created: t,
      updated: t,
      version: 1,
    };
    const f = n.folder ? folders.get(n.folder) : undefined;
    if (f) x.parent = f.id;
    if (links[i].length) x.links = links[i].map((j) => `vn${j}`);
    out.push(x);
  });
  // some marked as waiting, some as running: the notes, not the roots
  const r = rng(o.seed ?? 1);
  const pool = out.filter((x) => x.id.startsWith("vn"));
  const mark = (k: number, status: GraphNode["status"]) => {
    for (let m = 0; m < k && pool.length; m++) {
      const x = pool.splice(Math.floor(r() * pool.length), 1)[0];
      x.status = status;
      if (status === "running") x.line = "en cours…";
    }
  };
  mark(Math.max(0, o.waits ?? 0), "waits");
  mark(Math.max(0, o.runs ?? 0), "running");
  return out;
}
