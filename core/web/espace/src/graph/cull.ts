// What the graph draws of a big graph: only the bubbles the camera sees,
// and a margin around, so that a pan finds them drawn. Below CULL_FROM
// nodes every bubble is drawn (what the keyboard and a screen reader reach
// is the whole graph); above, a bubble out of view is not in the page.
// A bubble is drawn once within MOUNT of the screen and kept until past
// UNMOUNT, a wider margin: a pan back and forth at the edge does not draw
// it again and again.

export const CULL_FROM = 200;
export const MOUNT = 300; // screen pixels around the screen
export const UNMOUNT = 700;

// The least a body says of its size, as the layout has it.
export type Sized = { id: string; r: number; lw: number; lh: number; tile?: readonly [number, number] };
export type View = { x: number; y: number; k: number; w: number; h: number };

// reach is how far a bubble's drawing goes from its centre (its halo, up
// to 2.5 times its radius; its label under it): [half width, above, below].
export function reach(b: Sized): [number, number, number] {
  const halo = b.r * 2.5;
  if (b.tile) return [Math.max(b.tile[0] / 2, b.lw / 2) + 10, b.tile[1] / 2 + 10, b.tile[1] / 2 + 12 + b.lh];
  return [Math.max(halo, b.lw / 2 + 4), halo, Math.max(halo, b.r + 16 + b.lh)];
}

// seen is whether a body at p shows on the screen widened by margin
// screen pixels on each side, the camera at (x, y) and scale k.
function seen(b: Sized, p: readonly [number, number], v: View, margin: number): boolean {
  const [h, up, down] = reach(b);
  const l = (-margin - v.x) / v.k, r = (v.w + margin - v.x) / v.k;
  const t = (-margin - v.y) / v.k, bt = (v.h + margin - v.y) / v.k;
  return p[0] + h >= l && p[0] - h <= r && p[1] + down >= t && p[1] - up <= bt;
}

// culled is the ids to draw: null for all of them (a small graph), else
// those within MOUNT of the screen, those drawn before (was) still within
// UNMOUNT, and those kept whatever (keep: held, focused, hovered).
export function culled(
  bodies: readonly Sized[],
  at: (id: string) => readonly [number, number] | undefined,
  v: View,
  was: ReadonlySet<string> | null,
  keep: Iterable<string | null | undefined> = [],
): Set<string> | null {
  if (bodies.length < CULL_FROM) return null;
  const out = new Set<string>();
  for (const b of bodies) {
    const p = at(b.id);
    if (!p) continue;
    if (seen(b, p, v, MOUNT) || (was !== null && was.has(b.id) && seen(b, p, v, UNMOUNT))) out.add(b.id);
  }
  for (const id of keep) if (id) out.add(id);
  return out;
}

// same tells whether two sets of ids drawn are the same.
export function same(a: ReadonlySet<string> | null, b: ReadonlySet<string> | null): boolean {
  if (a === null || b === null) return a === b;
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}
