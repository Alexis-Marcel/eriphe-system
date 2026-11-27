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

import { graph, rng } from "../src/graph/generated.ts";
import { extent, pairs, Sim, T, type LNode, type Pt } from "../src/graph/layout.ts";

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

export function run(n: number) {
  const nodes = graph(n);
  // a frame's slice, as Graph.tsx gives it (an engine without one ignores it)
  const sim = new (Sim as unknown as new (f: undefined, o: { slice: number }) => Engine)(undefined, { slice: 8 });
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

if (import.meta.main) {
  const sizes = process.argv.slice(2).map(Number).filter((v) => v > 0);
  console.log("    n settle_ms frames  block_ms   tick_ms   drag_ms overlaps    spread      link");
  for (const n of sizes.length ? sizes : [100, 250, 500, 1000, 2000, 3000]) run(n);
}
