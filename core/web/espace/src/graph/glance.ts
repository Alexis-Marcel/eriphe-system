import type { Glance, GraphNode } from "../data/api";
import type { Lang } from "../i18n";
import { waits } from "./model";

// A node's glance (node_glance.go): a figure one of its sources holds,
// that eriphe-core reads with no model and sends with the node. While it
// is past its threshold and the user has not opened the node since
// (shown, not seen: eriphe-core keeps both, with its hysteresis), the
// node's bubble is a small tile that shows it: information worth a look,
// never a request. The violet halo stays the only « t'attend »: a node
// that waits keeps its circle.

// CAP: the most tiles at once; the furthest past their threshold win.
export const CAP = 3;
// KEEP: a tile keeps its place against a newcomer not clearly stronger
// (a fifth more), so two near figures do not swap at each read.
export const KEEP = 1.2;
// TILE: a tile's size, fixed (its digits are tabular): the layout makes
// room once, not at each value.
export const TILE: [number, number] = [148, 60];

// weightOf is how far past its threshold a glance is.
export const weightOf = (g: Glance) => (g.threshold > 0 ? Math.abs(g.delta) / g.threshold : 0);

// pickTiles is which nodes are tiles now, the strongest first: those
// whose glance is shown and not seen, that do not wait for the user and
// are not open; at most CAP, a tile already (was) weighing KEEP times more.
export function pickTiles(nodes: GraphNode[], open: string[], was: string[]): string[] {
  return nodes
    .filter((x) => x.glance?.shown && !x.glance.seen && !waits(x) && !open.includes(x.id))
    .map((x) => ({ id: x.id, w: weightOf(x.glance!) * (was.includes(x.id) ? KEEP : 1) }))
    .sort((a, b) => b.w - a.w || a.id.localeCompare(b.id))
    .slice(0, CAP)
    .map((o) => o.id);
}

// coreShape is what a bubble's core morphs to: its glance's tile, a
// rounded rectangle, or its circle; with less motion, at once.
export function coreShape(tile: [number, number] | undefined, r: number, still: boolean) {
  return {
    to: tile ? { width: tile[0], height: tile[1], borderRadius: 14 } : { width: r * 2, height: r * 2, borderRadius: r },
    move: still ? { duration: 0 } : ({ type: "spring", stiffness: 170, damping: 24 } as const),
  };
}

// variation writes a glance's variation in the page's language: its
// arrow, and « 3,2 % » or « 12 » (in the figure's units).
export function variation(g: Glance, lang: Lang): { up: boolean; arrow: string; text: string } {
  const up = g.delta >= 0;
  const a = Math.abs(g.delta);
  const digits = g.unit === "amount" ? (Number.isInteger(a) ? 0 : a < 10 ? 1 : 0) : a < 10 ? 1 : 0;
  const n = a.toLocaleString(lang === "fr" ? "fr-FR" : "en-US", { minimumFractionDigits: digits, maximumFractionDigits: digits });
  return { up, arrow: up ? "▲" : "▼", text: g.unit === "amount" ? n : lang === "fr" ? `${n} %` : `${n}%` };
}

// sparkPath is a glance's recent values as a line of w × h, its last
// point apart; its scale holds the reference, so a little noise never
// looks like a move. Fewer than two values draw nothing.
export function sparkPath(g: Glance, w: number, h: number): { line: string; area: string; last: [number, number] } | null {
  const vs = g.series ?? [];
  if (vs.length < 2) return null;
  const lo = Math.min(g.ref, ...vs), hi = Math.max(g.ref, ...vs);
  const span = hi - lo || 1;
  const pts = vs.map((v, i) => [(i / (vs.length - 1)) * w, h - 2 - ((v - lo) / span) * (h - 4)] as [number, number]);
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join("");
  return { line, area: `${line}L${w},${h}L0,${h}Z`, last: pts[pts.length - 1] };
}
