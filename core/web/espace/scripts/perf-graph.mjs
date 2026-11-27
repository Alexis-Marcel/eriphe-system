// perf-graph measures how smoothly the graph draws (npm run perf:graph):
// it serves the app with vite (its dev server, on a port of its own), opens
// ?leger (src/dev-leger.tsx) at each size in Chromium, and records, through
// the page's own frames and a PerformanceObserver of long tasks:
//   - settle: the first 3 s after the graph is drawn, the layout moving;
//   - rest: the time the layout took to first come to rest (data-still);
//   - pan: 3 s of a steady wheel pan across the graph once at rest, the
//     render alone; zoom: 3 s of wheel pans and zooms, in and out;
// and for each phase the main thread's busy time as Chromium counts it
// (Performance.getMetrics: tasks, scripts, layout, style), steadier than
// frames per second on a machine doing other things.
// Not part of CI: a machine's figures, to compare a change with the code
// before it. Options: --sizes 25,100,… --root <an espace folder> (another
// checkout, to measure it with the same script) --json <file> --port <n>
// --throttle <k> (Chromium's CPU slowed k times, as on a modest laptop)
// --react dev (React's development build; by default its production one,
// as the built app has it: ?leger, a dev page, is still served).

import { chromium } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { createServer } from "vite";

const arg = (name, def) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : def;
};
const sizes = arg("sizes", "25,100,250,500,1000,2000,3000").split(",").map(Number);
const root = resolve(arg("root", "."));
const port = Number(arg("port", "5310"));
const json = arg("json", "");
const throttle = Number(arg("throttle", "1"));
// React as in production (the dev server otherwise serves its development
// build, several times slower): the dev page stays on (import.meta.env.DEV).
const prodReact = arg("react", "prod") !== "dev";
if (prodReact) process.env.NODE_ENV = "production";

const server = await createServer({
  root,
  configFile: resolve(root, "vite.config.ts"),
  server: { port, strictPort: true, host: "127.0.0.1", hmr: false },
  define: prodReact ? { "import.meta.env.DEV": "true" } : {},
  cacheDir: resolve(root, "node_modules/.vite-perf-" + (prodReact ? "prod" : "dev")),
  logLevel: "error",
});
let browser;
const rows = [];
try {
  await server.listen();
  browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, locale: "fr-FR", colorScheme: "light" });
  const cdp = await page.context().newCDPSession(page);
  if (throttle > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: throttle });
  await cdp.send("Performance.enable");
  const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map((m) => [m.name, m.value]));
  // busy runs f and says how long the main thread worked meanwhile, in ms.
  const busy = async (f) => {
    const a = await metrics();
    const out = await f();
    const b = await metrics();
    const d = (k) => Math.round((b[k] - a[k]) * 1000);
    // and how many layouts and style recalculations it ran: counts, the
    // same however busy the machine is.
    const c = (k) => b[k] - a[k];
    return { ...out, task: d("TaskDuration"), script: d("ScriptDuration"), layout: d("LayoutDuration"), style: d("RecalcStyleDuration"), layouts: c("LayoutCount"), styles: c("RecalcStyleCount"), dom: b.Nodes };
  };
  // The first load compiles the modules: once, before measuring.
  await page.goto(`http://127.0.0.1:${port}/?leger&n=25`);
  await page.waitForFunction(() => window.leger, null, { timeout: 60_000 });
  for (const n of sizes) {
    await page.goto(`http://127.0.0.1:${port}/?leger&n=${n}`);
    await page.waitForFunction(() => window.leger, null, { timeout: 180_000, polling: 250 });
    const settle = await busy(() => page.evaluate(() => window.leger.sample(3000)));
    page.setDefaultTimeout(180_000);
    const cap = n >= 1000 ? 120_000 : 30_000;
    const rested = await page.waitForFunction(() => window.leger.stats().stable !== null, null, { timeout: cap, polling: 200 }).then(() => true, () => false);
    const stable = await page.evaluate(() => window.leger.stats().stable);
    const pan = await busy(() => page.evaluate(() => window.leger.sample(3000, window.leger.pan)));
    const bubbles = await page.evaluate(() => window.leger.stats().bubbles);
    const zoom = await busy(() => page.evaluate(() => window.leger.sample(3000, window.leger.panZoom)));
    const row = { n, stable: rested ? stable : null, settle, pan, zoom, bubbles };
    rows.push(row);
    console.error(`${n}: rest ${rested ? `${stable} ms` : "not within " + cap / 1000 + " s"}, settle ${settle.fps} fps (worst ${settle.worst} ms), pan ${pan.fps} fps (p95 ${pan.p95}, worst ${pan.worst} ms, long ${pan.longTasks}/${pan.longMs} ms, busy ${pan.task} ms: script ${pan.script}, layout ${pan.layout}, style ${pan.style}, ${pan.layouts} layouts), zoom ${zoom.fps} fps (p95 ${zoom.p95}, busy ${zoom.task} ms), settle busy ${settle.task} ms (${settle.layouts} layouts), ${bubbles} bubbles, ${pan.dom} DOM nodes`);
  }
} finally {
  await browser?.close();
  await server.close();
}

const ms = (x) => (x === null ? "no rest" : `${(x / 1000).toFixed(1)} s`);
console.log("| nodes | to rest | settle fps (worst ms) | settle busy ms (layouts) | pan fps (p95 / worst ms) | pan busy ms: script / layout / style (layouts) | zoom fps (p95 ms) | bubbles in DOM | DOM nodes |");
console.log("|---:|---:|---:|---:|---:|---:|---:|---:|---:|");
for (const r of rows)
  console.log(`| ${r.n} | ${ms(r.stable)} | ${r.settle.fps} (${r.settle.worst}) | ${r.settle.task} (${r.settle.layouts}) | ${r.pan.fps} (${r.pan.p95} / ${r.pan.worst}) | ${r.pan.task}: ${r.pan.script} / ${r.pan.layout} / ${r.pan.style} (${r.pan.layouts}) | ${r.zoom.fps} (${r.zoom.p95}) | ${r.bubbles} | ${r.pan.dom} |`);
if (json) writeFileSync(json, JSON.stringify(rows, null, 2));
