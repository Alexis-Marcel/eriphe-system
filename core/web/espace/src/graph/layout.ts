// The graph's layout: a small force simulation. Roots are held to their
// home (where they first found room, or where the user dropped them),
// children pulled to their parent, links as weak springs, everything
// pushing everything else away. Seeded from the previous positions, so a
// new node grows out of its parent and the rest barely moves. A node the
// user dropped somewhere is pinned there like a magnet: a root keeps that
// spot, a child keeps that offset from its parent. An open page is a box
// node, its size the page's: the others make room around it, and come back
// when it closes. Every body, a bubble with its label or an open page, is
// solid to every other by one collision (collide): one that comes pushes
// the others softly out of its way, and in the end two never sit on each
// other. Nothing here knows a node by its name.

export type Pt = [number, number];
export type Pos = Record<string, Pt>;
export type Pin = { abs?: Pt; rel?: Pt };
// r: the bubble's radius; lw, lh: the size of the label under it, so
// labels never sit on each other; box: an open page, drawn in the graph;
// opened: its rank among the open pages, the later one giving way.
export type LNode = { id: string; parent?: string; links: string[]; r: number; lw: number; lh: number; box?: Pt; opened?: number };

// The user's reach when dragging (#633): a subject stays within STEP of
// the nearest other subject, a bubble within TETHER of its parent; past it
// the bubble lags behind the pointer by RUBBER, and dropped there it comes
// back to the edge.
export const STEP = 380;
export const TETHER = 300;
export const RUBBER = 0.3;

// ROOM is the space a new root needs from the others' homes.
export const ROOM = 300;

export function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0) / 4294967296;
}

// tether keeps a point within an ellipse of radii reach around from: past
// it, it lags behind (RUBBER); dropped, it comes back to the edge.
export function tether(at: Pt, from: Pt, [lx, ly]: Pt, dropped: boolean): Pt {
  const dx = at[0] - from[0], dy = at[1] - from[1];
  const e = Math.hypot(dx / lx, dy / ly);
  if (e <= 1) return at;
  const f = dropped ? 1 / e : (1 + (e - 1) * RUBBER) / e;
  return [from[0] + dx * f, from[1] + dy * f];
}

// An other subject a dragged one keeps near: its place, and its page's
// size when it is open.
export type Near = { at: Pt; box?: Pt };

// leash keeps a dragged subject within STEP of the nearest other one,
// measured from the edge of its page when it is open (own: the dragged
// one's page): never alone in the void, but always free to go one step
// past the last subject.
export function leash(raw: Pt, others: Near[], own: Pt | undefined, dropped: boolean): Pt {
  const [ow, oh] = own ?? [0, 0];
  let best: { d: number; q: Pt } | null = null;
  for (const o of others) {
    const [bw, bh] = o.box ?? [0, 0];
    const hw = (bw + ow) / 2, hh = (bh + oh) / 2;
    const q: Pt = [Math.max(o.at[0] - hw, Math.min(o.at[0] + hw, raw[0])), Math.max(o.at[1] - hh, Math.min(o.at[1] + hh, raw[1]))];
    const d = Math.hypot(raw[0] - q[0], raw[1] - q[1]);
    if (!best || d < best.d) best = { d, q };
  }
  if (!best || best.d <= STEP) return raw;
  const f = (dropped ? STEP : STEP + (best.d - STEP) * RUBBER) / best.d;
  return [best.q[0] + (raw[0] - best.q[0]) * f, best.q[1] + (raw[1] - best.q[1]) * f];
}

// homesFor gives each root without a home a free spot, in the order given
// (the order the roots were made): around the others, on a widening
// spiral, ROOM away from every home and every spot in taken (where the
// user dropped a subject: it is there, not at its home). The homes known
// stay where they are: a new subject never moves the others.
export function homesFor(roots: string[], known: Pos, taken: Pt[] = []): Pos {
  const homes: Pos = { ...known };
  const others = () => [...Object.values(homes), ...taken];
  for (const id of roots) {
    if (homes[id]) continue;
    const all = others();
    if (all.length === 0) {
      homes[id] = [0, 0];
      continue;
    }
    const cx = all.reduce((a, p) => a + p[0], 0) / all.length, cy = all.reduce((a, p) => a + p[1], 0) / all.length;
    const turn = hash(id) * Math.PI * 2;
    let spot: Pt | null = null;
    for (let i = 1; i < 4000 && !spot; i++) {
      const a = turn + i * 2.39996; // the golden angle: spread all around
      const d = 120 * Math.sqrt(i);
      const p: Pt = [cx + Math.cos(a) * d * 1.45, cy + Math.sin(a) * d * 0.85];
      if (all.every((q) => Math.hypot(p[0] - q[0], p[1] - q[1]) >= ROOM)) spot = p;
    }
    homes[id] = spot ?? [cx + all.length * ROOM, cy];
  }
  return homes;
}

// simulate places the nodes. prev: where they were; homes: the roots'
// homes (homesFor); pins: where the user dropped them; held: the node
// being dragged, kept under the pointer.
export function simulate(nodes: LNode[], prev: Pos, homes: Pos, pins: Record<string, Pin> = {}, held?: { id: string; at: Pt }, iterations?: number): Pos {
  const byId = new Map(nodes.map((x) => [x.id, x]));
  const anc: Pos = {};
  for (const x of nodes) {
    if (x.parent && byId.has(x.parent)) continue;
    const a = pins[x.id]?.abs ?? homes[x.id];
    if (a) anc[x.id] = a;
  }
  const opened = nodes.some((x) => x.box);
  const p: Pos = {};
  const v: Pos = {};
  // seed: the previous spot, else the home, else beside the parent
  const place = (x: LNode, depth = 0): Pt => {
    if (p[x.id]) return p[x.id];
    if (prev[x.id]) return (p[x.id] = [...prev[x.id]]);
    if (anc[x.id]) return (p[x.id] = [...anc[x.id]]);
    const par = x.parent && depth < 64 ? byId.get(x.parent) : undefined;
    const [px, py] = par ? place(par, depth + 1) : [0, 0];
    const rel = pins[x.id]?.rel;
    if (rel) return (p[x.id] = [px + rel[0], py + rel[1]]);
    const a = hash(x.id) * Math.PI * 2;
    return (p[x.id] = [px + Math.cos(a) * 90, py + Math.sin(a) * 90]);
  };
  nodes.forEach((x) => place(x));
  nodes.forEach((x) => (v[x.id] = [0, 0]));
  const fresh = new Set(nodes.filter((x) => !prev[x.id]).map((x) => x.id));
  const settled = Object.keys(prev).length > 0;
  const iters = iterations ?? (settled ? 160 : 320);
  if (held && p[held.id]) p[held.id] = [...held.at];
  const pinned = (x: LNode) => !opened && !!(pins[x.id]?.abs || (pins[x.id]?.rel && x.parent && byId.has(x.parent)));
  // who gives way when two bodies collide (rank): the one held never, then
  // one the user dropped (unless a page needs the room), then the open
  // pages, the first opened last, then the bubbles; of a rank, a new
  // bubble before one already in place (share)
  const rank = (x: LNode) => (held?.id === x.id ? 0 : pinned(x) ? 1 : x.box ? 2 + (x.opened ?? 0) : BUBBLE);
  const share = (x: LNode) => (fresh.has(x.id) || !settled ? 2 : 1);
  const bodies = (strength: number) => collide(nodes, p, v, rank, share, strength);

  for (let it = 0; it < iters; it++) {
    const f: Pos = {};
    nodes.forEach((x) => (f[x.id] = [0, 0]));
    // repulsion: everything pushes everything else away, gently
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        if (a.box || b.box) continue; // a page makes room by its box, below
        let dx = p[b.id][0] - p[a.id][0], dy = p[b.id][1] - p[a.id][1];
        let d = Math.hypot(dx, dy);
        if (d < 0.01) {
          dx = hash(a.id + b.id) - 0.5;
          dy = 0.5 - hash(b.id + a.id);
          d = 0.5;
        }
        // siblings, and a parent with its children, push harder: children spread all around
        const kin = (a.parent && a.parent === b.parent) || a.parent === b.id || b.parent === a.id;
        const s = (kin ? 9000 : 1800) / (d * d);
        const ux = dx / d, uy = dy / d;
        f[a.id][0] -= ux * s;
        f[a.id][1] -= uy * s;
        f[b.id][0] += ux * s;
        f[b.id][1] += uy * s;
      }
    }
    // parent springs
    for (const x of nodes) {
      const par = x.parent && byId.get(x.parent);
      if (!par) continue;
      const dx = p[x.id][0] - p[par.id][0], dy = p[x.id][1] - p[par.id][1];
      const d = Math.max(0.01, Math.hypot(dx, dy));
      // around an open page, its bubbles sit just past its edge
      const rest = par.box && !x.box ? edgeRest(par.box, dx / d, dy / d, x) : (par.box ? par.box[1] / 2 : par.r + par.lh / 2) + (x.box ? x.box[1] / 2 : x.r) + 70;
      const s = (d - rest) * 0.09;
      if (!x.box && !pins[x.id]?.rel) {
        f[x.id][0] -= (dx / d) * s;
        f[x.id][1] -= (dy / d) * s;
      }
      if (!par.box) {
        f[par.id][0] += (dx / d) * s * 0.25;
        f[par.id][1] += (dy / d) * s * 0.25;
      }
    }
    // links: weak springs
    for (const x of nodes) {
      for (const l of x.links) {
        const o = byId.get(l);
        if (!o || x.box || o.box) continue;
        const dx = p[l][0] - p[x.id][0], dy = p[l][1] - p[x.id][1];
        const d = Math.max(0.01, Math.hypot(dx, dy));
        const s = (d - 220) * 0.006;
        f[x.id][0] += (dx / d) * s;
        f[x.id][1] += (dy / d) * s;
        f[l][0] -= (dx / d) * s;
        f[l][1] -= (dy / d) * s;
      }
    }
    // a child dropped somewhere keeps that place beside its parent
    for (const x of nodes) {
      const rel = pins[x.id]?.rel, par = x.parent && byId.get(x.parent);
      if (!rel || !par || x.box) continue;
      f[x.id][0] += (p[par.id][0] + rel[0] - p[x.id][0]) * 0.12;
      f[x.id][1] += (p[par.id][1] + rel[1] - p[x.id][1]) * 0.12;
    }
    // roots held to their homes; a faint pull to the centre for the others
    for (const x of nodes) {
      const a = anc[x.id];
      if (a) {
        // while a page is open the roots give way, then come back to their
        // home; an open page stays where it was laid, not pulled back over
        // the page it stepped aside from
        const ka = x.box ? 0 : opened ? 0.012 : 0.08;
        f[x.id][0] += (a[0] - p[x.id][0]) * ka;
        f[x.id][1] += (a[1] - p[x.id][1]) * ka;
      } else if (!x.parent || !byId.has(x.parent)) {
        f[x.id][0] -= p[x.id][0] * 0.002;
        f[x.id][1] -= p[x.id][1] * 0.002;
      }
    }
    const cool = 1 - it / iters;
    for (const x of nodes) {
      if (held && x.id === held.id) continue;
      // a node the user dropped stays there, unless a page needs the room
      if (pinned(x)) continue;
      // known nodes move little, new ones find their place
      const mob = x.box ? 0.04 : fresh.has(x.id) || !settled ? 1 : 0.35;
      v[x.id][0] = (v[x.id][0] + f[x.id][0] * mob) * 0.55;
      v[x.id][1] = (v[x.id][1] + f[x.id][1] * mob) * 0.55;
      const sp = Math.hypot(v[x.id][0], v[x.id][1]), cap = 30 * cool + 2;
      if (sp > cap) {
        v[x.id][0] *= cap / sp;
        v[x.id][1] *= cap / sp;
      }
      p[x.id][0] += v[x.id][0];
      p[x.id][1] += v[x.id][1];
    }
    // bodies that touch push each other apart softly, a part of the way
    // at each step: the others give way as one comes, smoothly
    bodies(SOFT);
  }
  // where the user dropped them, exactly
  if (!opened) {
    for (const x of nodes) {
      const pin = pins[x.id];
      if (held?.id === x.id || !pin) continue;
      if (pin.abs) p[x.id] = [...pin.abs];
    }
    for (const x of nodes) {
      const rel = pins[x.id]?.rel;
      if (held?.id === x.id || !rel || !x.parent || !p[x.parent]) continue;
      p[x.id] = [p[x.parent][0] + rel[0], p[x.parent][1] + rel[1]];
    }
  }
  // and in the end no two bodies sit on each other, whatever pulled them
  for (let i = 0; i < 64 && bodies(1); i++);
  return p;
}

// SOFT is how much of their overlap two bodies that touch undo at each
// step of the simulation: a soft push, finished over a few steps.
const SOFT = 0.5;
// BUBBLE is a bubble's rank: below every open page.
const BUBBLE = 1e6;

// collide is the graph's one collision: every body (a bubble with its
// label under it, an open page with its margin: extent) is a solid box to
// every other. Two that overlap part along the shorter way out, sideways
// when it is not much longer (the screen is wide), by strength of their
// overlap. Of the two, the one of higher rank steps aside alone; of the
// same rank, each by its share (two opened together part halfway). It
// tells whether it moved one.
function collide(nodes: LNode[], p: Pos, v: Pos, rank: (x: LNode) => number, share: (x: LNode) => number, strength: number): boolean {
  let moved = false;
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      const a = nodes[i], b = nodes[j];
      const [ha, ta, ba] = extent(a, p[a.id]), [hb, tb, bb] = extent(b, p[b.id]);
      const dx = p[b.id][0] - p[a.id][0];
      const ox = ha + hb - Math.abs(dx);
      const oy = Math.min(ba, bb) - Math.max(ta, tb);
      if (ox <= 0.5 || oy <= 0.5) continue;
      moved = true;
      const ra = rank(a), rb = rank(b);
      const sb = ra === rb ? share(b) / (share(a) + share(b)) : rb > ra ? 1 : 0, sa = 1 - sb;
      if (ox <= oy * 1.5) {
        // one right on the other: the way out comes from their names
        const s = (Math.abs(dx) > 0.01 ? Math.sign(dx) : hash(a.id + b.id) < 0.5 ? 1 : -1) * (ox + 0.5) * strength;
        p[a.id][0] -= s * sa;
        p[b.id][0] += s * sb;
        v[a.id][0] = v[b.id][0] = 0;
      } else {
        const s = ((tb + bb) / 2 >= (ta + ba) / 2 ? 1 : -1) * (oy + 0.5) * strength;
        p[a.id][1] -= s * sa;
        p[b.id][1] += s * sb;
        v[a.id][1] = v[b.id][1] = 0;
      }
    }
  }
  return moved;
}

// edgeRest is how far from an open page's centre its bubble x rests, in
// the direction (ux, uy): just past the page's edge, its label clear.
function edgeRest([w, h]: Pt, ux: number, uy: number, x: LNode): number {
  const hx = w / 2 + 24 + Math.max(x.r, x.lw / 2) + 10, hy = h / 2 + 24 + x.r + x.lh + 10;
  return Math.min(Math.abs(ux) > 1e-6 ? hx / Math.abs(ux) : Infinity, Math.abs(uy) > 1e-6 ? hy / Math.abs(uy) : Infinity) + 30;
}

// extent is a node's box: its half width, its top and its bottom (an open
// page with a margin, a bubble with its label under it).
export function extent(x: LNode, at: Pt): [number, number, number] {
  return x.box
    ? [x.box[0] / 2 + 24, at[1] - x.box[1] / 2 - 24, at[1] + x.box[1] / 2 + 24]
    : [Math.max(x.r, x.lw / 2) + 10, at[1] - x.r - 8, at[1] + x.r + 10 + x.lh];
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
