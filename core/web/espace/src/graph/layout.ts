// The graph's layout: a force graph like Obsidian's, with four forces and
// one constant each (FORCES):
//
//   - centre: every body is pulled to the middle, the graph is a disc;
//   - repulsion: every body pushes every other away (many-body);
//   - link force: two linked bodies (a bubble and its parent, two subjects
//     a text links) pull on each other like a spring;
//   - link distance: that spring's rest length, the room between the two
//     bodies' shapes (their circle, a page's rectangle), not their labels.
//
// And collision: no two bodies ever overlap, a bubble by its circle and its
// label, an open page by its rectangle. A page is a node like any other,
// only a bigger shape: the same forces act on it, open or closed, so a
// subject and the page it links to end up side by side because they are
// linked.
//
// An open page is big: a body linked to something on its far side would
// be pulled straight into it and held there by the collision, its link
// drawn behind the page. So a link's pull goes round an open page that is
// neither of its ends: the body heads for the page's corner that starts
// the shortest way round, then the next, and rejoins what it is linked to.
//
// One energy (alpha) drives it all, as in d3: any change (a node come or
// gone, a link, a page opened, closed or resized, the forces tuned, a
// drag) warms the graph, the forces adapt everything as it cools, and once
// it is cold the graph rests: nothing ticks until the next change, nothing
// trembles. A new node is born in the most free space near what it links
// to (in free space on the disc when it links to nothing), then settles.
//
// A drag: the body held follows the pointer, its cluster comes along (one
// link away nearly all the way, two a little, HOP1 and HOP2) and the graph
// stays warm. Dropped, nothing is pinned: the body and its cluster take up
// the forces again from where they were let go, as if born there, so they
// stay on the side the user chose while the centre keeps them in the disc.
// The node in focus (focus) can be the centre: the centre force pulls to
// it and it stays where it is, and what belongs to it is spread all round
// it (what is linked to it, and each group that hangs nowhere on it, by
// its root); a body that hangs on it through another follows that one.
// Nothing here knows a node by its name.

export type Pt = [number, number];
export type Pos = Record<string, Pt>;
// r: the bubble's radius; lw, lh: the size of the label under it; box: an
// open page, drawn in the graph. tile: the bubble drawn as its glance, a
// rounded rectangle of that size (graph/glance.ts): its body follows that
// shape.
export type LNode = { id: string; parent?: string; links: string[]; r: number; lw: number; lh: number; box?: Pt; tile?: Pt };

// The four forces, the user's to tune in the workspace's settings
// (hud/Settings.tsx; eriphe-core keeps them, web_gestures.go). One object
// shared by the simulations; setForces changes it and warms them.
export type Forces = { centre: number; repulsion: number; link: number; distance: number };
export const DEFAULT_FORCES: Readonly<Forces> = { centre: 0.1, repulsion: 2000, link: 0.8, distance: 30 };
export const FORCES: Forces = { ...DEFAULT_FORCES };

// The rest of the tuning: how the energy cools, how the bodies move.
export const T = {
  decay: 0.0228, // the energy cools by this share a tick (about 300 ticks from 1, like d3)
  alphaMin: 0.004, // ... and below this it is none: the graph rests
  warm: 0.5, // a change warms the graph back to this at least
  dragWarm: 0.3, // the energy the graph keeps while a body is held
  friction: 0.6, // the velocity kept from one tick to the next
  chargeMin: 30, // the repulsion never counts a distance below this
  cap: 40, // no body moves more than this a tick
  soft: 0.4, // collision: the share of an overlap undone a pass while warm
  passes: 2, // ... passes a tick
  spread: 0.2, // after a focus change, the pull to a place round the node in focus, × the energy
  jam: 2, // two bodies overlapping less than this are apart (within their margins: never visible)
  // How the graph stays fast with thousands of nodes, the rules the same.
  // Up to `exact` bodies, the repulsion and the collision go over every
  // pair and the graph is laid out at once (hurry): a small graph is where
  // it always was. Past it, the repulsion of a far group of bodies is that
  // of one body as heavy as they all, at their middle (Barnes-Hut: a group
  // seen smaller than `theta` from a body), a body is only tested against
  // those in its cells of a grid, and a hurry takes a slice a frame
  // (SLICE). The tree is the faster past some 20 bodies. A theta of 0.5
  // keeps the rules (layout.test.ts plays them all on the tree): past 0.6,
  // two linked open pages may settle apart, a bubble stay behind a page.
  theta: 0.5,
  exact: 50,
  cell: 128, // the grid's cell, in the graph's units
  rests: 1000, // at rest, the collision's passes at most (over frames past `exact`); a dense graph of hundreds needs more than 200
};

// SLICE is the most a frame of the graph may take of the main thread, in
// ms, while it hurries (the first layout, a change settled at once): what
// draws the graph gives it to its simulation (Graph.tsx), the rest is
// done in the frames after.
export const SLICE = 8;

// FORCE_RANGES are the bounds a setting may give each force; eriphe-core
// keeps a force within the same (web_gestures.go, espaceForceRanges).
export const FORCE_RANGES: Record<keyof Forces, { min: number; max: number; step: number }> = {
  centre: { min: 0, max: 0.4, step: 0.005 },
  repulsion: { min: 0, max: 6000, step: 50 },
  link: { min: 0, max: 1.5, step: 0.02 },
  distance: { min: 0, max: 300, step: 5 },
};

// forcesFrom is the forces a kept setting gives: the defaults, overridden
// by what it holds that is a force within its range.
export function forcesFrom(kept: unknown): Forces {
  const out = { ...DEFAULT_FORCES };
  if (kept && typeof kept === "object")
    for (const k of Object.keys(out) as (keyof Forces)[]) {
      const v = (kept as Record<string, unknown>)[k];
      if (typeof v === "number" && Number.isFinite(v)) out[k] = Math.min(FORCE_RANGES[k].max, Math.max(FORCE_RANGES[k].min, v));
    }
  return out;
}

// HOP1 and HOP2 are the shares of a drag's moves the bodies one and two
// links from the one held take: its cluster comes along.
export const HOP1 = 0.85;
export const HOP2 = 0.35;

const sims = new Set<WeakRef<Sim>>();
const tuned = new Set<() => void>();

// setForces changes the forces (some of them) and warms every graph; what
// ticks one (Graph.tsx, onForces) is told, to wake. The same forces again
// change nothing.
export function setForces(f: Partial<Forces>): void {
  if ((Object.keys(f) as (keyof Forces)[]).every((k) => FORCES[k] === f[k])) return;
  Object.assign(FORCES, f);
  for (const r of sims) {
    const s = r.deref();
    if (s) s.reheat();
    else sims.delete(r);
  }
  for (const w of tuned) w();
}

// CENTRING says whether the node in focus (the open page the bar writes
// to) becomes the graph's centre: the centre force pulls to it, it stays
// where it is, the camera frames it in the middle and the graph around
// it (Graph.tsx). Off, the centre is the graph's middle.
export const CENTRING = { focus: true };

// setCentring turns it on or off; what ticks a graph is told, as for the
// forces.
export function setCentring(on: boolean): void {
  if (CENTRING.focus === on) return;
  CENTRING.focus = on;
  for (const w of tuned) w();
}

// onForces calls f each time the forces (or the centring) change; it
// gives what stops it.
export function onForces(f: () => void): () => void {
  tuned.add(f);
  return () => void tuned.delete(f);
}

export function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0) / 4294967296;
}

// extent is a node's box: its half width, its top and its bottom (an open
// page with a margin, a bubble with its label under it).
export function extent(x: LNode, at: Pt): [number, number, number] {
  ext(x, at[1]);
  return [EH, ET, EB];
}

// ext is extent for the simulation's loops, with nothing allocated: it
// leaves the box of x at height y in EH, ET and EB.
let EH = 0, ET = 0, EB = 0;
function ext(x: LNode, y: number): void {
  if (x.box) {
    EH = x.box[0] / 2 + 24;
    ET = y - x.box[1] / 2 - 24;
    EB = y + x.box[1] / 2 + 24;
  } else if (x.tile) {
    EH = Math.max(x.tile[0] / 2, x.lw / 2) + 10;
    ET = y - x.tile[1] / 2 - 8;
    EB = y + x.tile[1] / 2 + 10 + x.lh;
  } else {
    EH = Math.max(x.r, x.lw / 2) + 10;
    ET = y - x.r - 8;
    EB = y + x.r + 10 + x.lh;
  }
}

// overlap tells whether two bodies at those points sit on each other.
export function overlap(a: LNode, pa: Pt, b: LNode, pb: Pt): boolean {
  const [ha, ta, ba] = extent(a, pa), [hb, tb, bb] = extent(b, pb);
  return ha + hb - Math.abs(pa[0] - pb[0]) > 0.5 && Math.min(ba, bb) - Math.max(ta, tb) > 0.5;
}

// pairs are the graph's links, one per pair of nodes whichever way they
// go: a node and its parent, two nodes a text links (one way or both).
// Both ends are in the graph. The simulation's springs and the lines drawn
// are these.
export function pairs(nodes: { id: string; parent?: string; links?: string[] }[]): [string, string][] {
  const ids = new Set(nodes.map((n) => n.id));
  const seen = new Set<string>();
  const out: [string, string][] = [];
  const add = (a: string, b: string) => {
    if (a === b || !ids.has(a) || !ids.has(b)) return;
    const k = a < b ? `${a}\n${b}` : `${b}\n${a}`;
    if (seen.has(k)) return;
    seen.add(k);
    out.push(a < b ? [a, b] : [b, a]);
  };
  for (const n of nodes) {
    if (n.parent) add(n.parent, n.id);
    for (const l of n.links ?? []) add(n.id, l);
  }
  return out;
}

// reach is how far a body's shape (its circle, its tile, its page, with a
// margin; never its label) goes from its centre in the direction (ux, uy):
// the springs' rest length counts from there.
function reach(x: LNode, ux: number, uy: number): number {
  const half: Pt | undefined = x.box ? [x.box[0] / 2 + 24, x.box[1] / 2 + 24] : x.tile ? [x.tile[0] / 2 + 6, x.tile[1] / 2 + 6] : undefined;
  if (!half) return x.r + 6;
  return Math.min(Math.abs(ux) > 1e-6 ? half[0] / Math.abs(ux) : Infinity, Math.abs(uy) > 1e-6 ? half[1] / Math.abs(uy) : Infinity);
}

// rect is an open page's box with its margin (extent): left, top, right,
// bottom.
function rect(o: LNode & { x: number; y: number }): [number, number, number, number] {
  const [h, t, b] = extent(o, [o.x, o.y]);
  return [o.x - h, t, o.x + h, b];
}

function inside([x, y]: Pt, [l, t, r, b]: number[]): boolean {
  return x > l && x < r && y > t && y < b;
}

// enters is where the segment from a to b enters the box (0 at a, 1 at b),
// Infinity when it misses it or only grazes its edge (Liang-Barsky).
function enters(a: Pt, b: Pt, [l, t, r, bt]: number[]): number {
  const dx = b[0] - a[0], dy = b[1] - a[1];
  let lo = 0, hi = 1;
  for (const [p, q] of [
    [-dx, a[0] - l],
    [dx, r - a[0]],
    [-dy, a[1] - t],
    [dy, bt - a[1]],
  ]) {
    if (Math.abs(p) < 1e-9) {
      if (q <= 0) return Infinity;
      continue;
    }
    const u = q / p;
    if (p < 0) lo = Math.max(lo, u);
    else hi = Math.min(hi, u);
  }
  return hi - lo > 1e-6 ? lo : Infinity;
}

// A body of the simulation: a node, where it is and how fast it goes.
type Body = LNode & { x: number; y: number; vx: number; vy: number };

// A point that pushes and is pushed: a body for the repulsion.
type Charge = { id: string; x: number; y: number; vx: number; vy: number };

// repelPairs is the repulsion over every pair of bodies, k the strength
// (the force × the energy): each pushes every other away, by k over the
// distance squared (never closer than T.chargeMin). Two on the same spot
// part in a direction their names give.
export function repelPairs(L: Charge[], k: number): void {
  for (let i = 0; i < L.length; i++)
    for (let j = i + 1; j < L.length; j++) {
      const p = L[i], q = L[j];
      let dx = q.x - p.x, dy = q.y - p.y, d = Math.hypot(dx, dy);
      if (d < 1) {
        dx = hash(p.id + q.id) - 0.5;
        dy = 0.5 - hash(q.id + p.id);
        d = Math.hypot(dx, dy) || 1;
      }
      const f = k / Math.max(d, T.chargeMin) / d;
      p.vx -= dx * f;
      p.vy -= dy * f;
      q.vx += dx * f;
      q.vy += dy * f;
    }
}

// LEAF marks a cell without children (its first child's place): -1 is a
// child missing, the quarter empty.
const LEAF = -2;

// Quad is a quadtree of the bodies, built again each tick, in flat arrays
// kept from one tick to the next: a cell is a square (x0, y0, its side s),
// its bodies (order[from..to]), their count and their middle;
// a cell of one body (or of bodies on one spot) has no children.
export class Quad {
  private cap = 0;
  private cells = 0;
  private x0 = new Float64Array(0);
  private y0 = new Float64Array(0);
  private s = new Float64Array(0);
  private sx = new Float64Array(0);
  private sy = new Float64Array(0);
  private m = new Float64Array(0);
  private kids = new Int32Array(0);
  private from = new Int32Array(0);
  private to = new Int32Array(0);
  private order = new Int32Array(0);
  private stack = new Int32Array(0);
  private px = new Float64Array(0); // the bodies' places, read once a tick
  private py = new Float64Array(0);

  private cell(): number {
    if (this.cells === this.cap) {
      const cap = Math.max(64, this.cap * 2);
      const grow = <A extends Float64Array | Int32Array>(a: A, n: number): A => {
        const b = new (a.constructor as new (n: number) => A)(n);
        b.set(a);
        return b;
      };
      this.x0 = grow(this.x0, cap);
      this.y0 = grow(this.y0, cap);
      this.s = grow(this.s, cap);
      this.sx = grow(this.sx, cap);
      this.sy = grow(this.sy, cap);
      this.m = grow(this.m, cap);
      this.kids = grow(this.kids, cap * 4);
      this.from = grow(this.from, cap);
      this.to = grow(this.to, cap);
      this.cap = cap;
    }
    return this.cells++;
  }

  // repel is the repulsion, k the strength, a far cell pushing as one body
  // as heavy as its bodies at their middle: far when its side is less than
  // theta times its distance (0: every pair, as repelPairs).
  repel(L: Charge[], k: number, theta: number): void {
    const n = L.length;
    if (n < 2) return;
    if (this.order.length < n) {
      this.order = new Int32Array(n);
      this.px = new Float64Array(n);
      this.py = new Float64Array(n);
    }
    const px = this.px, py = this.py;
    let lx = Infinity, ly = Infinity, hx = -Infinity, hy = -Infinity;
    for (let i = 0; i < n; i++) {
      this.order[i] = i;
      const x = (px[i] = L[i].x), y = (py[i] = L[i].y);
      if (x < lx) lx = x;
      if (y < ly) ly = y;
      if (x > hx) hx = x;
      if (y > hy) hy = y;
    }
    this.cells = 0;
    const root = this.build(0, n, lx, ly, Math.max(hx - lx, hy - ly) + 1, 0);
    if (this.stack.length < this.cells * 4 + 4) this.stack = new Int32Array(this.cells * 4 + 4);
    const { x0, y0, s, sx, sy, m, kids, from, to, order, stack } = this;
    const cmin = T.chargeMin, theta2 = theta * theta;
    for (let i = 0; i < n; i++) {
      const x = px[i], y = py[i];
      let fx = 0, fy = 0, top = 0;
      stack[top++] = root;
      while (top) {
        const c = stack[--top];
        const k0 = c * 4;
        if (kids[k0] === LEAF) {
          // a leaf: its bodies one by one, as a pair each
          for (let o = from[c]; o < to[c]; o++) {
            const j = order[o];
            if (j === i) continue;
            // the pair as repelPairs takes it, from the first in the list:
            // the push on this one, away from the other
            let dx = px[j] - x, dy = py[j] - y, d = Math.sqrt(dx * dx + dy * dy);
            if (d < 1) {
              const a = L[Math.min(i, j)].id, b = L[Math.max(i, j)].id, sign = j > i ? 1 : -1;
              dx = (hash(a + b) - 0.5) * sign;
              dy = (0.5 - hash(b + a)) * sign;
              d = Math.hypot(dx, dy) || 1;
            }
            const f = k / (d > cmin ? d : cmin) / d;
            fx -= dx * f;
            fy -= dy * f;
          }
          continue;
        }
        const dx = sx[c] - x, dy = sy[c] - y, d2 = dx * dx + dy * dy, side = s[c];
        const inside = x >= x0[c] && x <= x0[c] + side && y >= y0[c] && y <= y0[c] + side;
        if (!inside && side * side < theta2 * d2) {
          const d = Math.sqrt(d2), f = (k * m[c]) / (d > cmin ? d : cmin) / d;
          fx -= dx * f;
          fy -= dy * f;
          continue;
        }
        for (let q = 0; q < 4; q++) if (kids[k0 + q] >= 0) stack[top++] = kids[k0 + q];
      }
      L[i].vx += fx;
      L[i].vy += fy;
    }
  }

  // build makes the cell of the bodies order[a..b), a square at (x, y) of
  // side s, and its children; it gives the cell.
  private build(a: number, b: number, x: number, y: number, s: number, depth: number): number {
    const c = this.cell();
    this.x0[c] = x;
    this.y0[c] = y;
    this.s[c] = s;
    this.from[c] = a;
    this.to[c] = b;
    this.m[c] = b - a;
    const k0 = c * 4;
    this.kids[k0] = this.kids[k0 + 1] = this.kids[k0 + 2] = this.kids[k0 + 3] = -1;
    // its middle: where its bodies push from, as one
    let sx = 0, sy = 0;
    for (let o = a; o < b; o++) {
      sx += this.px[this.order[o]];
      sy += this.py[this.order[o]];
    }
    this.sx[c] = sx / (b - a);
    this.sy[c] = sy / (b - a);
    if (b - a < 2 || depth > 40) {
      this.kids[k0] = LEAF;
      return c;
    }
    const h = s / 2, mx = x + h, my = y + h;
    // the bodies in four: by side, then each side by height
    const m1 = this.split(a, b, mx, true);
    const m0 = this.split(a, m1, my, false), m2 = this.split(m1, b, my, false);
    // (the arrays may grow while a child is built: read them after)
    const k1 = m0 > a ? this.build(a, m0, x, y, h, depth + 1) : -1;
    const k2 = m1 > m0 ? this.build(m0, m1, x, my, h, depth + 1) : -1;
    const k3 = m2 > m1 ? this.build(m1, m2, mx, y, h, depth + 1) : -1;
    const k4 = b > m2 ? this.build(m2, b, mx, my, h, depth + 1) : -1;
    this.kids[k0] = k1;
    this.kids[k0 + 1] = k2;
    this.kids[k0 + 2] = k3;
    this.kids[k0 + 3] = k4;
    return c;
  }

  // split puts the bodies of order[a..b) below at (by x, or by y) first;
  // it gives where the others start.
  private split(a: number, b: number, at: number, byX: boolean): number {
    const order = this.order, v0 = byX ? this.px : this.py;
    let i = a, j = b - 1;
    while (i <= j) {
      const v = v0[order[i]];
      if (v < at) i++;
      else {
        const t = order[i];
        order[i] = order[j];
        order[j--] = t;
      }
    }
    return i;
  }
}

// Grid is a uniform grid over the bodies' boxes (and a margin round
// them), in flat arrays kept from one pass to the next: what may touch a
// body is in its cells.
class Grid {
  private start = new Int32Array(0);
  private cursor = new Int32Array(0);
  private items = new Int32Array(0);
  private box = new Float64Array(0); // per body: its first and last column and row
  private seen = new Int32Array(0);
  private found = new Int32Array(0);
  private cols = 0;

  // fill puts the bodies of L in the grid, each box grown by margin.
  fill(L: (LNode & { x: number; y: number })[], margin: number): void {
    const n = L.length;
    if (this.box.length < n * 4) {
      this.box = new Float64Array(n * 4);
      this.seen = new Int32Array(n);
      this.found = new Int32Array(n);
    }
    this.seen.fill(0);
    let lx = Infinity, ly = Infinity, hx = -Infinity, hy = -Infinity;
    for (let i = 0; i < n; i++) {
      const x = L[i].x;
      ext(L[i], L[i].y);
      const l = x - EH - margin, r = x + EH + margin, t = ET - margin, b = EB + margin;
      this.box[i * 4] = l;
      this.box[i * 4 + 1] = r;
      this.box[i * 4 + 2] = t;
      this.box[i * 4 + 3] = b;
      if (l < lx) lx = l;
      if (t < ly) ly = t;
      if (r > hx) hx = r;
      if (b > hy) hy = b;
    }
    // a cell of T.cell, bigger when the graph is spread very far
    let size = T.cell;
    while (((hx - lx) / size + 1) * ((hy - ly) / size + 1) > 4 * n + 4096) size *= 2;
    const cols = Math.floor((hx - lx) / size) + 1, rows = Math.floor((hy - ly) / size) + 1;
    this.cols = cols;
    for (let i = 0; i < n; i++) {
      const o = i * 4;
      this.box[o] = Math.floor((this.box[o] - lx) / size);
      this.box[o + 1] = Math.floor((this.box[o + 1] - lx) / size);
      this.box[o + 2] = Math.floor((this.box[o + 2] - ly) / size);
      this.box[o + 3] = Math.floor((this.box[o + 3] - ly) / size);
    }
    if (this.start.length < cols * rows + 1) this.start = new Int32Array(cols * rows + 1);
    const start = this.start;
    start.fill(0, 0, cols * rows + 1);
    let total = 0;
    for (let i = 0; i < n; i++) {
      const o = i * 4;
      for (let r = this.box[o + 2]; r <= this.box[o + 3]; r++)
        for (let c = this.box[o]; c <= this.box[o + 1]; c++) {
          start[r * cols + c + 1]++;
          total++;
        }
    }
    for (let k = 1; k <= cols * rows; k++) start[k] += start[k - 1];
    if (this.items.length < total) this.items = new Int32Array(total);
    if (this.cursor.length < cols * rows) this.cursor = new Int32Array(cols * rows);
    const cursor = this.cursor;
    cursor.fill(0, 0, cols * rows);
    for (let i = 0; i < n; i++) {
      const o = i * 4;
      for (let r = this.box[o + 2]; r <= this.box[o + 3]; r++)
        for (let c = this.box[o]; c <= this.box[o + 1]; c++) {
          const cell = r * cols + c;
          this.items[start[cell] + cursor[cell]++] = i;
        }
    }
  }

  // after gives the bodies after i in the list that share a cell with it,
  // in the cells' order (the same every time).
  after(i: number): Int32Array {
    const o = i * 4, cols = this.cols, start = this.start, items = this.items, seen = this.seen;
    let k = 0;
    for (let r = this.box[o + 2]; r <= this.box[o + 3]; r++)
      for (let c = this.box[o]; c <= this.box[o + 1]; c++) {
        const cell = r * cols + c;
        for (let e = start[cell]; e < start[cell + 1]; e++) {
          const j = items[e];
          if (j > i && seen[j] !== i + 1) {
            seen[j] = i + 1;
            this.found[k++] = j;
          }
        }
      }
    return this.found.subarray(0, k);
  }
}

// ROOM is the most free space a new body's place counts (seed): more room
// round it than this is as good.
const ROOM = 80;

// Room holds the bodies placed while new ones are born (seed), in cells of
// T.cell, to find the closest to a place without looking at all of them.
class Room {
  private cells = new Map<number, number[]>(); // the bodies in a cell, by their number here
  private x: number[] = []; // per body: its middle, half width, top and bottom
  private h: number[] = [];
  private t: number[] = [];
  private b: number[] = [];
  private seen: number[] = [];
  private stamp = 0;

  private key(c: number, r: number): number {
    return (c + 0x100000) * 0x200000 + (r + 0x100000);
  }

  add(o: Body): void {
    ext(o, o.y);
    const i = this.x.length;
    this.x.push(o.x);
    this.h.push(EH);
    this.t.push(ET);
    this.b.push(EB);
    this.seen.push(0);
    const C = T.cell, l = Math.floor((o.x - EH) / C), r = Math.floor((o.x + EH) / C), t = Math.floor(ET / C), b = Math.floor(EB / C);
    for (let y = t; y <= b; y++)
      for (let x = l; x <= r; x++) {
        const k = this.key(x, y);
        const cell = this.cells.get(k);
        if (cell) cell.push(i);
        else this.cells.set(k, [i]);
      }
  }

  // clearance is the room between a body x at p and the closest one
  // placed, box to box (below 0: they overlap); Infinity when none is
  // within ROOM: only those can be closer than ROOM. It stops as soon as
  // it is below enough.
  clearance(x: Body, p: Pt, enough = -Infinity): number {
    ext(x, p[1]);
    const h = EH, t = ET, b = EB, C = T.cell;
    const c0 = Math.floor((p[0] - h - ROOM) / C), c1 = Math.floor((p[0] + h + ROOM) / C);
    const r0 = Math.floor((t - ROOM) / C), r1 = Math.floor((b + ROOM) / C);
    const stamp = ++this.stamp, X = this.x, H = this.h, Tp = this.t, B = this.b, seen = this.seen;
    let least = Infinity;
    for (let r = r0; r <= r1; r++)
      for (let c = c0; c <= c1; c++) {
        const cell = this.cells.get(this.key(c, r));
        if (!cell) continue;
        for (const o of cell) {
          if (seen[o] === stamp) continue;
          seen[o] = stamp;
          const gx = Math.abs(p[0] - X[o]) - h - H[o], gy = Math.max(Tp[o] - b, t - B[o]);
          least = Math.min(least, gx > 0 && gy > 0 ? Math.hypot(gx, gy) : Math.max(gx, gy));
          if (least <= enough) return least;
        }
      }
    return least;
  }
}

// Sim is the graph's simulation: Graph.tsx keeps one for the session,
// tells it the nodes (sync), the drags (hold, drop), and ticks it a frame
// at a time while it is awake.
export class Sim {
  private list: Body[] = [];
  private byId = new Map<string, Body>();
  private edges: [Body, Body][] = [];
  private degree = new Map<string, number>();
  private alpha = 0;
  private held: { id: string; at: Pt } | null = null;
  private carried = new Map<string, number>(); // the held body's cluster: the share of its moves each one takes
  private focusId: string | null = null; // the node in focus: the centre, it stays where it is
  private centre: Pt = [0, 0]; // where the centre force pulls: the node in focus, else where the last one was
  private spread = new Map<string, Pt>(); // after a focus change: the angle and distance each body heads for, round the node in focus
  private forces: Forces;
  private slice: number; // the most a frame may take, in ms (Infinity: all at once)
  private hurried = false; // the graph is laid out at once, not shown moving (hurry)
  private later: { id: string | null } | null = null; // a focus asked while the graph hurries: made once it rests
  private again = false; // ... and a hurry asked after it: the focus too is laid out at once
  private resting = -1; // at rest: the collision's passes done so far (-1: not resting)
  private deadline = Infinity; // when the frame under way must give the main thread back
  private adjacent = new Map<Body, Body[]>(); // each body's linked ones, in the links' order
  private quad = new Quad();
  private grid = new Grid();

  // slice: what draws the graph gives the most a frame may take (SLICE);
  // without one, the first layout and every hurry are done at once.
  constructor(forces: Forces = FORCES, { slice = Infinity }: { slice?: number } = {}) {
    this.forces = forces;
    this.slice = slice;
    sims.add(new WeakRef(this));
  }

  // sync tells the simulation the graph as it is now: the nodes in a
  // stable order, the open ones with their page's size. The first graph is
  // laid out and settled at once; later, any change warms the graph.
  sync(nodes: LNode[]): void {
    const want = new Map(nodes.map((n) => [n.id, n]));
    let changed = false;
    for (const b of this.list)
      if (!want.has(b.id)) {
        this.byId.delete(b.id);
        if (this.held?.id === b.id) this.held = null;
        if (this.focusId === b.id) this.focusId = null;
        changed = true;
      }
    const first = this.list.length === 0 && nodes.length > 0;
    const fresh: Body[] = [];
    this.list = nodes.map((n) => {
      let b = this.byId.get(n.id);
      if (!b) {
        // every body of one shape: the loops over them stay fast
        b = { id: n.id, parent: n.parent, links: [...n.links], r: n.r, lw: n.lw, lh: n.lh, box: n.box, tile: n.tile, x: 0, y: 0, vx: 0, vy: 0 };
        this.byId.set(n.id, b);
        fresh.push(b);
        return b;
      }
      if (b.r !== n.r || b.lw !== n.lw || b.lh !== n.lh || String(b.tile) !== String(n.tile) || String(b.box) !== String(n.box) || b.parent !== n.parent || b.links.join() !== n.links.join()) {
        Object.assign(b, { parent: n.parent, links: [...n.links], r: n.r, lw: n.lw, lh: n.lh, tile: n.tile, box: n.box });
        changed = true;
      }
      return b;
    });
    for (const b of this.list) if (b.parent && !this.byId.has(b.parent)) b.parent = undefined;
    this.edges = pairs(this.list).map(([a, b]) => [this.byId.get(a)!, this.byId.get(b)!]);
    this.degree = new Map();
    this.adjacent = new Map();
    for (const [a, b] of this.edges) {
      this.degree.set(a.id, (this.degree.get(a.id) ?? 0) + 1);
      this.degree.set(b.id, (this.degree.get(b.id) ?? 0) + 1);
      if (!this.adjacent.has(a)) this.adjacent.set(a, []);
      if (!this.adjacent.has(b)) this.adjacent.set(b, []);
      this.adjacent.get(a)!.push(b);
      this.adjacent.get(b)!.push(a);
    }
    // the new ones are born one by one, the closest to the root first
    if (fresh.length) {
      const isFresh = new Set(fresh);
      const placed = new Set(this.list.filter((b) => !isFresh.has(b)).map((b) => b.id));
      const room = new Room();
      for (const b of this.list) if (!isFresh.has(b)) room.add(b);
      for (const b of fresh.sort((a, c) => this.depth(a) - this.depth(c))) {
        this.seed(b, placed, room);
        placed.add(b.id);
        room.add(b);
      }
    }
    if (first) {
      this.alpha = 1;
      this.hurry();
    } else if (changed || fresh.length) this.reheat();
  }

  // reheat warms the graph (the forces changed, or the graph): it adapts,
  // then rests again.
  reheat(a = T.warm): void {
    this.alpha = Math.max(this.alpha, a);
    this.resting = -1;
  }

  // hold keeps the body id under the pointer (at, in the graph's units);
  // the graph stays warm meanwhile, what is linked to it following.
  hold(id: string, at: Pt): void {
    const x = this.byId.get(id);
    if (!x) return;
    if (this.held?.id !== id) this.carried = this.cluster(id);
    // its cluster comes along: one link away nearly all the way, two links
    // a little; the springs do the rest, the cluster stays formed
    const dx = at[0] - x.x, dy = at[1] - x.y;
    for (const [k, share] of this.carried) {
      const b = this.byId.get(k);
      if (!b || this.focusId === k) continue;
      b.x += dx * share;
      b.y += dy * share;
    }
    this.held = { id, at };
    this.hurried = false;
    [x.x, x.y] = at;
    x.vx = x.vy = 0;
    this.reheat(T.dragWarm);
  }

  // cluster is the bodies one and two links from id, with the share of
  // its moves they take when it is dragged.
  private cluster(id: string): Map<string, number> {
    const out = new Map<string, number>();
    const seen = new Set([id]);
    let ring = [id];
    for (const share of [HOP1, HOP2]) {
      const next: string[] = [];
      for (const [p, q] of this.edges)
        for (const [a, b] of [
          [p.id, q.id],
          [q.id, p.id],
        ])
          if (ring.includes(a) && !seen.has(b)) {
            seen.add(b);
            out.set(b, share);
            next.push(b);
          }
      ring = next;
    }
    return out;
  }

  // drop lets go of the body held, to the forces like any other (as in
  // Obsidian): the graph, warmed, settles again, what is linked to it
  // coming back together.
  drop(): void {
    if (!this.held) return;
    this.held = null;
    this.carried = new Map();
    this.reheat();
  }

  // focus makes a node the graph's centre (null: none): the centre force
  // pulls every other body to it, and it stays where it is, the graph
  // settling round it. Nothing else moves at once: the graph, warmed,
  // glides to its new centre. Without one, the centre stays where the last
  // one was: the graph's middle, nothing drifts. Asked while the graph is
  // being laid out at once (hurry), it is made once that layout rests, as
  // if the layout had been all at once.
  focus(id: string | null): void {
    if (this.hurried) {
      this.later = { id };
      return;
    }
    const x = id ? this.byId.get(id) : undefined;
    const next = x ? x.id : null;
    if (next === this.focusId) return;
    this.focusId = next;
    this.spread = new Map();
    if (x) {
      this.centre = [x.x, x.y];
      this.spreadRound(x);
    }
    this.reheat();
  }

  // spreadRound gives the bodies that belong to the node in focus a place
  // to head for while the graph is warm, so that they stand all round it,
  // not on the side they came from. What belongs to it: each body linked to
  // it, and the root of each group that hangs nowhere on it (bodies linked
  // among themselves, or one alone). Any other body hangs on the page
  // through another (a bubble of its bubble, a subject's bubble) and gets
  // no place: its spring keeps it by what it hangs on, round the page if
  // need be (aim), so it is never sent to the far side of the page. The
  // places: the same order round it as now, evenly spread, each as far as
  // now or just past its edge. A glide: the forces and the collision settle
  // the rest.
  private spreadRound(f: Body): void {
    const kin = new Set<Body>();
    for (const [p, q] of this.edges) {
      if (p === f) kin.add(q);
      if (q === f) kin.add(p);
    }
    // the groups of the others, by their links that do not go through f
    const group = new Map<Body, Body[]>();
    for (const b of this.list) if (b !== f) group.set(b, [b]);
    for (const [p, q] of this.edges) {
      if (p === f || q === f) continue;
      const gp = group.get(p)!, gq = group.get(q)!;
      if (gp === gq) continue;
      for (const b of gq) {
        gp.push(b);
        group.set(b, gp);
      }
    }
    // what is spread: what is linked to f, and the root of each group
    // hanging nowhere on it (its subject, not its bubbles)
    const heads = [...kin];
    for (const g of new Set(group.values())) if (!g.some((b) => kin.has(b))) heads.push(g.reduce((r, b) => (this.depth(b) < this.depth(r) ? b : r)));
    const round = heads.map((b) => ({ b, t: Math.atan2(b.y - f.y, b.x - f.x), d: Math.hypot(b.x - f.x, b.y - f.y) })).sort((p, q) => p.t - q.t);
    const n = round.length;
    if (n < 2) return;
    // the even angles, turned to move the bodies least
    let turn = 0;
    for (let i = 0; i < n; i++) turn += Math.atan2(Math.sin(round[i].t - (i / n) * Math.PI * 2), Math.cos(round[i].t - (i / n) * Math.PI * 2));
    turn /= n;
    round.forEach(({ b, d }, i) => {
      const t = turn + (i / n) * Math.PI * 2, ux = Math.cos(t), uy = Math.sin(t);
      const r = Math.max(d, reach(f, ux, uy) + reach(b, ux, uy) + this.forces.distance);
      this.spread.set(b.id, [t, r]);
    });
  }

  // focused is the node in focus, if any.
  focused(): string | null {
    return this.focusId;
  }

  // heldId is the body held, if any.
  heldId(): string | null {
    return this.held?.id ?? null;
  }

  // asleep tells whether nothing moves any more: no need to tick until
  // something changes (sync, hold, drop, setForces warm it).
  asleep(): boolean {
    return this.alpha === 0 && !this.held && this.resting < 0;
  }

  // settle ticks until the graph is at rest (max ticks at most), all at
  // once.
  settle(max = 3000): void {
    for (let i = 0; i < max && !this.asleep(); i++) this.tick();
  }

  // hurry lays the graph out at once, not shown moving: the first layout,
  // and every change when the user asks for less motion. Past T.exact
  // bodies, what fits in a slice is done now, the rest in the next frames
  // (frame); it tells whether the graph is at rest already.
  hurry(): boolean {
    if (this.hurried && this.later) this.again = true;
    this.hurried = true;
    this.frame();
    return this.asleep();
  }

  // hurrying tells whether the graph is being laid out at once (hurry):
  // what draws it shows it once at rest, not on the way.
  hurrying(): boolean {
    return this.hurried;
  }

  // frame is a frame's work: a tick, or while the graph hurries, as many
  // as fit in the slice (3000 at most, as settle; up to T.exact bodies,
  // all). A rest's collision too goes on in the next frame once the slice
  // is spent. Once the hurry rests, the focus asked meanwhile is made.
  frame(): void {
    this.deadline = this.list.length > T.exact ? performance.now() + this.slice : Infinity;
    try {
      if (!this.hurried) this.tick();
      else
        for (let i = 0; i < 3000 && !this.asleep(); i++) {
          this.tick();
          if (performance.now() > this.deadline) break;
        }
    } finally {
      this.deadline = Infinity;
    }
    if (!this.asleep()) return;
    this.hurried = false;
    if (this.later) {
      const { id } = this.later;
      this.later = null;
      this.focus(id);
      this.hurried = this.again;
      this.again = false;
    }
  }

  // positions are where the bodies are now.
  positions(): Pos {
    const out: Pos = {};
    for (const b of this.list) out[b.id] = [b.x, b.y];
    return out;
  }

  // at is where one body is now.
  at(id: string): Pt | undefined {
    const b = this.byId.get(id);
    return b ? [b.x, b.y] : undefined;
  }

  // energy is the graph's energy now (0: at rest).
  energy(): number {
    return this.alpha;
  }

  /* ---------- the simulation ---------- */

  // tick moves the graph one frame on: the four forces, then the
  // collision; cold, it rests.
  tick(): void {
    if (this.asleep()) return;
    if (this.resting >= 0) {
      this.rest();
      return;
    }
    const held = this.held ? this.byId.get(this.held.id) : undefined;
    if (held) [held.x, held.y] = this.held!.at;
    const target = held ? T.dragWarm : 0;
    this.alpha += (target - this.alpha) * T.decay;
    if (!held && this.alpha < T.alphaMin) {
      this.rest();
      return;
    }
    const a = this.alpha, F = this.forces, L = this.list;
    const fx = this.focusId ? this.byId.get(this.focusId) : undefined;
    if (fx) this.centre = [fx.x, fx.y];
    const [cx, cy] = this.centre;
    // repulsion: every body pushes every other away (a far group as one,
    // past T.exact bodies)
    if (L.length <= T.exact) repelPairs(L, F.repulsion * a);
    else this.quad.repel(L, F.repulsion * a, T.theta);
    // links: two linked bodies are held at the link distance, shape to
    // shape; the one with fewer links moves more (d3's bias). Pulled
    // together, each heads for the other the shortest way that does not go
    // through an open page (aim): round it, not stuck behind it.
    const pages = L.filter((b) => b.box);
    for (const [p, q] of this.edges) {
      const dx = q.x - p.x, dy = q.y - p.y, d = Math.max(0.01, Math.hypot(dx, dy));
      const ux = dx / d, uy = dy / d;
      const rest = reach(p, ux, uy) + reach(q, ux, uy) + F.distance;
      const l = ((d - rest) / d) * F.link * a;
      const dp = this.degree.get(p.id) ?? 1, dq = this.degree.get(q.id) ?? 1, bias = dp / (dp + dq);
      const round = l > 0 && pages.length > 0;
      const wq = round ? this.aim(q, p, pages) : null, wp = round ? this.aim(p, q, pages) : null;
      if (wq) {
        q.vx += wq[0] * d * l * bias;
        q.vy += wq[1] * d * l * bias;
      } else {
        q.vx -= dx * l * bias;
        q.vy -= dy * l * bias;
      }
      if (wp) {
        p.vx += wp[0] * d * l * (1 - bias);
        p.vy += wp[1] * d * l * (1 - bias);
      } else {
        p.vx += dx * l * (1 - bias);
        p.vy += dy * l * (1 - bias);
      }
    }
    // centre: every body is pulled to the middle (the node in focus)
    for (const x of L) {
      x.vx -= (x.x - cx) * F.centre * a;
      x.vy -= (x.y - cy) * F.centre * a;
    }
    // after a focus change, a gentle pull to the places round it
    // (spreadRound): round it, not through it
    if (fx)
      for (const [id, [t1, r1]] of this.spread) {
        const x = this.byId.get(id);
        if (!x) continue;
        const rx = x.x - fx.x, ry = x.y - fx.y, r = Math.max(1, Math.hypot(rx, ry)), t = Math.atan2(ry, rx);
        const turn = Math.atan2(Math.sin(t1 - t), Math.cos(t1 - t));
        const ux = rx / r, uy = ry / r;
        x.vx += (-uy * turn * r + ux * (r1 - r)) * T.spread * a;
        x.vy += (ux * turn * r + uy * (r1 - r)) * T.spread * a;
      }
    // move: what is held or in focus stays
    for (const x of L) {
      if (this.fixed(x)) {
        x.vx = x.vy = 0;
        continue;
      }
      x.vx *= T.friction;
      x.vy *= T.friction;
      const sp = Math.hypot(x.vx, x.vy);
      if (sp > T.cap) {
        x.vx *= T.cap / sp;
        x.vy *= T.cap / sp;
      }
      x.x += x.vx;
      x.y += x.vy;
    }
    // collision last: softly while the graph is warm
    for (let k = 0; k < T.passes; k++) this.collide(T.soft);
  }

  // rest: the graph is cold. What still overlaps is set apart at once
  // (over the next frames if the slice is spent), and nothing moves until
  // the next change.
  private rest(): void {
    if (this.resting < 0) {
      this.alpha = 0;
      this.spread = new Map();
      for (const x of this.list) x.vx = x.vy = 0;
      this.resting = 0;
    }
    while (this.resting < T.rests) {
      if (this.collide(1) <= T.jam) break;
      this.resting++;
      if (performance.now() > this.deadline) return;
    }
    this.resting = -1;
  }

  // collide is the graph's one collision: every body (a bubble with its
  // label under it, an open page with its margin: extent) is a solid box
  // to every other. Two that overlap are set apart the shorter way, each
  // by a share that falls with its size (a page moves less than a bubble);
  // what is held or in focus does not move. It gives the largest
  // overlap it found.
  private collide(strength: number): number {
    let most = 0;
    const L = this.list;
    if (L.length <= T.exact) {
      for (let i = 0; i < L.length; i++) for (let j = i + 1; j < L.length; j++) most = Math.max(most, this.part(L[i], L[j], strength));
      return most;
    }
    // past T.exact bodies, each against those in its cells only, in the
    // same order; the margin covers what the pass moves them
    this.grid.fill(L, 2 * T.jam + 2);
    for (let i = 0; i < L.length; i++) for (const j of this.grid.after(i)) most = Math.max(most, this.part(L[i], L[j], strength));
    return most;
  }

  // part sets a and b apart if they overlap (collide); it gives by how
  // much they did (0: they did not, or neither may move).
  private part(a: Body, b: Body, strength: number): number {
    ext(a, a.y);
    const ha = EH, ta = ET, ba = EB;
    ext(b, b.y);
    const hb = EH, tb = ET, bb = EB;
    const dx = b.x - a.x;
    const ox = ha + hb - Math.abs(dx), oy = Math.min(ba, bb) - Math.max(ta, tb);
    if (ox <= 0 || oy <= 0) return 0;
    const fa = this.fixed(a), fb = this.fixed(b);
    if (fa && fb) return 0;
    const ma = 2 * ha * (ba - ta), mb = 2 * hb * (bb - tb);
    const sb = fa ? 1 : fb ? 0 : ma / (ma + mb), sa = 1 - sb;
    // sideways a little preferred: the screen is wide
    if (ox <= oy * 1.3) {
      const s = (Math.abs(dx) > 0.01 ? Math.sign(dx) : hash(a.id + b.id) < 0.5 ? 1 : -1) * ox * strength;
      a.x -= s * sa;
      b.x += s * sb;
    } else {
      const s = ((tb + bb) / 2 >= (ta + ba) / 2 ? 1 : -1) * oy * strength;
      a.y -= s * sa;
      b.y += s * sb;
    }
    return Math.min(ox, oy);
  }

  // aim is the way x goes to join to (a unit vector) when the straight
  // line between them crosses an open page that is neither (the first one
  // on that line): toward the corner that starts the shortest way round
  // the page. x goes round it as the collision lets it, its whole box
  // clear of the page's: the page widened by x's box (room), the corners
  // just past it, x heading only for a corner it can reach without
  // touching the page; the last stretch, from a corner to the other end,
  // is a link line, it only has to miss the page. Null when the line is
  // clear (or x is on the page: the collision sets it off first):
  // straight to it.
  private aim(x: Body, to: Body, pages: Body[]): Pt | null {
    const from: Pt = [x.x, x.y], goal: Pt = [to.x, to.y];
    let page: Body | null = null, first = Infinity;
    for (const o of pages) {
      if (o === x || o === to) continue;
      const r = rect(o);
      if (inside(from, r)) return null;
      const t = enters(from, goal, r);
      if (t < first) [first, page] = [t, o];
    }
    if (!page) return null;
    const r = rect(page), [h, top, bottom] = extent(x, from);
    const room = [r[0] - h, r[1] - (bottom - x.y), r[2] + h, r[3] + (x.y - top)];
    const corners: Pt[] = [
      [room[0] - 4, room[1] - 4],
      [room[2] + 4, room[1] - 4],
      [room[2] + 4, room[3] + 4],
      [room[0] - 4, room[3] + 4],
    ];
    // x in the room already (two bodies jammed): its first stretch only
    // has to miss the page
    const near = inside(from, room) ? r : room;
    const free = (a: Pt, b: Pt, box: number[]) => enters(a, b, box) === Infinity;
    const len = (a: Pt, b: Pt) => Math.hypot(a[0] - b[0], a[1] - b[1]);
    // the ways round a box: by one corner, or by two that share a side
    let best = null as Pt | null, shortest = Infinity;
    corners.forEach((c, i) => {
      if (!free(from, c, near)) return;
      if (free(c, goal, r) && len(from, c) + len(c, goal) < shortest) [shortest, best] = [len(from, c) + len(c, goal), c];
      for (const k of [corners[(i + 1) % 4], corners[(i + 3) % 4]]) {
        const s = len(from, c) + len(c, k) + len(k, goal);
        if (free(k, goal, r) && s < shortest) [shortest, best] = [s, c];
      }
    });
    if (!best) return null;
    const ex = best[0] - x.x, ey = best[1] - x.y, e = Math.hypot(ex, ey);
    return e > 0.01 ? [ex / e, ey / e] : null;
  }

  private fixed(x: Body): boolean {
    return this.held?.id === x.id || this.focusId === x.id;
  }

  /* ---------- birth ---------- */

  // seed places a new body in the most free space near what it is linked
  // to (those already placed), or, linked to nothing, in the most free
  // space on the disc, near its middle. The forces do the rest. room holds
  // the bodies placed.
  private seed(x: Body, placed: Set<string>, room: Room): void {
    if (!placed.size) {
      x.x = x.y = 0;
      return;
    }
    const kin = (this.adjacent.get(x) ?? []).filter((o) => placed.has(o.id));
    const turn = hash(x.id) * Math.PI * 2, N = 24;
    let best: Pt = [0, 0], score = -Infinity;
    const consider = (p: Pt, near: Pt) => {
      // a place too far to do better than the best yet, however free, is
      // not looked at; nor the bodies round one once it cannot
      const far = 0.05 * Math.hypot(p[0] - near[0], p[1] - near[1]);
      if (ROOM - far <= score) return;
      const s = Math.min(room.clearance(x, p, score + far), ROOM) - far;
      if (s > score) {
        score = s;
        best = p;
      }
    };
    if (kin.length) {
      const c: Pt = [kin.reduce((s, o) => s + o.x, 0) / kin.length, kin.reduce((s, o) => s + o.y, 0) / kin.length];
      for (const step of [1, 1.4, 1.9, 2.6, 3.5])
        for (let i = 0; i < N; i++) {
          const t = turn + (i / N) * Math.PI * 2, ux = Math.cos(t), uy = Math.sin(t);
          const d = (Math.max(...kin.map((o) => reach(o, ux, uy))) + reach(x, ux, uy) + this.forces.distance) * step;
          consider([c[0] + ux * d, c[1] + uy * d], c);
        }
    } else {
      const others = this.list.filter((o) => o !== x && placed.has(o.id));
      const c: Pt = [others.reduce((s, o) => s + o.x, 0) / others.length, others.reduce((s, o) => s + o.y, 0) / others.length];
      let R = 0;
      for (const o of others) {
        ext(o, o.y);
        R = Math.max(R, Math.hypot(o.x - c[0], o.y - c[1]) + EH);
      }
      for (let d = 60; d <= R + 240; d += 60)
        for (let i = 0; i < N; i++) {
          const t = turn + (i / N) * Math.PI * 2;
          consider([c[0] + Math.cos(t) * d, c[1] + Math.sin(t) * d * 0.75], c);
        }
    }
    [x.x, x.y] = best;
    x.vx = x.vy = 0;
  }

  private depth(x: Body): number {
    let d = 0;
    for (let p = x.parent; p && d < 64; p = this.byId.get(p)?.parent) d++;
    return d;
  }
}

// Edge scrolling while a bubble is dragged (#633): when the bubble, not the
// pointer, comes within EDGE_ZONE of the screen's edge, the camera moves,
// after EDGE_DELAY, speeding up over EDGE_RAMP to EDGE_SPEED px a frame.
export const EDGE_ZONE = 110;
export const EDGE_DELAY = 200;
export const EDGE_RAMP = 700;
export const EDGE_SPEED = 16;

// edgePush is how hard a point at p presses on the edges lo and hi: -1 to
// 1, 0 away from them.
export function edgePush(p: number, lo: number, hi: number): number {
  const z = EDGE_ZONE;
  return Math.max(-1, Math.min(1, p < lo + z ? (p - lo - z) / z : p > hi - z ? (p - hi + z) / z : 0));
}

// edgeStep is how far the camera moves this frame, pressed since since.
export function edgeStep(push: number, now: number, since: number): number {
  const ramp = Math.min(1, Math.max(0, (now - since - EDGE_DELAY) / EDGE_RAMP));
  return -push * EDGE_SPEED * ramp;
}
