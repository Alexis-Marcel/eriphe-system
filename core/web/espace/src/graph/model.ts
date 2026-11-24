import type { GraphNode } from "../data/api";
import { depthOf } from "../data/store";

// What a node looks like on the graph comes from what it is, never chosen
// by a model: its weight, computed once by eriphe-core from its links, its
// status and its freshness (node_weight.go), sizes and lights its bubble;
// a subject is bigger than its bubbles.

export function radiusOf(x: GraphNode, nodes: Record<string, GraphNode>): number {
  return depthOf(nodes, x.id) === 0 ? 20 + 18 * x.weight : 9 + 13 * x.weight;
}

// waits tells whether a node waits for the user (its status, eriphe-core's).
export const waits = (x: GraphNode) => x.status === "waits";

// titleOf is a node's name as plain words (a label, a sentence): its
// title, marked with ⚠ when it may be someone else's words, never
// replaced; fallback only when it has none. On screen, NodeTitle
// (Title.tsx) draws the mark.
export const titleOf = (x: GraphNode | undefined, fallback: string) => {
  const title = x?.title || fallback;
  return x?.titleExternal ? `${title} ⚠` : title;
};

// plainTitle is a node's title alone, for what measures or shortens it.
export const plainTitle = (x: GraphNode | undefined, fallback: string) => x?.title || fallback;

// codeOf is the short word inside a subject's bubble.
export const codeOf = (title: string) => title.replace(/[^\p{L}\p{N}]/gu, "").slice(0, 3).toUpperCase() || "·";

// labelBox is the room a node's label takes under it, roughly: its title
// and, while it runs, its line.
export function labelBox(x: GraphNode, title: string, d: number, r: number) {
  const run = !!x.line && x.status === "running";
  const long = Math.max(title.length, run ? x.line!.length * 0.9 : 0);
  return { r, lw: Math.min(300, long * (d === 0 ? 8.4 : 7.4) + 8), lh: d === 0 ? 44 : run ? 34 : 18 };
}

// listOrder is the graph as the list reads it: each root, then the nodes
// under it, depth first, each group in the order it was made.
export function listOrder(nodes: Record<string, GraphNode>): GraphNode[] {
  const made = (a: GraphNode, b: GraphNode) => a.created.localeCompare(b.created) || a.id.localeCompare(b.id);
  const kids = new Map<string, GraphNode[]>();
  const roots: GraphNode[] = [];
  for (const x of Object.values(nodes)) {
    if (x.parent && nodes[x.parent]) kids.set(x.parent, [...(kids.get(x.parent) ?? []), x]);
    else roots.push(x);
  }
  const out: GraphNode[] = [];
  const walk = (x: GraphNode) => {
    if (out.includes(x)) return;
    out.push(x);
    for (const k of (kids.get(x.id) ?? []).sort(made)) walk(k);
  };
  roots.sort(made).forEach(walk);
  return out;
}
