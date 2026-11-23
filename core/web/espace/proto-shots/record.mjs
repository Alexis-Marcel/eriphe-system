// Prototype « belles vues »: short videos of the motions (motion on), and
// a few frames caught mid-animation, under proto-shots/motion/ (dev server
// on 5318 running).
import { chromium } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { mkdirSync, renameSync } from "node:fs";

const dir = join(dirname(fileURLToPath(import.meta.url)), "motion");
mkdirSync(dir, { recursive: true });
const base = process.env.BASE ?? "http://127.0.0.1:5318/proto.html";
const browser = await chromium.launch();
const pause = (p, ms) => p.waitForTimeout(ms);

async function take(name, size, q, play) {
  const ctx = await browser.newContext({ viewport: size, reducedMotion: "no-preference", recordVideo: { dir, size } });
  const page = await ctx.newPage();
  await page.goto(`${base}?frame=1&${q}`, { waitUntil: "networkidle" });
  await pause(page, 600);
  await play(page);
  await pause(page, 800);
  const v = page.video();
  await ctx.close();
  renameSync(await v.path(), join(dir, `${name}.webm`));
  console.log(name);
}
const desk = { width: 1100, height: 800 }, phone = { width: 390, height: 844 };

await take("open-from-node", desk, "s=invest&v=apres&closed", async (p) => {
  await p.locator(".bv-node.me").click();
  await pause(p, 1200);
  await p.getByRole("button", { name: "Fermer vers le nœud" }).click();
  await pause(p, 900);
  await p.locator(".bv-node.me").click();
});
await take("mails-handle", phone, "s=mails&v=apres", async (p) => {
  await p.locator(".bv-sec .bv-yes").first().click();
  await pause(p, 1200);
  await p.getByRole("button", { name: "Un mail arrive" }).click();
  await pause(p, 1200);
  await p.getByRole("button", { name: "Oui, pour tous" }).click();
});
await take("figures-tick", desk, "s=invest&v=apres", async (p) => {
  for (let i = 0; i < 3; i++) {
    await p.getByRole("button", { name: "Le marché bouge" }).click();
    await pause(p, 1000);
  }
});
await take("gallery-filter", phone, "s=garde-robe&v=apres", async (p) => {
  await p.locator(".k-Gallery").nth(1).scrollIntoViewIfNeeded();
  for (const c of ["Chaussures", "Vestes", "Tout"]) {
    await p.getByRole("tab", { name: new RegExp(c) }).click();
    await pause(p, 900);
  }
});
await take("timeline-step", desk, "s=projet&v=apres", async (p) => {
  for (let i = 0; i < 2; i++) {
    await p.getByRole("button", { name: "Étape suivante" }).click();
    await pause(p, 900);
  }
});

// frames caught in the middle: the page growing out of its node, a row leaving
{
  const ctx = await browser.newContext({ viewport: desk, reducedMotion: "no-preference" });
  const p = await ctx.newPage();
  await p.goto(`${base}?frame=1&s=invest&v=apres&closed`, { waitUntil: "networkidle" });
  await p.locator(".bv-node.me").click();
  for (const ms of [60, 120, 180]) {
    await p.screenshot({ path: join(dir, `open-${ms}ms.png`) });
    await pause(p, 60);
  }
  await p.goto(`${base}?frame=1&s=mails&v=apres`, { waitUntil: "networkidle" });
  await p.locator(".bv-sec .bv-yes").first().click();
  await pause(p, 300);
  await p.locator(".bv-sec").screenshot({ path: join(dir, "row-leaving.png") });
  await ctx.close();
}
await browser.close();
