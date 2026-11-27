import { describe, expect, it } from "vitest";
import { BUDGET, decide, drawnRadius, families, FAR, hit, HOLD, labelAlpha, LOD, NEAR, WAITS, type Item, type Knobs, type View } from "./lod";

// A row of nodes 100 apart on the x axis, from x = 0, of radius r each.
const row = (n: number, r = 20, waits: (i: number) => boolean = () => false): Item[] => Array.from({ length: n }, (_, i) => ({ id: `n${i}`, r, waits: waits(i) }));
const xyOf = (items: Item[], at: (i: number) => [number, number] = (i) => [i * 100, 0]) => Float32Array.from(items.flatMap((_, i) => at(i)));
// A screen of 1000 × 600, the camera at scale k with x = 0 at its left.
const view = (x: number, k = 1): View => ({ x, y: 300, k, w: 1000, h: 600 });
const knobs = (k: Partial<Knobs> = {}): Knobs => ({ ...LOD, from: 10, bubbleAt: 16, cap: 120, size: 1, ...k });
const ids = (s: Set<string> | null) => [...(s ?? [])].map((id) => Number(id.slice(1))).sort((a, b) => a - b);
// all is what decide gives once the budget is spent: called again with
// what it gave until nothing waits, as the graph does frame after frame.
const all = (items: Item[], xy: Float32Array, v: View, o: Knobs, keep: string[] = []) => {
  let r = decide(items, xy, v, null, keep, o);
  for (let i = 0; r.more && i < 100; i++) r = decide(items, xy, v, r.ids, keep, o);
  return r.ids!;
};

describe("the level of detail", () => {
  it("draws every node of a small graph as a bubble", () => {
    const items = row(9);
    expect(decide(items, xyOf(items), view(0), null, [], knobs())).toEqual({ ids: null, more: false });
  });

  it("draws as bubbles the nodes big on the screen, near it", () => {
    const items = row(40);
    // scale 1: a radius of 20 on screen, past 16: those within NEAR of the screen
    const s = all(items, xyOf(items), view(0), knobs());
    expect(ids(s)).toEqual(Array.from({ length: Math.floor((1000 + NEAR + 20) / 100) + 1 }, (_, i) => i));
    // scale 0.5: a radius of 10, a disc: none
    expect(all(items, xyOf(items), view(0, 0.5), knobs()).size).toBe(0);
  });

  it("keeps what is kept, wherever it is, and draws the rest up to the cap, the biggest first", () => {
    const items = row(40).map((x, i) => ({ ...x, r: 17 + (i % 5) }));
    const s = all(items, xyOf(items), view(0), knobs({ cap: 4 }), ["n39"]);
    expect(s.has("n39")).toBe(true);
    expect(s.size).toBe(4);
    // of those near the screen (x up to 1140), the two of radius 21, then
    // the first of radius 20
    expect(ids(s)).toEqual([3, 4, 9, 39]);
  });

  it("draws a node that waits sooner, and before the others", () => {
    const items = row(20, 10, (i) => i === 3);
    // a radius of 10 on screen: below 16, past 16 × WAITS for the one that waits
    expect(10).toBeGreaterThan(16 * WAITS);
    expect(ids(all(items, xyOf(items), view(0), knobs()))).toEqual([3]);
    const big = row(20, 20, (i) => i === 8);
    const s = all(big, xyOf(big), view(0), knobs({ cap: 1 }));
    expect(ids(s)).toEqual([8]);
  });

  it("holds a bubble a little past the threshold and the screen, not a new one", () => {
    const items = row(40, 15);
    const xy = xyOf(items);
    // 15 < 16: nothing new; drawn before, 15 > 16 × HOLD: kept
    expect(15).toBeGreaterThan(16 * HOLD);
    expect(decide(items, xy, view(0), null, [], knobs()).ids!.size).toBe(0);
    const was = new Set(["n2", "n12"]);
    // n12 is at x = 1200: past NEAR, within FAR of the screen
    expect(1200 - 15 - 1000).toBeGreaterThan(NEAR);
    expect(1200 - 15 - 1000).toBeLessThan(FAR);
    expect(ids(decide(items, xy, view(0), was, [], knobs()).ids)).toEqual([2, 12]);
  });

  it("draws at most BUDGET new bubbles a frame, those on screen first", () => {
    const items = row(400, 20);
    const xy = xyOf(items, (i) => [(i % 20) * 50, Math.floor(i / 20) * 30 - 300]);
    const first = decide(items, xy, view(0), null, [], knobs({ cap: 400 }));
    expect(first.ids!.size).toBe(BUDGET);
    expect(first.more).toBe(true);
    const next = decide(items, xy, view(0), first.ids, [], knobs({ cap: 400 }));
    expect(next.ids!.size).toBe(BUDGET * 2);
    for (const id of first.ids!) expect(next.ids!.has(id)).toBe(true);
  });

  it("skips a node not placed yet", () => {
    const items = row(12);
    const xy = xyOf(items);
    xy[0] = NaN;
    expect(all(items, xy, view(0), knobs()).has("n0")).toBe(false);
  });
});

describe("hitting a disc", () => {
  const items = row(10, 20);
  const xy = xyOf(items);

  it("finds the disc under the pointer, by its radius on screen", () => {
    // n3 at x = 300, y = 0: on screen (300, 300) at scale 1
    expect(hit(items, xy, view(0), 300, 300)).toBe("n3");
    expect(hit(items, xy, view(0), 318, 300)).toBe("n3");
    expect(hit(items, xy, view(0), 350, 300)).toBeNull();
    // at scale 0.5 the discs are half as big, and half as far apart
    expect(hit(items, xy, view(0, 0.5), 150, 300)).toBe("n3");
    expect(hit(items, xy, view(0, 0.5), 150 + 16, 300)).toBeNull();
  });

  it("reaches a little wider for a finger", () => {
    expect(hit(items, xy, view(0), 326, 300, 4)).toBeNull();
    expect(hit(items, xy, view(0), 326, 300, 12)).toBe("n3");
  });

  it("gives the nearest edge, a tiny disc a few pixels, and skips what is covered", () => {
    const two: Item[] = [
      { id: "big", r: 40, waits: false },
      { id: "small", r: 1, waits: false },
    ];
    const at = Float32Array.from([0, 0, 50, 0]);
    // the small one, 1 px wide, is reached within 3 px and the slop
    expect(hit(two, at, view(0), 55, 300, 4)).toBe("small");
    expect(hit(two, at, view(0), 20, 300)).toBe("big");
    expect(hit(two, at, view(0), 20, 300, 4, (id) => id === "big")).toBeNull();
  });

  it("hits the disc as drawn, its size knob's", () => {
    const o = knobs({ size: 2 });
    // radius 20 at scale 0.25: 5 on screen, drawn 2 × 20 eased: past 5
    expect(drawnRadius(20, 0.25, o) * 0.25).toBeGreaterThan(5);
    expect(hit(items, xy, view(0, 0.25), 75 + 8, 300, 0, undefined, o)).toBe("n3");
    expect(hit(items, xy, view(0, 0.25), 75 + 8, 300, 0, undefined, knobs())).toBeNull();
  });
});

describe("a disc's size and label", () => {
  it("eases the size knob to the bubble's radius at the threshold", () => {
    const o = knobs({ size: 2, bubbleAt: 16 });
    expect(drawnRadius(10, 0.01, o)).toBeCloseTo(20, 0);
    expect(drawnRadius(10, 1.6, o)).toBe(10);
    expect(drawnRadius(10, 5, o)).toBe(10);
    expect(drawnRadius(10, 0.3, knobs())).toBe(10);
  });

  it("shows the label from labelAt, whole from half as much again", () => {
    const o = knobs({ labelAt: 8 });
    expect(labelAlpha(7, o)).toBe(0);
    expect(labelAlpha(10, o)).toBeCloseTo(0.5);
    expect(labelAlpha(12, o)).toBe(1);
  });
});

describe("families", () => {
  it("gives each node its root's colour, the biggest families first, none to a node alone", () => {
    const nodes = [
      { id: "a" },
      { id: "a1", parent: "a" },
      { id: "a2", parent: "a" },
      { id: "a21", parent: "a2" },
      { id: "b" },
      { id: "b1", parent: "b" },
      { id: "c" },
      { id: "lost", parent: "gone" },
    ];
    const f = families(nodes, 8);
    expect(["a", "a1", "a2", "a21"].map((id) => f.get(id))).toEqual([0, 0, 0, 0]);
    expect([f.get("b"), f.get("b1")]).toEqual([1, 1]);
    expect(f.get("c")).toBe(-1);
    expect(f.get("lost")).toBe(-1);
    // past the palette, the colours come round again
    expect(families([...nodes, ...Array.from({ length: 20 }, (_, i) => [{ id: `r${i}` }, { id: `r${i}c`, parent: `r${i}` }]).flat()], 8).get("r10c")).toBeLessThan(8);
  });
});
