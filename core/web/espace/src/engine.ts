import type { Composition, Ev, Item, Obj, Ref, Rule } from "./types";

// The layout engine, in code and without words: it takes a composition
// (refs and weights), the objects, the user's rules and the focus, and
// says where each module goes on a fixed grid, and what is left as a chip.
// The page never scrolls: what does not fit shrinks, then becomes a chip.

export const COLS = 12;
export const ROWS = 8;

export type Size = "xl" | "l" | "m" | "s";

// Each size is a block of cells: xl 30, l 12, m 6, s 3.
export const DIMS: Record<Size, { w: number; h: number }> = {
  xl: { w: 6, h: 5 },
  l: { w: 4, h: 3 },
  m: { w: 3, h: 2 },
  s: { w: 3, h: 1 },
};
const smaller: Record<Size, Size | null> = { xl: "l", l: "m", m: "s", s: null };
const bigger: Record<Size, Size | null> = { xl: null, l: "xl", m: "l", s: "m" };

export const sizeOf = (weight: number): Size => (weight >= 5 ? "xl" : weight >= 4 ? "l" : weight >= 3 ? "m" : "s");

// A module on the grid: col and row count from 0.
export type Placed = {
  obj: Obj;
  size: Size;
  col: number;
  row: number;
  w: number; // in cells: the size's, or wider when the cells to its right were left empty
  h: number;
  weight: number;
  why: string;
  say?: string;
  focus: boolean; // what the user opened
  related: boolean; // what relates to the focus
};

// A chip: what the composition put aside, or what did not fit.
export type Chip = { obj: Obj; why: string; from: "composition" | "overflow" };

export type Layout = { placed: Placed[]; chips: Chip[] };

// What a rule applies to, from the narrowest key to the widest.
export function keysOf(o: Obj): string[] {
  switch (o.type) {
    case "mail": {
      const domain = o.from.match(/@([^\s>]+)/)?.[1]?.toLowerCase();
      return domain ? [`mail:${domain}`, "mail"] : ["mail"];
    }
    case "alert":
      return [`alert:${o.monitor}`, "alert"];
    default:
      return [o.type];
  }
}

// The weight a rule adds to an object: "more" is +1, "less" -1, "never" -5.
export function ruleDelta(o: Obj, rules: Rule[]): number {
  const keys = keysOf(o);
  return rules.filter((r) => keys.includes(r.key)).reduce((s, r) => s + r.delta, 0);
}

// The dates an object is about.
export function datesOf(o: Obj): string[] {
  switch (o.type) {
    case "card":
      return o.input.start ? [o.input.start.slice(0, 10)] : [];
    case "event":
      return [o.start.slice(0, 10)];
    case "agenda":
      return [o.date];
    default:
      return [];
  }
}

// Relations, computed, never declared by the composition: what points to
// an object, and the agenda of the days it is about.
export function related(focus: Obj, all: Obj[]): Ref[] {
  const out = new Set<Ref>();
  for (const o of all) {
    if (o.ref === focus.ref) continue;
    if (o.type === "card" && o.about === focus.ref) out.add(o.ref);
    if (focus.type === "card" && focus.about === o.ref) out.add(o.ref);
    if (o.type === "choice" && o.session === focus.ref) out.add(o.ref);
    if (focus.type === "choice" && focus.session === o.ref) out.add(o.ref);
    if (focus.type === "group" && focus.refs.includes(o.ref)) out.add(o.ref);
  }
  const dates = new Set([focus, ...all.filter((o) => out.has(o.ref))].flatMap(datesOf));
  for (const o of all) if (o.type === "agenda" && dates.has(o.date)) out.add(o.ref);
  out.delete(focus.ref);
  return [...out];
}

// The proposals (cards) that land on a day, and whether they overlap what
// is already there: an agenda shows them as ghosts.
export function ghostsFor(date: string, all: Obj[]): { ev: Ev; ref: Ref; state: string; conflict: boolean }[] {
  const day = all.find((o): o is Extract<Obj, { type: "agenda" }> => o.type === "agenda" && o.date === date);
  const out = [];
  for (const o of all) {
    if (o.type !== "card" || !o.input.start || o.input.start.slice(0, 10) !== date) continue;
    const ev: Ev = {
      start: o.input.start.slice(11, 16),
      end: o.input.end?.slice(11, 16),
      title: o.input.title ?? o.text,
      where: o.input.location,
    };
    const conflict = !!day?.events.some((e) => e.start < (ev.end ?? ev.start) && ev.start < (e.end ?? e.start));
    out.push({ ev, ref: o.ref, state: o.state, conflict });
  }
  return out;
}

// pack puts each module, in order, at the first place where it fits, top
// left first; a module that fits nowhere shrinks, then is left out (null).
export function pack(sizes: Size[]): ({ size: Size; col: number; row: number } | null)[] {
  const grid = Array.from({ length: ROWS }, () => new Array<boolean>(COLS).fill(false));
  const free = (col: number, row: number, w: number, h: number) => {
    for (let r = row; r < row + h; r++) for (let c = col; c < col + w; c++) if (grid[r][c]) return false;
    return true;
  };
  return sizes.map((want) => {
    for (let size: Size | null = want; size; size = smaller[size]) {
      const { w, h } = DIMS[size];
      for (let row = 0; row + h <= ROWS; row++) {
        for (let col = 0; col + w <= COLS; col++) {
          if (!free(col, row, w, h)) continue;
          for (let r = row; r < row + h; r++) for (let c = col; c < col + w; c++) grid[r][c] = true;
          return { size, col, row };
        }
      }
    }
    return null;
  });
}

// widen stretches modules to the right over columns nobody uses on their
// rows, so the sizes' widths (6, 4, 3) leave no empty strip at the edge.
function widen(placed: Placed[]) {
  const used = (col: number, row: number, h: number, self: Placed) =>
    placed.some((p) => p !== self && col >= p.col && col < p.col + p.w && row < p.row + p.h && p.row < row + h);
  for (const p of placed) {
    while (p.col + p.w < COLS && !used(p.col + p.w, p.row, p.h, p)) p.w++;
  }
}

type Want = { obj: Obj; weight: number; why: string; say?: string; focus: boolean; related: boolean; chip: boolean };

// place lays a composition out. The composer gives weights; the user's rules
// move them; a focus takes the centre and pulls its relations next to it.
// One xl at most; weight 0 is not shown; a ref the composer invented is
// dropped here, by code.
export function place(comp: Composition, store: Map<Ref, Obj>, rules: Rule[] = [], focus: Ref | null = null, extra: Item[] = []): Layout {
  const items = [...comp.items, ...extra.filter((e) => !comp.items.some((i) => i.ref === e.ref))];
  const chipRefs = (comp.chips ?? []).filter((r) => store.has(r));
  const present = new Set([...items.map((i) => i.ref), ...chipRefs]);
  const focused = focus ? store.get(focus) : undefined;
  // A focus pulls in what relates to it among what is there, and its agenda.
  const rel = focused
    ? related(focused, [...store.values()]).filter((r) => present.has(r) || store.get(r)?.type === "agenda")
    : [];

  const wants: Want[] = [];
  const seen = new Set<Ref>();
  for (const i of items) {
    const obj = store.get(i.ref);
    if (!obj || seen.has(i.ref)) continue;
    seen.add(i.ref);
    let weight = Math.max(0, Math.min(5, Math.round(i.weight) + ruleDelta(obj, rules)));
    const isRel = rel.includes(i.ref);
    if (i.ref === focus) weight = 6;
    else if (focused && weight > 0) weight = isRel ? 3 : Math.min(weight, 2);
    wants.push({ obj, weight, why: i.why, say: i.say, focus: i.ref === focus, related: isRel, chip: false });
  }
  for (const r of chipRefs) {
    if (seen.has(r)) continue;
    seen.add(r);
    const obj = store.get(r)!;
    const isFocus = r === focus, isRel = rel.includes(r);
    wants.push({ obj, weight: isFocus ? 6 : isRel ? 3 : 0, why: "", focus: isFocus, related: isRel, chip: !isFocus && !isRel });
  }
  if (focused && !seen.has(focused.ref)) wants.push({ obj: focused, weight: 6, why: "", focus: true, related: false, chip: false });
  for (const r of rel) {
    if (seen.has(r) || r === focus) continue;
    seen.add(r);
    wants.push({ obj: store.get(r)!, weight: 3, why: "", focus: false, related: true, chip: false });
  }

  const chips: Chip[] = wants.filter((w) => w.chip).map((w) => ({ obj: w.obj, why: w.why, from: "composition" }));
  const shown = wants.filter((w) => !w.chip && w.weight > 0).sort((a, b) => b.weight - a.weight);

  let xl = false;
  const sizes = shown.map((w) => {
    let size = sizeOf(w.weight);
    if (size === "xl") {
      if (xl) size = "l";
      xl = true;
    }
    return size;
  });
  let spots = pack(sizes);
  // Room left: what matters (weight 3 and up) grows one step, most important
  // first, so a quiet screen is not half empty. Never into xl: xl is for
  // what is urgent or opened. A step is kept only if nothing else shrinks.
  for (let i = 0; i < shown.length; i++) {
    const spot = spots[i];
    if (!spot || shown[i].weight < 3) continue;
    const up = bigger[spot.size];
    if (!up || up === "xl") continue;
    const trial = spots.map((s, j) => (j === i ? up : s ? s.size : sizes[j]));
    const next = pack(trial);
    if (next.every((s, j) => (spots[j] ? s?.size === trial[j] : !s))) spots = next;
  }

  const placed: Placed[] = [];
  shown.forEach((w, i) => {
    const s = spots[i];
    if (!s) {
      chips.push({ obj: w.obj, why: w.why, from: "overflow" });
      return;
    }
    placed.push({ obj: w.obj, size: s.size, col: s.col, row: s.row, ...DIMS[s.size], weight: w.weight, why: w.why, say: w.say, focus: w.focus, related: w.related });
  });
  widen(placed);
  return { placed, chips };
}
