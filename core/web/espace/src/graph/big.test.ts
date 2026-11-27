import { describe, expect, it } from "vitest";
import type { GraphNode } from "../data/api";
import { subtree } from "../data/store";
import { listOrder } from "./model";

// A graph of thousands of nodes is read in time linear in its size: the
// list's order, and each open page's subtree (Graph.tsx, at every change
// of a node), never a search through what was already read for each node.

const N = 5000;

// big is N nodes: one subject holding a third of them (the worst case of a
// search per child), the rest in subjects of a few children and
// grandchildren, a few lost (their parent gone).
function big(N: number): Record<string, GraphNode> {
  const out: GraphNode[] = [];
  const at = (i: number) => new Date(Date.parse("2026-11-01T00:00:00Z") + i * 1000).toISOString();
  const add = (id: string, parent?: string) => out.push({ id, title: id, parent, weight: 0.5, status: "idle", created: at(out.length), updated: at(0), version: 1 });
  add("hub");
  for (let i = 0; i < N / 3; i++) add(`h${i}`, "hub");
  let s = 0;
  while (out.length < N) {
    const root = `s${s++}`;
    add(root);
    for (let c = 0; c < 6 && out.length < N; c++) {
      add(`${root}c${c}`, root);
      if (c % 2 === 0 && out.length < N) add(`${root}c${c}g`, `${root}c${c}`);
    }
    if (s % 50 === 0 && out.length < N) add(`lost${s}`, "gone");
  }
  // Given in no particular order, as a stream would.
  return Object.fromEntries(out.reverse().map((x) => [x.id, x]));
}

// under is a node and what is under it, the slow plain way: each node
// walks its parents.
function under(nodes: Record<string, GraphNode>, id: string): Set<string> {
  const out = new Set<string>();
  for (const x of Object.values(nodes)) for (let c: string | undefined = x.id; c && nodes[c]; c = nodes[c].parent) if (c === id) out.add(x.id);
  return out;
}

const time = (f: () => void) => {
  const t = performance.now();
  f();
  return performance.now() - t;
};

describe("a graph of thousands of nodes", () => {
  const nodes = big(N);

  it("is listed whole, each node once, under its parent, depth first", () => {
    const rows = listOrder(nodes);
    expect(rows).toHaveLength(N);
    expect(new Set(rows.map((x) => x.id)).size).toBe(N);
    const pos = new Map(rows.map((x, i) => [x.id, i]));
    for (const x of rows) if (x.parent && nodes[x.parent]) expect(pos.get(x.parent)!).toBeLessThan(pos.get(x.id)!);
    // A subject's nodes follow it, together.
    for (const root of ["hub", "s3", "s120"]) {
      const sub = under(nodes, root);
      const at = pos.get(root)!;
      expect(new Set(rows.slice(at, at + sub.size).map((x) => x.id))).toEqual(sub);
    }
  });

  it("gives each subtree whole", () => {
    for (const id of ["hub", "s0", "s7c2", "h12", "lost50"]) expect(new Set(subtree(nodes, id))).toEqual(under(nodes, id));
  });

  it("is read in linear time", () => {
    // Four times as many nodes, the best of a few runs (a loaded runner,
    // not the code, slows one): linear, some ten milliseconds; a search
    // through what was read for each node, hundreds.
    const best = (f: () => void) => Math.min(...[0, 1, 2].map(() => time(f)));
    const g = big(N * 4);
    expect(best(() => listOrder(g))).toBeLessThan(60);
    // A node changed (a new graph), ten pages open: ten subtrees of it.
    const open = ["hub", "s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"];
    expect(best(() => {
      const changed = { ...g };
      open.forEach((id) => subtree(changed, id));
    })).toBeLessThan(60);
  });
});
