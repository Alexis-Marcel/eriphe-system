import { describe, expect, it } from "vitest";
import { BOTTOM, boxAt, edgeMark, FOOT, framePages, pageMax, READABLE, TOP } from "./camera";

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
    // the console on the home: the last page opened (the one Eriphe just showed)
    const home = framePages(far, null, [], size);
    expect(home.x + 2600 * home.k).toBeCloseTo(size.w / 2, 0);
  });

  it("frames a page shown beside whole on a laptop: its head on screen, its foot above the bar", () => {
    const laptop = { w: 1180, h: 720 };
    const h = pageMax(laptop, 640);
    expect(h).toBe(720 - TOP - BOTTOM - 40);
    const subject = { id: "s", box: boxAt([0, 0], [620, h]) };
    const view = { id: "v", box: boxAt([620 + 48, 0], [620, h]) };
    const cam = framePages([subject, view], "v", [], laptop, 0.9);
    const top = cam.y + view.box[1] * cam.k, bottom = cam.y + view.box[3] * cam.k;
    const left = cam.x + view.box[0] * cam.k, right = cam.x + view.box[2] * cam.k;
    expect(top).toBeGreaterThanOrEqual(TOP);
    expect(bottom).toBeLessThanOrEqual(laptop.h - BOTTOM);
    expect(left).toBeGreaterThanOrEqual(0);
    expect(right).toBeLessThanOrEqual(laptop.w);
    // the bar grown with cards: the page stops above them
    const tall = { ...laptop, foot: 320 };
    const c2 = framePages([view], "v", [], tall, 0.9);
    expect(c2.y + view.box[3] * c2.k).toBeLessThanOrEqual(laptop.h - 320);
    expect(pageMax(tall, 640)).toBe(720 - TOP - 320 - 40);
  });
});

describe("the edge marks", () => {
  const size = { w: 1000, h: 800 };
  it("mark no node in view", () => {
    expect(edgeMark(500, 400, size)).toBeNull();
  });
  it("sit at the nearest edge, turned toward the node", () => {
    const right = edgeMark(1400, 300, size)!;
    expect([right.x, right.y, right.turn]).toEqual([982, 300, 0]);
    const above = edgeMark(200, -500, size)!;
    expect([above.x, above.y]).toEqual([200, 18]);
    expect(above.turn).toBeCloseTo(-Math.PI / 2);
  });
  it("keep above the console", () => {
    const low = edgeMark(500, 760, size)!;
    expect(low.y).toBe(800 - FOOT);
    expect(low.turn).toBeCloseTo(Math.PI / 2);
  });
});
