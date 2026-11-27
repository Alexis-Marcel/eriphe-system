import { describe, expect, it } from "vitest";
import { asleep, DAY, degrees, LIFE, LINK_ALPHA, lifeFactor, lifeRadius, linkAlpha, linkLife, SLEEP_R, type LifeOf } from "./life";
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

  it("is a bubble only close, as its dot grows on screen", () => {
    // children of radius 14 awake: dots of 7 asleep, every other one
    const items: Item[] = Array.from({ length: 12 }, (_, i) => ({ id: `n${i}`, r: i % 2 === 0 ? 14 * SLEEP_R : 14, waits: false, sleeps: i % 2 === 0 }));
    const xy = Float32Array.from(items.flatMap((_, i) => [i * 60, 0]));
    const o: Knobs = { ...LOD, from: 10, bubbleAt: 16 };
    const at = (k: number, keep: string[] = []) => [...decide(items, xy, { x: 0, y: 300, k, w: 4000, h: 600 }, null, keep, o, 100).ids!].map((id) => Number(id.slice(1)));
    // scale 1.2: the awake ones are bubbles, the dots not
    expect(at(1.2).length).toBeGreaterThan(0);
    expect(at(1.2).every((i) => i % 2 === 1)).toBe(true);
    // close (the camera goes to 2.6), the dots too
    expect(at(2.4).some((i) => i % 2 === 0)).toBe(true);
    expect(16 * SLEEPS).toBeLessThanOrEqual(14 * SLEEP_R * 2.6);
    // hovered (kept), one at once
    expect(at(0.5, ["n0"])).toContain(0);
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

describe("a root", () => {
  it("never sleeps and is never smaller than its radius: it names its family", () => {
    const old = node({ updated: ago(300) });
    const child = lifeRadius(20, old, 0, now, on);
    const root = lifeRadius(20, old, 0, now, on, true);
    expect(child.sleeps).toBe(true);
    expect(root.sleeps).toBe(false);
    expect(root.r).toBe(20);
    // alive, it grows as any node
    expect(lifeRadius(20, node({ status: "waits" }), 0, now, on, true).r).toBeGreaterThan(20);
  });
});

describe("a link", () => {
  it("takes its nodes' life: drawn between living nodes, faint to a sleeping one, hardly there between two asleep", () => {
    expect(linkAlpha(false, false)).toBe(1);
    expect(linkAlpha(false, true)).toBe(linkAlpha(true, false));
    expect(linkAlpha(false, true)).toBeLessThan(0.5);
    expect(linkAlpha(true, true)).toBeLessThan(linkAlpha(false, true));
    expect(linkAlpha(true, true)).toBeLessThanOrEqual(0.1);
    expect(LINK_ALPHA).toHaveLength(3);
  });

  it("to a root takes its other end's life: no fan of spokes to sleeping notes", () => {
    expect(linkLife(null, true)).toBe(2);
    expect(linkLife(false, null)).toBe(0);
    expect(linkLife(null, null)).toBe(0);
  });
});
