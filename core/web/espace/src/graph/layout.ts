// The graph's layout: a living force graph, like Obsidian's. Every bubble
// pushes every other away (many-body, no cutoff), all are pulled to the
// centre, a bubble and its parent, and two subjects a text links, are held
// close by short strong springs; a bubble never rests on a link it is not
// part of. Every body, a bubble with its label or an open page, is solid
// to every other by one collision (collide). The graph is a compact disc
// around the centre, the subjects linked to nothing on its rim.
//
// Nothing is remembered: no place is kept, nothing orders the subjects.
// What the user arranges holds for the session because the energy is
// local. The first layout runs on a global energy (alpha) and is settled
// before it is shown; afterwards only what changes is warm: a new node
// (and a little around it), the bubbles of a page that opens or closes,
// what a drag carries. Everything else is cold and moves only to make
// room (collision), never by the forces. The whole graph is never warmed
// again.
//
// A drag (hold, drop): the held body passes over everything; the bodies
// linked to it follow on their springs, with an energy that falls with
// the links between them (HOP1, HOP2, beyond still), passing over what is
// not theirs; nothing else moves. Dropped, it stays exactly there and wins
// every collision: what is under it steps aside by the smallest move;
// its followers settle on their springs, giving way to the bodies in
// place. Nothing here knows a node by its name.

export type Pt = [number, number];
export type Pos = Record<string, Pt>;
// r: the bubble's radius; lw, lh: the size of the label under it, so
// labels never sit on each other; box: an open page, drawn in the graph.
export type LNode = { id: string; parent?: string; links: string[]; r: number; lw: number; lh: number; box?: Pt };

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
  passWarm: 0.3, // ... warm at least this meanwhile
  passNear: 60, // ... back: none of its links stretched more than this
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
  const hx = w / 2 + 24 + Math.max(x.r, x.lw / 2) + 10, hy = h / 2 + 24 + x.r + x.lh + 10;
  return Math.min(Math.abs(ux) > 1e-6 ? hx / Math.abs(ux) : Infinity, Math.abs(uy) > 1e-6 ? hy / Math.abs(uy) : Infinity) + 30;
}

// A body of the simulation: a node, where it is and how fast it goes.
// was: where it stood before an open page pushed it (a subject's spot, a
// bubble's offset from its parent), to go back there when the page
// closes; rel: a bubble's offset from its subject when the subject's page
// opened, given back when it closes.
type Body = LNode & { x: number; y: number; vx: number; vy: number; born: number; opened?: number; was?: Pt; rel?: Pt };

// HELD is the rank of the body held: it never gives way.
const HELD = 0;

// Sim is the graph's simulation: Graph.tsx keeps one for the session,
// tells it the nodes (sync), the drags (hold, drop), and ticks it a frame
// at a time while it is awake.
export class Sim {
  private list: Body[] = [];
  private byId = new Map<string, Body>();
  private alpha = 0;
  private ticks = 0;
  private lastMove = Infinity;
  private heat = new Map<string, number>(); // a body's own energy, cooling
  private held: { id: string; at: Pt } | null = null;
  private group = new Map<string, number>(); // the held body's followers, their energy
  private pin: { id: string; at: Pt; clear: number; ticks: number } | null = null; // the body just dropped
  private trail = new Set<string>(); // the followers of the last drop, giving way while they settle
  private anchor: Pt = [0, 0]; // where the last drop was: its followers gather around it
  private passing = new Map<string, number>(); // the followers still coming round, the ticks they have left
  private openSeq = 0;

  // sync tells the simulation the graph as it is now: the nodes in a
  // stable order, the open ones with their page's size. beside is the page
  // a page opened now goes beside (the one the console talks to), else the
  // last one opened. The first graph is laid out on a global energy and
  // settled at once; later, only what changed is warmed.
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
        b = { id: n.id, parent: n.parent, links: n.links, r: n.r, lw: n.lw, lh: n.lh, x: 0, y: 0, vx: 0, vy: 0, born: this.ticks };
        this.byId.set(n.id, b);
        fresh.push(b);
      } else if (b.r !== n.r || b.lw !== n.lw || b.lh !== n.lh || b.parent !== n.parent || b.links.join() !== n.links.join()) {
        Object.assign(b, { parent: n.parent, links: n.links, r: n.r, lw: n.lw, lh: n.lh });
        this.lastMove = Infinity; // only collisions sort it out: nothing is warmed
      }
      next.push(b);
    }
    this.list = next;
    for (const b of this.list) if (b.parent && !this.byId.has(b.parent)) b.parent = undefined;
    if (first) this.seedAll();
    else for (const b of fresh.sort((a, c) => this.depth(a) - this.depth(c))) this.seed(b);
    if (first) {
      this.alpha = 1;
      this.lastMove = Infinity;
      this.settle();
    }
    // the pages: those closed first (a click that opens one alone closes
    // the others), those that changed size, then those opened, in order
    for (const b of this.list) if (b.box && !want.get(b.id)!.box) this.close(b);
    for (const b of this.list) {
      const box = want.get(b.id)!.box;
      if (b.box && box && (b.box[0] !== box[0] || b.box[1] !== box[1])) {
        b.box = box;
        for (const k of this.below(b)) this.warmUp(k.id, 0.3);
        this.lastMove = Infinity;
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
      if (this.pin?.id === id) this.pin = null;
      this.trail = new Set();
      this.passing = new Map();
    }
    const p = this.leash(x, at, false);
    this.held = { id, at: p };
    [x.x, x.y] = p;
    x.vx = x.vy = 0;
    this.lastMove = Infinity;
  }

  // drop lets go of the body held: it stays where it is (back at the
  // disc's edge if it was out in the void), wins every collision until
  // nothing sits on it, and forgets where a page had pushed it from. Its
  // followers settle on their springs, giving way to the bodies in place.
  drop(): void {
    const h = this.held;
    if (!h) return;
    this.held = null;
    const x = this.byId.get(h.id);
    if (!x) return;
    this.pin = { id: x.id, at: this.leash(x, [x.x, x.y], true), clear: 0, ticks: 0 };
    x.was = x.rel = undefined;
    for (const [id, e] of this.group) this.warmUp(id, e);
    this.trail = new Set(this.group.keys());
    this.anchor = this.pin.at;
    // the bubbles of the followers that stayed still come back to them
    for (const id of [...this.trail])
      for (const k of this.below(this.byId.get(id)!))
        if (k !== x && !this.trail.has(k.id)) {
          this.warmUp(k.id, HOP2);
          this.trail.add(k.id);
        }
    this.passing = new Map([...this.trail].map((id) => [id, T.pass]));
    this.group = new Map();
    this.lastMove = Infinity;
  }

  // heldId is the body held, if any.
  heldId(): string | null {
    return this.held?.id ?? null;
  }

  // reach is where a body dragged to raw is kept (leash).
  reach(id: string, raw: Pt): Pt {
    const x = this.byId.get(id);
    return x ? this.leash(x, raw, false) : raw;
  }

  // asleep tells whether nothing moves any more: no need to tick.
  asleep(): boolean {
    return this.alpha === 0 && this.lastMove < 0.05 && !this.held && !this.pin && this.heat.size === 0 && this.passing.size === 0;
  }

  // settle ticks until the graph is at rest (max ticks at most): the
  // first layout, and every change when the user asks for less motion.
  settle(max = 2000): void {
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

  tick(): void {
    this.ticks++;
    const held = this.held ? this.byId.get(this.held.id) : undefined;
    if (held) [held.x, held.y] = this.held!.at;
    this.alpha += (0 - this.alpha) * T.decay;
    if (this.alpha < T.alphaMin) this.alpha = 0;
    const before = this.list.map((x) => [x.x, x.y]);
    const group = this.group;
    const free = (x: Body) => !x.box && held !== x && this.pin?.id !== x.id && (!held || group.has(x.id));
    // each body's energy: the global one (the first layout) or its own; during a drag, its hop's
    const E = new Map(this.list.map((x) => [x.id, held ? (group.get(x.id) ?? 0) : Math.max(this.alpha, this.heat.get(x.id) ?? 0)]));
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
        spring(p, x, rest, T.childK, 0.15);
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
            a.vx -= (nx / n) * f * 0.25 * e(a);
            a.vy -= (ny / n) * f * 0.25 * e(a);
          }
          if (free(b)) {
            b.vx -= (nx / n) * f * 0.25 * e(b);
            b.vy -= (ny / n) * f * 0.25 * e(b);
          }
        }
      }
    }
    // a body's own energy cools
    for (const [id, h] of this.heat) {
      if (h < 0.005 || !this.byId.has(id)) this.heat.delete(id);
      else this.heat.set(id, h * T.cool);
    }
    // the pull to the centre: the graph stays a compact disc (none during a
    // drag; what followed one gathers around where it was dropped)
    if (!held)
      for (const x of L) {
        if (!free(x)) continue;
        const [cx, cy] = this.trail.has(x.id) ? this.anchor : [0, 0];
        x.vx -= (x.x - cx) * T.gravity * e(x);
        x.vy -= (x.y - cy) * T.gravity * e(x);
      }
    // move
    const cap = 40;
    for (const x of L) {
      if (!free(x)) {
        x.vx = x.vy = 0;
        continue;
      }
      x.vx *= T.friction;
      x.vy *= T.friction;
      const sp = Math.hypot(x.vx, x.vy);
      if (sp > cap) {
        x.vx *= cap / sp;
        x.vy *= cap / sp;
      }
      x.x += x.vx;
      x.y += x.vy;
    }
    // the body just dropped glides to its spot (it is there already,
    // unless it came back from the void)
    const pin = this.pin ? this.byId.get(this.pin.id) : undefined;
    if (pin) {
      pin.x += (this.pin!.at[0] - pin.x) * T.glide;
      pin.y += (this.pin!.at[1] - pin.y) * T.glide;
    }
    // collision last: nothing overlaps, softly
    this.collide(T.soft);
    if (pin && this.pin) {
      const near = Math.hypot(this.pin.at[0] - pin.x, this.pin.at[1] - pin.y) < 1;
      const under = new Set(this.below(pin).map((k) => k.id));
      const hit = L.some((o) => o !== pin && !under.has(o.id) && overlap(pin, [pin.x, pin.y], o, [o.x, o.y]));
      this.pin.clear = near && !hit ? this.pin.clear + 1 : 0;
      if (this.pin.clear > 20 || ++this.pin.ticks > 300) this.pin = null;
    }
    // a follower comes round what is in its way, warm, until it is back on its springs
    for (const [id, n] of this.passing) {
      const x = this.byId.get(id);
      if (!x || n <= 1 || this.stretch(x) < T.passNear) this.passing.delete(id);
      else {
        this.passing.set(id, n - 1);
        this.warmUp(id, T.passWarm);
      }
    }
    if (!this.pin && !held && this.trail.size && ![...this.trail].some((id) => this.heat.has(id))) this.trail = new Set();
    let mv = 0;
    L.forEach((x, i) => (mv = Math.max(mv, Math.hypot(x.x - before[i][0], x.y - before[i][1]))));
    this.lastMove = mv;
  }

  // rank is who gives way when two bodies collide: the higher. The one
  // held never; the one just dropped wins; then the open pages; then the
  // bubbles at rest; a bubble that moves on its own warmth (a follower of
  // the last drop, the bubbles of a page that opened) gives way to them, a
  // new bubble first of all.
  private rank(x: Body): number {
    if (this.held?.id === x.id) return HELD;
    if (this.pin?.id === x.id) return 1;
    if (x.box) return 10;
    if (this.ticks - x.born < T.fresh) return 2000;
    return this.trail.has(x.id) || this.heat.has(x.id) ? 1500 : 1000;
  }

  // collide is the graph's one collision: every body (a bubble with its
  // label under it, an open page with its margin: extent) is a solid box
  // to every other. Of two that overlap, the one of higher rank steps
  // aside alone (a bubble by the shortest way that leads onto nothing
  // else), of the same rank each by half; a bubble gives way to its
  // parent. What is held passes over everything, its followers over what
  // is not theirs, and nothing else moves during a drag. A bubble an open
  // page pushes remembers where it stood.
  private collide(strength: number): void {
    const L = this.list;
    const held = this.held;
    for (let i = 0; i < L.length; i++)
      for (let j = i + 1; j < L.length; j++) {
        const a = L[i], b = L[j];
        const [ha, ta, ba] = extent(a, [a.x, a.y]), [hb, tb, bb] = extent(b, [b.x, b.y]);
        const dx = b.x - a.x;
        const ox = ha + hb - Math.abs(dx), oy = Math.min(ba, bb) - Math.max(ta, tb);
        if (ox <= 0.5 || oy <= 0.5) continue;
        const ra = this.rank(a), rb = this.rank(b);
        if (ra === HELD || rb === HELD) continue;
        if (held && !(this.group.has(a.id) && this.group.has(b.id))) continue;
        // a follower of a drop passes over the others until it is back on its springs
        if ((this.passing.has(a.id) && !this.trail.has(b.id)) || (this.passing.has(b.id) && !this.trail.has(a.id))) if (this.pin?.id !== a.id && this.pin?.id !== b.id) continue;
        const sb = b.parent === a.id ? 1 : a.parent === b.id ? 0 : ra === rb ? 0.5 : rb > ra ? 1 : 0;
        const sa = 1 - sb;
        const out = sb === 1 && !b.box && !this.trail.has(b.id) ? this.wayOut(b, a) : sa === 1 && !a.box && !this.trail.has(a.id) ? this.wayOut(a, b) : null;
        if (out) {
          const [x, by] = sb === 1 ? [b, a] : [a, b];
          this.remember(x, by);
          this.shove(x, out[0] * strength, out[1] * strength);
          continue;
        }
        if (sb > 0) this.remember(b, a);
        if (sa > 0) this.remember(a, b);
        // a page dropped on another moves it by the smallest move; else
        // sideways when it is not much longer (the screen is wide)
        const least = this.pin && (this.pin.id === a.id || this.pin.id === b.id) && (a.box || b.box);
        if (least ? ox <= oy : ox <= oy * 1.5) {
          const s = (Math.abs(dx) > 0.01 ? Math.sign(dx) : hash(a.id + b.id) < 0.5 ? 1 : -1) * (ox + 0.5) * strength;
          this.shove(a, -s * sa, 0);
          this.shove(b, s * sb, 0);
        } else {
          const s = ((tb + bb) / 2 >= (ta + ba) / 2 ? 1 : -1) * (oy + 0.5) * strength;
          this.shove(a, 0, -s * sa);
          this.shove(b, 0, s * sb);
        }
      }
  }

  // remember keeps where a bubble stood when it is pushed while a page is
  // open (by the page, or by what the page pushed), unless it is the
  // page's own: a subject's spot, a bubble's offset from its parent.
  private remember(x: Body, by: Body): void {
    if (x.box || x.was || x.rel || !(by.box || by.was || by.rel) || !this.list.some((o) => o.box)) return;
    const p = x.parent ? this.byId.get(x.parent) : undefined;
    x.was = p ? [x.x - p.x, x.y - p.y] : [x.x, x.y];
  }

  // shove moves a body and what is under it (during a drag, only what
  // follows: the rest stays still).
  private shove(x: Body, dx: number, dy: number): void {
    if (!dx && !dy) return;
    x.x += dx;
    x.y += dy;
    x.vx = x.vy = 0;
    for (const k of this.below(x)) {
      if (this.held && (this.held.id === k.id || !this.group.has(k.id))) continue;
      k.x += dx;
      k.y += dy;
    }
  }

  // wayOut is how a bubble x on the body pg steps off it: the shortest of
  // the four ways (sideways a little preferred, the screen is wide) that
  // leads onto no other body; null when each does.
  private wayOut(x: Body, pg: Body): Pt | null {
    const [hp, tp, bp] = extent(pg, [pg.x, pg.y]), [hx, tx, bx] = extent(x, [x.x, x.y]);
    const ways: Pt[] = [
      [pg.x + hp + hx - x.x + 0.5, 0],
      [pg.x - hp - hx - x.x - 0.5, 0],
      [0, bp - tx + 0.5],
      [0, tp - bx - 0.5],
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
  // (or the last opened) when it would cover it. Its bubbles keep their
  // offsets, to come back the same way, and only they are warmed.
  private open(x: Body, box: Pt, beside?: string): void {
    const open = this.list.filter((o) => o.box && o !== x);
    const from = (beside && open.find((o) => o.id === beside)) || open.sort((a, b) => (b.opened ?? 0) - (a.opened ?? 0))[0];
    const p = x.parent ? this.byId.get(x.parent) : undefined;
    x.was ??= p ? [x.x - p.x, x.y - p.y] : [x.x, x.y];
    for (const k of this.below(x)) k.rel = [k.x - x.x, k.y - x.y];
    if (from) [x.x, x.y] = bornBeside([x.x, x.y], box, [from.x, from.y], from.box!);
    x.box = box;
    x.opened = ++this.openSeq;
    x.vx = x.vy = 0;
    for (const k of this.below(x)) this.warmUp(k.id, 0.5);
    this.lastMove = Infinity;
  }

  // close puts a page back to a bubble: the bubbles pages had pushed go
  // back where they stood (where no open page covers that spot any more),
  // its own bubbles around it as they were; collision sorts out the rest.
  private close(x: Body): void {
    x.box = undefined;
    x.opened = undefined;
    const byDepth = [...this.list].sort((a, b) => this.depth(a) - this.depth(b));
    for (const y of byDepth) {
      if (!y.was || y.box) continue;
      const p = y.parent ? this.byId.get(y.parent) : undefined;
      const to: Pt = p ? [p.x + y.was[0], p.y + y.was[1]] : y.was;
      if (this.list.some((o) => o.box && overlap(y, to, o, [o.x, o.y]))) continue;
      this.shove(y, to[0] - y.x, to[1] - y.y);
      y.was = undefined;
    }
    for (const k of this.below(x))
      if (k.rel) {
        this.shove(k, x.x + k.rel[0] - k.x, x.y + k.rel[1] - k.y);
        k.rel = undefined;
      }
    for (const k of this.below(x)) this.warmUp(k.id, 0.5);
    if (!this.list.some((o) => o.box)) for (const y of this.list) y.was = undefined;
    this.lastMove = Infinity;
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

  private depth(x: Body): number {
    let d = 0;
    for (let p = x.parent; p && d < 64; p = this.byId.get(p)?.parent) d++;
    return d;
  }

  private warmUp(id: string, h: number): void {
    this.heat.set(id, Math.max(this.heat.get(id) ?? 0, h));
    this.lastMove = Infinity;
  }

  private forget(id: string): void {
    this.byId.delete(id);
    this.heat.delete(id);
    this.trail.delete(id);
    this.passing.delete(id);
    this.group.delete(id);
    if (this.held?.id === id) {
      this.held = null;
      this.group = new Map();
    }
    if (this.pin?.id === id) this.pin = null;
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
