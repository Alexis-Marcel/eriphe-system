import { describe, expect, it } from "vitest";
import { CULL_FROM, culled, MOUNT, reach, same, UNMOUNT, type Sized, type View } from "./cull";

// A row of bubbles 100 apart on the x axis, from x = 0.
const row = (n: number): Sized[] => Array.from({ length: n }, (_, i) => ({ id: `n${i}`, r: 10, lw: 60, lh: 18 }));
const at = (id: string): [number, number] => [Number(id.slice(1)) * 100, 0];
// A screen of 1000 × 600, the camera at scale 1 with x = 0 at its left.
const view = (x: number, k = 1): View => ({ x, y: 300, k, w: 1000, h: 600 });
const ids = (s: Set<string> | null) => [...(s ?? [])].map((id) => Number(id.slice(1))).sort((a, b) => a - b);

describe("culling a big graph", () => {
  it("draws every bubble of a small graph", () => {
    expect(culled(row(CULL_FROM - 1), at, view(0), null)).toBeNull();
  });

  it("draws the bubbles the camera sees and those within the margin", () => {
    const s = culled(row(400), at, view(0), null);
    // On screen: x from 0 to 1000; with the margin, -MOUNT to 1000 + MOUNT,
    // and a bubble's reach (its halo) beyond.
    const h = reach(row(1)[0])[0];
    const last = Math.floor((1000 + MOUNT + h) / 100);
    expect(ids(s)).toEqual(Array.from({ length: last + 1 }, (_, i) => i));
  });

  it("follows the camera and its scale", () => {
    // Panned 5000 to the right: x from 5000 to 6000 on screen.
    const s = ids(culled(row(400), at, view(-5000), null));
    expect(s[0]).toBeGreaterThanOrEqual(46);
    expect(s).toContain(50);
    expect(s).toContain(60);
    expect(s.at(-1)).toBeLessThanOrEqual(64);
    // Zoomed out to a quarter: four times the graph shows.
    expect(culled(row(400), at, view(0, 0.25), null)!.size).toBeGreaterThan(40);
  });

  it("keeps a bubble drawn until it is well out, and the ones held, focused or hovered", () => {
    const first = culled(row(400), at, view(0), null)!;
    expect(first.has("n2")).toBe(true);
    // Panned a little: n2 is out of the first margin, within the second.
    const shift = MOUNT + 400;
    const kept = culled(row(400), at, view(-shift), first)!;
    expect(culled(row(400), at, view(-shift), null)!.has("n2")).toBe(false);
    expect(kept.has("n2")).toBe(true);
    // Far: gone, except what is kept whatever.
    const far = culled(row(400), at, view(-(UNMOUNT + 2000)), kept, ["n2", null, undefined])!;
    expect(far.has("n2")).toBe(true);
    expect(far.has("n3")).toBe(false);
  });

  it("leaves out a body the simulation has no place for yet", () => {
    const s = culled(row(400), (id) => (id === "n1" ? undefined : at(id)), view(0), null)!;
    expect(s.has("n1")).toBe(false);
    expect(s.has("n0")).toBe(true);
  });

  it("tells two sets drawn apart", () => {
    expect(same(null, null)).toBe(true);
    expect(same(new Set(["a"]), null)).toBe(false);
    expect(same(new Set(["a", "b"]), new Set(["b", "a"]))).toBe(true);
    expect(same(new Set(["a", "b"]), new Set(["a", "c"]))).toBe(false);
  });
});
