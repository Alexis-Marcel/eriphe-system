import type { Pt } from "./layout";

// Where the camera looks: a scale k and the world's offset on screen.
export type Cam = { k: number; x: number; y: number };
export type Box = [number, number, number, number]; // left, top, right, bottom, in the world
// foot: the room the bar takes at the screen's foot, counted when well over
// BOTTOM (cards in it, a long question unfolded).
export type Size = { w: number; h: number; foot?: number };

// The screen's free band, between the corner's icons at the top and the
// bar (its field, the last question, Eriphe's line: always kept, so the
// camera does not move when that line goes).
export const TOP = 60;
export const BOTTOM = 160;
const footOf = (size: Size) => ((size.foot ?? 0) > BOTTOM + 24 ? size.foot! : BOTTOM);

// pageMax is the tallest a page may be on this screen: within the free
// band at scale 1, so that framed it is whole, its head on screen and its
// foot above the bar.
export const pageMax = (size: Size, most: number) => Math.round(Math.max(240, Math.min(most, size.h - TOP - footOf(size) - 40)));
export const SIDE = 20;

// READABLE is the smallest scale at which several open pages are framed
// together; below it, the camera frames the page in focus, the
// others stay open in place, a drag or a zoom away.
export const READABLE = 0.8;

const fitK = (bs: Box[], size: Size) => {
  const w = Math.max(...bs.map((b) => b[2])) - Math.min(...bs.map((b) => b[0])) + 60;
  const h = Math.max(...bs.map((b) => b[3])) - Math.min(...bs.map((b) => b[1])) + 40;
  return Math.min((size.w - 2 * SIDE) / w, (size.h - TOP - footOf(size)) / h);
};

// fit frames boxes in the free band, at most at scale max.
export function fit(bs: Box[], size: Size, max = 1, min = 0.3): Cam {
  const minX = Math.min(...bs.map((b) => b[0])) - 30, maxX = Math.max(...bs.map((b) => b[2])) + 30;
  const minY = Math.min(...bs.map((b) => b[1])) - 20, maxY = Math.max(...bs.map((b) => b[3])) + 20;
  const left = SIDE, right = size.w - SIDE, top = TOP, bottom = size.h - footOf(size);
  const k = Math.max(min, Math.min(max, (right - left) / (maxX - minX), (bottom - top) / (maxY - minY)));
  return { k, x: (left + right) / 2 - ((minX + maxX) / 2) * k, y: (top + bottom) / 2 - ((minY + maxY) / 2) * k };
}

export const boxAt = (at: Pt, [w, h]: Pt): Box => [at[0] - w / 2, at[1] - h / 2, at[0] + w / 2, at[1] + h / 2];

// ALL is the smallest scale at which the camera frames the whole graph
// with the open pages.
export const ALL = 0.72;

// framePages frames the open pages: all of them if they fit at READABLE or
// more, else the one in focus (the last one opened, or the one the user
// last turned to), else the last one opened (pages in the order they
// opened); with the whole graph (all) while the pages stay readable, else
// the bubbles around them (near). The room left on screen goes to the
// rest of the graph: the view slides toward it (slide), so that no bubble
// is cut at an edge while there is room at the other.
export function framePages(pages: { id: string; box: Box }[], focus: string | null, near: Box[], size: Size, max = 1, all: Box[] = []): Cam {
  let boxes = pages.map((p) => p.box);
  if (boxes.length > 1 && fitK(boxes, size) < READABLE) {
    const t = pages.find((p) => p.id === focus) ?? pages[pages.length - 1];
    boxes = [t.box];
  }
  if (all.length && fitK([...boxes, ...all], size) >= ALL) boxes = [...boxes, ...all];
  else if (near.length && fitK([...boxes, ...near], size) >= ALL) boxes = [...boxes, ...near];
  return slide(fit(boxes, size, max), boxes, [...boxes, ...all], size);
}

// frameAlone frames one page alone, at scale 1 at most: an enlarged page,
// whole and readable, with nothing pulled in that would shrink it.
export const frameAlone = (box: Box, size: Size): Cam => fit([box], size, 1);

// bigBox is an enlarged page's size on this screen: most of it, above
// the bar, a margin left on each side where the graph shows, framed
// whole at scale 1 (frameAlone).
export const bigBox = (size: Size, least: number): Pt => [Math.round(Math.max(least, Math.min(1500, size.w - 2 * SIDE - 200))), pageMax(size, 4000)];

// bigRoom is the room an enlarged page keeps clear around it in the graph:
// framed alone, its foot is just above the bar, so a bubble under it
// would sit behind the bar. Its box grows, above and below alike (its
// centre stays), by the bar's height and a margin: the bubbles stand
// beside it, in the margins the screen leaves on each side.
export const bigRoom = ([w, h]: Pt, size: Size): Pt => [w, h + 2 * (footOf(size) + 40)];

// slide moves the camera, at its scale, toward the middle of the whole
// graph (all), as far as the boxes it frames (keep) stay whole in the free
// band.
export function slide(cam: Cam, keep: Box[], all: Box[], size: Size): Cam {
  if (!keep.length || !all.length) return cam;
  const { k } = cam;
  const band = (lo: number, hi: number, k0: number, k1: number, a0: number, a1: number, at: number) => {
    const min = lo - k0 * k, max = hi - k1 * k; // the offsets that keep the kept boxes whole
    if (min > max) return at;
    return Math.max(min, Math.min(max, (lo + hi) / 2 - ((a0 + a1) / 2) * k));
  };
  const span = (bs: Box[], i: number, j: number, m: number): Pt => [Math.min(...bs.map((b) => b[i])) - m, Math.max(...bs.map((b) => b[j])) + m];
  const [kx0, kx1] = span(keep, 0, 2, 30), [ky0, ky1] = span(keep, 1, 3, 20);
  const [ax0, ax1] = span(all, 0, 2, 30), [ay0, ay1] = span(all, 1, 3, 20);
  return { k, x: band(SIDE, size.w - SIDE, kx0, kx1, ax0, ax1, cam.x), y: band(TOP, size.h - footOf(size), ky0, ky1, ay0, ay1, cam.y) };
}

// FLOOR is the smallest scale the camera takes by itself on a page in
// focus: pages stay readable. Below it, only the user's wheel or pinch.
export const FLOOR = 0.9;

// frameAround frames the page in focus, readable: at scale 1, or as much
// less as it takes to show it whole, never below FLOOR; in the middle of
// the free band, slid toward its neighbours (near) as far as it stays
// whole. What does not fit stays off screen, its edge mark showing the way
// to what waits.
export function frameAround(focus: Box, near: Box[], size: Size): Cam {
  const cx = (focus[0] + focus[2]) / 2, cy = (focus[1] + focus[3]) / 2;
  const left = SIDE, right = size.w - SIDE, top = TOP, bottom = size.h - footOf(size);
  const k = Math.max(FLOOR, Math.min(1, (right - left) / (focus[2] - focus[0] + 60), (bottom - top) / (focus[3] - focus[1] + 40)));
  const cam = { k, x: (left + right) / 2 - cx * k, y: (top + bottom) / 2 - cy * k };
  return near.length ? slide(cam, [focus], [focus, ...near], size) : cam;
}

// frameAll frames the whole graph, the home's view: each body whole, a
// bubble with its label under it.
export function frameAll(boxes: Box[], size: Size): Cam {
  if (boxes.length === 0) return { k: 1, x: size.w / 2, y: size.h / 2 };
  return fit(boxes, size, 1.05, 0.25);
}

// EDGE is how far from the screen's edges an edge mark stays; FOOT, the
// room the console takes at the foot.
export const EDGE = 18;
export const FOOT = 120;

// edgeMark is where the mark of a node out of view sits on screen (the
// node at sx, sy on screen): at the nearest edge, turned toward it
// (radians); null when the node is in view.
export function edgeMark(sx: number, sy: number, size: Size): { x: number; y: number; turn: number } | null {
  if (sx >= 0 && sx <= size.w && sy >= 0 && sy <= size.h - FOOT) return null;
  const x = Math.min(size.w - EDGE, Math.max(EDGE, sx)), y = Math.min(size.h - FOOT, Math.max(EDGE, sy));
  return { x, y, turn: Math.atan2(sy - y, sx - x) };
}
