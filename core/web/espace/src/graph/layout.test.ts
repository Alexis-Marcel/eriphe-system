import { describe, expect, it } from "vitest";
import { BOTTOM, boxAt, edgeMark, FOOT, framePages, pageMax, READABLE, TOP } from "./camera";
import {
  EDGE_DELAY, EDGE_RAMP, EDGE_SPEED, EDGE_ZONE, edgePush, edgeStep, extent, homesFor, leash, ROOM, simulate, STEP, tether, TETHER,
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
  const roots = nodes.filter((n) => !n.parent).map((n) => n.id);
  const homes = homesFor(roots, {});
  return { homes, pos: simulate(nodes, prev, homes, pins) };
}

// Two nodes' boxes overlap by how much, if at all.
function overlap(a: LNode, pa: Pt, b: LNode, pb: Pt): number {
  const [ha, ta, ba] = extent(a, pa), [hb, tb, bb] = extent(b, pb);
  const ox = ha + hb - Math.abs(pa[0] - pb[0]), oy = Math.min(ba, bb) - Math.max(ta, tb);
  return ox > 0 && oy > 0 ? Math.min(ox, oy) : 0;
}

describe("the layout", () => {
  it("gives each new subject room, and never moves the homes it gave", () => {
    const h1 = homesFor(["a", "b", "c"], {});
    for (const [x, y] of [["a", "b"], ["a", "c"], ["b", "c"]]) expect(dist(h1[x], h1[y])).toBeGreaterThanOrEqual(ROOM);
    const h2 = homesFor(["a", "b", "c", "d", "e"], h1);
    for (const id of ["a", "b", "c"]) expect(h2[id]).toEqual(h1[id]);
    expect(Math.min(...["a", "b", "c", "d"].map((id) => dist(h2.e, h2[id])))).toBeGreaterThanOrEqual(ROOM);
  });

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
      if (overlap(page, pos.a, n, pos[n.id]) === 0 && dist(pos[n.id], pos.a) > 700) expect(dist(open[n.id], pos[n.id]), n.id).toBeLessThan(40);
    }
    // closed, the subjects go back home
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
      const { homes, pos } = settle([...rest, subject]);
      // the view seeded exactly on the subject, its home there too
      const h = { ...homes, v: pos.s };
      return { homes: h, pos: simulate(all, { ...pos, v: pos.s }, h) };
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
      // the subject dropped where the view's home is: it stays, the view goes
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

    it("never gives a new subject a home on one the user dropped", () => {
      const homes = homesFor(["a", "b"], {});
      // a subject dropped right where the next home would be
      const dropped = homesFor(["a", "b", "c"], homes).c;
      const h = homesFor(["a", "b", "c"], homes, [dropped]);
      expect(dist(h.c, dropped)).toBeGreaterThanOrEqual(ROOM);
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
    // dropped back home, b comes back to its place
    const back = simulate(nodes, simulate(nodes, on, homes, {}, { id: "a", at: pos.a }, 24), homes);
    expect(dist(back.b, pos.b)).toBeLessThan(40);
    // dropped there, a stays and b keeps out of its way
    const dropped = simulate(nodes, on, homes, { a: { abs: pos.b } });
    expect(dropped.a).toEqual(pos.b);
    for (const n of nodes) if (n.id !== "a") expect(overlap(nodes[0], dropped.a, n, dropped[n.id]), `a on ${n.id}`).toBeLessThanOrEqual(0.5);
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
