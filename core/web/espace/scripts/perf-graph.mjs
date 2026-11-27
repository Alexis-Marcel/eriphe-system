// perf-graph measures how smoothly the graph draws (npm run perf:graph):
// it serves the app with vite (its dev server, on a port of its own), opens
// ?leger (src/dev-leger.tsx) at each size in Chromium, and records, through
// the page's own frames and a PerformanceObserver of long tasks:
//   - settle: the first 3 s after the graph is drawn, the layout moving;
//   - rest: the time the layout took to first come to rest (data-still);
//   - pan/zoom: 3 s of wheel pans and zooms once at rest, the render alone.
// Not part of CI: a machine's figures, to compare a change with the code
// before it. Options: --sizes 25,100,… --root <an espace folder> (another
// checkout, to measure it with the same script) --json <file> --port <n>
// --throttle <k> (Chromium's CPU slowed k times, as on a modest laptop).

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

const server = await createServer({ root, configFile: resolve(root, "vite.config.ts"), server: { port, strictPort: true, host: "127.0.0.1" }, logLevel: "error" });
let browser;
const rows = [];
try {
  await server.listen();
  browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, locale: "fr-FR", colorScheme: "light" });
  if (throttle > 1) await (await page.context().newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: throttle });
  // The first load compiles the modules: once, before measuring.
  await page.goto(`http://127.0.0.1:${port}/?leger&n=25`);
  await page.waitForFunction(() => window.leger, null, { timeout: 60_000 });
  for (const n of sizes) {
    await page.goto(`http://127.0.0.1:${port}/?leger&n=${n}`);
    await page.waitForFunction(() => window.leger, null, { timeout: 180_000, polling: 250 });
    const settle = await page.evaluate(() => window.leger.sample(3000));
    page.setDefaultTimeout(180_000);
    const cap = n >= 1000 ? 120_000 : 30_000;
    const rested = await page.waitForFunction(() => window.leger.stats().stable !== null, null, { timeout: cap, polling: 200 }).then(() => true, () => false);
    const stable = await page.evaluate(() => window.leger.stats().stable);
    const pan = await page.evaluate(() => window.leger.sample(3000, window.leger.panZoom));
    const bubbles = await page.evaluate(() => window.leger.stats().bubbles);
    const row = { n, stable: rested ? stable : null, settle, pan, bubbles };
    rows.push(row);
    console.error(`${n}: rest ${rested ? `${stable} ms` : "not within " + cap / 1000 + " s"}, settle ${settle.fps} fps (worst ${settle.worst} ms), pan ${pan.fps} fps (p95 ${pan.p95}, worst ${pan.worst} ms, long ${pan.longTasks}/${pan.longMs} ms), ${bubbles} bubbles`);
  }
} finally {
  await browser?.close();
  await server.close();
}

const ms = (x) => (x === null ? "no rest" : `${(x / 1000).toFixed(1)} s`);
console.log("| nodes | to rest | settle fps (worst ms) | pan/zoom fps | pan p95 / worst ms | pan long tasks (ms) | bubbles in DOM |");
console.log("|---:|---:|---:|---:|---:|---:|---:|");
for (const r of rows)
  console.log(`| ${r.n} | ${ms(r.stable)} | ${r.settle.fps} (${r.settle.worst}) | ${r.pan.fps} | ${r.pan.p95} / ${r.pan.worst} | ${r.pan.longTasks} (${r.pan.longMs}) | ${r.bubbles} |`);
if (json) writeFileSync(json, JSON.stringify(rows, null, 2));
