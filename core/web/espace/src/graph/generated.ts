// Generated graphs for the layout's tests and benchmark (layout.test.ts,
// bench/layout.ts): any size, shaped like a user's, the same every time.
// Not in the built app.

import type { LNode } from "./layout";

// rng is a small seeded generator (mulberry32).
export function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// graph gives n nodes: about one in eight a subject with 2 to 12 bubbles
// (some with their own), one in ten alone, about one in twelve linking
// across to another node; labels from 4 to 40 characters; one in forty a
// glance; two pages open.
export function graph(n: number, seed = 1): LNode[] {
  const r = rng(seed);
  const out: LNode[] = [];
  const title = (root: boolean) => {
    const len = 4 + Math.floor(r() * 36);
    return { lw: Math.min(300, len * (root ? 8.4 : 7.4) + 8), lh: root ? (len > 22 ? 44 : 22) : 18 };
  };
  const roots: string[] = [];
  while (out.length < n) {
    const i = out.length;
    if (r() < 0.1 && roots.length) {
      out.push({ id: `a${i}`, links: [], r: 26 + r() * 14, ...title(true) });
      continue;
    }
    const root = `s${i}`;
    out.push({ id: root, links: [], r: 30 + r() * 10, ...title(true) });
    roots.push(root);
    const kids = 2 + Math.floor(r() * 11);
    for (let k = 0; k < kids && out.length < n; k++) {
      const id = `s${i}c${k}`;
      out.push({ id, parent: root, links: [], r: 12 + r() * 6, ...title(false) });
      if (r() < 0.15 && out.length < n) out.push({ id: `${id}g`, parent: id, links: [], r: 12, ...title(false) });
    }
  }
  for (let i = 0; i < out.length; i++) {
    const x = out[i];
    if (r() < 0.08) x.links.push(out[Math.floor(r() * out.length)].id);
    if (r() < 0.025) {
      x.tile = [148, 60];
      x.r = 74;
    }
  }
  for (const id of [roots[0], roots[Math.min(roots.length - 1, 3)]]) {
    const x = out.find((o) => o.id === id)!;
    x.box = [620, 480];
  }
  return out;
}
