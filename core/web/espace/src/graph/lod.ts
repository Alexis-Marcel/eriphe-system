// The level of detail of a big graph (semantic zoom): from LOD.from nodes
// on, every node is a disc and every link a line on one canvas (paint.ts),
// and only the nodes big on the screen are bubbles in the page, with their
// title, halo, motion, glance and gestures: those whose radius on screen
// is past LOD.bubbleAt, those open, focused, held or hovered, and those
// that wait for the user near the camera; LOD.cap of them at most, the
// most important first. A smaller graph is all bubbles (what the keyboard
// and a screen reader reach is the whole graph).
// The knobs are the ?leger page's (dev-leger.tsx), to tune by eye.

export type Knobs = {
  from: number; // nodes from which the graph has a level of detail
  bubbleAt: number; // a node's radius on screen, in px, from which it is a bubble
  labelAt: number; // ... from which its label shows on the canvas (fading in over half as much again)
  size: number; // the discs' size, times their radius (eased to 1 as they near bubbleAt)
  color: boolean; // a disc's colour by its family (its root)
  cap: number; // bubbles at most, those kept aside
  life: boolean; // a node's size by its life (life.ts): what waits, runs, is kept up or fresh is bigger
  lifeStrength: number; // ... how much (0: none, 1: as designed)
  sleep: boolean; // a node idle for sleepDays days, nothing waiting, running or kept up, is a pale dot (life.ts)
  sleepDays: number;
  zones: boolean; // each family a soft zone behind its nodes, its name far out (zones.ts)
  group: number; // ... and how much a family keeps together in the layout (layout.ts, FAMILY), with its zone
};

export const LOD: Knobs = { from: 200, bubbleAt: 16, labelAt: 7, size: 1, color: true, cap: 120, life: true, lifeStrength: 1, sleep: true, sleepDays: 21, zones: true, group: 3 };

const told = new Set<() => void>();
let version = 0;
export const onLod = (f: () => void) => {
  told.add(f);
  return () => void told.delete(f);
};
// lodVersion changes with each change of the knobs (for React).
export const lodVersion = () => version;
export function setLod(k: Partial<Knobs>) {
  Object.assign(LOD, k);
  version++;
  for (const f of told) f();
}

// same tells whether two sets of ids drawn are the same.
export function same(a: ReadonlySet<string> | null, b: ReadonlySet<string> | null): boolean {
  if (a === null || b === null) return a === b;
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}

// A bubble once is kept while its radius on screen is past HOLD times the
// threshold and it is within FAR of the screen: a zoom back and forth at
// the threshold does not draw it again and again.
export const HOLD = 0.8;
export const NEAR = 120; // screen px around the screen: a bubble there is drawn before it shows
export const FAR = 300;
// A node that waits is a bubble from WAITS times the threshold: its halo
// as the page draws it, sooner than the others.
export const WAITS = 0.5;
// At most BUDGET bubbles new to the page in a frame: a zoom in draws the
// rest in the frames after, and no frame waits for a hundred.
export const BUDGET = 40;
// A disc smaller than GRAB px on screen is not dragged: pressed and moved,
// it is the view that moves (a press meant as a pan on a far graph lands
// on a disc more often than not); pressed and let go, it opens.
export const GRAB = 8;

export type Item = { id: string; r: number; waits: boolean; sleeps?: boolean };
// A node asleep (life.ts) is a bubble from SLEEPS times the threshold, its
// dot's radius on screen (half its radius awake, life.ts SLEEP_R): about
// when it would be 1.2 times the threshold awake, close (a child's dot
// from a scale of 1.5 to 2.4; the camera stops at 2.6), or hovered, open,
// held.
export const SLEEPS = 0.6;
export type View = { x: number; y: number; k: number; w: number; h: number };

// drawnRadius is a disc's radius in the graph's units: its radius times
// the size knob, eased to its radius as it nears the threshold, so that
// it becomes its bubble at the same size.
export function drawnRadius(r: number, k: number, o: Knobs = LOD): number {
  if (o.size === 1) return r;
  const t = Math.min(1, (r * k) / o.bubbleAt);
  return r * (o.size + (1 - o.size) * t);
}

// labelAlpha is how much of a disc's label shows, by its radius on screen:
// none below labelAt, all from 1.5 times it; the heaviest first.
export function labelAlpha(rs: number, o: Knobs = LOD): number {
  return Math.max(0, Math.min(1, (rs - o.labelAt) / (o.labelAt * 0.5)));
}

// within tells whether a disc at (px, py) on screen, of screen radius rs,
// is within margin px of a screen w × h.
const within = (px: number, py: number, rs: number, v: View, margin: number) =>
  px + rs >= -margin && px - rs <= v.w + margin && py + rs >= -margin && py - rs <= v.h + margin;

// decide is the nodes drawn as bubbles for the camera v: null for all of
// them (a graph below o.from nodes); else those kept (keep: open, held,
// focused, hovered) and, up to o.cap in all, the others by their size on
// screen, a node that waits counting twice; was: the bubbles of before
// (they hold a little longer). xy: each item's x and y, in its order. Of
// the bubbles new to the page, BUDGET at most (more: some wait).
export function decide(
  items: readonly Item[],
  xy: ArrayLike<number>,
  v: View,
  was: ReadonlySet<string> | null,
  keep: Iterable<string | null | undefined> = [],
  o: Knobs = LOD,
  budget = BUDGET,
): { ids: Set<string> | null; more: boolean } {
  if (items.length < o.from) return { ids: null, more: false };
  const out = new Set<string>();
  for (const id of keep) if (id) out.add(id);
  const cand: { id: string; score: number; old: boolean }[] = [];
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (out.has(it.id)) continue;
    const x = xy[i * 2], y = xy[i * 2 + 1];
    if (x !== x) continue; // NaN: not placed yet
    const rs = it.r * v.k, px = x * v.k + v.x, py = y * v.k + v.y;
    const old = !!was?.has(it.id);
    const at = o.bubbleAt * (it.waits ? WAITS : it.sleeps ? SLEEPS : 1) * (old ? HOLD : 1);
    if (rs < at || !within(px, py, rs, v, old ? FAR : NEAR)) continue;
    cand.push({ id: it.id, score: rs * (it.waits ? 2 : 1), old });
  }
  cand.sort((a, b) => b.score - a.score || (a.id < b.id ? -1 : 1));
  let room = Math.max(0, o.cap - out.size);
  let more = false;
  for (const c of cand) {
    if (room <= 0) break;
    if (!c.old) {
      if (budget <= 0) {
        more = true;
        continue;
      }
      budget--;
    }
    out.add(c.id);
    room--;
  }
  return { ids: out, more };
}

// hit is the node under the screen point (sx, sy) for the camera v: the
// one whose disc is nearest within its radius on screen and slop px (a
// finger is wider than a pointer); none skipped (an open page covers its
// node). null: none there.
export function hit(
  items: readonly Item[],
  xy: ArrayLike<number>,
  v: View,
  sx: number,
  sy: number,
  slop = 4,
  skip?: (id: string) => boolean,
  o: Knobs = LOD,
): string | null {
  let best: string | null = null, bestD = Infinity;
  const gx = (sx - v.x) / v.k, gy = (sy - v.y) / v.k; // the point in the graph's units
  for (let i = 0; i < items.length; i++) {
    const x = xy[i * 2], y = xy[i * 2 + 1];
    if (x !== x) continue;
    const it = items[i];
    const rs = Math.max(3, drawnRadius(it.r, v.k, o) * v.k);
    const reach = (rs + slop) / v.k;
    const dx = x - gx, dy = y - gy;
    if (Math.abs(dx) > reach || Math.abs(dy) > reach) continue;
    // how far past its edge, in screen px: the nearest edge wins (a small
    // disc beside a big one is reachable)
    const d = Math.hypot(dx, dy) * v.k - rs;
    if (d <= slop && d < bestD && !skip?.(it.id)) {
      best = it.id;
      bestD = d;
    }
  }
  return best;
}

// families gives each node its family's colour, an index in a palette of
// n colours: its root's, the biggest families first, so that they differ;
// a node alone (a root with nothing under it) has none (-1).
export function families(nodes: readonly { id: string; parent?: string }[], n: number): Map<string, number> {
  const { rootOf, size } = rootsOf(nodes);
  const roots = [...size].filter(([, k]) => k > 1).sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1));
  const colour = new Map(roots.map(([id], i) => [id, i % n]));
  return new Map(nodes.map((x) => [x.id, colour.get(rootOf.get(x.id)!) ?? -1]));
}

// rootsOf is each node's root (the top of its parents) and each root's
// family's size, itself included.
export function rootsOf(nodes: readonly { id: string; parent?: string }[]): { rootOf: Map<string, string>; size: Map<string, number> } {
  const parent = new Map(nodes.map((x) => [x.id, x.parent]));
  const rootOf = new Map<string, string>();
  const find = (id: string): string => {
    const known = rootOf.get(id);
    if (known) return known;
    const path: string[] = [];
    let c = id;
    for (let p = parent.get(c); p && parent.has(p) && path.length < 64; p = parent.get(c)) {
      path.push(c);
      c = p;
      const k = rootOf.get(c);
      if (k) {
        c = k;
        break;
      }
    }
    for (const x of path) rootOf.set(x, c);
    rootOf.set(id, c);
    return c;
  };
  const size = new Map<string, number>();
  for (const x of nodes) {
    const r = find(x.id);
    size.set(r, (size.get(r) ?? 0) + 1);
  }
  return { rootOf, size };
}
