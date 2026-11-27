// What the graph draws of a big graph: only the bubbles the camera sees,
// and a margin around, so that a pan finds them drawn. Below CULL_FROM
// nodes every bubble is drawn (what the keyboard and a screen reader reach
// is the whole graph); above, a bubble out of view is not in the page.
// A bubble is drawn once within MOUNT of the screen and kept until past
// UNMOUNT, a wider margin: a pan back and forth at the edge does not draw
// it again and again. The camera may move half the margin before it is
// looked at again (covered), and at most BUDGET bubbles are drawn anew in a
// frame, those on screen first: a zoom out draws the rest in the frames
// after, and no frame waits for hundreds.

export const CULL_FROM = 200;
export const MOUNT = 200; // screen pixels around the screen
export const UNMOUNT = 450;
export const ZOOMED = 1.25; // a zoom in by this much draws less
export const BUDGET = 80;

// The least a body says of its size, as the layout has it.
export type Sized = { id: string; r: number; lw: number; lh: number; tile?: readonly [number, number] };
export type View = { x: number; y: number; k: number; w: number; h: number };
type Rect = [number, number, number, number];

// reach is how far a bubble's drawing goes from its centre (its halo, up
// to 2.5 times its radius; its label under it): [half width, above, below].
export function reach(b: Sized): [number, number, number] {
  const halo = b.r * 2.5;
  if (b.tile) return [Math.max(b.tile[0] / 2, b.lw / 2) + 10, b.tile[1] / 2 + 10, b.tile[1] / 2 + 12 + b.lh];
  return [Math.max(halo, b.lw / 2 + 4), halo, Math.max(halo, b.r + 16 + b.lh)];
}

// area is the part of the graph on the screen widened by margin screen
// pixels on each side, the camera at (x, y) and scale k: [left, top,
// right, bottom] in the graph's units.
function area(v: View, margin: number): Rect {
  return [(-margin - v.x) / v.k, (-margin - v.y) / v.k, (v.w + margin - v.x) / v.k, (v.h + margin - v.y) / v.k];
}

function seen(b: Sized, p: readonly [number, number], a: Rect): boolean {
  const [h, up, down] = reach(b);
  return p[0] + h >= a[0] && p[0] - h <= a[2] && p[1] + down >= a[1] && p[1] - up <= a[3];
}

// covered tells whether what was drawn for the camera at was still covers
// the camera now: the screen and half the margin within the area drawn,
// and not zoomed in so far that much of it is drawn for nothing.
export function covered(was: View, now: View): boolean {
  if (now.k > was.k * ZOOMED) return false;
  const a = area(was, MOUNT), b = area(now, MOUNT / 2);
  return b[0] >= a[0] && b[1] >= a[1] && b[2] <= a[2] && b[3] <= a[3];
}

// culled is the ids to draw: null for all of them (a small graph), else
// those within MOUNT of the screen, those drawn before (was) still within
// UNMOUNT, and those kept whatever (keep: held, focused, hovered); of the
// new ones, at most BUDGET, those on screen first (more: some wait).
export function culled(
  bodies: readonly Sized[],
  at: (id: string) => readonly [number, number] | undefined,
  v: View,
  was: ReadonlySet<string> | null,
  keep: Iterable<string | null | undefined> = [],
): { ids: Set<string> | null; more: boolean } {
  if (bodies.length < CULL_FROM) return { ids: null, more: false };
  const near = area(v, MOUNT), far = area(v, UNMOUNT), screen = area(v, 0);
  const out = new Set<string>();
  const later: Sized[] = [];
  let budget = BUDGET;
  for (const b of bodies) {
    const p = at(b.id);
    if (!p) continue;
    if (was?.has(b.id) ? seen(b, p, far) : false) out.add(b.id);
    else if (seen(b, p, near)) {
      if (!seen(b, p, screen)) later.push(b);
      else if (budget > 0) {
        out.add(b.id);
        budget--;
      } else later.push(b);
    }
  }
  let more = false;
  for (const b of later) {
    if (budget <= 0) {
      more = true;
      break;
    }
    out.add(b.id);
    budget--;
  }
  for (const id of keep) if (id) out.add(id);
  return { ids: out, more };
}

// same tells whether two sets of ids drawn are the same.
export function same(a: ReadonlySet<string> | null, b: ReadonlySet<string> | null): boolean {
  if (a === null || b === null) return a === b;
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}
