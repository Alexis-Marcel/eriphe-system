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
// stay on the side the user chose while the centre keeps them in the disc. The node in focus (focus) can be the
// centre: the centre force pulls to it and it stays where it is. Nothing
// here knows a node by its name.

export type Pt = [number, number];
export type Pos = Record<string, Pt>;
// r: the bubble's radius; lw, lh: the size of the label under it; box: an
// open page, drawn in the graph. tile: the bubble drawn as its glance, a
// rounded rectangle of that size (graph/glance.ts): its body follows that
// shape.
export type LNode = { id: string; parent?: string; links: string[]; r: number; lw: number; lh: number; box?: Pt; tile?: Pt };

// The four forces, the owner's to tune (the ?physique dev page). One
// object shared by the simulations; setForces changes it and warms them.
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
};

// FORCE_RANGES are the bounds a setting may give each force.
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
// ticks one (Graph.tsx, onForces) is told, to wake.
export function setForces(f: Partial<Forces>): void {
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
  return x.box
    ? [x.box[0] / 2 + 24, at[1] - x.box[1] / 2 - 24, at[1] + x.box[1] / 2 + 24]
    : x.tile
      ? [Math.max(x.tile[0] / 2, x.lw / 2) + 10, at[1] - x.tile[1] / 2 - 8, at[1] + x.tile[1] / 2 + 10 + x.lh]
      : [Math.max(x.r, x.lw / 2) + 10, at[1] - x.r - 8, at[1] + x.r + 10 + x.lh];
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

// A body of the simulation: a node, where it is and how fast it goes.
type Body = LNode & { x: number; y: number; vx: number; vy: number };

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

  constructor(forces: Forces = FORCES) {
    this.forces = forces;
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
        b = { ...n, links: [...n.links], x: 0, y: 0, vx: 0, vy: 0 };
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
    for (const [a, b] of this.edges) {
      this.degree.set(a.id, (this.degree.get(a.id) ?? 0) + 1);
      this.degree.set(b.id, (this.degree.get(b.id) ?? 0) + 1);
    }
    // the new ones are born one by one, the closest to the root first
    const placed = new Set(this.list.filter((b) => !fresh.includes(b)).map((b) => b.id));
    for (const b of fresh.sort((a, c) => this.depth(a) - this.depth(c))) {
      this.seed(b, placed);
      placed.add(b.id);
    }
    if (first) {
      this.alpha = 1;
      this.settle();
    } else if (changed || fresh.length) this.reheat();
  }

  // reheat warms the graph (the forces changed, or the graph): it adapts,
  // then rests again.
  reheat(a = T.warm): void {
    this.alpha = Math.max(this.alpha, a);
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
  // one was: the graph's middle, nothing drifts.
  focus(id: string | null): void {
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

  // spreadRound gives each other body a place to head for while the graph
  // is warm, so that they stand all round the node in focus, not on the
  // side they came from: the same order round it as now (a cluster stays
  // together), evenly spread, each as far as now or just past its edge.
  // A glide: the forces and the collision settle the rest.
  private spreadRound(f: Body): void {
    const others = this.list
      .filter((b) => b !== f)
      .map((b) => ({ b, t: Math.atan2(b.y - f.y, b.x - f.x), d: Math.hypot(b.x - f.x, b.y - f.y) }))
      .sort((p, q) => p.t - q.t);
    const n = others.length;
    if (n < 2) return;
    // the even angles, turned to move the bodies least
    let turn = 0;
    for (let i = 0; i < n; i++) turn += Math.atan2(Math.sin(others[i].t - (i / n) * Math.PI * 2), Math.cos(others[i].t - (i / n) * Math.PI * 2));
    turn /= n;
    others.forEach(({ b, d }, i) => {
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
    return this.alpha === 0 && !this.held;
  }

  // settle ticks until the graph is at rest (max ticks at most): the
  // first layout, and every change when the user asks for less motion.
  settle(max = 3000): void {
    for (let i = 0; i < max && !this.asleep(); i++) this.tick();
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
    // repulsion: every body pushes every other away
    for (let i = 0; i < L.length; i++)
      for (let j = i + 1; j < L.length; j++) {
        const p = L[i], q = L[j];
        let dx = q.x - p.x, dy = q.y - p.y, d = Math.hypot(dx, dy);
        if (d < 1) {
          dx = hash(p.id + q.id) - 0.5;
          dy = 0.5 - hash(q.id + p.id);
          d = Math.hypot(dx, dy) || 1;
        }
        const f = (F.repulsion * a) / Math.max(d, T.chargeMin) / d;
        p.vx -= dx * f;
        p.vy -= dy * f;
        q.vx += dx * f;
        q.vy += dy * f;
      }
    // links: two linked bodies are held at the link distance, shape to
    // shape; the one with fewer links moves more (d3's bias)
    for (const [p, q] of this.edges) {
      const dx = q.x - p.x, dy = q.y - p.y, d = Math.max(0.01, Math.hypot(dx, dy));
      const ux = dx / d, uy = dy / d;
      const rest = reach(p, ux, uy) + reach(q, ux, uy) + F.distance;
      const l = ((d - rest) / d) * F.link * a;
      const dp = this.degree.get(p.id) ?? 1, dq = this.degree.get(q.id) ?? 1, bias = dp / (dp + dq);
      q.vx -= dx * l * bias;
      q.vy -= dy * l * bias;
      p.vx += dx * l * (1 - bias);
      p.vy += dy * l * (1 - bias);
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

  // rest: the graph is cold. What still overlaps is set apart at once,
  // and nothing moves until the next change.
  private rest(): void {
    this.alpha = 0;
    this.spread = new Map();
    for (let i = 0; i < 200 && this.collide(1) > T.jam; i++);
    for (const x of this.list) x.vx = x.vy = 0;
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
    for (let i = 0; i < L.length; i++)
      for (let j = i + 1; j < L.length; j++) {
        const a = L[i], b = L[j];
        const [ha, ta, ba] = extent(a, [a.x, a.y]), [hb, tb, bb] = extent(b, [b.x, b.y]);
        const dx = b.x - a.x;
        const ox = ha + hb - Math.abs(dx), oy = Math.min(ba, bb) - Math.max(ta, tb);
        if (ox <= 0 || oy <= 0) continue;
        const fa = this.fixed(a), fb = this.fixed(b);
        if (fa && fb) continue;
        most = Math.max(most, Math.min(ox, oy));
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
      }
    return most;
  }

  private fixed(x: Body): boolean {
    return this.held?.id === x.id || this.focusId === x.id;
  }

  /* ---------- birth ---------- */

  // seed places a new body in the most free space near what it is linked
  // to (those already placed), or, linked to nothing, in the most free
  // space on the disc, near its middle. The forces do the rest.
  private seed(x: Body, placed: Set<string>): void {
    const others = this.list.filter((o) => o !== x && placed.has(o.id));
    if (!others.length) {
      x.x = x.y = 0;
      return;
    }
    const kin = this.edges.flatMap(([p, q]) => (p === x && placed.has(q.id) ? [q] : q === x && placed.has(p.id) ? [p] : []));
    const turn = hash(x.id) * Math.PI * 2, N = 24;
    let best: Pt = [0, 0], score = -Infinity;
    const consider = (p: Pt, near: Pt) => {
      const s = Math.min(this.clearance(x, p, others), 80) - 0.05 * Math.hypot(p[0] - near[0], p[1] - near[1]);
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
      const c: Pt = [others.reduce((s, o) => s + o.x, 0) / others.length, others.reduce((s, o) => s + o.y, 0) / others.length];
      let R = 0;
      for (const o of others) R = Math.max(R, Math.hypot(o.x - c[0], o.y - c[1]) + extent(o, [o.x, o.y])[0]);
      for (let d = 60; d <= R + 240; d += 60)
        for (let i = 0; i < N; i++) {
          const t = turn + (i / N) * Math.PI * 2;
          consider([c[0] + Math.cos(t) * d, c[1] + Math.sin(t) * d * 0.75], c);
        }
    }
    [x.x, x.y] = best;
    x.vx = x.vy = 0;
  }

  // clearance is the room between a body x at p and the closest other one,
  // box to box (below 0: they overlap).
  private clearance(x: Body, p: Pt, others: Body[]): number {
    const [h, t, b] = extent(x, p);
    let least = Infinity;
    for (const o of others) {
      const [ho, to, bo] = extent(o, [o.x, o.y]);
      const gx = Math.abs(p[0] - o.x) - h - ho, gy = Math.max(to - b, t - bo);
      least = Math.min(least, gx > 0 && gy > 0 ? Math.hypot(gx, gy) : Math.max(gx, gy));
    }
    return least;
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
