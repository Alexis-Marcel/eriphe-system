import { describe, expect, it } from "vitest";
import { BUDGET, covered, CULL_FROM, culled, MOUNT, reach, same, UNMOUNT, type Sized, type View } from "./cull";

// A row of bubbles 100 apart on the x axis, from x = 0.
const row = (n: number): Sized[] => Array.from({ length: n }, (_, i) => ({ id: `n${i}`, r: 10, lw: 60, lh: 18 }));
const at = (id: string): [number, number] => [Number(id.slice(1)) * 100, 0];
// A screen of 1000 × 600, the camera at scale 1 with x = 0 at its left.
const view = (x: number, k = 1): View => ({ x, y: 300, k, w: 1000, h: 600 });
const ids = (s: Set<string> | null) => [...(s ?? [])].map((id) => Number(id.slice(1))).sort((a, b) => a - b);
// drawn is the ids to draw, the budget aside: called again with what it
// drew until nothing waits, as the graph does frame after frame.
const drawn = (bodies: Sized[], v: View, was: Set<string> | null = null, keep: (string | null | undefined)[] = []) => {
  let r = culled(bodies, at, v, was, keep);
  for (let i = 0; r.more && i < 100; i++) r = culled(bodies, at, v, r.ids, keep);
  return r.ids!;
};

describe("culling a big graph", () => {
  it("draws every bubble of a small graph", () => {
    expect(culled(row(CULL_FROM - 1), at, view(0), null)).toEqual({ ids: null, more: false });
  });

  it("draws the bubbles the camera sees and those within the margin", () => {
    const s = drawn(row(400), view(0));
    // On screen: x from 0 to 1000; with the margin, -MOUNT to 1000 + MOUNT,
    // and a bubble's reach (its halo) beyond.
    const h = reach(row(1)[0])[0];
    const last = Math.floor((1000 + MOUNT + h) / 100);
    expect(ids(s)).toEqual(Array.from({ length: last + 1 }, (_, i) => i));
  });

  it("follows the camera and its scale", () => {
    // Panned 5000 to the right: x from 5000 to 6000 on screen.
    const s = ids(drawn(row(400), view(-5000)));
    expect(s[0]).toBe(Math.ceil((5000 - MOUNT - reach(row(1)[0])[0]) / 100));
    expect(s).toContain(50);
    expect(s).toContain(60);
    expect(s.at(-1)).toBe(Math.floor((6000 + MOUNT + reach(row(1)[0])[0]) / 100));
    // Zoomed out to a quarter: four times the graph shows.
    expect(drawn(row(400), view(0, 0.25)).size).toBeGreaterThan(40);
  });

  it("keeps a bubble drawn until it is well out, and the ones held, focused or hovered", () => {
    const first = drawn(row(400), view(0));
    expect(first.has("n2")).toBe(true);
    // Panned a little: n2 is out of the first margin, within the second.
    const shift = MOUNT + 400;
    expect(drawn(row(400), view(-shift)).has("n2")).toBe(false);
    const kept = drawn(row(400), view(-shift), first);
    expect(kept.has("n2")).toBe(true);
    // Far: gone, except what is kept whatever.
    const far = drawn(row(400), view(-(UNMOUNT + 2000)), kept, ["n2", null, undefined]);
    expect(far.has("n2")).toBe(true);
    expect(far.has("n3")).toBe(false);
  });

  it("draws at most a budget of new bubbles a frame, those on screen first", () => {
    // Zoomed far out: the 400 on screen at once.
    const v = view(0, 0.02);
    const one = culled(row(400), at, v, null);
    expect(one.ids!.size).toBe(BUDGET);
    expect(one.more).toBe(true);
    let r = one;
    let frames = 1;
    while (r.more) {
      r = culled(row(400), at, v, r.ids);
      frames++;
    }
    expect(r.ids!.size).toBe(400);
    expect(frames).toBe(Math.ceil(400 / BUDGET));
    // Of a pan's new ones, those on screen come before the margin's.
    const wide = Array.from({ length: 400 }, (_, i) => ({ id: `n${i}`, r: 10, lw: 60, lh: 18 }));
    const dense = (id: string): [number, number] => [(Number(id.slice(1)) % 100) * 14 - 200, Math.floor(Number(id.slice(1)) / 100) * 40];
    const first = culled(wide, dense, view(0), null);
    // (more on screen than the budget: the budget's all on screen)
    const h = reach(wide[0])[0];
    const on = (id: string) => dense(id)[0] >= -h && dense(id)[0] <= 1000 + h;
    expect(wide.filter((b) => on(b.id)).length).toBeGreaterThan(BUDGET);
    expect(first.ids!.size).toBe(BUDGET);
    for (const id of first.ids!) expect(on(id)).toBe(true);
    expect(first.more).toBe(true);
  });

  it("looks again only once the camera leaves what was drawn for it", () => {
    expect(covered(view(0), view(-MOUNT / 4))).toBe(true);
    expect(covered(view(0), view(-MOUNT))).toBe(false);
    expect(covered(view(0), view(0, 0.98))).toBe(true);
    expect(covered(view(0), view(0, 0.5))).toBe(false);
    // Zoomed in far: less to draw, looked at again.
    expect(covered(view(0), view(0, 1.1))).toBe(true);
    expect(covered(view(0), view(0, 1.5))).toBe(false);
    expect(covered(view(0), { ...view(0), w: 1600 })).toBe(false);
  });

  it("leaves out a body the simulation has no place for yet", () => {
    const s = culled(row(400), (id) => (id === "n1" ? undefined : at(id)), view(0), null).ids!;
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
