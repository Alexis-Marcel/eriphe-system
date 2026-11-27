// A big graph's continents (lod.ts, LOD.zones): each family (a root and
// what is under it) of at least MIN_FAMILY nodes is a soft zone of its
// colour behind its nodes, and far out its name, large, in its middle. A
// zone is the union of its nodes' discs widened by a margin, blurred
// (paint.ts draws it, at a low rate, in the graph's units: a pan or a zoom
// only moves the picture); a node far from the rest of its family (linked
// across the graph) does not stretch it.

export const MIN_FAMILY = 5;
// A member further from its family's middle than TRIM times the distance
// most of the family is within (QUANTILE of it) is left out of its zone.
export const QUANTILE = 0.85;
export const TRIM = 1.7;

// A family drawn as a zone: its root and its colour, its members (indices
// in the scene's order), whether one of them waits for the user.
export type Family = { root: number; fam: number; members: Int32Array; waits: boolean };

// zoneFamilies is the families big enough to be a zone, the biggest first;
// root: each node's root's index; fam: its colour (-1: none).
export function zoneFamilies(root: ArrayLike<number>, fam: ArrayLike<number>, waits: (i: number) => boolean, min = MIN_FAMILY): Family[] {
  const by = new Map<number, number[]>();
  for (let i = 0; i < root.length; i++) {
    const r = root[i];
    if (r < 0) continue;
    const m = by.get(r);
    if (m) m.push(i);
    else by.set(r, [i]);
  }
  const out: Family[] = [];
  for (const [r, m] of by) {
    if (m.length < min || fam[r] < 0) continue;
    out.push({ root: r, fam: fam[r], members: Int32Array.from(m), waits: m.some(waits) });
  }
  return out.sort((a, b) => b.members.length - a.members.length || a.root - b.root);
}

// A zone where its family is now: the members it covers (those placed,
// not too far), its middle, its spread.
export type Shape = { keep: Int32Array; cx: number; cy: number; spread: number };

// zoneShape is a family's zone for the positions xy (x then y, by index).
export function zoneShape(f: Family, xy: ArrayLike<number>): Shape {
  const xs: number[] = [], ys: number[] = [];
  for (const i of f.members) {
    const x = xy[i * 2], y = xy[i * 2 + 1];
    if (x !== x || y !== y) continue;
    xs.push(x);
    ys.push(y);
  }
  if (!xs.length) return { keep: new Int32Array(0), cx: 0, cy: 0, spread: 0 };
  // the distances from its median point: a far member does not pull it
  const mid = (v: number[]) => [...v].sort((a, b) => a - b)[v.length >> 1];
  const mx = mid(xs), my = mid(ys);
  const d = xs.map((x, j) => Math.hypot(x - mx, ys[j] - my)).sort((a, b) => a - b);
  const cut = Math.max(1, d[Math.floor((d.length - 1) * QUANTILE)] * TRIM);
  const keep: number[] = [];
  let sx = 0, sy = 0;
  for (const i of f.members) {
    const x = xy[i * 2], y = xy[i * 2 + 1];
    if (x !== x || y !== y || Math.hypot(x - mx, y - my) > cut) continue;
    keep.push(i);
    sx += x;
    sy += y;
  }
  return { keep: Int32Array.from(keep), cx: sx / keep.length, cy: sy / keep.length, spread: d[d.length >> 1] };
}

// padOf is a zone's margin round each of its nodes, in the graph's
// units: at least min, and wider for a family spread wide (a share of how
// far its nodes are from its middle), so that its zone is one region, not
// a spot round each node.
export function padOf(sh: Shape, min: number): number {
  return Math.max(min, Math.min(min * 8, sh.spread * 0.4));
}

// zoneAlpha is how much of the zones shows at scale k: all of it far out,
// fading as the nodes become readable, none close (where the nodes speak
// for themselves).
export function zoneAlpha(k: number): number {
  return Math.max(0, Math.min(1, 1 - (k - 0.15) / 0.6));
}

// declutter is which of the boxes (x0, y0, x1, y1), in their order (the
// most important first), are drawn: those that do not overlap one drawn
// before them, within margin.
export function declutter(boxes: readonly [number, number, number, number][], margin = 6): boolean[] {
  const kept: [number, number, number, number][] = [];
  return boxes.map((b) => {
    const hit = kept.some((k) => b[0] < k[2] + margin && b[2] > k[0] - margin && b[1] < k[3] + margin && b[3] > k[1] - margin);
    if (!hit) kept.push(b);
    return !hit;
  });
}

// nameAlpha is how much of a family's name shows: all of it while its
// root is too small for its own label, none once that label shows whole
// (labelAlpha of the root's radius on screen): the two cross-fade.
export function nameAlpha(rootLabel: number): number {
  return 1 - rootLabel;
}
