import { describe, expect, it } from "vitest";
import { boxAt, framePages, READABLE } from "./camera";
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
  });
});
