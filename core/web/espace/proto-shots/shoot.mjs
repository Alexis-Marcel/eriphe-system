// Prototype « belles vues »: screenshots of the frames (dev server on 5318
// running: npx vite --config vite.proto.config.ts). Stills are taken with
// reduced motion, so they are the same every time.
//   node proto-shots/shoot.mjs            every key shot
//   node proto-shots/shoot.mjs mails      only the shots whose name has « mails »
import { chromium } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const base = process.env.BASE ?? "http://127.0.0.1:5318/proto.html";
const only = process.argv[2] ?? "";

const scenarios = ["mails", "invest", "cuisine", "garde-robe", "projet", "premiere"];
const sizes = { phone: { width: 390, height: 844 }, desktop: { width: 1280, height: 900 } };
const shots = [];
for (const s of scenarios)
  for (const v of ["avant", "apres"])
    for (const w of ["phone", "desktop"])
      for (const theme of ["light", "dark"]) shots.push({ name: `${s}-${v}-${w}-${theme}`, q: { s, v, theme }, w });
// renderer-only polish, states, the sizes table
for (const s of ["mails", "invest", "cuisine"]) shots.push({ name: `${s}-habille-desktop-light`, q: { s, v: "habille", theme: "light" }, w: "desktop" });
for (const state of ["loading", "empty", "error"]) {
  shots.push({ name: `mails-state-${state}-apres-phone-light`, q: { s: "mails", v: "apres", theme: "light", state }, w: "phone" });
  shots.push({ name: `mails-state-${state}-avant-phone-light`, q: { s: "mails", v: "avant", theme: "light", state }, w: "phone" });
}
shots.push({ name: "premiere-state-error-apres-phone-dark", q: { s: "premiere", v: "apres", theme: "dark", state: "error" }, w: "phone" });

const browser = await chromium.launch();
let errors = 0;
for (const sh of shots) {
  if (only && !sh.name.includes(only)) continue;
  const ctx = await browser.newContext({ viewport: sizes[sh.w], deviceScaleFactor: 2, reducedMotion: "reduce", colorScheme: sh.q.theme });
  const page = await ctx.newPage();
  page.on("console", (m) => m.type() === "error" && (errors++, console.log(`  ${sh.name}: ${m.text()}`)));
  page.on("pageerror", (e) => (errors++, console.log(`  ${sh.name}: ${e.message}`)));
  const q = new URLSearchParams({ ...sh.q, frame: "1", shot: "1" });
  await page.goto(`${base}?${q}`, { waitUntil: "networkidle" });
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(here, `${sh.name}.png`), fullPage: true });
  await ctx.close();
  console.log(sh.name);
}
// the shell: the sizes table and a side by side with its JSON
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1, reducedMotion: "reduce" });
  const page = await ctx.newPage();
  if (!only || "tailles".includes(only)) {
    await page.goto(`${base}?s=tailles`, { waitUntil: "networkidle" });
    await page.screenshot({ path: join(here, "tailles.png") });
  }
  if (!only || "shell".includes(only)) {
    await page.goto(`${base}?s=mails&v=cote&w=phone&json=1`, { waitUntil: "networkidle" });
    await page.waitForTimeout(800);
    await page.screenshot({ path: join(here, "shell-mails-cote.png") });
  }
  await ctx.close();
}
await browser.close();
console.log(errors ? `${errors} console errors` : "no console errors");
