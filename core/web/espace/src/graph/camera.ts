import type { Pt } from "./layout";

// Where the camera looks: a scale k and the world's offset on screen.
export type Cam = { k: number; x: number; y: number };
export type Box = [number, number, number, number]; // left, top, right, bottom, in the world
export type Size = { w: number; h: number };

// The screen's free band, between the head-up display and the console.
export const TOP = 78;
export const BOTTOM = 150;
export const SIDE = 20;

// READABLE is the smallest scale at which several open pages are framed
// together; below it, the camera frames the page the console talks to, the
// others stay open in place, a drag or a zoom away.
export const READABLE = 0.8;

const fitK = (bs: Box[], size: Size) => {
  const w = Math.max(...bs.map((b) => b[2])) - Math.min(...bs.map((b) => b[0])) + 60;
  const h = Math.max(...bs.map((b) => b[3])) - Math.min(...bs.map((b) => b[1])) + 40;
  return Math.min((size.w - 2 * SIDE) / w, (size.h - TOP - BOTTOM) / h);
};

// fit frames boxes in the free band, at most at scale max.
export function fit(bs: Box[], size: Size, max = 1, min = 0.3): Cam {
  const minX = Math.min(...bs.map((b) => b[0])) - 30, maxX = Math.max(...bs.map((b) => b[2])) + 30;
  const minY = Math.min(...bs.map((b) => b[1])) - 20, maxY = Math.max(...bs.map((b) => b[3])) + 20;
  const left = SIDE, right = size.w - SIDE, top = TOP, bottom = size.h - BOTTOM;
  const k = Math.max(min, Math.min(max, (right - left) / (maxX - minX), (bottom - top) / (maxY - minY)));
  return { k, x: (left + right) / 2 - ((minX + maxX) / 2) * k, y: (top + bottom) / 2 - ((minY + maxY) / 2) * k };
}

export const boxAt = (at: Pt, [w, h]: Pt): Box => [at[0] - w / 2, at[1] - h / 2, at[0] + w / 2, at[1] + h / 2];

// framePages frames the open pages: all of them if they fit at READABLE or
// more, else the one the console talks to (talk), or with the home's
// console the last one opened (pages in the order they opened); the bubbles around them
// too (near) while the pages stay readable.
export function framePages(pages: { id: string; box: Box }[], talk: string | null, near: Box[], size: Size, max = 1): Cam {
  let boxes = pages.map((p) => p.box);
  if (boxes.length > 1 && fitK(boxes, size) < READABLE) {
    const t = pages.find((p) => p.id === talk) ?? pages[pages.length - 1];
    boxes = [t.box];
  }
  if (near.length && fitK([...boxes, ...near], size) >= 0.72) boxes = [...boxes, ...near];
  return fit(boxes, size, max);
}

// frameAll frames the whole graph, the home's view.
export function frameAll(points: Pt[], size: Size): Cam {
  if (points.length === 0) return { k: 1, x: size.w / 2, y: size.h / 2 };
  const bs: Box[] = points.map((p) => [p[0] - 90, p[1] - 60, p[0] + 90, p[1] + 90]);
  return fit(bs, size, 1.05, 0.25);
}
