import { describe, expect, it } from "vitest";
import { asleep, DAY, degrees, LIFE, lifeFactor, lifeRadius, SLEEP_R, type LifeOf } from "./life";
import { decide, LOD, SLEEPS, type Item, type Knobs } from "./lod";

// A big graph's sizes by life: what waits for the user is the biggest,
// then what runs, what a skill keeps up, what changed lately, what many
// nodes link to; idle for weeks, smaller; asleep, a pale dot.

const now = Date.parse("2026-11-27T12:00:00Z");
const ago = (days: number) => new Date(now - days * DAY).toISOString();
const node = (x: Partial<LifeOf> = {}): LifeOf => ({ status: "idle", updated: ago(3), ...x });
const on = { life: true, lifeStrength: 1, sleep: true, sleepDays: 21 };

describe("a node's life", () => {
  it("orders the nodes as the user needs them", () => {
    const f = (x: Partial<LifeOf>, degree = 1) => lifeFactor(node(x), degree, now);
    const waits = f({ status: "waits" }), runs = f({ status: "running" }), kept = f({ maintained: ["suivi"] }), idle = f({});
    expect(waits).toBeGreaterThan(runs);
    expect(runs).toBeGreaterThan(kept);
    expect(kept).toBeGreaterThan(idle);
    // fresh is bigger than a week old, which is bigger than months old
    expect(f({ updated: ago(0) })).toBeGreaterThan(f({ updated: ago(7) }));
    expect(f({ updated: ago(7) })).toBeGreaterThan(f({ updated: ago(90) }));
    // a hub, by the log of its links: more, never without end
    expect(f({}, 30)).toBeGreaterThan(f({}, 3));
    expect(f({}, 3000) / f({}, 300)).toBeCloseTo(1, 5);
  });

  it("stays within its bounds, whatever adds up", () => {
    const most = lifeFactor(node({ status: "waits", maintained: ["a"], updated: ago(0) }), 500, now, 2);
    const least = lifeFactor(node({ updated: ago(400) }), 0, now, 2);
    expect(most).toBe(LIFE.max);
    expect(least).toBe(LIFE.min);
  });

  it("is 1 at strength 0, and grows with it", () => {
    const x = node({ status: "waits" });
    expect(lifeFactor(x, 5, now, 0)).toBe(1);
    expect(lifeFactor(x, 5, now, 0.5)).toBeLessThan(lifeFactor(x, 5, now, 1));
  });

  it("does not shrink what waits, runs or is kept up, however old", () => {
    for (const x of [{ status: "waits" as const }, { status: "running" as const }, { maintained: ["suivi"] }]) expect(lifeFactor(node({ ...x, updated: ago(300) }), 0, now)).toBeGreaterThanOrEqual(1);
  });

  it("reads a date it cannot parse as old, not as fresh", () => {
    expect(lifeFactor(node({ updated: "?" }), 0, now)).toBeLessThanOrEqual(lifeFactor(node({ updated: ago(30) }), 0, now));
  });
});

describe("a node asleep", () => {
  it("is idle past its days, nothing keeping it awake", () => {
    expect(asleep(node({ updated: ago(30) }), now, 21)).toBe(true);
    expect(asleep(node({ updated: ago(10) }), now, 21)).toBe(false);
    expect(asleep(node({ updated: ago(10) }), now, 7)).toBe(true);
    expect(asleep(node({ updated: ago(30), status: "done" }), now, 21)).toBe(true);
  });

  it("never when it waits, runs, is kept up or shows a figure", () => {
    for (const x of [{ status: "waits" as const }, { status: "running" as const }, { maintained: ["suivi"] }, { tile: true }]) expect(asleep(node({ ...x, updated: ago(200) }), now, 21)).toBe(false);
  });

  it("is a small dot, its radius the one its bubble takes", () => {
    const awake = lifeRadius(20, node({ updated: ago(30) }), 0, now, { ...on, sleep: false });
    const dot = lifeRadius(20, node({ updated: ago(30) }), 0, now, on);
    expect(dot.sleeps).toBe(true);
    expect(dot.r).toBeLessThanOrEqual(20 * SLEEP_R);
    expect(awake.sleeps).toBe(false);
    expect(awake.r).toBeGreaterThan(dot.r);
    // the radius in half units: a little ageing does not move the layout
    expect((dot.r * 2) % 1).toBe(0);
  });

  it("is a bubble only from SLEEPS times the threshold", () => {
    const items: Item[] = Array.from({ length: 12 }, (_, i) => ({ id: `n${i}`, r: 20, waits: false, sleeps: i % 2 === 0 }));
    const xy = Float32Array.from(items.flatMap((_, i) => [i * 60, 0]));
    const o: Knobs = { ...LOD, from: 10, bubbleAt: 16 };
    // scale 1: a radius of 20, past 16 but short of 16 × SLEEPS
    const { ids } = decide(items, xy, { x: 0, y: 300, k: 1, w: 1000, h: 600 }, null, [], o, 100);
    expect([...ids!].every((id) => Number(id.slice(1)) % 2 === 1)).toBe(true);
    const close = decide(items, xy, { x: 0, y: 300, k: SLEEPS, w: 2000, h: 600 }, null, [], o, 100);
    expect([...close.ids!].some((id) => Number(id.slice(1)) % 2 === 0)).toBe(true);
    // hovered (kept), it is one at once
    expect(decide(items, xy, { x: 0, y: 300, k: 1, w: 1000, h: 600 }, null, ["n0"], o, 100).ids!.has("n0")).toBe(true);
  });
});

describe("the degrees", () => {
  it("count each link once, the parent's too", () => {
    const d = degrees([{ id: "a" }, { id: "b", parent: "a", links: ["a", "c"] }, { id: "c", links: ["b", "zz"] }]);
    expect(d.get("a")).toBe(1);
    expect(d.get("b")).toBe(2);
    expect(d.get("c")).toBe(1);
  });
});
