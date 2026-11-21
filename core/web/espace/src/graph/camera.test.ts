import { describe, expect, it } from "vitest";
import { ALL, bigBox, bigRoom, BOTTOM, boxAt, edgeMark, FOOT, frameAll, frameAlone, framePages, pageMax, READABLE, SIDE, slide, TOP, type Box, type Cam } from "./camera";

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

describe("the camera and the whole graph", () => {
  const size = { w: 1440, h: 900 };
  const onScreen = (cam: Cam, b: Box) => [cam.x + b[0] * cam.k, cam.y + b[1] * cam.k, cam.x + b[2] * cam.k, cam.y + b[3] * cam.k];
  const whole = (cam: Cam, b: Box) => {
    const [l, t, r, bt] = onScreen(cam, b);
    return l >= 0 && r <= size.w && t >= 0 && bt <= size.h;
  };

  it("frames every bubble whole at home, its long label too", () => {
    const boxes: Box[] = [
      [-500, -40, -200, 80], // a label 300 wide on the left
      [200, -60, 300, 60],
      [-60, 200, 60, 320],
    ];
    const cam = frameAll(boxes, size);
    for (const b of boxes) expect(whole(cam, b)).toBe(true);
  });

  it("frames the pages with the whole graph when it fits", () => {
    const page = { id: "p", box: boxAt([0, 0], [700, 520]) };
    const graph: Box[] = [[-700, -50, -480, 60], [420, 100, 600, 200]];
    const cam = framePages([page], "p", [], size, 1, graph);
    expect(cam.k).toBeGreaterThanOrEqual(ALL);
    for (const b of [page.box, ...graph]) expect(whole(cam, b)).toBe(true);
  });

  it("slides toward the rest of the graph when it does not fit, the page whole", () => {
    const page = { id: "p", box: boxAt([0, 0], [700, 520]) };
    const far: Box[] = [[-1600, -50, -1400, 60], [-1300, 80, -1100, 180]];
    const cam = framePages([page], "p", [], size, 1, far);
    expect(whole(cam, page.box)).toBe(true);
    const alone = framePages([page], "p", [], size, 1);
    // the page is no longer in the middle: the room is on the graph's side
    expect(cam.x).toBeGreaterThan(alone.x + 100);
    expect(onScreen(cam, page.box)[2]).toBeGreaterThan(size.w - SIDE - 40);
  });

  it("leaves the camera alone when what it frames takes the whole screen", () => {
    const cam = { k: 1, x: 100, y: 50 };
    expect(slide(cam, [[0, 0, 3000, 3000]], [[-500, 0, 0, 10]], size)).toEqual(cam);
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

describe("an enlarged page", () => {
  it("takes most of the screen above the bar, a margin left for the graph, and is framed whole at scale 1", () => {
    for (const size of [{ w: 1440, h: 900 }, { w: 1180, h: 720 }]) {
      const [w, h] = bigBox(size, 700);
      expect(w).toBeLessThanOrEqual(size.w - 2 * SIDE - 200);
      expect(h).toBe(pageMax(size, 4000));
      const box = boxAt([300, -120], [w, h]);
      const cam = frameAlone(box, size);
      expect(cam.k).toBe(1);
      // whole: its head under the corner's icons, its foot above the bar
      expect(cam.y + box[1] * cam.k).toBeGreaterThanOrEqual(TOP);
      expect(cam.y + box[3] * cam.k).toBeLessThanOrEqual(size.h - BOTTOM);
      expect(cam.x + box[0] * cam.k).toBeGreaterThanOrEqual(SIDE + 90);
    }
  });

  it("is never narrower than a page", () => {
    expect(bigBox({ w: 600, h: 700 }, 700)[0]).toBe(700);
  });

  it("keeps the bar's room clear: no bubble at rest beside it sits behind the bar", () => {
    for (const size of [{ w: 1440, h: 900 }, { w: 1440, h: 900, foot: 260 }, { w: 1180, h: 720 }]) {
      const page = bigBox(size, 700);
      const room = bigRoom(page, size);
      expect(room[0]).toBe(page[0]);
      const at: [number, number] = [300, -120];
      const cam = frameAlone(boxAt(at, page), size);
      // A bubble just out of the room, under the page, is under the screen's foot.
      expect(cam.y + (at[1] + room[1] / 2) * cam.k).toBeGreaterThanOrEqual(size.h);
    }
  });
});
