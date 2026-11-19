import { describe, expect, it } from "vitest";
import { BOTTOM, boxAt, edgeMark, FOOT, framePages, pageMax, READABLE, TOP } from "./camera";
import {
  bornBeside, constellation, EDGE_DELAY, EDGE_RAMP, EDGE_SPEED, EDGE_ZONE, edgePush, edgeStep, extent, leash, simulate, SPACE, STEP, tether, TETHER, WIDE,
  type LNode, type Pos, type Pt,
} from "./layout";

const dist = (a: Pt, b: Pt) => Math.hypot(a[0] - b[0], a[1] - b[1]);
const bubble = (id: string, parent?: string, links: string[] = []): LNode => ({ id, parent, links, r: parent ? 14 : 26, lw: 120, lh: parent ? 18 : 44 });

// A graph as a user has it after a few weeks: four subjects, their bubbles.
function graph(): LNode[] {
  const out: LNode[] = [];
  for (const root of ["a", "b", "c", "d"]) {
    out.push(bubble(root));
    for (let i = 0; i < 3; i++) out.push(bubble(`${root}${i}`, root));
  }
  out.push(bubble("e", undefined, ["a"]));
  return out;
}

function settle(nodes: LNode[], prev: Pos = {}, pins = {}) {
  const homes = constellation(nodes);
  return { homes, pos: simulate(nodes, prev, homes, pins) };
}

// Two nodes' boxes overlap by how much, if at all.
function overlap(a: LNode, pa: Pt, b: LNode, pb: Pt): number {
  const [ha, ta, ba] = extent(a, pa), [hb, tb, bb] = extent(b, pb);
  const ox = ha + hb - Math.abs(pa[0] - pb[0]), oy = Math.min(ba, bb) - Math.max(ta, tb);
  return ox > 0 && oy > 0 ? Math.min(ox, oy) : 0;
}

describe("the layout", () => {
  it("keeps bubbles and their labels from sitting on each other", () => {
    const nodes = graph();
    const { pos } = settle(nodes);
    for (let i = 0; i < nodes.length; i++)
      for (let j = i + 1; j < nodes.length; j++) expect(overlap(nodes[i], pos[nodes[i].id], nodes[j], pos[nodes[j].id]), `${nodes[i].id} on ${nodes[j].id}`).toBeLessThan(6);
  });

  it("keeps each bubble near its parent", () => {
    const { pos } = settle(graph());
    for (const id of ["a0", "b1", "c2"]) expect(dist(pos[id], pos[id[0]])).toBeLessThan(TETHER);
  });

  it("keeps a dropped subject at its spot, and a dropped bubble at its offset from its parent", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const spot: Pt = [pos.a[0] + 260, pos.a[1] - 140];
    const rel: Pt = [-120, 90];
    const again = simulate(nodes, pos, homes, { a: { abs: spot }, b1: { rel } });
    expect(dist(again.a, spot)).toBeLessThan(25);
    expect(dist(again.b1, [again.b[0] + rel[0], again.b[1] + rel[1]])).toBeLessThan(25);
    // and it stays there as the graph is laid out again, and again
    const later = simulate(nodes, again, homes, { a: { abs: spot }, b1: { rel } });
    expect(dist(later.a, spot)).toBeLessThan(25);
  });

  it("makes room for an open page and moves nothing else beyond that, then gives the subjects their place back", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const box: Pt = [700, 520];
    const opened = nodes.map((n) => (n.id === "a" ? { ...n, box } : n));
    const open = simulate(opened, pos, homes);
    // the page stays where its bubble was
    expect(dist(open.a, pos.a)).toBeLessThan(30);
    const page = opened.find((n) => n.id === "a")!;
    for (const n of nodes) {
      if (n.id === "a") continue;
      // nothing is left on the page
      expect(overlap(page, open.a, n, open[n.id]), `${n.id} on the page`).toBeLessThan(6);
      // what was not in its way did not move
      // (a subject near it steps aside with its bubbles, all together)
      if (overlap(page, pos.a, n, pos[n.id]) === 0 && dist(pos[n.id], pos.a) > 900) expect(dist(open[n.id], pos[n.id]), n.id).toBeLessThan(40);
    }
    // closed, the subjects go back to their spot
    const closed = simulate(nodes, open, homes);
    for (const id of ["b", "c", "d"]) expect(dist(closed[id], pos[id]), id).toBeLessThan(40);
  });

  it("keeps a dragged subject within a step of the others, by a rubber band, and lets it drop one step past the last", () => {
    const others = [{ at: [0, 0] as Pt }, { at: [400, 0] as Pt }];
    // right of the rightmost subject, within a step: as asked
    expect(leash([400 + STEP - 20, 0], others, undefined, true)).toEqual([400 + STEP - 20, 0]);
    // past the step: it lags behind while held, comes back to the step dropped
    const far: Pt = [400 + STEP + 200, 0];
    const held = leash(far, others, undefined, false);
    expect(held[0]).toBeCloseTo(400 + STEP + 200 * 0.3);
    expect(leash(far, others, undefined, true)[0]).toBeCloseTo(400 + STEP);
    // an open page counts with its size
    expect(leash([400 + 350 + STEP - 10, 0], [{ at: [400, 0], box: [700, 400] }], undefined, true)[0]).toBeCloseTo(400 + 350 + STEP - 10);
  });

  it("lets a subject be dropped right of the rightmost subject, and keeps it exactly there", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const roots = nodes.filter((n) => !n.parent);
    const right = roots.reduce((a, b) => (pos[b.id][0] > pos[a.id][0] ? b : a));
    const others = roots.filter((n) => n.id !== "a").map((n) => ({ at: pos[n.id] }));
    const want: Pt = [pos[right.id][0] + STEP - 40, pos[right.id][1]];
    // the drag lets it go there (if a is the rightmost, the next one counts)
    const spot = leash(want, others, undefined, true);
    expect(spot[0]).toBeGreaterThan(Math.max(...others.map((o) => o.at[0])));
    let at = simulate(nodes, pos, homes, { a: { abs: spot } });
    expect(at.a).toEqual(spot);
    // and nothing that comes after moves it: the graph laid out again, a new bubble
    at = simulate([...nodes, bubble("b9", "b")], at, homes, { a: { abs: spot } });
    expect(at.a).toEqual(spot);
  });

  it("keeps a dropped bubble exactly at its offset, wherever its subject goes", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const rel: Pt = [-150, 110]; // clear of its subject's label: two bubbles dropped on each other part
    const spot: Pt = [pos.b[0] + 200, pos.b[1] + 100];
    const at = simulate(nodes, pos, homes, { b: { abs: spot }, b1: { rel } });
    expect(at.b1).toEqual([spot[0] + rel[0], spot[1] + rel[1]]);
  });

  it("makes room for several pages open at once (⌘-click), and moves nothing far from them", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const box: Pt = [620, 520];
    const opened = nodes.map((n) => (n.id === "a" || n.id === "b" ? { ...n, box } : n));
    const open = simulate(opened, pos, homes);
    const pages = opened.filter((n) => n.box);
    // the two pages part only as far as they must: each by less than half its width
    for (const pg of pages) expect(dist(open[pg.id], pos[pg.id]), pg.id).toBeLessThan(box[0] / 2);
    // side by side, never one on the other
    const [pa, pb] = [open[pages[0].id], open[pages[1].id]];
    expect(Math.abs(pa[0] - pb[0]) >= box[0] || Math.abs(pa[1] - pb[1]) >= box[1]).toBe(true);
    for (const n of nodes) {
      if (n.id === "a" || n.id === "b") continue;
      for (const pg of pages) expect(overlap(pg, open[pg.id], n, open[n.id]), `${n.id} on ${pg.id}`).toBeLessThan(6);
      const far = pages.every((pg) => dist(pos[n.id], pos[pg.id]) > 900);
      if (far) expect(dist(open[n.id], pos[n.id]), n.id).toBeLessThan(40);
    }
  });

  // Two open pages' boxes, margins aside, apart?
  const apart = (pa: Pt, sa: Pt, pb: Pt, sb: Pt) => Math.abs(pa[0] - pb[0]) >= (sa[0] + sb[0]) / 2 || Math.abs(pa[1] - pb[1]) >= (sa[1] + sb[1]) / 2;

  it("parts a page opened on another: the new one steps aside, the first stays", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const tall: Pt = [620, 640];
    // the subject's page open, then a new view's page born on its spot
    const one = simulate(nodes.map((n) => (n.id === "a" ? { ...n, box: tall, opened: 0 } : n)), pos, homes);
    const prev = { ...one, e: [...one.a] as Pt };
    const two = nodes.map((n) => (n.id === "a" ? { ...n, box: tall, opened: 0 } : n.id === "e" ? { ...n, box: tall, opened: 1 } : n));
    const at = simulate(two, prev, homes);
    expect(apart(at.a, tall, at.e, tall)).toBe(true);
    expect(dist(at.a, one.a)).toBeLessThan(40);
    // laid out again, nothing jumps
    const again = simulate(two, at, homes);
    expect(dist(again.a, at.a)).toBeLessThan(20);
    expect(dist(again.e, at.e)).toBeLessThan(20);
  });

  it("parts a page whose content grew after it was laid out", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const short: Pt = [620, 200], tall: Pt = [620, 640];
    const open = (h: Pt) => nodes.map((n) => (n.id === "a" ? { ...n, box: tall, opened: 0 } : n.id === "b" ? { ...n, box: h, opened: 1 } : n));
    const first = simulate(open(short), pos, homes);
    // b's mail list comes: its box grows where it stands, over a's
    const grown = simulate(open(tall), { ...first, b: [first.a[0] + 100, first.a[1] + 300] }, homes);
    expect(apart(grown.a, tall, grown.b, tall)).toBe(true);
  });

  it("keeps three or four pages opened one after the other apart", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const box: Pt = [620, 640];
    let prev = pos;
    const order = ["a", "b", "c", "e"];
    for (let k = 1; k <= order.length; k++) {
      const opened = order.slice(0, k);
      const ns = nodes.map((n) => (opened.includes(n.id) ? { ...n, box, opened: opened.indexOf(n.id) } : n));
      // each new page born where the last one is (Eriphe showed it from there)
      const seed = k > 1 ? { ...prev, [order[k - 1]]: [...prev[order[k - 2]]] as Pt } : prev;
      prev = simulate(ns, seed, homes);
      for (let i = 0; i < k; i++) for (let j = i + 1; j < k; j++) expect(apart(prev[order[i]], box, prev[order[j]], box), `${order[i]} ${order[j]}`).toBe(true);
    }
  });

  it("never moves the page held, the other gives way", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const box: Pt = [620, 520];
    const ns = nodes.map((n) => (n.id === "a" ? { ...n, box, opened: 0 } : n.id === "b" ? { ...n, box, opened: 1 } : n));
    const open = simulate(ns, pos, homes);
    const at = simulate(ns, open, homes, {}, { id: "b", at: open.a }, 24);
    expect(at.b).toEqual(open.a);
    expect(apart(at.a, box, at.b, box)).toBe(true);
  });

  // A subject and the standing view made from its card: two big bubbles,
  // the view linked to the subject, laid out right on it.
  describe("a big bubble made on another", () => {
    const subject: LNode = { id: "s", links: [], r: 40, lw: 235, lh: 44 };
    const view: LNode = { id: "v", links: ["s"], r: 35, lw: 151, lh: 44 };
    const rest = graph();
    const all = [...rest, subject, view];
    const clear = (pos: Pos, nodes = all) => {
      for (let i = 0; i < nodes.length; i++)
        for (let j = i + 1; j < nodes.length; j++) {
          const a = nodes[i], b = nodes[j];
          expect(dist(pos[a.id], pos[b.id]), `${a.id} on ${b.id}`).toBeGreaterThanOrEqual(a.r + b.r);
          expect(overlap(a, pos[a.id], b, pos[b.id]), `${a.id}'s label on ${b.id}'s`).toBeLessThanOrEqual(0.5);
        }
    };
    const born = () => {
      const { pos } = settle([...rest, subject]);
      // the view seeded exactly on the subject, its spot in the constellation beside it
      const homes = constellation(all);
      return { homes, pos: simulate(all, { ...pos, v: pos.s }, homes) };
    };

    it("parts them: neither the bubbles nor their labels overlap", () => {
      clear(born().pos);
    });

    it("keeps them apart after a page opens and closes", () => {
      const { homes, pos } = born();
      const box: Pt = [720, 520];
      const open = simulate(all.map((n) => (n.id === "s" ? { ...n, box, opened: 0 } : n)), pos, homes);
      clear(simulate(all, open, homes));
    });

    it("moves the other one when the user dropped one there", () => {
      const { homes, pos } = born();
      // the subject dropped where the view's spot is: it stays, the view goes
      const spot = homes.v;
      const at = simulate(all, pos, homes, { s: { abs: spot } });
      expect(at.s).toEqual(spot);
      clear(at);
      // the view dropped on the subject: it stays, the subject goes
      const on: Pt = [at.s[0] + 20, at.s[1] + 10];
      const again = simulate(all, at, homes, { v: { abs: on } });
      expect(again.v).toEqual(on);
      clear(again);
    });

  });

  it("parts every body by one collision: page and page, bubble and page, bubble and bubble", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const box: Pt = [640, 420];
    // two pages opened one on the other, among the bubbles
    const ns = nodes.map((n) => (n.id === "a" ? { ...n, box, opened: 0 } : n.id === "b" ? { ...n, box, opened: 1 } : n));
    const at = simulate(ns, { ...pos, b: pos.a }, homes);
    for (let i = 0; i < ns.length; i++)
      for (let j = i + 1; j < ns.length; j++) expect(overlap(ns[i], at[ns[i].id], ns[j], at[ns[j].id]), `${ns[i].id} on ${ns[j].id}`).toBeLessThanOrEqual(0.5);
  });

  it("pushes the others out of the way of what is dragged, and lets them come back when it is gone", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    // a dragged right onto b: it stays under the pointer, b gives way
    const on = simulate(nodes, pos, homes, {}, { id: "a", at: pos.b }, 24);
    expect(on.a).toEqual(pos.b);
    for (const n of nodes) if (n.id !== "a") expect(overlap(nodes[0], on.a, n, on[n.id]), `a on ${n.id}`).toBeLessThanOrEqual(0.5);
    expect(dist(on.b, pos.b)).toBeGreaterThan(20);
    // a step at a time, b is pushed softly, not thrown: never further than it must
    expect(dist(on.b, pos.b)).toBeLessThan(400);
    // dropped back where it was, b comes back to its place
    const back = simulate(nodes, simulate(nodes, on, homes, {}, { id: "a", at: pos.a }, 24), homes);
    expect(dist(back.b, pos.b)).toBeLessThan(40);
    // dropped there, a stays and b keeps out of its way
    const dropped = simulate(nodes, on, homes, { a: { abs: pos.b } });
    expect(dropped.a).toEqual(pos.b);
    for (const n of nodes) if (n.id !== "a") expect(overlap(nodes[0], dropped.a, n, dropped[n.id]), `a on ${n.id}`).toBeLessThanOrEqual(0.5);
  });

  it("moves a subject not for its own bubble dropped on its label: the bubble steps aside", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const pins = { b1: { rel: [-120, 90] as Pt } };
    let at = simulate(nodes, pos, homes, pins);
    const first = at.b;
    for (let i = 0; i < 5; i++) at = simulate(nodes, at, homes, pins);
    expect(dist(at.b, first)).toBeLessThan(1);
    expect(overlap(nodes.find((n) => n.id === "b")!, at.b, nodes.find((n) => n.id === "b1")!, at.b1)).toBeLessThanOrEqual(0.5);
  });

  it("lets a bubble dragged pass over an open page, which does not move", () => {
    const nodes = graph();
    const { homes, pos } = settle(nodes);
    const ns = nodes.map((n) => (n.id === "a" ? { ...n, box: [640, 420] as Pt, opened: 0 } : n));
    const open = simulate(ns, pos, homes);
    const over = simulate(ns, open, homes, {}, { id: "c", at: open.a }, 24);
    expect(over.c).toEqual(open.a);
    expect(dist(over.a, open.a)).toBeLessThan(1);
  });

  it("moves nothing when laid out again, even where children press on each other", () => {
    const nodes = graph();
    for (let i = 3; i < 8; i++) nodes.push(bubble(`a${i}`, "a"));
    const { homes, pos } = settle(nodes);
    let at = pos;
    for (let i = 0; i < 3; i++) at = simulate(nodes, at, homes);
    for (const n of nodes) expect(dist(at[n.id], pos[n.id]), n.id).toBeLessThan(1);
  });

  it("tethers a bubble to its parent", () => {
    expect(tether([100, 0], [0, 0], [TETHER, TETHER], true)).toEqual([100, 0]);
    expect(tether([500, 0], [0, 0], [TETHER, TETHER], true)[0]).toBeCloseTo(TETHER);
    expect(tether([500, 0], [0, 0], [TETHER, TETHER], false)[0]).toBeCloseTo(TETHER + 200 * 0.3);
  });

  it("scrolls at the edge after a wait, speeding up gently", () => {
    expect(edgePush(500, 0, 1000)).toBe(0);
    expect(edgePush(1000 - EDGE_ZONE / 2, 0, 1000)).toBeCloseTo(0.5);
    expect(edgePush(0, 0, 1000)).toBe(-1);
    expect(edgeStep(1, 1000, 1000)).toBe(-0);
    expect(edgeStep(1, 1000 + EDGE_DELAY, 1000)).toBe(-0);
    expect(edgeStep(1, 1000 + EDGE_DELAY + EDGE_RAMP / 2, 1000)).toBeCloseTo(-EDGE_SPEED / 2);
    expect(edgeStep(-1, 1000 + EDGE_DELAY + EDGE_RAMP * 3, 1000)).toBeCloseTo(EDGE_SPEED);
  });
});

describe("the constellation", () => {
  const roots = (n: number) => Array.from({ length: n }, (_, i) => bubble(`r${i}`));
  const centre = (ps: Pt[]): Pt => [ps.reduce((a, p) => a + p[0], 0) / ps.length, ps.reduce((a, p) => a + p[1], 0) / ps.length];
  const withKids = (n: number, kids = 3) => {
    const nodes: LNode[] = [];
    for (const r of roots(n)) {
      nodes.push(r);
      for (let i = 0; i < kids; i++) nodes.push(bubble(`${r.id}.${i}`, r.id));
    }
    return nodes;
  };

  it("holds one subject at the centre, two side by side", () => {
    expect(constellation(roots(1))).toEqual({ r0: [0, 0] });
    const two = constellation(roots(2));
    expect(two.r0[1]).toBeCloseTo(0);
    expect(two.r1[1]).toBeCloseTo(0);
    expect(two.r0[0]).toBeCloseTo(-two.r1[0]);
  });

  it("spaces three subjects evenly around the centre, the ellipse no bigger than they need", () => {
    const at = constellation(roots(3));
    const ps = Object.values(at);
    const c = centre(ps);
    expect(Math.hypot(c[0], c[1])).toBeLessThan(1);
    // evenly: the same angle between each and the next, on the unstretched ellipse
    const angles = ps.map(([x, y]) => Math.atan2(y, x / WIDE)).sort((a, b) => a - b);
    const gaps = angles.map((a, i) => (i ? a - angles[i - 1] : a + 2 * Math.PI - angles[angles.length - 1]));
    for (const g of gaps) expect(g).toBeCloseTo((2 * Math.PI) / 3);
    for (const p of ps) expect(Math.abs(p[0])).toBeLessThanOrEqual(SPACE * WIDE);
    for (const p of ps) expect(Math.abs(p[1])).toBeLessThanOrEqual(SPACE);
  });

  it("lays out a few subjects with their bubbles compactly around the centre, nothing on anything", () => {
    for (const n of [2, 3, 4, 5]) {
      const nodes = withKids(n);
      const { homes, pos } = settle(nodes);
      const rs = roots(n).map((r) => pos[r.id]);
      // no empty middle: the subjects' centre is the graph's, and none is far
      const c = centre(rs);
      expect(Math.hypot(c[0], c[1]), `${n}: centre`).toBeLessThan(60);
      const reach = Math.max(...Object.values(homes).map((h) => Math.hypot(h[0], h[1])));
      for (const r of roots(n)) expect(Math.hypot(pos[r.id][0], pos[r.id][1]), `${n}: ${r.id}`).toBeLessThan(reach + 120);
      // the whole graph fits a screen or so
      const xs = Object.values(pos).map((p) => p[0]), ys = Object.values(pos).map((p) => p[1]);
      expect(Math.max(...xs) - Math.min(...xs), `${n}: width`).toBeLessThan(1900);
      expect(Math.max(...ys) - Math.min(...ys), `${n}: height`).toBeLessThan(1100);
      for (let i = 0; i < nodes.length; i++)
        for (let j = i + 1; j < nodes.length; j++) expect(overlap(nodes[i], pos[nodes[i].id], nodes[j], pos[nodes[j].id]), `${n}: ${nodes[i].id} on ${nodes[j].id}`).toBeLessThanOrEqual(0.5);
    }
  });

  it("slides the others only a little when a subject comes, linked to one of them or not", () => {
    for (const n of [2, 3, 4, 5, 6]) {
      const before = constellation(roots(n));
      const comers = [bubble("new"), ...roots(n).map((r) => bubble("new", undefined, [r.id]))];
      for (const c of comers) {
        const after = constellation([...roots(n), c]);
        for (const id of Object.keys(before)) expect(dist(before[id], after[id]), `${n} + ${c.links}: ${id}`).toBeLessThan(SPACE * 0.75);
      }
    }
    // laid out, the graph follows its spots
    const nodes = withKids(3);
    const { pos } = settle(nodes);
    const more = [...nodes, bubble("r3")];
    const at = simulate(more, pos, constellation(more));
    for (const r of roots(3)) expect(dist(at[r.id], pos[r.id]), r.id).toBeLessThan(SPACE * 0.75);
  });

  it("puts linked subjects side by side, whenever they were made", () => {
    const nodes = [...roots(5), bubble("v", undefined, ["r1"])];
    const at = constellation(nodes);
    // v's nearest subject is r1, its neighbour on the ellipse
    const near = (id: string) => Object.keys(at).filter((o) => o !== id).sort((a, b) => dist(at[id], at[a]) - dist(at[id], at[b]))[0];
    expect(near("v")).toBe("r1");
    // a link from a bubble counts for its subject
    const kids = [...roots(5), bubble("r4.0", "r4", ["r1"])];
    const k = constellation(kids);
    const others = ["r0", "r2", "r3"].map((id) => dist(k.r4, k[id]));
    expect(dist(k.r4, k.r1)).toBeLessThanOrEqual(Math.min(...others) + 1);
  });

  it("keeps a subject the user dropped where it was dropped, the others on their ellipse", () => {
    const nodes = withKids(4);
    const { homes, pos } = settle(nodes);
    const spot: Pt = [pos.r0[0] - 200, pos.r0[1] + 150];
    const at = simulate(nodes, pos, homes, { r0: { abs: spot } });
    expect(at.r0).toEqual(spot);
    for (const id of ["r1", "r2", "r3"]) expect(dist(at[id], pos[id]), id).toBeLessThan(60);
  });

  it("does not move a subject's spot for an open page, and gives every subject its spot back when it closes", () => {
    const nodes = withKids(4);
    const { homes, pos } = settle(nodes);
    const opened = nodes.map((n) => (n.id === "r0" ? { ...n, box: [700, 520] as Pt, opened: 0 } : n));
    expect(constellation(opened)).toEqual(homes);
    const open = simulate(opened, pos, homes);
    const page = opened.find((n) => n.id === "r0")!;
    for (const n of nodes) if (n.id !== "r0") expect(overlap(page, open.r0, n, open[n.id]), `${n.id} on the page`).toBeLessThan(6);
    const closed = simulate(nodes, open, homes);
    for (const r of roots(4)) expect(dist(closed[r.id], pos[r.id]), r.id).toBeLessThan(40);
  });

  it("opens a page that would cover the open one right beside it, at its height, and one clear of it where it is", () => {
    const from: Pt = [100, 50], box: Pt = [620, 400];
    expect(bornBeside([300, -100], box, from, [620, 600])).toEqual([100 + 620 + 48, 50]);
    expect(bornBeside([1200, 0], box, from, [620, 600])).toEqual([1200, 0]);
    // laid out, it stays beside: neither moves over the other
    const nodes = withKids(3);
    const { homes, pos } = settle(nodes);
    const at = { ...pos, r1: bornBeside(pos.r1, box, pos.r0, box) };
    const open = simulate(nodes.map((n) => (n.id === "r0" ? { ...n, box, opened: 0 } : n.id === "r1" ? { ...n, box, opened: 1 } : n)), at, homes);
    expect(Math.abs(open.r1[1] - open.r0[1])).toBeLessThan(30);
    expect(Math.abs(open.r1[0] - open.r0[0])).toBeGreaterThanOrEqual(620);
  });

  it("does not drift as the graph is laid out again and again, and nothing sits on anything", () => {
    for (const n of [2, 3, 4, 5])
      for (const kids of [0, 3, 6]) {
        const nodes = withKids(n, kids);
        const { homes, pos } = settle(nodes);
        let at = pos;
        for (let i = 0; i < 6; i++) at = simulate(nodes, at, homes);
        for (const x of nodes) expect(dist(at[x.id], pos[x.id]), `${n}×${kids}: ${x.id}`).toBeLessThan(1);
        for (let i = 0; i < nodes.length; i++)
          for (let j = i + 1; j < nodes.length; j++) expect(overlap(nodes[i], at[nodes[i].id], nodes[j], at[nodes[j].id]), `${n}×${kids}: ${nodes[i].id} on ${nodes[j].id}`).toBeLessThanOrEqual(0.5);
      }
  });

  it("keeps an open page clear and gives the subjects their spot back, whichever opens", () => {
    for (const n of [2, 3, 4, 5])
      for (const kids of [0, 3]) {
        const nodes = withKids(n, kids);
        const { homes, pos } = settle(nodes);
        for (const r of roots(n)) {
          const ns = nodes.map((x) => (x.id === r.id ? { ...x, box: [700, 560] as Pt, opened: 0 } : x));
          const open = simulate(ns, pos, homes);
          const page = ns.find((x) => x.id === r.id)!;
          for (const x of ns) if (x.id !== r.id) expect(overlap(page, open[r.id], x, open[x.id]), `${n}×${kids} ${r.id}: ${x.id} on the page`).toBeLessThan(6);
          const closed = simulate(nodes, open, homes);
          for (const q of roots(n)) expect(dist(closed[q.id], pos[q.id]), `${n}×${kids} ${r.id}: ${q.id}`).toBeLessThan(50);
        }
      }
  });
});

describe("the camera", () => {
  const size = { w: 1440, h: 900 };
  it("frames every open page when they fit readable, else the one the console talks to", () => {
    const two = [
      { id: "a", box: boxAt([0, 0], [620, 520]) },
      { id: "b", box: boxAt([700, 0], [620, 520]) },
    ];
    const both = framePages(two, "b", [], size);
    expect(both.k).toBeGreaterThanOrEqual(READABLE);
    const far = [two[0], { id: "b", box: boxAt([2600, 900], [620, 520]) }];
    const one = framePages(far, "b", [], size);
    // b in the middle of the screen
    expect(one.x + 2600 * one.k).toBeCloseTo(size.w / 2, 0);
    expect(one.k).toBeGreaterThanOrEqual(READABLE);
    // the console on the home: the last page opened (the one Eriphe just showed)
    const home = framePages(far, null, [], size);
    expect(home.x + 2600 * home.k).toBeCloseTo(size.w / 2, 0);
  });

  it("frames a page shown beside whole on a laptop: its head on screen, its foot above the bar", () => {
    const laptop = { w: 1180, h: 720 };
    const h = pageMax(laptop, 640);
    expect(h).toBe(720 - TOP - BOTTOM - 40);
    const subject = { id: "s", box: boxAt([0, 0], [620, h]) };
    const view = { id: "v", box: boxAt([620 + 48, 0], [620, h]) };
    const cam = framePages([subject, view], "v", [], laptop, 0.9);
    const top = cam.y + view.box[1] * cam.k, bottom = cam.y + view.box[3] * cam.k;
    const left = cam.x + view.box[0] * cam.k, right = cam.x + view.box[2] * cam.k;
    expect(top).toBeGreaterThanOrEqual(TOP);
    expect(bottom).toBeLessThanOrEqual(laptop.h - BOTTOM);
    expect(left).toBeGreaterThanOrEqual(0);
    expect(right).toBeLessThanOrEqual(laptop.w);
    // the bar grown with cards: the page stops above them
    const tall = { ...laptop, foot: 320 };
    const c2 = framePages([view], "v", [], tall, 0.9);
    expect(c2.y + view.box[3] * c2.k).toBeLessThanOrEqual(laptop.h - 320);
    expect(pageMax(tall, 640)).toBe(720 - TOP - 320 - 40);
  });
});

describe("the edge marks", () => {
  const size = { w: 1000, h: 800 };
  it("mark no node in view", () => {
    expect(edgeMark(500, 400, size)).toBeNull();
  });
  it("sit at the nearest edge, turned toward the node", () => {
    const right = edgeMark(1400, 300, size)!;
    expect([right.x, right.y, right.turn]).toEqual([982, 300, 0]);
    const above = edgeMark(200, -500, size)!;
    expect([above.x, above.y]).toEqual([200, 18]);
    expect(above.turn).toBeCloseTo(-Math.PI / 2);
  });
  it("keep above the console", () => {
    const low = edgeMark(500, 760, size)!;
    expect(low.y).toBe(800 - FOOT);
    expect(low.turn).toBeCloseTo(Math.PI / 2);
  });
});
