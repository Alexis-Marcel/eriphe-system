import { describe, expect, it } from "vitest";
import { bornBeside, EDGE_DELAY, EDGE_RAMP, EDGE_SPEED, EDGE_ZONE, edgePush, edgeStep, extent, overlap, REACH, RIM, Sim, TETHER, type LNode, type Pos, type Pt } from "./layout";

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

function laid(nodes: LNode[]): Sim {
  const sim = new Sim();
  sim.sync(nodes);
  return sim;
}

// overlaps are the pairs of bodies that sit on each other.
function overlaps(sim: Sim, nodes: LNode[]): string[] {
  const p = sim.positions(), out: string[] = [];
  for (let i = 0; i < nodes.length; i++)
    for (let j = i + 1; j < nodes.length; j++) if (overlap(nodes[i], p[nodes[i].id], nodes[j], p[nodes[j].id])) out.push(`${nodes[i].id}/${nodes[j].id}`);
  return out;
}

// onLinks are the bubbles that sit on a link they are not part of.
function onLinks(sim: Sim, nodes: LNode[]): string[] {
  const p = sim.positions(), out: string[] = [];
  const ids = new Set(nodes.map((n) => n.id));
  for (const a of nodes)
    for (const b of [...(a.parent ? [a.parent] : []), ...a.links]) {
      if (!ids.has(b) || a.box) continue;
      const [ax, ay] = p[a.id], [bx, by] = p[b], ex = bx - ax, ey = by - ay, L2 = ex * ex + ey * ey;
      for (const c of nodes) {
        if (c.id === a.id || c.id === b || c.box) continue;
        const t = ((p[c.id][0] - ax) * ex + (p[c.id][1] - ay) * ey) / L2;
        if (t <= 0 || t >= 1) continue;
        if (Math.hypot(p[c.id][0] - ax - ex * t, p[c.id][1] - ay - ey * t) < c.r) out.push(`${c.id} on ${a.id}-${b}`);
      }
    }
  return out;
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
  for (let i = 0; i < 15; i++) sim.tick();
  sim.drop();
  sim.settle();
  return { mid };
}

const moved = (a: Pos, b: Pos, ids: string[]) => Math.max(0, ...ids.map((id) => dist(a[id], b[id])));

describe("the living graph", () => {
  it("lays the graph out as a compact disc: nothing on anything, no bubble on a link not its own", () => {
    for (const nodes of [small(), big()]) {
      const sim = laid(nodes);
      expect(sim.asleep()).toBe(true);
      expect(overlaps(sim, nodes)).toEqual([]);
      expect(onLinks(sim, nodes)).toEqual([]);
      const p = sim.positions();
      const far = Math.max(...nodes.map((n) => Math.hypot(...p[n.id])));
      expect(far).toBeLessThan(nodes.length > 10 ? 900 : 400);
    }
  });

  it("keeps a bubble close to its parent and linked subjects near each other", () => {
    const p = laid(small()).positions();
    expect(dist(p.UNE, p.ETU)).toBeGreaterThan(70);
    expect(dist(p.UNE, p.ETU)).toBeLessThan(130);
    expect(dist(p.PRO, p.FAI)).toBeLessThan(130);
    expect(dist(p.MES, p.UNE)).toBeGreaterThan(120);
    expect(dist(p.MES, p.UNE)).toBeLessThan(260);
    const b = laid(big()).positions();
    for (let i = 0; i < 9; i++) for (let j = 0; j < 3; j++) expect(dist(b[`s${i}`], b[`s${i}c${j}`])).toBeLessThan(150);
    expect(dist(b.s3, b.s2)).toBeLessThan(300);
  });

  it("leaves the subjects linked to nothing on the rim", () => {
    const nodes = big();
    const p = laid(nodes).positions();
    const r = (id: string) => Math.hypot(...p[id]);
    const busy = ["s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"].map(r).sort((a, b) => a - b);
    const median = busy[busy.length >> 1];
    for (let i = 0; i < 4; i++) expect(r(`lone${i}`), `lone${i}`).toBeGreaterThan(median);
  });

  it("is the same graph laid out twice, and does not drift as the graph comes again and again", () => {
    expect(laid(big()).positions()).toEqual(laid(big()).positions());
    const nodes = big();
    const sim = laid(nodes);
    const before = sim.positions();
    for (let i = 0; i < 5; i++) {
      sim.sync(nodes.map((n) => ({ ...n, lw: n.lw + (i % 2) })));
      sim.settle();
    }
    sim.sync(nodes);
    sim.settle();
    expect(moved(before, sim.positions(), nodes.map((n) => n.id))).toBeLessThan(2);
  });

  describe("a drag", () => {
    it("lets the body held pass over everything, carries what is linked to it by hops, moves nothing else", () => {
      const nodes = big();
      const sim = laid(nodes);
      const before = sim.positions();
      // s3 is linked to s2; s2's bubbles are two links away; s1, three
      const to: Pt = [before.s3[0] + 300, before.s3[1] + 120];
      const from = before.s3;
      for (let i = 1; i <= 40; i++) {
        sim.hold("s3", [from[0] + (to[0] - from[0]) * (i / 40), from[1] + (to[1] - from[1]) * (i / 40)]);
        sim.tick();
      }
      const mid = sim.positions();
      expect(dist(mid.s3, to)).toBeLessThan(1);
      const near = ["s3c0", "s3c1", "s3c2", "s2"], second = ["s2c0", "s2c1", "s2c2"];
      const unrelated = nodes.map((n) => n.id).filter((id) => id !== "s3" && !near.includes(id) && !second.includes(id));
      expect(moved(before, mid, unrelated)).toBeLessThan(0.01);
      for (const id of near) expect(dist(before[id], mid[id]), id).toBeGreaterThan(60);
      const one = near.reduce((s, id) => s + dist(before[id], mid[id]), 0) / near.length;
      const two = second.reduce((s, id) => s + dist(before[id], mid[id]), 0) / second.length;
      expect(two).toBeGreaterThan(1);
      expect(two).toBeLessThan(one);
    });

    it("drops a bubble exactly where it is let go, and what was there steps aside", () => {
      const nodes = small();
      const sim = laid(nodes);
      const p0 = sim.positions();
      const onto: Pt = [p0.MES[0] + 15, p0.MES[1]];
      const { mid } = drag(sim, "MAJ", onto);
      // held over Mes mails, which stayed still
      expect(dist(mid.MES, p0.MES)).toBeLessThan(0.01);
      const p = sim.positions();
      expect(dist(p.MAJ, onto)).toBeLessThan(1);
      expect(dist(p.MES, p0.MES)).toBeGreaterThan(20);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("lets the followers of a drop come round what is in their way: no long line", () => {
      const nodes = big();
      const sim = laid(nodes);
      const p0 = sim.positions();
      // s4 dragged across the graph, to the other side of s0
      const to: Pt = [-p0.s4[0] * 0.9, -p0.s4[1] * 0.9];
      drag(sim, "s4", to);
      const p = sim.positions();
      expect(dist(p.s4, to)).toBeLessThan(1);
      for (let j = 0; j < 3; j++) expect(dist(p.s4, p[`s4c${j}`]), `s4c${j}`).toBeLessThan(220);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("carries a dragged bubble only within reach of its parent", () => {
      const sim = laid(small());
      const p0 = sim.positions();
      const far: Pt = [p0.PRO[0] + 900, p0.PRO[1]];
      sim.hold("FAI", far);
      sim.tick();
      expect(dist(sim.at("FAI")!, p0.PRO)).toBeGreaterThan(TETHER);
      expect(dist(sim.at("FAI")!, far)).toBeGreaterThan(300);
      sim.drop();
      sim.settle();
      expect(dist(sim.at("FAI")!, sim.at("PRO")!)).toBeLessThan(TETHER + 1);
    });

    it("lets a subject dragged out in the void lag behind, and brings it back to the disc's edge when dropped", () => {
      const nodes = big();
      const sim = laid(nodes);
      const p0 = sim.positions();
      const R = Math.max(...nodes.filter((n) => !n.parent && n.id !== "lone0").map((n) => Math.hypot(...p0[n.id]) + extent(n, p0[n.id])[0]));
      const far: Pt = [p0.lone0[0] * 8, p0.lone0[1] * 8];
      sim.hold("lone0", far);
      sim.tick();
      const held = sim.at("lone0")!;
      expect(dist(held, far)).toBeGreaterThan(500);
      expect(Math.hypot(...held)).toBeGreaterThan(R + REACH - 80);
      sim.drop();
      sim.settle();
      const back = Math.hypot(...sim.at("lone0")!);
      expect(back).toBeLessThan(R + RIM + 120);
      expect(back).toBeGreaterThan(R * 0.6);
      expect(overlaps(sim, nodes)).toEqual([]);
    });
  });

  describe("what the user arranged holds for the session", () => {
    it("keeps a dropped subject in place when a subject comes, linked or not, or a bubble", () => {
      const nodes = small();
      const sim = laid(nodes);
      const p0 = sim.positions();
      const spot: Pt = [p0.MES[0] - 260, p0.MES[1] - 40];
      drag(sim, "MES", spot);
      const after = [...nodes];
      after.push(node("N1", "Sujet lié", { links: ["MES"] }));
      sim.sync(after);
      sim.settle();
      after.push(node("N2", "Nouveau sujet"), node("N3", "Sous-sujet", { parent: "MES" }), node("N4", "Encore un"));
      sim.sync(after);
      sim.settle();
      expect(dist(sim.at("MES")!, spot)).toBeLessThan(60);
      expect(overlaps(sim, after)).toEqual([]);
      expect(dist(sim.at("N1")!, sim.at("MES")!)).toBeLessThan(300);
    });

    it("keeps a dropped subject in place as pages open and close", () => {
      const nodes = small();
      const sim = laid(nodes);
      const p0 = sim.positions();
      const spot: Pt = [p0.MES[0] - 200, p0.MES[1] + 30];
      drag(sim, "MES", spot);
      const box: Pt = [700, 520];
      sim.sync(nodes.map((n) => (n.id === "PRO" ? { ...n, box } : n)));
      sim.settle();
      sim.sync(nodes.map((n) => (n.id === "PRO" || n.id === "MAJ" ? { ...n, box: [620, 480] as Pt } : n)), "PRO");
      sim.settle();
      sim.sync(nodes);
      sim.settle();
      sim.sync(nodes.map((n) => (n.id === "MES" ? { ...n, box } : n)));
      sim.settle();
      sim.sync(nodes);
      sim.settle();
      expect(dist(sim.at("MES")!, spot)).toBeLessThan(60);
      expect(overlaps(sim, nodes)).toEqual([]);
    });
  });

  describe("pages", () => {
    it("makes room for an open page, and gives the bubbles it pushed their place back when it closes", () => {
      const nodes = big();
      const sim = laid(nodes);
      const p0 = sim.positions();
      const opened = nodes.map((n) => (n.id === "s0" ? { ...n, box: [700, 520] as Pt } : n));
      sim.sync(opened);
      sim.settle();
      expect(overlaps(sim, opened)).toEqual([]);
      const p1 = sim.positions();
      // the page stays where its bubble was; far bubbles do not move
      expect(dist(p1.s0, p0.s0)).toBeLessThan(30);
      const pushed = nodes.filter((n) => dist(p0[n.id], p1[n.id]) > 20 && n.parent !== "s0" && n.id !== "s0").map((n) => n.id);
      expect(pushed.length).toBeGreaterThan(0);
      sim.sync(nodes);
      sim.settle();
      const p2 = sim.positions();
      for (const id of pushed) expect(dist(p2[id], p0[id]), id).toBeLessThan(20);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("opens a page that would cover the one open right beside it, at its height", () => {
      const nodes = small();
      const sim = laid(nodes);
      const one = nodes.map((n) => (n.id === "MES" ? { ...n, box: [620, 480] as Pt } : n));
      sim.sync(one);
      sim.settle();
      const two = one.map((n) => (n.id === "UNE" ? { ...n, box: [620, 480] as Pt } : n));
      sim.sync(two, "MES");
      sim.settle();
      const p = sim.positions();
      expect(overlaps(sim, two)).toEqual([]);
      expect(Math.abs(p.UNE[1] - p.MES[1])).toBeLessThan(40);
      expect(bornBeside([20, 0], [600, 400], [10, 0], [600, 400])).toEqual([658, 0]);
      expect(bornBeside([-50, 0], [600, 400], [10, 0], [600, 400])).toEqual([-638, 0]);
      expect(bornBeside([2000, 0], [600, 400], [10, 0], [600, 400])).toEqual([2000, 0]);
    });

    it("warms only the page's own bubbles when it opens: the others move only to make room", () => {
      const nodes = big();
      const sim = laid(nodes);
      const p0 = sim.positions();
      sim.sync(nodes.map((n) => (n.id === "lone1" ? { ...n, box: [300, 200] as Pt } : n)));
      sim.settle();
      const p1 = sim.positions();
      const far = nodes.filter((n) => n.id !== "lone1" && dist(p0[n.id], p0.lone1) > 700).map((n) => n.id);
      expect(far.length).toBeGreaterThan(5);
      expect(moved(p0, p1, far)).toBeLessThan(2);
    });

    it("lets a page dropped on another win, and moves the other by the smallest move", () => {
      const nodes = small();
      const sim = laid(nodes);
      const box: Pt = [620, 480];
      const two = nodes.map((n) => (n.id === "MES" || n.id === "PRO" ? { ...n, box } : n));
      sim.sync(two.map((n) => (n.id === "PRO" ? { ...n, box: undefined } : n)));
      sim.settle();
      sim.sync(two, "MES");
      sim.settle();
      const mes = sim.at("MES")!;
      const onto: Pt = [mes[0] + 620 * 0.35, mes[1]];
      drag(sim, "PRO", onto);
      expect(dist(sim.at("PRO")!, onto)).toBeLessThan(1);
      expect(overlaps(sim, two)).toEqual([]);
      // pushed off sideways: the least it takes, not more
      const now = sim.at("MES")!;
      expect(Math.abs(now[1] - mes[1])).toBeLessThan(5);
      expect(Math.abs(now[0] - onto[0])).toBeLessThan(620 + 48 + 20);
    });
  });

  it("scrolls at the edge after a wait, speeding up gently", () => {
    expect(edgePush(500, 0, 1000)).toBe(0);
    expect(edgePush(0, 0, 1000)).toBe(-1);
    expect(edgePush(1000 - EDGE_ZONE / 2, 0, 1000)).toBeCloseTo(0.5);
    expect(edgeStep(-1, 1000, 1000 + 1 - EDGE_DELAY)).toBe(0);
    expect(edgeStep(-1, 1000 + EDGE_DELAY + EDGE_RAMP, 1000)).toBe(EDGE_SPEED);
    expect(edgeStep(1, 1000 + EDGE_DELAY + EDGE_RAMP / 2, 1000)).toBeCloseTo(-EDGE_SPEED / 2);
  });

  it("settles some forty nodes fast enough to tick every frame", () => {
    const nodes = big();
    const sim = laid(nodes);
    const t0 = performance.now();
    const at = sim.at("s3")!;
    for (let i = 0; i < 120; i++) {
      sim.hold("s3", [at[0] + i * 3, at[1]]);
      sim.tick();
    }
    const per = (performance.now() - t0) / 120;
    expect(per).toBeLessThan(4);
  });
});
