import type { FoldedChip, Layout, Tile, TileSize } from "./types";

// Where the page itself places tiles, over eriphe-core's layout
// (workspace_layout.go), for two things core cannot know at once:
//
//   - fitted: how tall a tile's content is at its width, on this screen. A
//     tile on the grid never scrolls and never clips a sentence: one whose
//     content needs more rows than core gave it grows, in core's order,
//     where the grid has room; what no longer fits is folded into the chip
//     strip. Measured by the page (Grid.tsx), so each screen fits its own
//     tiles; core's layout, and what the brain reads of it, stay core's.
//   - focusedIn: a surface the user opens, laid out at once by core's
//     stage rule (it in front, the one on stage before it reduced next to
//     it, still live), while core's own layout is on its way; and
//     unfocused, the way back.
//
// Both pack the way core does (packTilesAt, widenTiles): each tile at the
// first place where it fits, top left first; one that fits nowhere shrinks
// a size, then is folded.

export const COLS = 12;
export const ROWS = 8;
export const SIZES: TileSize[] = ["s", "m", "l", "xl"];
export const DIMS: Record<TileSize, { w: number; h: number }> = { s: { w: 3, h: 1 }, m: { w: 3, h: 2 }, l: { w: 4, h: 3 }, xl: { w: 6, h: 5 } };
const SMALLER: Record<TileSize, TileSize | null> = { xl: "l", l: "m", m: "s", s: null };

type Box = { col: number; row: number; w: number; h: number };

// cells is the grid's occupancy.
function cells() {
  const taken = Array.from({ length: ROWS }, () => new Array<boolean>(COLS).fill(false));
  const free = (b: Box) => {
    if (b.col < 0 || b.row < 0 || b.col + b.w > COLS || b.row + b.h > ROWS) return false;
    for (let r = b.row; r < b.row + b.h; r++) for (let c = b.col; c < b.col + b.w; c++) if (taken[r][c]) return false;
    return true;
  };
  const take = (b: Box) => {
    for (let r = b.row; r < b.row + b.h; r++) for (let c = b.col; c < b.col + b.w; c++) taken[r][c] = true;
  };
  // first is the first free place for w × h, top left first.
  const first = (w: number, h: number): Box | null => {
    for (let row = 0; row + h <= ROWS; row++) for (let col = 0; col + w <= COLS; col++) if (free({ col, row, w, h })) return { col, row, w, h };
    return null;
  };
  return { free, take, first };
}

const chipOf = (t: Tile): FoldedChip => ({ id: t.id, title: t.title, why: t.why });

// fitted is core's layout with each tile as tall as its content needs
// (needs: rows by surface id, measured at the tile's width). The identity
// when every tile already holds its content. In core's order, a tile that
// needs more grows: as tall as it needs, at its place when the rows under
// it are free, else at the first place where it fits as tall; else a row
// less, and so on down to core's height; one with no place left is
// folded, first of the chips. The surface open, and a tile the user
// placed, keep their cells, taken first: the one scrolls, the other shows
// what it can (Grid.tsx).
export function fitted(l: Layout, needs: Record<string, number>): Layout {
  const kept = (t: Tile) => t.id === l.focus || !!t.pinned;
  const grows = (t: Tile) => !kept(t) && (needs[t.id] ?? 0) > t.h;
  if (!l.items.some(grows)) return l;
  const g = cells();
  // The surface open and what the user placed keep their cells: taken first.
  for (const t of l.items) if (kept(t)) g.take(t);
  const items: Tile[] = [];
  const folded: FoldedChip[] = [];
  for (const t of l.items) {
    if (kept(t)) {
      items.push(t);
      continue;
    }
    const at = (h: number): Box => ({ col: t.col, row: t.row, w: t.w, h });
    let box: Box | null = null;
    // As tall as it needs, then a row less each time: at its place, else the first free one.
    for (let h = grows(t) ? Math.min(needs[t.id], ROWS) : t.h; h >= t.h && !box; h--) box = g.free(at(h)) ? at(h) : g.first(t.w, h);
    if (!box) {
      folded.push(chipOf(t));
      continue;
    }
    g.take(box);
    items.push({ ...t, ...box });
  }
  return { ...l, items, chips: [...folded, ...l.chips] };
}

// widen stretches tiles to the right over columns nobody uses on their
// rows, as core does; a tile the user placed keeps its width.
export function widen(tiles: Tile[]): Tile[] {
  const out = tiles.map((t) => ({ ...t }));
  const used = (col: number, row: number, h: number, self: number) =>
    out.some((t, i) => i !== self && col >= t.col && col < t.col + t.w && row < t.row + t.h && t.row < row + h);
  out.forEach((t, i) => {
    while (!t.pinned && t.col + t.w < COLS && !used(t.col + t.w, t.row, t.h, i)) t.w++;
  });
  return out;
}

// focusedIn is the layout with a surface opened, at once, by core's stage
// rule (layoutStage, workspace_layout.go): what the user placed at its
// place, what is urgent, then the surface opened, l at least, then the one
// that was on stage before it (previous), m at most, next to it and never
// folded for the others, then the other tiles in core's order at their
// size; each at the first place where it fits, top left first, shrinking a
// size when it fits nowhere, folded last. A chip opened comes out of the
// strip. Core's composition around it may then make it larger.
export function focusedIn(l: Layout, id: string): Layout {
  const tile = l.items.find((t) => t.id === id);
  const chip = l.chips.find((c) => c.id === id);
  if (!tile && !chip) return { ...l, focus: id, say: undefined };
  const open: Tile = tile ?? { id, title: chip!.title, why: chip!.why, size: "l", col: 0, row: 0, w: 4, h: 3 };
  const prev = l.focus && l.focus !== id ? l.items.find((t) => t.id === l.focus) : undefined;
  const others = l.items.filter((t) => t.id !== id && t.id !== prev?.id);
  const pinned = others.filter((t) => t.pinned);
  const urgent = others.filter((t) => !t.pinned && t.urgent);
  const rest = others.filter((t) => !t.pinned && !t.urgent);
  const order = open.pinned ? [open, ...pinned, ...urgent] : [...pinned, ...urgent, open];
  if (prev) order.push(prev);
  order.push(...rest);
  const g = cells();
  // What the user placed holds its place first, as in core (packTilesAt).
  const at = new Map<string, Box>();
  for (const t of order) {
    const box = { col: t.col, row: t.row, w: DIMS[t.size].w, h: DIMS[t.size].h };
    if (t.pinned && g.free(box)) {
      g.take(box);
      at.set(t.id, box);
    }
  }
  const items: Tile[] = [];
  const folded: FoldedChip[] = [];
  let xl = false;
  for (const t of order) {
    let size: TileSize = t.size;
    if (t.id === id && !t.pinned && SIZES.indexOf(size) < SIZES.indexOf("l")) size = "l";
    if (t.id === prev?.id && !t.pinned && SIZES.indexOf(size) > SIZES.indexOf("m")) size = "m";
    if (size === "xl" && xl) size = "l";
    let box: Box | null = at.get(t.id) ?? null;
    for (let s: TileSize | null = size; s && !box; s = SMALLER[s]) {
      box = g.first(DIMS[s].w, DIMS[s].h);
      if (box) size = s;
    }
    if (!box) {
      folded.push(chipOf(t));
      continue;
    }
    if (size === "xl") xl = true;
    if (!at.has(t.id)) g.take(box);
    items.push({ ...t, ...box, size, related: undefined });
  }
  const placed = new Set(items.map((t) => t.id));
  return {
    ...l,
    focus: placed.has(id) ? id : undefined,
    focusBy: undefined,
    previous: prev && placed.has(prev.id) ? prev.id : undefined,
    say: undefined,
    items: widen(items),
    chips: [...folded, ...l.chips.filter((c) => c.id !== id || !placed.has(id))],
  };
}

// unfocused is the layout when the focus ends: the one before it when the
// page kept it, else the same with nothing open, until core's comes.
export function unfocused(l: Layout, before: Layout | null): Layout {
  return before ?? { ...l, focus: undefined, say: undefined, items: l.items.map(({ related: _, ...i }) => i) };
}

// needRows is how many rows a tile needs: its chrome (what is not its
// body) and its content's natural height, on cells of cell px apart by
// gap px. A pixel of rounding is forgiven.
export function needRows(tilePx: number, bodyPx: number, contentPx: number, cell: number, gap: number): number {
  if (cell <= 0) return 0;
  const px = tilePx - bodyPx + contentPx;
  return Math.max(1, Math.ceil((px - 1 + gap) / (cell + gap)));
}

// FOLDABLE are the blocks a tile with too little room hides, the last
// first, so that what it shows is whole: never a sentence cut in two.
export const FOLDABLE =
  "li, tr, .a2-column > *, .a2-card-body > *, .a2-markdown > *, .a2-tabpanel > *, .a2-proposal-why, .a2-proposal-fields, .a2-message-excerpt, .a2-chart, .a2-legend, .a2-day-allday";

// foldOut hides (class fit-out) the content's last blocks until it holds
// in room px, the first block always kept; it tells whether it hid any.
// height reads an element's height (offsetHeight; the tests give theirs).
export function foldOut(content: HTMLElement, room: number, height: (el: HTMLElement) => number = (el) => el.offsetHeight): boolean {
  // The last first, the innermost first (a block's own last blocks go
  // before it); never the first innermost block, nor what holds it.
  const blocks = Array.from(content.querySelectorAll<HTMLElement>(FOLDABLE));
  const lead = blocks.find((b) => !blocks.some((o) => o !== b && b.contains(o)));
  let hid = false;
  for (let i = blocks.length - 1; i >= 0 && height(content) > room + 1; i--) {
    if (!lead || blocks[i].contains(lead) || blocks[i].closest(".fit-out")) continue;
    blocks[i].classList.add("fit-out");
    hid = true;
  }
  return hid;
}
