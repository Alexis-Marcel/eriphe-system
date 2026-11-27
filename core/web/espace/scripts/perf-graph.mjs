// perf-graph measures how smoothly the graph draws (npm run perf:graph):
// it serves the app with vite (its dev server, on a port of its own, React
// as in production), opens ?leger (src/dev-leger.tsx) at each size in
// Chromium and plays, one after the other:
//   - settle: the first 3 s, the layout moving (first frame: from the
//     graph given to the first frame with bubbles, apart);
//   - rest: the time the layout took to first come to rest (data-still);
//   - pan: 3 s of a steady wheel pan out and back, at rest, the camera as
//     it framed the graph; pan 1: the same at scale 1, as when reading;
//   - zoom: 3 s of wheel pans and zooms in and out.
// For each phase: the main thread's busy time as Chromium counts it
// (Performance.getMetrics: all its tasks, and of them scripts, layout,
// style) and how many layouts it ran, steadier than frames per second on a
// machine doing other things; the frames too (the page's own, median of
// the frames' rate, the 95th percentile and worst gaps).
// Not part of CI: a machine's figures, to compare a change with the code
// before it. Options: --sizes 25,100,… --root <an espace folder> (another
// checkout, to measure it with the same script) --json <file> --port <n>
// --throttle <k> (Chromium's CPU slowed k times, as on a modest laptop)
// --react dev (React's development build, several times slower).

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
// build): the dev page stays on (import.meta.env.DEV).
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
  page.setDefaultTimeout(180_000);
  const cdp = await page.context().newCDPSession(page);
  if (throttle > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: throttle });
  await cdp.send("Performance.enable");
  const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map((m) => [m.name, m.value]));
  // play runs a phase of ms milliseconds (move called at each frame) and
  // says its frames and the main thread's work meanwhile.
  const play = async (move) => {
    const a = await metrics();
    const f = await page.evaluate((m) => window.leger.sample(3000, m ? window.leger[m] : undefined), move);
    const b = await metrics();
    const d = (k) => Math.round((b[k] - a[k]) * 1000);
    return { ...f, task: d("TaskDuration"), script: d("ScriptDuration"), layout: d("LayoutDuration"), style: d("RecalcStyleDuration"), layouts: b.LayoutCount - a.LayoutCount, dom: b.Nodes };
  };
  const stats = () => page.evaluate(() => window.leger.stats());
  // The first load compiles the modules: once, before measuring.
  await page.goto(`http://127.0.0.1:${port}/?leger&n=25`);
  await page.waitForFunction(() => window.leger, null, { timeout: 60_000 });
  for (const n of sizes) {
    await page.goto(`http://127.0.0.1:${port}/?leger&n=${n}`);
    await page.waitForFunction(() => window.leger, null, { polling: 100 });
    const settle = await play();
    const cap = n >= 1000 ? 120_000 : 30_000;
    const rested = await page.waitForFunction(() => window.leger.stats().stable !== null, null, { timeout: cap, polling: 200 }).then(() => true, () => false);
    const at = await stats();
    const pan = await play("pan");
    await page.evaluate(() => window.leger.zoomTo(1));
    await page.waitForTimeout(800);
    const at1 = await stats();
    const pan1 = await play("pan");
    const zoom = await play("panZoom");
    const row = { n, stable: rested ? at.stable : null, drawn: at.drawn, k: at.k, bubbles: at.bubbles, bubbles1: at1.bubbles, settle, pan, pan1, zoom };
    rows.push(row);
    const ph = (p) => `${p.fps} fps (p95 ${p.p95}, worst ${p.worst} ms), busy ${p.task} ms (script ${p.script}, layout ${p.layout}, style ${p.style}, ${p.layouts} layouts)`;
    console.error(`${n}: drawn ${at.drawn} ms, rest ${rested ? `${at.stable} ms` : "not within " + cap / 1000 + " s"}, scale ${at.k}, ${at.bubbles} bubbles (${at1.bubbles} at 1), ${pan.dom} DOM nodes`);
    for (const [name, p] of [["settle", settle], ["pan", pan], ["pan 1", pan1], ["zoom", zoom]]) console.error(`  ${name}: ${ph(p)}`);
  }
} finally {
  await browser?.close();
  await server.close();
}

const s = (x) => (x === null ? "no rest" : `${(x / 1000).toFixed(1)} s`);
const cell = (p) => `${p.task} · ${(p.task / Math.max(1, p.frames)).toFixed(1)} · ${p.fps}`;
console.log("Each phase (3 s): main-thread busy ms · busy ms per frame · frames per second.\n");
console.log("| nodes | first frame | to rest | scale at rest | bubbles in DOM (at 1) | settle | pan | pan at scale 1 | zoom |");
console.log("|---:|---:|---:|---:|---:|---:|---:|---:|---:|");
for (const r of rows)
  console.log(`| ${r.n} | ${r.drawn} ms | ${s(r.stable)} | ${r.k} | ${r.bubbles} (${r.bubbles1}) | ${cell(r.settle)} | ${cell(r.pan)} | ${cell(r.pan1)} | ${cell(r.zoom)} |`);
if (json) writeFileSync(json, JSON.stringify(rows, null, 2));
