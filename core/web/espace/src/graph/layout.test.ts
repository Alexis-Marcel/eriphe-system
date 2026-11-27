import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { boxAt, FLOOR, frameAround } from "./camera";
import { graph, rng } from "./generated";
import { DEFAULT_FORCES, EDGE_DELAY, EDGE_RAMP, EDGE_SPEED, EDGE_ZONE, edgePush, edgeStep, extent, FORCES, pairs, Quad, repelPairs, setForces, Sim, T, type LNode, type Pos, type Pt } from "./layout";

const dist = (a: Pt, b: Pt) => Math.hypot(a[0] - b[0], a[1] - b[1]);

// A node as Graph.tsx gives it (model.ts's sizes): a subject bigger, its
// label two lines; a bubble smaller, its label one.
const node = (id: string, title: string, o: Partial<LNode> = {}): LNode => {
  const root = !o.parent;
  return { id, links: [], r: root ? 37 : 16, lw: Math.min(300, title.length * (root ? 8.4 : 7.4) + 8), lh: root ? 44 : 18, ...o };
};

// The prototype's graph: three subjects, one linked to Mes mails, two
// bubbles.
function small(): LNode[] {
  return [
    node("MES", "Mes mails"),
    node("PRO", "Prod GetOut"),
    node("MAJ", "Ma journée"),
    node("UNE", "Une vue mails rangée par sections", { links: ["MES"] }),
    node("ETU", "Étudier les sections", { parent: "UNE" }),
    node("FAI", "Faire le tour de l'infra", { parent: "PRO" }),
  ];
}

// A graph as a user has it after a few weeks: some forty nodes, subjects
// with their bubbles (one deeper), text links between some, a few alone.
function big(): LNode[] {
  const out: LNode[] = [];
  for (let i = 0; i < 9; i++) {
    out.push(node(`s${i}`, `Sujet numéro ${i}`, { links: i > 0 && i % 3 === 0 ? [`s${i - 1}`] : [] }));
    for (let j = 0; j < 3; j++) out.push(node(`s${i}c${j}`, `Bulle ${i}.${j}`, { parent: `s${i}` }));
  }
  out.push(node("s0c0g", "Plus bas", { parent: "s0c0" }));
  for (let i = 0; i < 4; i++) out.push(node(`lone${i}`, `Seul ${i}`));
  return out;
}

// The owner's graph: three subjects each linked both ways to the view it
// follows, one alone.
function owner(): LNode[] {
  return [
    node("SJ", "Suivre mes journées", { links: ["MJ"] }),
    node("MJ", "Ma journée", { links: ["SJ"] }),
    node("SM", "Suivre mes mails", { links: ["MM"] }),
    node("MM", "Mes mails", { links: ["SM"] }),
    node("SI", "Suivre mes investissements", { links: ["MP"] }),
    node("MP", "Mon portefeuille", { links: ["SI"] }),
    node("OUT", "Outils à revoir"),
  ];
}

// open gives the graph with those pages open.
const open = (nodes: LNode[], ids: string[], box: Pt = ids.length > 1 ? [620, 480] : [700, 520]) => nodes.map((n) => (ids.includes(n.id) ? { ...n, box } : n));

// still is how far the bodies go in ticks more, once settled.
function still(sim: Sim, nodes: LNode[], ticks = 200): number {
  const p0 = sim.positions();
  for (let i = 0; i < ticks; i++) sim.tick();
  return moved(p0, sim.positions(), nodes.map((n) => n.id));
}

function laid(nodes: LNode[]): Sim {
  const sim = new Sim({ ...DEFAULT_FORCES });
  sim.sync(nodes);
  return sim;
}

// overlaps are the pairs of bodies that sit on each other: more than the
// graph tolerates at rest (T.jam), well within their margins.
function overlaps(sim: Sim, nodes: LNode[]): string[] {
  const p = sim.positions(), out: string[] = [];
  const on = (a: LNode, pa: Pt, b: LNode, pb: Pt) => {
    const [ha, ta, ba] = extent(a, pa), [hb, tb, bb] = extent(b, pb);
    return ha + hb - Math.abs(pa[0] - pb[0]) > T.jam + 0.5 && Math.min(ba, bb) - Math.max(ta, tb) > T.jam + 0.5;
  };
  for (let i = 0; i < nodes.length; i++)
    for (let j = i + 1; j < nodes.length; j++) if (on(nodes[i], p[nodes[i].id], nodes[j], p[nodes[j].id])) out.push(`${nodes[i].id}/${nodes[j].id}`);
  return out;
}

// gap is the room between two bodies' boxes (0: they touch or overlap).
function gap(sim: Sim, a: LNode, b: LNode): number {
  const pa = sim.at(a.id)!, pb = sim.at(b.id)!;
  const [ha, ta, ba] = extent(a, pa), [hb, tb, bb] = extent(b, pb);
  const gx = Math.max(0, Math.abs(pa[0] - pb[0]) - ha - hb), gy = Math.max(0, tb - ba, ta - bb);
  return Math.hypot(gx, gy);
}

// linkedVsNot is the mean distance between linked pairs, and the median
// distance between unlinked ones.
function linkedVsNot(sim: Sim, nodes: LNode[]): { linked: number; unlinked: number } {
  const p = sim.positions();
  const ps = pairs(nodes), key = new Set(ps.map(([a, b]) => `${a}\n${b}`));
  const linked = ps.map(([a, b]) => dist(p[a], p[b]));
  const unlinked: number[] = [];
  for (let i = 0; i < nodes.length; i++)
    for (let j = i + 1; j < nodes.length; j++) {
      const [a, b] = [nodes[i].id, nodes[j].id].sort();
      if (!key.has(`${a}\n${b}`)) unlinked.push(dist(p[a], p[b]));
    }
  unlinked.sort((a, b) => a - b);
  return { linked: linked.reduce((s, d) => s + d, 0) / linked.length, unlinked: unlinked[unlinked.length >> 1] };
}

// drag holds id and takes it to `to` in frames, a tick each, then lets go
// and lets the graph settle; during: what the frames saw half way.
function drag(sim: Sim, id: string, to: Pt, frames = 60): { mid: Pos } {
  const from = sim.at(id)!;
  let mid: Pos = {};
  for (let i = 1; i <= frames; i++) {
    const u = i / frames;
    sim.hold(id, [from[0] + (to[0] - from[0]) * u, from[1] + (to[1] - from[1]) * u]);
    sim.tick();
    if (i === frames >> 1) mid = sim.positions();
  }
  sim.drop();
  sim.settle();
  return { mid };
}

const moved = (a: Pos, b: Pos, ids: string[]) => Math.max(0, ...ids.map((id) => dist(a[id], b[id])));

// The same rules whatever the graph's size: each test is played as a small
// graph is laid out (every pair), and as one past T.exact bodies is (the
// tree and the grid).
describe.each([
  ["", T.exact],
  [", by the tree and the grid", 0],
])("the force graph%s", (_, exact) => {
  const was = T.exact;
  beforeAll(() => void (T.exact = exact));
  afterAll(() => void (T.exact = was));

  it("draws one link per pair of nodes, whichever way and however often it goes", () => {
    expect(pairs(owner())).toEqual([
      ["MJ", "SJ"],
      ["MM", "SM"],
      ["MP", "SI"],
    ]);
    expect(pairs([{ id: "a" }, { id: "b", parent: "a", links: ["a", "a", "zz"] }, { id: "c", links: ["c"] }])).toEqual([["a", "b"]]);
  });

  it("lays the graph out as a compact disc, nothing on anything, and rests", () => {
    for (const nodes of [small(), owner(), big()]) {
      const sim = laid(nodes);
      expect(sim.asleep()).toBe(true);
      expect(overlaps(sim, nodes)).toEqual([]);
      const p = sim.positions();
      const far = Math.max(...nodes.map((n) => Math.hypot(...p[n.id])));
      expect(far).toBeLessThan(nodes.length > 10 ? 900 : 450);
    }
  });

  it("holds linked nodes closer than unlinked ones", () => {
    for (const nodes of [small(), owner(), big()]) {
      const { linked, unlinked } = linkedVsNot(laid(nodes), nodes);
      expect(linked).toBeLessThan(unlinked * 0.75);
    }
  });

  it("is the same graph laid out twice", () => {
    expect(laid(big()).positions()).toEqual(laid(big()).positions());
  });

  it("is still once at rest: nothing ticks, nothing trembles", () => {
    for (const nodes of [small(), owner(), big()]) {
      const sim = laid(nodes);
      expect(still(sim, nodes)).toBe(0);
      sim.sync(open(nodes, [nodes[0].id]));
      sim.settle();
      expect(sim.asleep()).toBe(true);
      expect(still(sim, nodes)).toBe(0);
    }
  });

  it("brings the ends of a link added at rest together, and rests again", () => {
    const nodes = owner();
    const sim = laid(nodes);
    const before = dist(sim.at("OUT")!, sim.at("SM")!);
    const linked = nodes.map((n) => (n.id === "OUT" ? { ...n, links: ["SM"] } : n));
    sim.sync(linked);
    expect(sim.asleep()).toBe(false);
    sim.settle();
    expect(sim.asleep()).toBe(true);
    const after = dist(sim.at("OUT")!, sim.at("SM")!);
    expect(after).toBeLessThan(before * 0.8);
    expect(gap(sim, linked.find((n) => n.id === "OUT")!, linked.find((n) => n.id === "SM")!)).toBeLessThan(120);
    expect(overlaps(sim, linked)).toEqual([]);
  });

  it("gives a new node room near what it links to, or on the disc's free space", () => {
    const nodes = owner();
    const sim = laid(nodes);
    const more = [...nodes, node("NEW", "Une nouvelle vue", { links: ["SI"] })];
    sim.sync(more);
    // born beside Suivre mes investissements, on nothing
    expect(gap(sim, more.at(-1)!, more.find((n) => n.id === "SI")!)).toBeLessThan(150);
    expect(overlaps(sim, more)).toEqual([]);
    sim.settle();
    expect(overlaps(sim, more)).toEqual([]);
    const lone = [...more, node("LON", "Seul")];
    sim.sync(lone);
    expect(overlaps(sim, lone)).toEqual([]);
    expect(Math.hypot(...sim.at("LON")!)).toBeLessThan(700);
  });

  describe("pages", () => {
    it("opens a page as a node: no overlap, the graph adapts, and comes back when it closes", () => {
      for (const nodes of [small(), owner(), big()]) {
        const sim = laid(nodes);
        const id = nodes[1].id;
        const opened = open(nodes, [id]);
        sim.sync(opened);
        sim.settle();
        expect(overlaps(sim, opened)).toEqual([]);
        sim.sync(nodes);
        sim.settle();
        expect(overlaps(sim, nodes)).toEqual([]);
        const p = sim.positions();
        expect(Math.max(...nodes.map((n) => Math.hypot(...p[n.id])))).toBeLessThan(nodes.length > 10 ? 900 : 450);
      }
    });

    it("sets two linked open pages side by side, touching", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const opened = open(nodes, ["SJ", "MJ"]);
      sim.sync(opened);
      sim.settle();
      expect(overlaps(sim, opened)).toEqual([]);
      const a = opened.find((n) => n.id === "SJ")!, b = opened.find((n) => n.id === "MJ")!;
      expect(gap(sim, a, b)).toBeLessThan(FORCES.distance + 10);
      // a subject open beside its closed view: the view is by its page
      const one = open(nodes, ["SM"]);
      sim.sync(one);
      sim.settle();
      expect(gap(sim, one.find((n) => n.id === "SM")!, one.find((n) => n.id === "MM")!)).toBeLessThan(FORCES.distance + 10);
    });

    it("pulls linked pages closer to each other than to the others", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const all = open(nodes, ["SJ", "MJ", "SM", "MM", "SI", "MP"]);
      sim.sync(all);
      sim.settle();
      expect(overlaps(sim, all)).toEqual([]);
      const { linked, unlinked } = linkedVsNot(sim, all);
      expect(linked).toBeLessThan(unlinked);
    });

    it("gives a tile its rectangle, the others stepping aside, and back", () => {
      const nodes = small();
      const sim = laid(nodes);
      const tiled = nodes.map((n) => (n.id === "PRO" || n.id === "MES" || n.id === "FAI" ? { ...n, r: 74, tile: [148, 60] as Pt } : n));
      sim.sync(tiled);
      sim.settle();
      expect(overlaps(sim, tiled)).toEqual([]);
      sim.sync(nodes);
      sim.settle();
      expect(overlaps(sim, nodes)).toEqual([]);
    });
  });

  describe("a drag", () => {
    it("carries the held body's cluster, and on drop lets it settle on the side it was put, linked, within the disc", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const p0 = sim.positions();
      // to the other side of the graph, far out
      const side = p0.SJ[0] > 0 ? -1 : 1;
      const to: Pt = [side * 900, p0.SJ[1]];
      const { mid } = drag(sim, "SJ", to);
      // its view came along: the cluster stayed formed while held
      expect(dist(mid.MJ, mid.SJ)).toBeLessThan(dist(p0.MJ, p0.SJ) + 60);
      // dropped: nothing pinned, the forces bring it back into the disc,
      // on the side it was put, its view beside it
      const p = sim.positions();
      expect(sim.asleep()).toBe(true);
      expect(Math.sign(p.SJ[0])).toBe(side);
      expect(Math.sign(p.MJ[0])).toBe(side);
      const sj = nodes.find((n) => n.id === "SJ")!, mj = nodes.find((n) => n.id === "MJ")!;
      expect(gap(sim, sj, mj)).toBeLessThan(FORCES.distance + 40);
      expect(Math.max(...nodes.map((n) => Math.hypot(...p[n.id])))).toBeLessThan(450);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("brings two linked nodes dragged apart back together", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const p0 = sim.positions();
      drag(sim, "SI", [p0.SI[0] - 500, p0.SI[1]]);
      drag(sim, "MP", [p0.MP[0] + 500, p0.MP[1]]);
      const si = nodes.find((n) => n.id === "SI")!, mp = nodes.find((n) => n.id === "MP")!;
      expect(gap(sim, si, mp)).toBeLessThan(FORCES.distance + 40);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("keeps a subject's bubbles round it while it is dragged across a big graph", () => {
      const nodes = big();
      const sim = laid(nodes);
      const p0 = sim.positions();
      const { mid } = drag(sim, "s4", [-p0.s4[0] * 1.5, -p0.s4[1] * 1.5]);
      for (let j = 0; j < 3; j++) expect(dist(mid[`s4c${j}`], mid.s4), `s4c${j}`).toBeLessThan(dist(p0[`s4c${j}`], p0.s4) + 80);
      expect(overlaps(sim, nodes)).toEqual([]);
    });
  });

  describe("the node in focus as the centre", () => {
    const size = { w: 1440, h: 900 };
    // view is the camera on the page in focus (as Graph.tsx frames it),
    // and that page's box.
    const view = (sim: Sim, nodes: LNode[], id: string) => {
      const f = nodes.find((n) => n.id === id)!;
      const page = boxAt(sim.at(id)!, f.box!);
      return { cam: frameAround(page, size), page };
    };
    // centred tells whether a box's middle is the screen's, left to right.
    const centred = (cam: { k: number; x: number }, [l, , r]: number[]) => Math.abs(cam.x + ((l + r) / 2) * cam.k - size.w / 2) < 1;
    // whole tells whether a box is whole on screen under the camera.
    const whole = (cam: { k: number; x: number; y: number }, [l, t, r, b]: number[]) =>
      cam.x + l * cam.k >= 0 && cam.x + r * cam.k <= size.w && cam.y + t * cam.k >= 0 && cam.y + b * cam.k <= size.h;
    // sides are the sides of the page in focus the other bodies stand on.
    const sides = (sim: Sim, nodes: LNode[], id: string) => {
      const p = sim.positions(), f = nodes.find((n) => n.id === id)!;
      const [l, t, r, b] = boxAt(p[id], f.box!);
      const out = new Set<string>();
      for (const n of nodes) {
        if (n.id === id) continue;
        const [x, y] = p[n.id];
        out.add(y < t ? "above" : y > b ? "below" : x < l ? "left" : x > r ? "right" : "on");
      }
      return out;
    };

    it("holds the focused page where it is, the others all round it, its neighbours closest, framed readable", () => {
      for (const id of ["MM", "MJ"]) {
        const nodes = owner();
        const sim = laid(nodes);
        const opened = open(nodes, [id]);
        sim.sync(opened);
        sim.focus(id);
        const at = sim.at(id)!;
        sim.settle();
        expect(sim.focused()).toBe(id);
        // it stayed put: the graph came round it
        expect(dist(sim.at(id)!, at)).toBeLessThan(0.01);
        const p = sim.positions();
        const c: Pt = [opened.reduce((s, n) => s + p[n.id][0], 0) / opened.length, opened.reduce((s, n) => s + p[n.id][1], 0) / opened.length];
        // the graph's middle is on its page
        expect(Math.abs(c[0] - at[0])).toBeLessThan(350);
        expect(Math.abs(c[1] - at[1])).toBeLessThan(260);
        // its neighbour is closer than the others
        const ps = pairs(opened);
        const kin = ps.flatMap(([a, b]) => (a === id ? [b] : b === id ? [a] : []));
        const me = opened.find((n) => n.id === id)!;
        const others = opened.filter((n) => n.id !== id && !kin.includes(n.id)).map((n) => gap(sim, me, n)).sort((a, b) => a - b);
        for (const k of kin) {
          const g = gap(sim, me, opened.find((n) => n.id === k)!);
          expect(g).toBeLessThan(FORCES.distance + 40);
          expect(g).toBeLessThanOrEqual(others[others.length >> 1] + 1e-6);
        }
        expect(overlaps(sim, opened)).toEqual([]);
        // the others stand all round it, not on one side
        const round = sides(sim, opened, id);
        expect(round.has("on")).toBe(false);
        expect(round.size, [...round].join()).toBeGreaterThanOrEqual(3);
        // the camera frames it whole and readable
        const { cam, page } = view(sim, opened, id);
        expect(cam.k).toBeGreaterThanOrEqual(FLOOR);
        expect(whole(cam, page)).toBe(true);
      }
    });

    it("keeps pages readable on a big graph with two pages open: no zoom below the floor, the focused page whole", () => {
      const nodes = big();
      const sim = laid(nodes);
      const opened = open(nodes, ["s3", "s2"]);
      sim.sync(opened);
      sim.focus("s3");
      sim.settle();
      for (const id of ["s3", "s2"]) {
        sim.focus(id);
        sim.settle();
        const { cam, page } = view(sim, opened, id);
        expect(cam.k).toBeGreaterThanOrEqual(FLOOR);
        expect(whole(cam, page)).toBe(true);
        expect(centred(cam, page)).toBe(true);
        expect(overlaps(sim, opened)).toEqual([]);
      }
    });

    // The real case: a page open in the middle with its own bubbles, a
    // subject above it (linked to the page or not) and the subject's bubble
    // below the page, on its far side, its other bubbles by it; a subject
    // alone.
    const entourage = (linked: boolean): LNode[] =>
      open(
        [
          node("ENT", "Mon entourage"),
          node("E1", "Anniversaires", { parent: "ENT" }),
          node("E2", "Appeler Sam", { parent: "ENT" }),
          node("E3", "Idées de cadeaux", { parent: "ENT" }),
          node("TAC", "Mes tâches", { links: linked ? ["ENT"] : [] }),
          node("MAJ", "Mettre à jour Mes tâches", { parent: "TAC" }),
          node("T1", "Ranger le garage", { parent: "TAC" }),
          node("T2", "Payer les factures", { parent: "TAC" }),
          node("OUT", "Outils à revoir"),
        ],
        ["ENT"],
      );
    // put places a body by hand, as a drag would leave it, its cluster
    // staying where it is.
    const put = (sim: Sim, id: string, p: Pt) => {
      const b = (sim as unknown as { byId: Map<string, { x: number; y: number }> }).byId.get(id)!;
      [b.x, b.y] = p;
    };
    // side is the side of the page box a body stands on.
    const side = (p: Pos, [l, t, r, b]: number[], id: string) => {
      const [x, y] = p[id];
      return y < t ? "above" : y > b ? "below" : x < l ? "left" : x > r ? "right" : "on";
    };
    // under tells whether a point is right under the box, within its width.
    const under = ([x, y]: Pt, [l, , r, b]: number[]) => y > b && x > l && x < r;
    // crosses tells whether the segment from a to b goes through the box.
    const crosses = (a: Pt, b: Pt, [l, t, r, bt]: number[]) => {
      for (let i = 0; i <= 400; i++) {
        const x = a[0] + ((b[0] - a[0]) * i) / 400, y = a[1] + ((b[1] - a[1]) * i) / 400;
        if (x > l && x < r && y > t && y < bt) return true;
      }
      return false;
    };

    it("brings a bubble left on the far side of the page round it, back by what it hangs on", () => {
      // left there as the page comes into focus (spreadRound and the
      // springs), or once it is in focus (the springs alone)
      for (const [linked, refocus] of [
        [false, true],
        [true, true],
        [false, false],
        [true, false],
      ]) {
        const nodes = entourage(linked);
        const sim = laid(nodes);
        sim.focus("ENT");
        sim.settle();
        const at = sim.at("ENT")!;
        put(sim, "TAC", [at[0], at[1] - 420]);
        put(sim, "T1", [at[0] - 120, at[1] - 470]);
        put(sim, "T2", [at[0] + 120, at[1] - 470]);
        put(sim, "MAJ", [at[0], at[1] + 420]);
        if (refocus) {
          sim.focus(null);
          sim.focus("ENT");
        } else sim.reheat();
        sim.settle();
        const p = sim.positions(), page = boxAt(p.ENT, nodes[0].box!);
        // neither under the page any more (the subject may have moved
        // round it, the spread or its bubble's pull), the link between
        // them clear of the page: the bubble came round, by its subject
        const why = `linked: ${linked}, refocus: ${refocus}`;
        expect(under(p.TAC, page), why).toBe(false);
        expect(under(p.MAJ, page), why).toBe(false);
        expect(crosses(p.MAJ, p.TAC, page), why).toBe(false);
        expect(overlaps(sim, nodes)).toEqual([]);
      }
    });

    it("still spreads the page's own bubbles all round it", () => {
      const nodes = entourage(false);
      const sim = laid(nodes);
      sim.focus("ENT");
      sim.settle();
      const p = sim.positions(), page = boxAt(p.ENT, nodes[0].box!);
      const own = new Set(["E1", "E2", "E3"].map((id) => side(p, page, id)));
      expect(own.has("on")).toBe(false);
      expect(own.size, [...own].join()).toBeGreaterThanOrEqual(2);
      expect(sides(sim, nodes, "ENT").size).toBeGreaterThanOrEqual(3);
      expect(crosses(p.MAJ, p.TAC, page)).toBe(false);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("glides to a new focus: nothing jumps, the new one stays, and none at all keeps the graph about where it is", () => {
      const nodes = open(owner(), ["MM", "SM"]);
      const sim = laid(nodes);
      sim.focus("MM");
      sim.settle();
      const before = sim.positions();
      sim.focus("SM");
      const at = sim.at("SM")!;
      sim.tick();
      expect(moved(before, sim.positions(), nodes.map((n) => n.id))).toBeLessThan(T.cap + 1);
      sim.settle();
      expect(dist(sim.at("SM")!, at)).toBeLessThan(0.01);
      // no focus: the centre stays where the last one was, the graph
      // settles round it as a disc, nothing drifts away
      sim.focus(null);
      sim.settle();
      const p = sim.positions();
      expect(Math.max(...nodes.map((n) => dist(p[n.id], at)))).toBeLessThan(800);
      expect(overlaps(sim, nodes)).toEqual([]);
    });
  });

  it("warms the graph when the forces change, and rests on the new ones", () => {
    const nodes = owner();
    const sim = new Sim(FORCES);
    sim.sync(nodes);
    const { linked: before } = linkedVsNot(sim, nodes);
    try {
      setForces({ distance: 160 });
      expect(sim.asleep()).toBe(false);
      sim.settle();
      expect(sim.asleep()).toBe(true);
      expect(linkedVsNot(sim, nodes).linked).toBeGreaterThan(before + 40);
    } finally {
      setForces(DEFAULT_FORCES);
    }
  });

  it("scrolls at the edge after a wait, speeding up gently", () => {
    expect(edgePush(500, 0, 1000)).toBe(0);
    expect(edgePush(0, 0, 1000)).toBe(-1);
    expect(edgePush(1000 - EDGE_ZONE / 2, 0, 1000)).toBeCloseTo(0.5);
    expect(edgeStep(-1, 1000, 1000 + 1 - EDGE_DELAY)).toBe(0);
    expect(edgeStep(-1, 1000 + EDGE_DELAY + EDGE_RAMP, 1000)).toBe(EDGE_SPEED);
    expect(edgeStep(1, 1000 + EDGE_DELAY + EDGE_RAMP / 2, 1000)).toBeCloseTo(-EDGE_SPEED / 2);
  });

  it("ticks some forty nodes fast enough for every frame", () => {
    const nodes = big();
    const sim = laid(nodes);
    const t0 = performance.now();
    const at = sim.at("s3")!;
    for (let i = 0; i < 120; i++) {
      sim.hold("s3", [at[0] + i * 3, at[1]]);
      sim.tick();
    }
    const per = (performance.now() - t0) / 120;
    expect(per).toBeLessThan(8);
  });
});

describe("thousands of nodes", () => {
  // charges are n bodies at random places, the same for a seed.
  const charges = (n: number, seed: number) => {
    const r = rng(seed);
    return Array.from({ length: n }, (_, i) => ({ id: `b${i}`, x: (r() - 0.5) * 3000, y: (r() - 0.5) * 2000, vx: 0, vy: 0 }));
  };
  // gap is how far the tree's pushes are from every pair's, over their size.
  const gap = (theta: number, seed: number) => {
    const exact = charges(500, seed), tree = charges(500, seed);
    repelPairs(exact, 1000);
    new Quad().repel(tree, 1000, theta);
    let err = 0, size = 0;
    for (let i = 0; i < exact.length; i++) {
      err += (exact[i].vx - tree[i].vx) ** 2 + (exact[i].vy - tree[i].vy) ** 2;
      size += exact[i].vx ** 2 + exact[i].vy ** 2;
    }
    return Math.sqrt(err / size);
  };

  it("pushes as every pair does: the same with theta 0, near enough with T.theta", () => {
    for (const seed of [1, 2, 3]) {
      expect(gap(0, seed)).toBeLessThan(1e-12);
      expect(gap(T.theta, seed)).toBeLessThan(0.03);
    }
  });

  it("parts two bodies on one spot, as every pair does", () => {
    const exact = [
      { id: "a", x: 5, y: 5, vx: 0, vy: 0 },
      { id: "b", x: 5, y: 5, vx: 0, vy: 0 },
      { id: "c", x: 400, y: 0, vx: 0, vy: 0 },
    ];
    const tree = exact.map((b) => ({ ...b }));
    repelPairs(exact, 1000);
    new Quad().repel(tree, 1000, 0);
    for (let i = 0; i < exact.length; i++) {
      expect(tree[i].vx).toBeCloseTo(exact[i].vx, 9);
      expect(tree[i].vy).toBeCloseTo(exact[i].vy, 9);
    }
    expect(exact[0].vx).not.toBe(exact[1].vx);
  });

  it("lays 500 nodes out, pages and glances among them, nothing on anything, and rests", () => {
    for (const seed of [1, 2]) {
      const nodes = graph(500, seed);
      expect(nodes.some((n) => n.box) && nodes.some((n) => n.tile)).toBe(true);
      const sim = laid(nodes);
      expect(sim.asleep()).toBe(true);
      expect(overlaps(sim, nodes)).toEqual([]);
      expect(still(sim, nodes, 20)).toBe(0);
      const { linked, unlinked } = linkedVsNot(sim, nodes);
      expect(linked).toBeLessThan(unlinked * 0.25);
    }
  });

  it("is the same graph laid out twice, at once or a slice a frame", () => {
    const nodes = graph(500);
    const once = laid(nodes);
    expect(laid(nodes).positions()).toEqual(once.positions());
    const sliced = new Sim({ ...DEFAULT_FORCES }, { slice: 0 });
    sliced.sync(nodes);
    expect(sliced.asleep()).toBe(false);
    expect(sliced.hurrying()).toBe(true);
    let frames = 0;
    while (!sliced.asleep() && frames < 5000) {
      sliced.frame();
      frames++;
    }
    expect(frames).toBeGreaterThan(100);
    expect(sliced.hurrying()).toBe(false);
    expect(sliced.positions()).toEqual(once.positions());
  });

  it("lays the owner's graph out where it always did", () => {
    const want: Pos = { SJ: [-11.35, -265.62], MJ: [21.6, -129.62], SM: [-238.47, -31.82], MM: [-207.76, 104.52], SI: [105.5, 139.94], MP: [58.23, 275.94], OUT: [272.26, -63.34] };
    const p = laid(owner()).positions();
    for (const [id, [x, y]] of Object.entries(want)) {
      expect(p[id][0], id).toBeCloseTo(x, 1);
      expect(p[id][1], id).toBeCloseTo(y, 1);
    }
  });
});
