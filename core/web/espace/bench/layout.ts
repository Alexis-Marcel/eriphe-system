// The graph layout's benchmark (npm run bench:layout): generated graphs of
// 100 to 3000 nodes shaped like a user's (subjects with their bubbles, some
// deeper, text links across, nodes alone, labels of every length, a few
// glances and open pages), laid out by src/graph/layout.ts as Graph.tsx
// drives it. For each size it prints:
//
//   - settle: the first layout, from sync to rest, in ms, and its ticks;
//   - block: the longest the main thread is held by one call while it
//     settles (sync, then a frame at a time: frame() when the engine has
//     it, else the whole settle);
//   - tick: one warm tick (energy 0.5, nothing held), mean of 20, in ms;
//   - drag: one tick while a subject is dragged, mean of 20, in ms;
//   - overlaps: pairs of bodies that sit on each other at rest (0 wanted);
//   - spread: the root mean square distance to the graph's middle, over
//     the square root of the bodies' area (how compact the disc is);
//   - link: the mean distance between linked bodies, over the median
//     between unlinked ones (sampled): how close the links hold.
//
// Nothing random: a seeded generator, the same graphs every run. Sizes on
// the command line replace the default ones (node bench/layout.ts 250 500).

import { extent, pairs, Sim, T, type LNode, type Pt } from "../src/graph/layout.ts";

// rng is a small seeded generator (mulberry32).
function rng(seed: number): () => number {
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

// overlaps counts the pairs of bodies on each other by more than the graph
// tolerates at rest (T.jam), as layout.test.ts does.
function overlaps(nodes: LNode[], p: Record<string, Pt>): number {
  const ex = nodes.map((x) => extent(x, p[x.id]));
  let c = 0;
  for (let i = 0; i < nodes.length; i++)
    for (let j = i + 1; j < nodes.length; j++) {
      const [ha, ta, ba] = ex[i], [hb, tb, bb] = ex[j];
      if (ha + hb - Math.abs(p[nodes[i].id][0] - p[nodes[j].id][0]) > T.jam + 0.5 && Math.min(ba, bb) - Math.max(ta, tb) > T.jam + 0.5) c++;
    }
  return c;
}

function shape(nodes: LNode[], p: Record<string, Pt>): { spread: number; link: number } {
  let mx = 0, my = 0, area = 0;
  for (const x of nodes) {
    mx += p[x.id][0] / nodes.length;
    my += p[x.id][1] / nodes.length;
    const [h, t, b] = extent(x, p[x.id]);
    area += 2 * h * (b - t);
  }
  let rms = 0;
  for (const x of nodes) rms += ((p[x.id][0] - mx) ** 2 + (p[x.id][1] - my) ** 2) / nodes.length;
  const d = (a: string, b: string) => Math.hypot(p[a][0] - p[b][0], p[a][1] - p[b][1]);
  const ps = pairs(nodes);
  const linked = ps.reduce((s, [a, b]) => s + d(a, b), 0) / Math.max(1, ps.length);
  const r = rng(7), sample: number[] = [];
  for (let k = 0; k < 4000; k++) sample.push(d(nodes[Math.floor(r() * nodes.length)].id, nodes[Math.floor(r() * nodes.length)].id));
  sample.sort((a, b) => a - b);
  return { spread: Math.sqrt(rms) / Math.sqrt(area), link: linked / sample[sample.length >> 1] };
}

type Engine = Sim & { frame?: () => void };

function run(n: number) {
  const nodes = graph(n);
  const sim = new Sim() as Engine;
  let block = 0, ticks = 0;
  const t0 = performance.now();
  let s = performance.now();
  sim.sync(nodes);
  block = performance.now() - s;
  if (sim.frame)
    while (!sim.asleep()) {
      s = performance.now();
      sim.frame();
      block = Math.max(block, performance.now() - s);
      ticks++;
    }
  const settle = performance.now() - t0;
  const p = sim.positions();
  const over = overlaps(nodes, p);
  const { spread, link } = shape(nodes, p);
  sim.reheat();
  s = performance.now();
  for (let k = 0; k < 20; k++) sim.tick();
  const tick = (performance.now() - s) / 20;
  const held = nodes.find((x) => !x.parent && !x.box)!.id, at = sim.at(held)!;
  s = performance.now();
  for (let k = 0; k < 20; k++) {
    sim.hold(held, [at[0] + k * 3, at[1]]);
    sim.tick();
  }
  const drag = (performance.now() - s) / 20;
  sim.drop();
  const f = (v: number, d = 1) => v.toFixed(d).padStart(9);
  console.log(`${String(n).padStart(5)} ${f(settle, 0)} ${String(sim.frame ? ticks : "-").padStart(6)} ${f(block)} ${f(tick, 2)} ${f(drag, 2)} ${String(over).padStart(8)} ${f(spread, 3)} ${f(link, 3)}`);
}

const sizes = process.argv.slice(2).map(Number).filter((v) => v > 0);
console.log("    n settle_ms frames  block_ms   tick_ms   drag_ms overlaps    spread      link");
for (const n of sizes.length ? sizes : [100, 250, 500, 1000, 2000, 3000]) run(n);
