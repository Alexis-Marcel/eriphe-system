import { describe, expect, it } from "vitest";
import { bornBeside, EDGE_DELAY, EDGE_RAMP, EDGE_SPEED, EDGE_ZONE, edgePush, edgeStep, extent, REACH, RIM, Sim, T, TETHER, type LNode, type Pos, type Pt } from "./layout";

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

// The owner's graph: the prototype's, and a few more subjects alone.
function owner(): LNode[] {
  return [
    ...small(),
    node("OUT", "Outils à revoir"),
    node("SUB", "Substack"),
    node("PLA", "Plan du 1er article", { parent: "SUB" }),
    node("FAC", "Factures du mois"),
    node("VOY", "Voyage d'été"),
  ];
}

// open gives the graph with those pages open.
const open = (nodes: LNode[], ids: string[], box: Pt = ids.length > 1 ? [620, 480] : [700, 520]) => nodes.map((n) => (ids.includes(n.id) ? { ...n, box } : n));

// reach is how far the graph goes from its centre: the farthest edge of a body.
function reach(sim: Sim, nodes: LNode[]): number {
  const p = sim.positions();
  return Math.max(...nodes.map((n) => Math.hypot(...p[n.id]) + extent(n, p[n.id])[0]));
}

// amid tells whether a point is among the graph's bubbles (the open pages
// left out): within the box they span.
function amid(sim: Sim, nodes: LNode[], [x, y]: Pt): boolean {
  const p = sim.positions(), bs = nodes.filter((n) => !n.box).map((n) => p[n.id]);
  const xs = bs.map((q) => q[0]), ys = bs.map((q) => q[1]);
  return x > Math.min(...xs) && x < Math.max(...xs) && y > Math.min(...ys) && y < Math.max(...ys);
}

// stragglers are the bodies farther than gap from every other one (edge to edge).
function stragglers(sim: Sim, nodes: LNode[], gap: number): string[] {
  const p = sim.positions();
  const box = (n: LNode) => {
    const [h, t, b] = extent(n, p[n.id]);
    return [p[n.id][0] - h, t, p[n.id][0] + h, b];
  };
  const apart = (a: LNode, b: LNode) => {
    const [l1, t1, r1, b1] = box(a), [l2, t2, r2, b2] = box(b);
    return Math.hypot(Math.max(0, l2 - r1, l1 - r2), Math.max(0, t2 - b1, t1 - b2));
  };
  return nodes.filter((a) => nodes.every((b) => b === a || apart(a, b) > gap)).map((a) => a.id);
}

// still is how far the bodies go in ticks more, once settled.
function still(sim: Sim, nodes: LNode[], ticks = 200): number {
  const p0 = sim.positions();
  for (let i = 0; i < ticks; i++) sim.tick();
  return moved(p0, sim.positions(), nodes.map((n) => n.id));
}

function laid(nodes: LNode[]): Sim {
  const sim = new Sim();
  sim.sync(nodes);
  return sim;
}

// overlaps are the pairs of bodies that sit on each other: more than the
// graph tolerates at rest (T.jam), well within their margins.
function overlaps(sim: Sim, nodes: LNode[]): string[] {
  const p = sim.positions(), out: string[] = [];
  const on = (a: LNode, pa: Pt, b: LNode, pb: Pt) => {
    const [ha, ta, ba] = extent(a, pa), [hb, tb, bb] = extent(b, pb);
    return ha + hb - Math.abs(pa[0] - pb[0]) > T.jam && Math.min(ba, bb) - Math.max(ta, tb) > T.jam;
  };
  for (let i = 0; i < nodes.length; i++)
    for (let j = i + 1; j < nodes.length; j++) if (on(nodes[i], p[nodes[i].id], nodes[j], p[nodes[j].id])) out.push(`${nodes[i].id}/${nodes[j].id}`);
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
    const ids = nodes.map((n) => n.id);
    const sim = laid(nodes);
    const before = sim.positions();
    const again = () => {
      for (let i = 0; i < 4; i++) {
        sim.sync(nodes.map((n) => ({ ...n, lw: n.lw + (i % 2) })));
        sim.settle();
      }
    };
    // the first changes let what touches settle under the ambient energy, a little
    again();
    const once = sim.positions();
    expect(moved(before, once, ids)).toBeLessThan(20);
    // then nothing wanders
    again();
    again();
    expect(moved(once, sim.positions(), ids)).toBeLessThan(5);
  });

  it("is still once settled: the ambient energy moves nothing at rest", () => {
    for (const nodes of [small(), owner(), big()]) {
      const sim = laid(nodes);
      expect(sim.asleep()).toBe(true);
      expect(still(sim, nodes)).toBeLessThan(0.5);
      sim.sync(open(nodes, [nodes[0].id]));
      sim.settle();
      expect(still(sim, nodes)).toBeLessThan(0.5);
    }
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
      // s4 dragged across the graph, to the other side of it; then s3
      // (linked to s2, whose bubbles are two links away)
      for (const id of ["s4", "s3"]) {
        const to: Pt = [-p0[id][0] * 1.3, -p0[id][1] * 1.3];
        drag(sim, id, to);
        expect(dist(sim.at(id)!, to)).toBeLessThan(1);
      }
      const p = sim.positions();
      for (const s of ["s4", "s3", "s2"]) for (let j = 0; j < 3; j++) expect(dist(p[s], p[`${s}c${j}`]), `${s}c${j}`).toBeLessThan(220);
      expect(dist(p.s3, p.s2)).toBeLessThan(320);
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
      expect(dist(sim.at("MES")!, spot)).toBeLessThan(5);
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
      expect(dist(sim.at("MES")!, spot)).toBeLessThan(5);
      expect(overlaps(sim, nodes)).toEqual([]);
    });

    it("keeps where the user put a subject beside another: dropped left of it, it stays left", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const mes = sim.at("MES")!;
      const spot: Pt = [mes[0] - 280, mes[1]];
      drag(sim, "PRO", spot);
      for (const ids of [["PRO"], ["PRO", "OUT"], [], ["MES"], []]) {
        sim.sync(open(nodes, ids), ids.length > 1 ? ids[0] : undefined);
        sim.settle();
        expect(dist(sim.at("PRO")!, spot), ids.join("+")).toBeLessThan(5);
      }
      const more = [...nodes, node("N1", "Un nouveau sujet"), node("N2", "Sujet lié", { links: ["PRO"] })];
      sim.sync(more);
      sim.settle();
      expect(dist(sim.at("PRO")!, spot)).toBeLessThan(5);
      expect(sim.at("PRO")![0]).toBeLessThan(sim.at("MES")![0]);
      expect(overlaps(sim, more)).toEqual([]);
    });

    it("lets a dropped subject the graph left far behind come back to it", () => {
      const nodes = owner();
      const sim = laid(nodes);
      // dropped at the disc's edge while a big page held the others far out
      sim.sync(open(nodes, ["MES"], [1400, 900]));
      sim.settle();
      const far = reach(sim, nodes);
      const p = sim.at("VOY")!, d = Math.hypot(...p);
      drag(sim, "VOY", [(p[0] / d) * far, (p[1] / d) * far]);
      sim.sync(nodes);
      sim.settle();
      const back = laid(nodes);
      expect(reach(sim, nodes)).toBeLessThan(reach(back, nodes) + 200);
    });

    it("keeps a dropped bubble by its subject: dropped on its label, it steps off, and the subject stays", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const pro = sim.at("PRO")!;
      drag(sim, "FAI", [pro[0] + 10, pro[1] + 50]);
      expect(sim.asleep()).toBe(true);
      expect(dist(sim.at("PRO")!, pro)).toBeLessThan(TETHER + 100);
      expect(overlaps(sim, nodes)).toEqual([]);
      // it stays by its subject as the subject moves, and as its page opens and closes
      const fai = sim.at("FAI")!, off = [fai[0] - sim.at("PRO")![0], fai[1] - sim.at("PRO")![1]];
      for (const ids of [["PRO"], [], ["MES"], []]) {
        const now = open(nodes, ids);
        sim.sync(now);
        sim.settle();
        expect(sim.asleep(), ids.join("+")).toBe(true);
        expect(overlaps(sim, now), ids.join("+")).toEqual([]);
        expect(dist(sim.at("FAI")!, sim.at("PRO")!), ids.join("+")).toBeLessThan(TETHER + 60 + (ids[0] === "PRO" ? 400 : 0));
      }
      const p = sim.at("PRO")!, f = sim.at("FAI")!;
      expect(dist([f[0] - p[0], f[1] - p[1]], off as Pt)).toBeLessThan(60);
    });
  });

  // Invariants on graphs of every shape: whatever the graph, and whatever
  // opens, closes or is dropped, it comes to rest soon, nothing sits on
  // anything, and it stays a compact disc.
  describe("on any graph", () => {
    const rnd = (seed: number) => () => ((seed = (Math.imul(seed, 1103515245) + 12345) >>> 0) / 4294967296);
    function any(seed: number, crowd = false): LNode[] {
      const r = rnd(seed), out: LNode[] = [];
      const subjects = (crowd ? 14 : 4) + Math.floor(r() * (crowd ? 10 : 8));
      for (let i = 0; i < subjects; i++) {
        const links = i > 0 && r() < 0.3 ? [`g${Math.floor(r() * i)}`] : [];
        out.push(node(`g${i}`, "Sujet ".repeat(1 + Math.floor(r() * 4)).trim() + ` ${i}`, { links }));
        const kids = Math.floor(r() * 4);
        for (let j = 0; j < kids; j++) out.push(node(`g${i}k${j}`, "Bulle ".repeat(1 + Math.floor(r() * 3)).trim() + ` ${j}`, { parent: `g${i}` }));
      }
      return out;
    }
    const rest = (sim: Sim, max = 1200) => {
      let t = 0;
      while (!sim.asleep() && t < max) {
        sim.tick();
        t++;
      }
      return t;
    };

    it("comes to rest soon, nothing on anything, as pages open and close and a subject is dropped", () => {
      for (let seed = 1; seed <= 24; seed++) {
        const nodes = any(seed);
        const sim = laid(nodes);
        const disc = reach(sim, nodes);
        const roots = nodes.filter((n) => !n.parent).map((n) => n.id);
        const [a, b, c] = [roots[seed % roots.length], roots[(seed + 1) % roots.length], roots[(seed + 2) % roots.length]];
        const steps: [string, LNode[], string?][] = [
          ["open", open(nodes, [a])],
          ["beside", open(nodes, [a, b]), a],
          ["close", nodes],
        ];
        for (const [what, now, beside] of steps) {
          sim.sync(now, beside);
          const t = rest(sim);
          expect(t, `${seed} ${what}`).toBeLessThan(600);
          expect(overlaps(sim, now), `${seed} ${what}`).toEqual([]);
          expect(still(sim, now), `${seed} ${what}`).toBeLessThan(5);
          expect(stragglers(sim, now, 120), `${seed} ${what}`).toEqual([]);
        }
        expect(reach(sim, nodes), `${seed}`).toBeLessThan(disc * 1.75);
        const p = sim.at(c)!;
        const spot: Pt = [p[0] + 140, p[1] - 60];
        drag(sim, c, spot);
        sim.sync(open(nodes, [a]));
        rest(sim);
        sim.sync(nodes);
        rest(sim);
        expect(overlaps(sim, nodes), `${seed} drop`).toEqual([]);
      }
    }, 60_000); // two dozen graphs: slow machines take a while

    it("comes to rest in a crowd too, nothing on anything: what is caught holds back what presses it", () => {
      for (const seed of [2, 7, 14, 15, 19, 34]) {
        const nodes = any(seed, true);
        const sim = laid(nodes);
        const roots = nodes.filter((n) => !n.parent).map((n) => n.id);
        const [a, b, c] = [roots[seed % roots.length], roots[(seed + 1) % roots.length], roots[(seed + 2) % roots.length]];
        const steps: [string, LNode[], string?][] = [
          ["open", open(nodes, [a])],
          ["beside", open(nodes, [a, b]), a],
          ["three", open(nodes, [a, b, c]), b],
          ["close", nodes],
        ];
        for (const [what, now, beside] of steps) {
          sim.sync(now, beside);
          expect(rest(sim), `${seed} ${what}`).toBeLessThan(1200);
          expect(overlaps(sim, now), `${seed} ${what}`).toEqual([]);
        }
      }
    }, 60_000);
  });

  describe("pages", () => {
    it("draws an open page to the centre, the bubbles gathered round it, and the disc back when it closes", () => {
      for (const nodes of [owner(), big()]) {
        const sim = laid(nodes);
        const disc = reach(sim, nodes);
        // the subject the farthest out
        const p0 = sim.positions();
        const rim = nodes.filter((n) => !n.parent).sort((a, b) => Math.hypot(...p0[b.id]) - Math.hypot(...p0[a.id]))[0].id;
        const opened = open(nodes, [rim]);
        sim.sync(opened);
        sim.settle();
        expect(overlaps(sim, opened)).toEqual([]);
        // among the bubbles, not where its bubble was on the rim
        expect(amid(sim, opened, sim.at(rim)!)).toBe(true);
        expect(Math.hypot(...sim.at(rim)!)).toBeLessThan(Math.hypot(...p0[rim]));
        expect(stragglers(sim, opened, 120)).toEqual([]);
        expect(reach(sim, opened)).toBeLessThan(disc + 700);
        sim.sync(nodes);
        sim.settle();
        expect(overlaps(sim, nodes)).toEqual([]);
        expect(reach(sim, nodes)).toBeLessThan(disc * 1.4);
        expect(stragglers(sim, nodes, 120)).toEqual([]);
      }
    });

    it("keeps two pages open side by side in the middle, the graph round them, and gives the disc back when they close", () => {
      const nodes = owner();
      const sim = laid(nodes);
      const disc = reach(sim, nodes);
      sim.sync(open(nodes, ["PRO"]));
      sim.settle();
      const two = open(nodes, ["PRO", "OUT"]);
      sim.sync(two, "PRO");
      sim.settle();
      const p = sim.positions();
      expect(overlaps(sim, two)).toEqual([]);
      // side by side, in the order they opened in, among the bubbles
      expect(Math.abs(p.PRO[1] - p.OUT[1])).toBeLessThan(80);
      expect(amid(sim, two, [(p.PRO[0] + p.OUT[0]) / 2, (p.PRO[1] + p.OUT[1]) / 2])).toBe(true);
      expect(stragglers(sim, two, 120)).toEqual([]);
      for (const n of nodes) if (n.id !== "PRO" && n.id !== "OUT") expect(Math.hypot(...p[n.id]), n.id).toBeLessThan(620 + 500);
      sim.sync(open(nodes, ["OUT"]));
      sim.settle();
      sim.sync(nodes);
      sim.settle();
      expect(overlaps(sim, nodes)).toEqual([]);
      expect(reach(sim, nodes)).toBeLessThan(disc * 1.4);
      expect(stragglers(sim, nodes, 120)).toEqual([]);
      expect(still(sim, nodes)).toBeLessThan(0.5);
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
      expect(Math.abs(now[1] - mes[1])).toBeLessThan(20);
      expect(Math.abs(now[0] - onto[0])).toBeLessThan(620 + 48 + 20);
    });
  });

  describe("a subject's bubbles, opened or not", () => {
    // A graph of k subjects more round the prototype's, Prod GetOut with
    // one bubble whose title is long (a task done).
    function crowd(k: number): LNode[] {
      const out = [...small().filter((n) => n.id !== "FAI"), node("ENQ", "Revoir la panne du site de la veille.", { parent: "PRO", r: 13 })];
      for (let i = 0; i < k; i++) {
        out.push(node(`s${i}`, `Sujet numéro ${i}`));
        for (let j = 0; j < i % 3; j++) out.push(node(`s${i}c${j}`, `Bulle ${i}.${j} avec un titre`, { parent: `s${i}` }));
      }
      return out;
    }
    // gap is the room between two bodies' boxes, edge to edge.
    function gap(sim: Sim, nodes: LNode[], a: string, b: string): number {
      const na = nodes.find((n) => n.id === a)!, nb = nodes.find((n) => n.id === b)!, pa = sim.at(a)!, pb = sim.at(b)!;
      const [ha, ta, ba] = extent(na, pa), [hb, tb, bb] = extent(nb, pb);
      return Math.hypot(Math.max(0, Math.abs(pa[0] - pb[0]) - ha - hb), Math.max(0, tb - ba, ta - bb));
    }
    // openPRO is the graph with Prod GetOut's page open, h high.
    const openPRO = (nodes: LNode[], h: number) => nodes.map((n) => (n.id === "PRO" ? { ...n, box: [700, h] as Pt } : n));

    it("keeps the bubble beside its subject's page, and nothing drifts as the graph wakes again and again", () => {
      for (const k of [3, 6, 10, 16, 24])
        for (const h of [280, 400]) {
          const nodes = crowd(k);
          const sim = laid(nodes);
          // the page opens at its first size, then takes its content's height
          sim.sync(openPRO(nodes, 520));
          for (let i = 0; i < 20; i++) sim.tick();
          // the app tells the graph again and again (the page measured anew,
          // a node's state), each time waking it
          let ended: Pt = [0, 0];
          for (let i = 0; i < 3600; i++) {
            const now = openPRO(nodes, h + (Math.floor(i / 300) % 2) * 20);
            if (i % 300 === 0) sim.sync(now);
            sim.tick();
            if (i >= 600 && i % 300 === 299) expect(gap(sim, now, "PRO", "ENQ"), `k=${k} h=${h} tick ${i}`).toBeLessThan(45);
            if (i === 2399) ended = sim.at("ENQ")!;
          }
          // the same after twelve hundred ticks more: it does not creep away
          expect(dist(sim.at("ENQ")!, ended), `k=${k} h=${h} drift`).toBeLessThan(10);
          // the page in the middle of the graph, the others round it
          const opened = openPRO(nodes, h);
          expect(Math.hypot(...sim.at("PRO")!), `k=${k} h=${h} centre`).toBeLessThan(60);
          expect(amid(sim, opened, sim.at("PRO")!)).toBe(true);
          expect(stragglers(sim, opened, 120)).toEqual([]);
          // closed, it comes back beside its subject
          sim.sync(nodes);
          sim.settle();
          expect(gap(sim, nodes, "PRO", "ENQ"), `k=${k} h=${h} closed`).toBeLessThan(60);
          expect(overlaps(sim, nodes)).toEqual([]);
        }
    }, 60_000);

    it("brings a bubble dropped by its subject's open page back beside its subject when the page closes", () => {
      const nodes = crowd(6);
      const sim = laid(nodes);
      const opened = openPRO(nodes, 280);
      sim.sync(opened);
      sim.settle();
      // dropped far past the page's corner
      const pro = sim.at("PRO")!;
      drag(sim, "ENQ", [pro[0] + 650, pro[1] + 340]);
      expect(gap(sim, opened, "PRO", "ENQ")).toBeLessThan(45);
      sim.sync(nodes);
      sim.settle();
      expect(gap(sim, nodes, "PRO", "ENQ")).toBeLessThan(60);
    });

    it("keeps a bubble's page the user dropped in place as its subject's page opens, and nothing runs off", () => {
      const nodes = crowd(6);
      const box: Pt = [620, 300];
      const pages = (ids: string[], h = 300) => nodes.map((n) => (n.id === "ENQ" && ids.includes(n.id) ? { ...n, box } : n.id === "PRO" && ids.includes(n.id) ? { ...n, box: [620, h] as Pt } : n));
      const sim = laid(nodes);
      // the bubble's page alone, dragged aside and dropped
      sim.sync(pages(["ENQ"]));
      sim.settle();
      const at = sim.at("ENQ")!;
      drag(sim, "ENQ", [at[0] - 900, at[1]]);
      const dropped = sim.at("ENQ")!;
      // its subject's page opens where its bubble was, measured anew again
      // and again, waking the graph
      for (let i = 0; i < 1200; i++) {
        if (i % 300 === 0) sim.sync(pages(["ENQ", "PRO"], 300 + (i / 300) * 20), "ENQ");
        sim.tick();
      }
      sim.settle();
      expect(dist(sim.at("ENQ")!, dropped), "dropped, then its subject opened").toBeLessThan(10);
      expect(dist(sim.at("PRO")!, dropped)).toBeLessThan(620 + 48 + 400);
      expect(sim.asleep()).toBe(true);
      // dragged again under its subject's page, both open: the subject's
      // page gliding to it does not carry it away, nothing runs off
      const pro = sim.at("PRO")!;
      drag(sim, "ENQ", [pro[0], pro[1] + 400]);
      for (let i = 0; i < 1200; i++) {
        if (i % 300 === 0) sim.sync(pages(["ENQ", "PRO"], 320 - (i / 300) * 20), "ENQ");
        sim.tick();
      }
      sim.settle();
      for (const id of ["ENQ", "PRO"]) expect(Math.hypot(...sim.at(id)!), id).toBeLessThan(2000);
      expect(sim.asleep()).toBe(true);
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
    expect(per).toBeLessThan(8);
  });
});
