import type { GraphNode } from "../data/api";
import { depthOf } from "../data/store";

// What a node looks like on the graph comes from what it is, computed
// here, never chosen by a model: a subject is bigger than its bubbles,
// what waits for the user brighter, what runs a little, what is done or
// old quieter.

export const OLD = 7 * 24 * 3600 * 1000;

export function weight(x: GraphNode, nodes: Record<string, GraphNode>, now = Date.now()): number {
  const d = depthOf(nodes, x.id);
  let v = d === 0 ? (x.lifetime === "standing" ? 0.82 : 0.95) : d === 1 ? 0.55 : 0.42;
  if (x.waits || x.state === "waiting") v += 0.35;
  if (x.state === "running") v += 0.18;
  if (x.state === "done") v -= 0.1;
  if (now - Date.parse(x.updated) > OLD) v -= 0.12;
  return Math.max(0.15, Math.min(1.3, v));
}

export function radiusOf(x: GraphNode, nodes: Record<string, GraphNode>, now = Date.now()): number {
  const wt = weight(x, nodes, now);
  return depthOf(nodes, x.id) === 0 ? 20 + 18 * wt : 9 + 13 * wt;
}

// waits tells whether a node waits for the user: a card of its, or its state.
export const waits = (x: GraphNode) => !!x.waits || x.state === "waiting";

// titleOf is a node's name on screen: its title, or what it is when its
// title may be someone else's words (eriphe-core sends none then).
export const titleOf = (x: GraphNode | undefined, external: string) => (x?.title ? x.title : external);

// codeOf is the short word inside a subject's bubble.
export const codeOf = (title: string) => title.replace(/[^\p{L}\p{N}]/gu, "").slice(0, 3).toUpperCase() || "·";

// labelBox is the room a node's label takes under it, roughly: its title
// and, while it runs, its line.
export function labelBox(x: GraphNode, title: string, d: number, r: number) {
  const long = Math.max(title.length, x.line && x.state === "running" ? x.line.length * 0.9 : 0);
  return { r, lw: Math.min(300, long * (d === 0 ? 8.4 : 7.4) + 8), lh: d === 0 ? 44 : x.line ? 34 : 18 };
}
