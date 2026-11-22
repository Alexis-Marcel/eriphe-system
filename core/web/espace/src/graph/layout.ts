// The graph's layout: a living force graph, like Obsidian's. Every bubble
// pushes every other away (many-body, no cutoff), all are pulled to the
// centre, a bubble and its parent, and two subjects a text links, are held
// close by short strong springs; a bubble never rests on a link it is not
// part of. Every body, a bubble with its label or an open page, is solid
// to every other by one collision (collide). The graph is a compact disc
// around the centre, the subjects linked to nothing on its rim.
//
// Nothing is remembered: no place is kept, nothing orders the subjects.
// Like Obsidian's graph, it never goes cold: every body always keeps a
// little energy (ambient), so the centre pull, the repulsion, the springs
// and the collision always act and the graph is always a compact disc,
// pages open or not. The first layout runs on a global energy (alpha) and
// is settled before it is shown; a page that opens or closes warms the
// whole graph a while (stir), a new node is warm, and so is what a drag
// carries. The open pages glide to the middle of the graph, side by side
// in the order they stand in; the bubbles gather round them, and come
// back when they close. Once nothing moves over a window of ticks the
// graph is at rest and ticks no more until something changes: friction
// and a little stiction keep it still, never trembling.
//
// A drag (hold, drop): the held body passes over everything; the bodies
// linked to it follow on their springs, with an energy that falls with
// the links between them (HOP1, HOP2, beyond still), passing over what is
// not theirs; nothing else moves. Dropped, it stays exactly there for the
// session (a bubble by its subject; one dropped by its subject's open
// page goes back on its springs) and wins every collision: what is
// under it steps aside by the smallest move; its followers settle on
// their springs, giving way to the bodies in place. A subject the graph
// left far behind (loose past the disc's edge) is let go. Nothing here
// knows a node by its name.

export type Pt = [number, number];
export type Pos = Record<string, Pt>;
// r: the bubble's radius; lw, lh: the size of the label under it, so
// labels never sit on each other; box: an open page, drawn in the graph.
// tile (prototype, proto/node-glance): the bubble drawn as a glance, a
// rounded rectangle of that size: its body follows that shape.
export type LNode = { id: string; parent?: string; links: string[]; r: number; lw: number; lh: number; box?: Pt; tile?: Pt };

// The tuning, validated in a prototype by the owner.
export const T = {
  decay: 0.0228, // the global energy cools by this share a tick (about 300 ticks, like d3)
  alphaMin: 0.004, // ... and below this it is none
  friction: 0.6, // the velocity kept from one tick to the next
  gravity: 0.09, // the pull to the centre, × the energy: the graph is a disc
  charge: 1600, // the many-body repulsion, strength / d (d3's manyBody shape), × the energy
  chargeMin: 40, // ... d never counts below this (d3's distanceMin)
  childK: 0.5, // a bubble's spring to its parent
  childGap: 34, // ... its rest: the two radii and this
  linkK: 0.4, // a text link's spring
  linkGap: 24, // ... its rest: the two bodies' half widths and this
  edgeAvoid: 0.5, // a bubble on another pair's link is pushed off it
  edgeClear: 18, // ... until its edge is this far from the line
  glide: 0.18, // the dropped body glides to its spot: the share of the gap closed each tick
  soft: 0.5, // collision: the share of an overlap undone each tick
  cool: 0.97, // a body's own warmth kept from one tick to the next
  fresh: 120, // a node is new (gives way first) for this many ticks
  pass: 150, // a follower of a drop passes over the others until it is back on its springs, this many ticks at most
  passWarm: 0.3, // ... warm at least this meanwhile (its drag energy when lower)
  passNear: 60, // ... back: none of its links stretched more than this
  ambient: 0.05, // the energy every body always has, at least: the graph is always drawn to its centre
  still: 2, // the graph is at rest when no body moved more than this ...
  calm: 24, // ... over this many ticks (contacts tremble a little under the forces, in place)
  stick: 0.25, // a body on the ambient energy alone that would move less than this a tick stays still: no creeping
  stir: 0.4, // a page that opens or closes warms the whole graph this much, cooling like the first layout
  stirDecay: 0.012, // ... cooling by this share a tick
  awake: 420, // the graph rests at the latest this many ticks after it last changed
  pageGlide: 0.08, // an open page glides to its place in the middle: the share of the way each tick
  pageWarm: 0.3, // a page opening is this warm: it comes to the centre soon
  jam: 4, // the graph does not rest while two bodies sit on each other more than this (within their margins: never visible)
  loose: 200, // a dropped subject this far past the disc's edge is let go
};

// The user's reach when dragging: a subject dragged past REACH beyond the
// disc's edge lags behind the pointer by RUBBER, and dropped there it
// comes back RIM past the edge, on that side; a bubble stays within TETHER
// of its parent (of its page's edge, when it is open).
export const REACH = 420;
export const RIM = 80;
export const RUBBER = 0.3;
export const TETHER = 300;

// HOP1 and HOP2 are the energies of the bodies one and two links away
// from the one dragged; beyond, they are still.
export const HOP1 = 0.8;
export const HOP2 = 0.25;

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

// bornBeside is where a page opened while another is open (from, its
// size fromBox) is born: where its bubble was (at), unless it would cover
// that page; then right beside it, at the same height (on the side of its
// bubble), so that the camera framing the new one keeps the other above
// the bar too.
export function bornBeside(at: Pt, box: Pt, from: Pt, fromBox: Pt): Pt {
  const gap = 48; // their two margins
  const apart = Math.abs(at[0] - from[0]) >= (box[0] + fromBox[0]) / 2 + gap || Math.abs(at[1] - from[1]) >= (box[1] + fromBox[1]) / 2 + gap;
  if (apart) return at;
  const side = at[0] < from[0] ? -1 : 1;
  return [from[0] + side * ((box[0] + fromBox[0]) / 2 + gap), from[1]];
}

// edgeRest is how far from an open page's centre its bubble x rests, in
// the direction (ux, uy): just past the page's edge, its label clear.
function edgeRest([w, h]: Pt, ux: number, uy: number, x: LNode): number {
  const [rx, ry] = x.tile ? [x.tile[0] / 2, x.tile[1] / 2] : [x.r, x.r];
  const hx = w / 2 + 24 + Math.max(rx, x.lw / 2) + 10, hy = h / 2 + 24 + ry + x.lh + 10;
  return Math.min(Math.abs(ux) > 1e-6 ? hx / Math.abs(ux) : Infinity, Math.abs(uy) > 1e-6 ? hy / Math.abs(uy) : Infinity) + 30;
}

// A body of the simulation: a node, where it is and how fast it goes.
type Body = LNode & { x: number; y: number; vx: number; vy: number; born: number; opened?: number };

// Contact is two bodies that touch, or nearly: n, the way from a to b;
// gap, the room between them; sb, the share of the meeting b takes (1: b
// gives way, 0: a does, 0.5: both) as they move.
type Contact = { a: Body; b: Body; n: Pt; gap: number; sb: number };

// Fix is a body the user dropped: where it stays (from its parent when it
// is a bubble), and when it was dropped (the last one wins).
type Fix = { at: Pt; of?: string; seq: number };

// HELD is the rank of the body held: it never gives way.
const HELD = 0;

// Sim is the graph's simulation: Graph.tsx keeps one for the session,
// tells it the nodes (sync), the drags (hold, drop), and ticks it a frame
// at a time while it is awake.
export class Sim {
  private list: Body[] = [];
  private byId = new Map<string, Body>();
  private alpha = 0;
  private stir = 0; // the energy a page opening or closing gives the whole graph, cooling back to the ambient one
  private woke = 0; // the tick the graph last changed at
  private jammed = false; // the collision left bodies on each other at the last tick: not at rest
  private ticks = 0;
  private calm = false; // at rest: nothing moved over the last window
  private mark: { at: Pt[]; tick: number } | null = null; // where the bodies were when the window began
  private heat = new Map<string, number>(); // a body's own energy, above the ambient one, cooling
  private held: { id: string; at: Pt } | null = null;
  private group = new Map<string, number>(); // the held body's followers, their energy
  private fixed = new Map<string, Fix>(); // the bodies the user dropped
  private fixSeq = 0;
  private trail = new Set<string>(); // the followers of the last drop, giving way while they settle
  private anchor: Pt = [0, 0]; // where the last drop was: its followers gather around it
  private passing = new Map<string, { n: number; e: number }>(); // the followers still coming round: the ticks they have left, their warmth
  private openSeq = 0;
  private contacts: Contact[] = []; // the bodies that touched at the last tick
  private home = new Set<string>(); // the bubbles of a page that closed, coming back round their parent

  // sync tells the simulation the graph as it is now: the nodes in a
  // stable order, the open ones with their page's size. beside is the page
  // a page opened now goes beside (the one the console talks to), else the
  // last one opened. The first graph is laid out on a global energy and
  // settled at once; later, what changed is warmed, and every change wakes
  // the graph: the ambient energy does the rest.
  sync(nodes: LNode[], beside?: string | null): void {
    const want = new Map(nodes.map((n) => [n.id, n]));
    // gone
    for (const b of this.list) if (!want.has(b.id)) this.forget(b.id);
    this.list = this.list.filter((b) => want.has(b.id));
    const first = this.list.length === 0 && nodes.length > 0;
    // the nodes, in their order; the new ones seeded below
    const fresh: Body[] = [];
    const next: Body[] = [];
    for (const n of nodes) {
      let b = this.byId.get(n.id);
      if (!b) {
        b = { id: n.id, parent: n.parent, links: n.links, r: n.r, lw: n.lw, lh: n.lh, tile: n.tile, x: 0, y: 0, vx: 0, vy: 0, born: this.ticks };
        this.byId.set(n.id, b);
        fresh.push(b);
      } else if (b.r !== n.r || b.lw !== n.lw || b.lh !== n.lh || String(b.tile) !== String(n.tile) || b.parent !== n.parent || b.links.join() !== n.links.join()) {
        // a bubble that turns into a tile, or back, is warmed: its neighbours step aside
        if (String(b.tile) !== String(n.tile)) this.warmUp(b.id, 0.3);
        Object.assign(b, { parent: n.parent, links: n.links, r: n.r, lw: n.lw, lh: n.lh, tile: n.tile });
        this.wake();
      }
      next.push(b);
    }
    this.list = next;
    for (const b of this.list) if (b.parent && !this.byId.has(b.parent)) b.parent = undefined;
    for (const [id, f] of this.fixed) if (f.of && this.byId.get(id)?.parent !== f.of) this.fixed.delete(id);
    if (first) this.seedAll();
    else for (const b of fresh.sort((a, c) => this.depth(a) - this.depth(c))) this.seed(b);
    if (first) {
      this.alpha = 1;
      this.settle();
    }
    // the pages: those closed first (a click that opens one alone closes
    // the others), those that changed size, then those opened, in order
    for (const b of this.list) if (b.box && !want.get(b.id)!.box) this.close(b);
    for (const b of this.list) {
      const box = want.get(b.id)!.box;
      if (b.box && box && (b.box[0] !== box[0] || b.box[1] !== box[1])) {
        // its bubbles go with its edges as it grows, beside it on the side
        // they stood (else the edge passing over them would put them on
        // any side); as it shrinks, their springs bring them back that
        // way: a page enlarged then given back its size has them where
        // they were
        const dw = Math.max(0, box[0] - b.box[0]) / 2, dh = Math.max(0, box[1] - b.box[1]) / 2;
        for (const k of this.own(b)) {
          const ox = k.x - b.x, oy = k.y - b.y;
          if (Math.abs(ox) > b.box[0] / 2) k.x += Math.sign(ox) * dw;
          if (Math.abs(oy) > b.box[1] / 2) k.y += Math.sign(oy) * dh;
        }
        b.box = box;
        for (const k of this.below(b)) this.warmUp(k.id, 0.3);
        this.wake();
      }
    }
    for (const n of nodes) {
      const b = this.byId.get(n.id)!;
      if (!b.box && n.box) this.open(b, n.box, beside ?? undefined);
    }
    if (first) this.settle();
  }

  // hold keeps the body id under the pointer (at, in the graph's units),
  // within reach (leash): it passes over everything, its linked bodies
  // follow, nothing else moves.
  hold(id: string, at: Pt): void {
    const x = this.byId.get(id);
    if (!x) return;
    if (this.held?.id !== id) {
      this.group = this.neighbourhood(id);
      this.fixed.delete(id);
      this.trail = new Set();
      this.passing = new Map();
    }
    const p = this.leash(x, at, false);
    this.held = { id, at: p };
    [x.x, x.y] = p;
    x.vx = x.vy = 0;
    this.wake();
  }

  // drop lets go of the body held: it stays where it is (back at the
  // disc's edge if it was out in the void) for the session, and wins every
  // collision (the last dropped first). Its followers settle on their
  // springs, giving way to the bodies in place.
  drop(): void {
    const h = this.held;
    if (!h) return;
    this.held = null;
    const x = this.byId.get(h.id);
    if (!x) return;
    const at = this.leash(x, [x.x, x.y], true);
    const p = x.parent ? this.byId.get(x.parent) : undefined;
    // a bubble (not a page) dropped by its subject's open page lives on
    // its springs round the page: nothing is kept of where it was
    // dropped, that the page closed would put far from its subject
    if (!p?.box || x.box) this.fixed.set(x.id, p ? { at: [at[0] - p.x, at[1] - p.y], of: p.id, seq: ++this.fixSeq } : { at, seq: ++this.fixSeq });
    else [x.x, x.y] = at;
    for (const [id, e] of this.group) this.warmUp(id, e);
    this.trail = new Set(this.group.keys());
    this.anchor = at;
    // the bubbles of the followers that stayed still come back to them
    for (const id of [...this.trail])
      for (const k of this.below(this.byId.get(id)!))
        if (k !== x && !this.trail.has(k.id)) {
          this.warmUp(k.id, HOP2);
          this.trail.add(k.id);
        }
    this.passing = new Map([...this.trail].map((id) => [id, { n: T.pass, e: Math.min(T.passWarm, this.heat.get(id) ?? 0) }]));
    this.group = new Map();
    this.wake();
  }

  // heldId is the body held, if any.
  heldId(): string | null {
    return this.held?.id ?? null;
  }

  // asleep tells whether nothing moves any more: no need to tick until
  // something changes (sync, hold, drop wake it).
  asleep(): boolean {
    return (this.calm || this.ticks - this.woke > T.awake) && !this.jammed && !this.held && this.heat.size === 0 && this.passing.size === 0 && this.alpha === 0 && this.stir === 0;
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

  /* ---------- the simulation ---------- */

  // tick moves the graph one frame on; a graph at rest does not move
  // until something changes.
  tick(): void {
    if (this.asleep()) return;
    this.ticks++;
    const held = this.held ? this.byId.get(this.held.id) : undefined;
    if (held) [held.x, held.y] = this.held!.at;
    this.alpha += (0 - this.alpha) * T.decay;
    if (this.alpha < T.alphaMin) this.alpha = 0;
    // the stir cools like the first layout, down to the ambient energy
    this.stir -= this.stir * T.stirDecay;
    if (this.stir < T.ambient) this.stir = 0;
    this.loosen();
    const group = this.group;
    const free = (x: Body) => !x.box && held !== x && !this.pinned(x) && (!held || group.has(x.id));
    // each body's energy: the ambient one at least, the first layout's,
    // or its own; during a drag, its hop's (nothing else moves)
    const E = new Map(this.list.map((x) => [x.id, held ? (group.get(x.id) ?? 0) : Math.max(T.ambient, this.alpha, this.stir, this.heat.get(x.id) ?? 0)]));
    const e = (x: Body) => E.get(x.id) ?? 0;
    const L = this.list;
    // many-body: every bubble pushes every other away, no cutoff (pages make room by collision)
    for (let i = 0; i < L.length; i++)
      for (let j = i + 1; j < L.length; j++) {
        const a = L[i], b = L[j];
        if (a.box || b.box) continue;
        const ea = e(a), eb = e(b);
        if (!ea && !eb) continue;
        // during a drag only the dragged group pushes itself apart: the followers trail on their springs
        if (held && !((a === held || group.has(a.id)) && (b === held || group.has(b.id)))) continue;
        let dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy);
        if (d < 1) {
          dx = hash(a.id + b.id) - 0.5;
          dy = 0.5 - hash(b.id + a.id);
          d = Math.hypot(dx, dy) || 1;
        }
        const s = T.charge / Math.max(d, T.chargeMin), sa = s * ea * 0.5, sb = s * eb * 0.5;
        const ux = dx / d, uy = dy / d;
        a.vx -= ux * sa;
        a.vy -= uy * sa;
        b.vx += ux * sb;
        b.vy += uy * sb;
      }
    // springs: a bubble to its parent (just past its page's edge when it
    // is open), and the text links between subjects
    const spring = (a: Body, b: Body, rest: number, k: number, wa: number) => {
      const dx = b.x - a.x, dy = b.y - a.y, d = Math.max(0.01, Math.hypot(dx, dy));
      const l = ((d - rest) / d) * k;
      if (free(a)) {
        a.vx += dx * l * wa * e(a);
        a.vy += dy * l * wa * e(a);
      }
      if (free(b)) {
        b.vx -= dx * l * (1 - wa) * e(b);
        b.vy -= dy * l * (1 - wa) * e(b);
      }
    };
    for (const x of L) {
      const p = x.parent ? this.byId.get(x.parent) : undefined;
      if (p) {
        const dx = x.x - p.x, dy = x.y - p.y, d = Math.max(0.01, Math.hypot(dx, dy));
        const rest = p.box ? edgeRest(p.box, dx / d, dy / d, x) : p.r + x.r + T.childGap;
        spring(p, x, rest, T.childK, p.box ? 0 : 0.15);
      }
      for (const l of x.links) {
        const o = this.byId.get(l);
        if (!o || o === x || x.box || o.box) continue;
        spring(x, o, extent(x, [0, 0])[0] + extent(o, [0, 0])[0] + T.linkGap, T.linkK, 0.5);
      }
    }
    // no bubble rests on a link it is not part of: pushed off it, and the
    // link's ends a little the other way
    for (const a of L) {
      for (const b of this.ends(a)) {
        if (a.box || b.box) continue;
        const ex = b.x - a.x, ey = b.y - a.y, L2 = ex * ex + ey * ey;
        if (L2 < 1) continue;
        for (const c of L) {
          if (c === a || c === b || c.box) continue;
          if (!e(c) && !e(a) && !e(b)) continue;
          const t = ((c.x - a.x) * ex + (c.y - a.y) * ey) / L2;
          if (t <= 0.05 || t >= 0.95) continue;
          let nx = c.x - (a.x + ex * t), ny = c.y - (a.y + ey * t);
          const d = Math.hypot(nx, ny), want = c.r + T.edgeClear;
          if (d >= want) continue;
          if (d < 0.01) {
            nx = -ey;
            ny = ex;
          }
          const n = Math.hypot(nx, ny), f = (want - d) * T.edgeAvoid;
          if (free(c)) {
            c.vx += (nx / n) * f * e(c);
            c.vy += (ny / n) * f * e(c);
          }
          if (free(a)) {
            a.vx -= (nx / n) * f * 0.5 * e(a);
            a.vy -= (ny / n) * f * 0.5 * e(a);
          }
          if (free(b)) {
            b.vx -= (nx / n) * f * 0.5 * e(b);
            b.vy -= (ny / n) * f * 0.5 * e(b);
          }
        }
      }
    }
    // a body's own energy cools
    for (const [id, h] of this.heat) {
      if (h < 0.005 || !this.byId.has(id)) {
        this.heat.delete(id);
        this.home.delete(id);
      } else this.heat.set(id, h * T.cool);
    }
    // the pull to the centre: the graph stays a compact disc (none during
    // a drag; what followed one gathers around where it was dropped while
    // it settles)
    if (!held)
      for (const x of L) {
        if (!free(x)) continue;
        const [cx, cy] = this.trail.has(x.id) ? this.anchor : [0, 0];
        x.vx -= (x.x - cx) * T.gravity * e(x);
        x.vy -= (x.y - cy) * T.gravity * e(x);
      }
    // move
    const cap = 40;
    // a body on the ambient energy alone, all but still, is still
    const stuck = (x: Body) => !held && !this.alpha && e(x) <= T.ambient && Math.hypot(x.vx, x.vy) < T.stick;
    for (const x of L) {
      if (!free(x)) x.vx = x.vy = 0;
      x.vx *= T.friction;
      x.vy *= T.friction;
      if (stuck(x)) x.vx = x.vy = 0;
    }
    // what touches meets without going into each other: the one that
    // gives way is carried, two of a rank go on together (after the first
    // layout: no trembling against what holds a body back)
    if (!held && !this.alpha)
      for (let k = 0; k < 4; k++)
        for (const { a, b, n, sb, gap } of this.contacts) {
        const fa = free(a), fb = free(b);
        if (!fa && !fb) continue;
        // two pages side by side go together: they stay as they were opened
        if (a.box && b.box && fa && fb) {
          a.vx = b.vx = (a.vx + b.vx) / 2;
          a.vy = b.vy = (a.vy + b.vy) / 2;
          continue;
        }
        const over = a.vx * n[0] + a.vy * n[1] - (b.vx * n[0] + b.vy * n[1]) - gap;
        if (over <= 0) continue;
        const share = !fa ? 1 : !fb ? 0 : sb;
        a.vx -= over * (1 - share) * n[0];
        a.vy -= over * (1 - share) * n[1];
        b.vx += over * share * n[0];
        b.vy += over * share * n[1];
      }
    for (const x of L) {
      if (!free(x)) continue;
      if (stuck(x)) {
        x.vx = x.vy = 0;
        continue;
      }
      const sp = Math.hypot(x.vx, x.vy);
      if (sp > cap) {
        x.vx *= cap / sp;
        x.vy *= cap / sp;
      }
      x.x += x.vx;
      x.y += x.vy;
    }
    // a body the user dropped glides to its spot (it is there already,
    // unless it came back from the void, or its parent moved)
    for (const [id] of this.fixed) {
      const x = this.byId.get(id)!, to = this.fixAt(id)!;
      if (!this.pinned(x)) continue;
      x.x += (to[0] - x.x) * T.glide;
      x.y += (to[1] - x.y) * T.glide;
    }
    // the open pages glide to the middle of the graph, side by side,
    // carrying their bubbles: nothing slips between a page and its own
    if (!held) for (const [x, to] of this.row()) {
      const dx = (to[0] - x.x) * T.pageGlide, dy = (to[1] - x.y) * T.pageGlide;
      x.x += dx;
      x.y += dy;
      for (const k of this.own(x)) {
        k.x += dx;
        k.y += dy;
      }
    }
    // collision last: nothing overlaps, softly while the first layout is
    // hot, at once afterwards (again while a shove put one body on another)
    let k = 0;
    while (k < 4 && this.collide(this.alpha ? T.soft : 1) > 0.5) k++;
    this.jammed = k === 4 && this.collide(0) > T.jam;
    // the forces still press what is in a crowd on each other a while
    // after the last change: the collision alone settles it, to rest
    if (this.jammed && !held && this.ticks - this.woke > T.awake) {
      for (let i = 0; i < 64 && this.collide(1) > 0.5; i++);
      this.jammed = this.collide(0) > T.jam;
    }
    // a follower comes round what is in its way, warm, until it is back on its springs
    for (const [id, p] of this.passing) {
      const x = this.byId.get(id);
      if (!x || p.n <= 1 || this.stretch(x) < T.passNear) this.passing.delete(id);
      else {
        p.n--;
        this.warmUp(id, p.e);
      }
    }
    if (!held && this.trail.size && ![...this.trail].some((id) => this.heat.has(id))) this.trail = new Set();
    // at rest: no body went anywhere over a window of ticks
    if (!this.mark || this.mark.at.length !== L.length) this.mark = { at: L.map((x) => [x.x, x.y]), tick: this.ticks };
    else if (this.ticks - this.mark.tick >= T.calm) {
      let mv = 0;
      L.forEach((x, i) => (mv = Math.max(mv, Math.hypot(x.x - this.mark!.at[i][0], x.y - this.mark!.at[i][1]))));
      this.calm = mv < T.still;
      this.mark = { at: L.map((x) => [x.x, x.y]), tick: this.ticks };
    }
  }

  // row is where the open pages go: side by side, in the order they stand
  // in from left to right, at the centre of the graph; around the one the
  // user dropped, if any (it stays where it was dropped).
  private row(): [Body, Pt][] {
    const pages = this.list.filter((x) => x.box).sort((a, b) => a.x - b.x);
    if (!pages.length) return [];
    const gap = 48; // their two margins
    const at: number[] = [];
    let w = 0;
    for (const x of pages) {
      at.push(w + x.box![0] / 2);
      w += x.box![0] + gap;
    }
    const anchor = pages.findIndex((x) => this.fixed.has(x.id));
    const [ox, oy] = anchor >= 0 ? [pages[anchor].x - at[anchor], pages[anchor].y] : [-(w - gap) / 2, 0];
    return pages.filter((x) => !this.fixed.has(x.id)).map((x) => [x, [ox + at[pages.indexOf(x)], oy]]);
  }

  // wake tells the graph something changed: it moves again until it is
  // at rest.
  private wake(): void {
    this.calm = false;
    this.woke = this.ticks;
    this.mark = null;
  }

  // pinned tells whether a body stays where the user dropped it now: a
  // bubble dropped by its subject lives on its springs while the
  // subject's page is open, and goes back to its spot when it closes.
  private pinned(x: Body): boolean {
    const f = this.fixed.get(x.id);
    return !!f && !(f.of && this.byId.get(f.of)?.box);
  }

  // fixAt is where a body the user dropped stays.
  private fixAt(id: string): Pt | undefined {
    const f = this.fixed.get(id);
    if (!f) return undefined;
    const p = f.of ? this.byId.get(f.of) : undefined;
    return p ? [p.x + f.at[0], p.y + f.at[1]] : f.at;
  }

  // loosen lets go of a subject the user dropped that the graph has left
  // far behind (the others came closer to the centre since): the centre
  // pulls it back like any other.
  private loosen(): void {
    for (const [id, f] of this.fixed) {
      if (f.of) continue;
      const rs = this.list.filter((o) => o.id !== id && !o.parent);
      if (!rs.length) continue;
      const c: Pt = [rs.reduce((s, o) => s + o.x, 0) / rs.length, rs.reduce((s, o) => s + o.y, 0) / rs.length];
      let R = 0;
      for (const o of rs) R = Math.max(R, Math.hypot(o.x - c[0], o.y - c[1]) + extent(o, [o.x, o.y])[0]);
      if (Math.hypot(f.at[0] - c[0], f.at[1] - c[1]) > R + RIM + T.loose) {
        this.fixed.delete(id);
        this.warmUp(id, 0.3);
      }
    }
  }

  // rank is who gives way when two bodies collide: the higher. The one
  // held never; then those the user dropped, the last one first; then the
  // open pages; then their bubbles, and those of a page that closed while
  // they come back round their subject: the other bubbles make way for
  // them, so that none is left behind one not its own; then the bubbles at
  // rest; a bubble that moves on its own warmth (a follower of the last
  // drop) gives way to them, a new bubble first of all.
  private rank(x: Body): number {
    if (this.held?.id === x.id) return HELD;
    const f = this.fixed.get(x.id);
    if (f) return 2 - f.seq / (this.fixSeq + 1);
    if (x.box) return 10;
    if (x.parent && (this.byId.get(x.parent)?.box || (this.home.has(x.id) && this.heat.has(x.id)))) return 20;
    if (this.ticks - x.born < T.fresh) return 2000;
    return this.trail.has(x.id) || this.heat.has(x.id) ? 1500 : 1000;
  }

  // collide is the graph's one collision: every body (a bubble with its
  // label under it, an open page with its margin: extent) is a solid box
  // to every other. Of two that overlap, the one of higher rank steps
  // aside alone (a bubble by the shortest way that leads onto nothing
  // else), of the same rank each by half; a bubble gives way to its
  // parent, unless the user dropped it. What is held passes over
  // everything, its followers over what is not theirs, and nothing else
  // moves during a drag.
  private collide(strength: number): number {
    let most = 0;
    const L = this.list;
    const held = this.held;
    this.contacts = [];
    for (let i = 0; i < L.length; i++)
      for (let j = i + 1; j < L.length; j++) {
        const a = L[i], b = L[j];
        const [ha, ta, ba] = extent(a, [a.x, a.y]), [hb, tb, bb] = extent(b, [b.x, b.y]);
        const dx = b.x - a.x;
        const ox = ha + hb - Math.abs(dx), oy = Math.min(ba, bb) - Math.max(ta, tb);
        if (ox <= -12 || oy <= -12) continue;
        const ra = this.rank(a), rb = this.rank(b);
        if (ra === HELD || rb === HELD) continue;
        if (held && !(this.group.has(a.id) && this.group.has(b.id))) continue;
        const fa = this.fixed.has(a.id), fb = this.fixed.has(b.id);
        // a follower of a drop passes over the others until it is back on its springs
        if ((this.passing.has(a.id) && !this.trail.has(b.id)) || (this.passing.has(b.id) && !this.trail.has(a.id))) if (!fa && !fb) continue;
        const sb = b.parent === a.id ? 1 : a.parent === b.id ? 0 : fa !== fb ? (fa ? 1 : 0) : ra === rb ? 0.5 : rb > ra ? 1 : 0;
        const sa = 1 - sb;
        // touching: they press on each other
        // a page is heavier than a bubble, not unmovable: what is caught
        // between it and the others holds it back
        const sv = !a.box === !b.box || fa || fb ? sb : a.box ? 0.9 : 0.1;
        this.contacts.push({ a, b, sb: sv, gap: Math.max(0, -Math.min(ox, oy)), n: ox <= oy ? [Math.abs(dx) > 0.01 ? Math.sign(dx) : 1, 0] : [0, (tb + bb) / 2 >= (ta + ba) / 2 ? 1 : -1] });
        if (ox <= 0.01 || oy <= 0.01) continue;
        most = Math.max(most, Math.min(ox, oy));
        const out = sb === 1 && !b.box && !this.trail.has(b.id) ? this.wayOut(b, a) : sa === 1 && !a.box && !this.trail.has(a.id) ? this.wayOut(a, b) : null;
        // a body just touching steps aside the least way, even onto another
        if (out && Math.min(ox, oy) > 2) {
          this.shove(sb === 1 ? b : a, out[0] * strength, out[1] * strength);
          continue;
        }
        // a page dropped on another moves it by the smallest move; else
        // sideways when it is not much longer (the screen is wide)
        const least = (fa || fb) && (a.box || b.box);
        // a bubble with no way off a page or its parent, caught between it
        // and the others, holds it back: they share the move
        const caught = !out && !fa && !fb && (!a.box !== !b.box || a.parent === b.id || b.parent === a.id);
        const qb = caught ? 0.5 : sb, qa = 1 - qb;
        if (least ? ox <= oy : ox <= oy * 1.5) {
          const s = (Math.abs(dx) > 0.01 ? Math.sign(dx) : hash(a.id + b.id) < 0.5 ? 1 : -1) * (ox + 0.02) * strength;
          this.shove(a, -s * qa, 0);
          this.shove(b, s * qb, 0);
        } else {
          const s = ((tb + bb) / 2 >= (ta + ba) / 2 ? 1 : -1) * (oy + 0.02) * strength;
          this.shove(a, 0, -s * qa);
          this.shove(b, 0, s * qb);
        }
      }
    return most;
  }

  // shove moves a body and what is under it (during a drag, only what
  // follows: the rest stays still). A body the user dropped that is
  // shoved (by one dropped after it) stays where it is shoved.
  private shove(x: Body, dx: number, dy: number): void {
    if (!dx && !dy) return;
    x.x += dx;
    x.y += dy;
    x.vx = x.vy = 0;
    this.fixMove(x, dx, dy);
    for (const k of this.below(x)) {
      if (this.held && (this.held.id === k.id || !this.group.has(k.id))) continue;
      k.x += dx;
      k.y += dy;
    }
  }

  private fixMove(x: Body, dx: number, dy: number): void {
    const f = this.fixed.get(x.id);
    if (f && this.pinned(x)) f.at = [f.at[0] + dx, f.at[1] + dy];
  }

  // wayOut is how a bubble x on the body pg steps off it: the shortest of
  // the four ways (sideways a little preferred, the screen is wide) that
  // leads onto no other body; null when each does.
  private wayOut(x: Body, pg: Body): Pt | null {
    const [hp, tp, bp] = extent(pg, [pg.x, pg.y]), [hx, tx, bx] = extent(x, [x.x, x.y]);
    const ways: Pt[] = [
      [pg.x + hp + hx - x.x + 0.02, 0],
      [pg.x - hp - hx - x.x - 0.02, 0],
      [0, bp - tx + 0.02],
      [0, tp - bx - 0.02],
    ];
    const mine = new Set(this.below(x).map((k) => k.id));
    let best: Pt | null = null, cost = Infinity;
    for (const w of ways) {
      const c = Math.abs(w[0]) / 1.5 + Math.abs(w[1]);
      if (c >= cost) continue;
      const at: Pt = [x.x + w[0], x.y + w[1]];
      if (!this.list.some((o) => o !== pg && o !== x && !mine.has(o.id) && this.held?.id !== o.id && overlap(x, at, o, [o.x, o.y]))) {
        best = w;
        cost = c;
      }
    }
    return best;
  }

  /* ---------- pages ---------- */

  // open draws a node's page where its bubble was, beside the page from
  // (or the last opened) when it would cover it. It then glides to its
  // place in the middle of the graph (row), a solid box the bubbles gather
  // around; it and its bubbles are warmed, to get there soon.
  private open(x: Body, box: Pt, beside?: string): void {
    const open = this.list.filter((o) => o.box && o !== x);
    const from = (beside && open.find((o) => o.id === beside)) || open.sort((a, b) => (b.opened ?? 0) - (a.opened ?? 0))[0];
    if (from) {
      const [nx, ny] = bornBeside([x.x, x.y], box, [from.x, from.y], from.box!);
      if (!this.fixed.get(x.id)?.of) this.fixMove(x, nx - x.x, ny - x.y);
      [x.x, x.y] = [nx, ny];
    }
    x.box = box;
    x.opened = ++this.openSeq;
    x.vx = x.vy = 0;
    this.stir = Math.max(this.stir, T.stir);
    this.warmUp(x.id, T.pageWarm);
    for (const k of this.below(x)) this.warmUp(k.id, 0.5);
    this.wake();
  }

  // close puts a page back to a bubble, its bubbles warmed to come back
  // round it; the centre draws in what the page had pushed away.
  private close(x: Body): void {
    x.box = undefined;
    x.opened = undefined;
    this.stir = Math.max(this.stir, T.stir);
    for (const k of this.below(x)) {
      this.warmUp(k.id, 0.5);
      this.home.add(k.id);
    }
    this.wake();
  }

  /* ---------- seeds ---------- */

  // seedAll places the first graph: the subjects around the centre, each
  // bubble beside its parent; the forces do the rest.
  private seedAll(): void {
    const roots = this.list.filter((x) => !x.parent);
    const r = 120 + 40 * Math.sqrt(roots.length);
    roots.forEach((x, i) => {
      const a = (i / roots.length) * Math.PI * 2 + hash(x.id) * 0.8;
      x.x = Math.cos(a) * r * 1.4;
      x.y = Math.sin(a) * r;
    });
    for (const x of [...this.list].sort((a, b) => this.depth(a) - this.depth(b))) {
      if (!x.parent) continue;
      const p = this.byId.get(x.parent)!, a = hash(x.id) * Math.PI * 2;
      x.x = p.x + Math.cos(a) * 90;
      x.y = p.y + Math.sin(a) * 90;
    }
  }

  // seed places a node that comes later: a bubble beside its parent
  // (past its page's edge when it is open), a subject linked to another
  // beside it, on the side away from the others, any other on the edge
  // of the graph. It is warm, and a little around it: the bubbles beside
  // a new bubble.
  private seed(x: Body): void {
    const p = x.parent ? this.byId.get(x.parent) : undefined;
    const a = hash(x.id) * Math.PI * 2;
    if (p) {
      const d = p.box ? edgeRest(p.box, Math.cos(a), Math.sin(a), x) : 80;
      x.x = p.x + Math.cos(a) * d;
      x.y = p.y + Math.sin(a) * d;
    } else {
      const o = this.list.find((y) => y !== x && !y.parent && (x.links.includes(y.id) || y.links.includes(x.id)));
      if (o) {
        let ax = 0, ay = 0;
        for (const y of this.list)
          if (y !== o && y !== x && !y.parent) {
            const d = Math.hypot(y.x - o.x, y.y - o.y) || 1;
            if (d < 500) {
              ax -= (y.x - o.x) / d;
              ay -= (y.y - o.y) / d;
            }
          }
        const d = Math.hypot(ax, ay);
        const [ux, uy] = d > 0.01 ? [ax / d, ay / d] : [Math.cos(a), Math.sin(a)];
        x.x = o.x + ux * 240;
        x.y = o.y + uy * 160;
      } else {
        let R = 0;
        for (const y of this.list) if (y !== x) R = Math.max(R, Math.hypot(y.x, y.y));
        x.x = Math.cos(a) * (R + 120);
        x.y = Math.sin(a) * (R + 120) * 0.6;
      }
    }
    x.born = this.ticks;
    this.warmUp(x.id, 1);
    // its siblings near it make a little room around their parent, which stays
    if (p) for (const o of this.list) if (o !== x && o.parent === p.id && Math.hypot(o.x - x.x, o.y - x.y) < 200) this.warmUp(o.id, 0.1);
  }

  /* ---------- the reach ---------- */

  // leash keeps a dragged body within reach: a subject within REACH past
  // the disc's edge (the disc: the other subjects' centroid and the
  // farthest edge from it), a bubble within TETHER of its parent (of its
  // page's edge). Past it, the body lags behind the pointer (RUBBER);
  // dropped there, it comes back to the edge (RIM past the disc's).
  private leash(x: Body, raw: Pt, dropped: boolean): Pt {
    let from: Pt, lim: number, back: number;
    const p = x.parent ? this.byId.get(x.parent) : undefined;
    if (p) {
      const [hw, hh] = p.box ? [p.box[0] / 2, p.box[1] / 2] : [0, 0];
      from = [Math.max(p.x - hw, Math.min(p.x + hw, raw[0])), Math.max(p.y - hh, Math.min(p.y + hh, raw[1]))];
      lim = back = TETHER;
    } else {
      const rs = this.list.filter((o) => o !== x && !o.parent);
      if (!rs.length) return raw;
      const c: Pt = [rs.reduce((s, o) => s + o.x, 0) / rs.length, rs.reduce((s, o) => s + o.y, 0) / rs.length];
      let R = 0;
      for (const o of rs) R = Math.max(R, Math.hypot(o.x - c[0], o.y - c[1]) + extent(o, [o.x, o.y])[0]);
      from = c;
      lim = R + REACH;
      back = R + RIM;
    }
    const d = Math.hypot(raw[0] - from[0], raw[1] - from[1]);
    if (d <= lim) return raw;
    const f = (dropped ? back : lim + (d - lim) * RUBBER) / d;
    return [from[0] + (raw[0] - from[0]) * f, from[1] + (raw[1] - from[1]) * f];
  }

  /* ---------- the graph's links ---------- */

  // neighbourhood is the bodies one and two links from id (a parent, a
  // child, a text link, either way) with their energy.
  private neighbourhood(id: string): Map<string, number> {
    const out = new Map<string, number>();
    let ring = [this.byId.get(id)!];
    const seen = new Set([id]);
    for (const h of [HOP1, HOP2]) {
      const next: Body[] = [];
      for (const a of ring)
        for (const b of this.list) {
          if (seen.has(b.id)) continue;
          if (b.parent === a.id || a.parent === b.id || a.links.includes(b.id) || b.links.includes(a.id)) {
            seen.add(b.id);
            out.set(b.id, h);
            next.push(b);
          }
        }
      ring = next;
    }
    return out;
  }

  // stretch is how much more than at rest the most stretched of a body's
  // links is long.
  private stretch(x: Body): number {
    let most = 0;
    for (const o of this.list) {
      if (o === x || o.box || x.box) continue;
      const kin = o.id === x.parent || o.parent === x.id;
      if (!kin && !x.links.includes(o.id) && !o.links.includes(x.id)) continue;
      const rest = kin ? x.r + o.r + T.childGap : extent(x, [0, 0])[0] + extent(o, [0, 0])[0] + T.linkGap;
      most = Math.max(most, Math.hypot(o.x - x.x, o.y - x.y) - rest);
    }
    return most;
  }

  // ends are the bodies a body's links lead to: its parent, its text links.
  private ends(a: Body): Body[] {
    const out: Body[] = [];
    if (a.parent && this.byId.has(a.parent)) out.push(this.byId.get(a.parent)!);
    for (const l of a.links) {
      const o = this.byId.get(l);
      if (o && o !== a) out.push(o);
    }
    return out;
  }

  // below is every body under x: its bubbles, theirs, and so on.
  private below(x: Body): Body[] {
    const out: Body[] = [];
    const seen = new Set([x.id]);
    for (let ring = [x]; ring.length; ) {
      const next: Body[] = [];
      for (const b of this.list) if (b.parent && !seen.has(b.id) && ring.some((r) => r.id === b.parent)) next.push(b);
      for (const b of next) seen.add(b.id);
      out.push(...next);
      ring = next;
    }
    return out;
  }

  // own is the bubbles a page carries: those under it, down to the pages
  // (an open page goes its own way, with its bubbles) and the bodies the
  // user dropped.
  private own(x: Body): Body[] {
    const out: Body[] = [];
    for (let ring = [x]; ring.length; ) {
      const next = this.list.filter((b) => b.parent && !b.box && !this.pinned(b) && ring.some((r) => r.id === b.parent));
      out.push(...next);
      ring = next;
    }
    return out;
  }

  private depth(x: Body): number {
    let d = 0;
    for (let p = x.parent; p && d < 64; p = this.byId.get(p)?.parent) d++;
    return d;
  }

  private warmUp(id: string, h: number): void {
    this.heat.set(id, Math.max(this.heat.get(id) ?? 0, h));
    this.wake();
  }

  private forget(id: string): void {
    this.byId.delete(id);
    this.heat.delete(id);
    this.trail.delete(id);
    this.home.delete(id);
    this.passing.delete(id);
    this.group.delete(id);
    if (this.held?.id === id) {
      this.held = null;
      this.group = new Map();
    }
    this.fixed.delete(id);
    for (const [k, f] of this.fixed) if (f.of === id) this.fixed.delete(k);
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
