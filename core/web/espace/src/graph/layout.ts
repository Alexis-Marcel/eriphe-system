// The graph's layout: a small force simulation, a constellation. Roots are
// held loosely on an ellipse around the centre (constellation), or where
// the user dropped them, children pulled to their parent, links as weak
// springs, everything pushing everything else away and pulled faintly to
// the centre. Seeded from the previous positions, so a
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

// The constellation: the subjects sit on an ellipse around the centre,
// evenly spaced, SPACE apart from one to the next (and BUSY more for each
// bubble a subject holds, on average), the ellipse WIDE times
// as wide as high at most (the screen's shape), held there loosely.
export const SPACE = 300;
export const BUSY = 24;
export const WIDE = 1.7;

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

// constellation is where each subject (a root) is held: on an ellipse
// around (0, 0), evenly spaced, the ellipse growing with their number and
// shaped like the screen (aspect, its width over its height). Their order
// is the order given (the order they were made), except that a subject
// linked to one already placed (a link of theirs or of their bubbles)
// comes right after it: related subjects sit side by side. A new subject
// takes its slot and the others only slide a little: the ring turns the
// way that keeps the ones already there nearest where they were. Nothing open changes it: an open page pushes the
// others aside for a while, not their place.
export function constellation(nodes: LNode[], aspect = WIDE): Pos {
  const byId = new Map(nodes.map((x) => [x.id, x]));
  const rootOf = (id: string): string => {
    let x = byId.get(id);
    for (let i = 0; x?.parent && byId.has(x.parent) && i < 64; i++) x = byId.get(x.parent);
    return x?.id ?? id;
  };
  const ties = new Map<string, Set<string>>();
  for (const x of nodes)
    for (const l of x.links) {
      if (!byId.has(l)) continue;
      const a = rootOf(x.id), b = rootOf(l);
      if (a === b) continue;
      if (!ties.has(a)) ties.set(a, new Set());
      if (!ties.has(b)) ties.set(b, new Set());
      ties.get(a)!.add(b);
      ties.get(b)!.add(a);
    }
  // the subjects come one by one, in the order given: each takes its slot
  // (after the last one it is linked to, else at the end of the ring), the
  // slots even again, the ring turned the way that moves the ones already
  // there least
  const order: string[] = [];
  let turn: Record<string, number> = {};
  for (const x of nodes) {
    if (x.parent && byId.has(x.parent)) continue;
    let near = -1;
    order.forEach((o, i) => ties.get(x.id)?.has(o) && (near = i));
    order.splice(near < 0 ? order.length : near + 1, 0, x.id);
    const k = order.length;
    let sin = 0, cos = 0;
    order.forEach((id, i) => {
      if (turn[id] === undefined) return;
      const d = turn[id] - (2 * Math.PI * i) / k;
      sin += Math.sin(d);
      cos += Math.cos(d);
    });
    // the first one starts on the left: two sit side by side
    const phi = k === 1 ? Math.PI : Math.atan2(sin, cos);
    turn = Object.fromEntries(order.map((id, i) => [id, phi + (2 * Math.PI * i) / k]));
  }
  const out: Pos = {};
  const m = order.length;
  if (m === 1) out[order[0]] = [0, 0];
  if (m < 2) return out;
  // neighbours SPACE apart on a circle, more as the subjects hold more
  // bubbles (BUSY for each, on average, up to 8), stretched sideways to
  // the screen
  const busy = Math.min(8, (nodes.length - m) / m);
  const r = (SPACE + BUSY * busy) / (2 * Math.sin(Math.PI / m));
  const e = Math.max(1, Math.min(WIDE, aspect));
  for (const id of order) out[id] = [Math.cos(turn[id]) * r * e, Math.sin(turn[id]) * r];
  return out;
}

// bornBeside is where a page opened while another is open (from, its
// size fromBox) is born: where its bubble was (at), unless it would cover
// that page; then right beside it, at the same height, so that the camera
// framing the new one keeps the other above the bar too.
export function bornBeside(at: Pt, box: Pt, from: Pt, fromBox: Pt): Pt {
  const gap = 48; // their two margins
  const apart = Math.abs(at[0] - from[0]) >= (box[0] + fromBox[0]) / 2 + gap || Math.abs(at[1] - from[1]) >= (box[1] + fromBox[1]) / 2 + gap;
  return apart ? at : [from[0] + (box[0] + fromBox[0]) / 2 + gap, from[1]];
}

// simulate places the nodes. prev: where they were; spots: where the
// roots are held (constellation); pins: where the user dropped them;
// held: the node being dragged, kept under the pointer.
export function simulate(nodes: LNode[], prev: Pos, spots: Pos, pins: Record<string, Pin> = {}, held?: { id: string; at: Pt }, iterations?: number): Pos {
  const byId = new Map(nodes.map((x) => [x.id, x]));
  const anc: Pos = {};
  for (const x of nodes) {
    if (x.parent && byId.has(x.parent)) continue;
    const a = pins[x.id]?.abs ?? spots[x.id];
    if (a) anc[x.id] = a;
  }
  const opened = nodes.some((x) => x.box);
  const p: Pos = {};
  const v: Pos = {};
  // seed: the previous spot, else its own, else beside the parent
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
  const rank = (x: LNode) => (held?.id === x.id ? HELD : pinned(x) ? 1 : x.box ? 2 + (x.opened ?? 0) : BUBBLE);
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
    // an open page presses on the bubbles near it, and on their parent with
    // them: a subject's bubbles caught between it and the page step aside
    // all together, the subject leading
    for (const pg of nodes) {
      if (!pg.box) continue;
      const [ha, ta, ba] = extent(pg, p[pg.id]);
      for (const x of nodes) {
        if (x.box || x.parent === pg.id || held?.id === x.id) continue;
        const [hb, tb, bb] = extent(x, p[x.id]);
        const dx = p[x.id][0] - p[pg.id][0];
        const ox = ha + hb + PRESS - Math.abs(dx), oy = Math.min(ba, bb) + PRESS - Math.max(ta, tb);
        if (ox <= 0 || oy <= 0) continue;
        const push: Pt = ox <= oy * 1.5 ? [(dx >= 0 ? 1 : -1) * ox * 0.1, 0] : [0, ((tb + bb) / 2 >= (ta + ba) / 2 ? 1 : -1) * oy * 0.1];
        f[x.id][0] += push[0];
        f[x.id][1] += push[1];
        const par = x.parent && byId.get(x.parent);
        if (par && !par.box) {
          f[par.id][0] += push[0] * 0.6;
          f[par.id][1] += push[1] * 0.6;
        }
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
    // roots held to their spot in the constellation (or where the user
    // dropped them); and every body pulled faintly to the centre, so that
    // nothing drifts off into a corner
    for (const x of nodes) {
      if (x.box) continue; // an open page stays where it was laid, not pulled back over the page it stepped aside from
      const a = anc[x.id];
      if (a) {
        // while a page is open the roots give way, then come back to their spot
        const ka = opened ? 0.012 : 0.08;
        f[x.id][0] += (a[0] - p[x.id][0]) * ka;
        f[x.id][1] += (a[1] - p[x.id][1]) * ka;
      }
      if (opened) continue; // while a page is open, the room it needs comes first
      f[x.id][0] -= p[x.id][0] * GRAVITY;
      f[x.id][1] -= p[x.id][1] * GRAVITY;
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
  // what barely moved stays where it was: laid out again, nothing creeps
  for (const x of nodes) {
    const was = prev[x.id];
    if (was && held?.id !== x.id && !pinned(x) && Math.hypot(p[x.id][0] - was[0], p[x.id][1] - was[1]) < STILL) p[x.id] = [...was];
  }
  // and in the end no two bodies sit on each other, whatever pulled them
  for (let i = 0; i < 64 && bodies(1); i++);
  // laid out from nothing, it settles again as from where it is, until it
  // is at rest (SETTLE times at most): the next layout then moves nothing
  if (!settled && nodes.length && !held && iterations === undefined) {
    let at = p;
    for (let i = 0; i < SETTLE; i++) {
      const next = simulate(nodes, at, spots, pins);
      const still = nodes.every((x) => Math.hypot(next[x.id][0] - at[x.id][0], next[x.id][1] - at[x.id][1]) < 1);
      at = next;
      if (still) break;
    }
    return at;
  }
  return p;
}

// SETTLE is how many times at most a graph laid out from nothing settles
// again before it is shown.
const SETTLE = 8;

// GRAVITY is how hard every body is pulled to the centre: faintly.
const GRAVITY = 0.003;

// STILL is how far a node may be found from where it was and stay there.
const STILL = 10;

// SOFT is how much of their overlap two bodies that touch undo at each
// step of the simulation: a soft push, finished over a few steps.
const SOFT = 0.5;
// PRESS is how far past its margin an open page presses on the bubbles.
const PRESS = 40;
// HELD is the rank of the node held, BUBBLE a bubble's: below every open
// page.
const HELD = 0;
const BUBBLE = 1e6;

// collide is the graph's one collision: every body (a bubble with its
// label under it, an open page with its margin: extent) is a solid box to
// every other. Two that overlap part along the shorter way out, sideways
// when it is not much longer (the screen is wide), by strength of their
// overlap. Of the two, the one of higher rank steps aside alone; of the
// same rank, each by its share (two opened together part halfway). A child
// gives way to its parent, unless held: its parent is not pushed away by
// it. A bubble held moves no page: it passes over it. It tells whether it
// moved one.
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
      const ra = rank(a), rb = rank(b);
      if ((ra === HELD && !a.box && b.box) || (rb === HELD && !b.box && a.box)) continue;
      moved = true;
      const sb =
        b.parent === a.id && rb !== HELD ? 1 : a.parent === b.id && ra !== HELD ? 0 : ra === rb ? share(b) / (share(a) + share(b)) : rb > ra ? 1 : 0;
      const sa = 1 - sb;
      // a bubble that gives way alone goes out by the shortest way that
      // does not lead onto an open page
      const out = sb === 1 && !b.box ? wayOut(b, a, nodes, p) : sa === 1 && !a.box ? wayOut(a, b, nodes, p) : null;
      if (out) {
        const x = sb === 1 ? b : a;
        p[x.id][0] += out[0] * strength;
        p[x.id][1] += out[1] * strength;
        v[x.id][0] = v[x.id][1] = 0;
      } else if (ox <= oy * 1.5) {
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

// wayOut is how a bubble x on the body pg (a page or a bubble) steps off
// it: the shortest of the four ways (sideways a little preferred, the
// screen is wide) that does not put it on an open page; null when each
// does.
function wayOut(x: LNode, pg: LNode, nodes: LNode[], p: Pos): Pt | null {
  const [hp, tp, bp] = extent(pg, p[pg.id]), [hx, tx, bx] = extent(x, p[x.id]);
  const cx = p[x.id][0], px = p[pg.id][0];
  const ways: Pt[] = [
    [px + hp + hx - cx + 0.5, 0],
    [px - hp - hx - cx - 0.5, 0],
    [0, bp - tx + 0.5],
    [0, tp - bx - 0.5],
  ];
  let best: Pt | null = null, cost = Infinity;
  for (const w of ways) {
    const c = Math.abs(w[0]) / 1.5 + Math.abs(w[1]);
    if (c >= cost) continue;
    const at: Pt = [cx + w[0], p[x.id][1] + w[1]];
    const [h2, t2, b2] = extent(x, at);
    const onOther = nodes.some((o) => {
      if (!o.box || o.id === pg.id) return false;
      const [ho, to, bo] = extent(o, p[o.id]);
      return ho + h2 - Math.abs(at[0] - p[o.id][0]) > 0.5 && Math.min(b2, bo) - Math.max(t2, to) > 0.5;
    });
    if (!onOther) (best = w), (cost = c);
  }
  return best;
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
