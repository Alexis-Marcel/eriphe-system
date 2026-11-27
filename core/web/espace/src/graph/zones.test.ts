import { describe, expect, it } from "vitest";
import { rootsOf } from "./lod";
import { declutter, MIN_FAMILY, nameAlpha, padOf, zoneAlpha, zoneFamilies, zoneShape } from "./zones";

// A big graph's continents: a zone per family big enough, its shape where
// its nodes are, a far member left out; far out, the family's name.

describe("the zones", () => {
  // two families: a (6 nodes) and b (3 nodes), and a node alone
  const nodes = [
    { id: "a" },
    ...Array.from({ length: 5 }, (_, i) => ({ id: `a${i}`, parent: i < 3 ? "a" : "a0" })),
    { id: "b" },
    { id: "b0", parent: "b" },
    { id: "b1", parent: "b" },
    { id: "z" },
  ];
  const { rootOf } = rootsOf(nodes);
  const index = new Map(nodes.map((x, i) => [x.id, i]));
  const root = Int32Array.from(nodes.map((x) => index.get(rootOf.get(x.id)!)!));
  const fam = Int16Array.from(nodes.map((x) => (rootOf.get(x.id) === "a" ? 2 : rootOf.get(x.id) === "b" ? 5 : -1)));

  it("is one per family of MIN_FAMILY nodes or more, with its colour", () => {
    const f = zoneFamilies(root, fam, (i) => nodes[i].id === "a3");
    expect(MIN_FAMILY).toBeLessThanOrEqual(6);
    expect(f).toHaveLength(1);
    expect(f[0].root).toBe(0);
    expect(f[0].fam).toBe(2);
    expect([...f[0].members].map((i) => nodes[i].id).sort()).toEqual(["a", "a0", "a1", "a2", "a3", "a4"]);
    expect(f[0].waits).toBe(true);
    expect(zoneFamilies(root, fam, () => false)[0].waits).toBe(false);
  });

  it("follows its nodes, a far member left out of it", () => {
    const [f] = zoneFamilies(root, fam, () => false);
    const xy = new Float32Array(nodes.length * 2);
    const at = (id: string, x: number, y: number) => {
      xy[index.get(id)! * 2] = x;
      xy[index.get(id)! * 2 + 1] = y;
    };
    at("a", 0, 0);
    at("a0", 10, 0);
    at("a1", -10, 0);
    at("a2", 0, 10);
    at("a3", 0, -10);
    at("a4", 5000, 0); // linked across the graph
    const sh = zoneShape(f, xy);
    expect([...sh.keep].map((i) => nodes[i].id)).not.toContain("a4");
    expect(sh.keep).toHaveLength(5);
    expect(sh.cx).toBeCloseTo(0);
    expect(sh.cy).toBeCloseTo(0);
    // moved, it moves
    for (const id of ["a", "a0", "a1", "a2", "a3"]) at(id, xy[index.get(id)! * 2] + 300, xy[index.get(id)! * 2 + 1]);
    expect(zoneShape(f, xy).cx).toBeCloseTo(300);
  });

  it("skips the nodes not placed yet", () => {
    const [f] = zoneFamilies(root, fam, () => false);
    const xy = new Float32Array(nodes.length * 2).fill(NaN);
    expect(zoneShape(f, xy).keep).toHaveLength(0);
  });

  it("is wider for a family spread wide, within bounds", () => {
    expect(padOf({ keep: new Int32Array(0), cx: 0, cy: 0, spread: 0 }, 40)).toBe(40);
    expect(padOf({ keep: new Int32Array(0), cx: 0, cy: 0, spread: 400 }, 40)).toBe(160);
    expect(padOf({ keep: new Int32Array(0), cx: 0, cy: 0, spread: 1e6 }, 40)).toBe(320);
  });

  it("shows far out, fades as the nodes become readable, none close", () => {
    expect(zoneAlpha(0.05)).toBe(1);
    expect(zoneAlpha(0.4)).toBeGreaterThan(0);
    expect(zoneAlpha(0.4)).toBeLessThan(1);
    expect(zoneAlpha(1)).toBe(0);
  });

  it("names its family until the root's own label shows: they cross-fade", () => {
    expect(nameAlpha(0)).toBe(1);
    expect(nameAlpha(1)).toBe(0);
    expect(nameAlpha(0.3) + 0.3).toBeCloseTo(1);
  });

  it("draws a name only where no more important one is", () => {
    expect(
      declutter([
        [0, 0, 100, 20],
        [50, 10, 150, 30], // over the first
        [200, 0, 300, 20],
        [0, 100, 100, 120],
      ]),
    ).toEqual([true, false, true, true]);
  });
});
