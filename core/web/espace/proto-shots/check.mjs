// Prototype « belles vues »: plays the motions once, with motion on, and
// checks they end where they should (dev server on 5318 running).
import { chromium } from "@playwright/test";

const base = process.env.BASE ?? "http://127.0.0.1:5318/proto.html";
const browser = await chromium.launch();
const page = await (await browser.newContext({ viewport: { width: 1280, height: 900 }, reducedMotion: "no-preference" })).newPage();
const errs = [];
page.on("pageerror", (e) => errs.push(e.message));
page.on("console", (m) => m.type() === "error" && errs.push(m.text()));
const ok = (c, what) => console.log(`${c ? "ok  " : "FAIL"} ${what}`);

await page.goto(`${base}?frame=1&s=mails&v=apres`, { waitUntil: "networkidle" });
const rows = () => page.locator(".bv-sec .bv-row").count();
ok((await rows()) === 4, "4 rows to handle");
await page.locator(".bv-sec .bv-yes").first().click();
await page.waitForTimeout(900);
ok((await rows()) === 3, "a row answered leaves");
await page.getByRole("button", { name: "Un mail arrive" }).click();
await page.waitForTimeout(500);
ok((await rows()) === 4, "a new mail enters");
await page.getByRole("button", { name: "Oui, pour tous" }).click();
await page.waitForTimeout(900);
ok((await page.locator(".bv-fold.batch").count()) === 0, "the batch leaves on its Oui");
await page.getByRole("button", { name: "Fermer vers le nœud" }).click();
await page.waitForTimeout(400);
ok((await page.locator(".bv-node.me").count()) === 1, "closed to its node");
await page.locator(".bv-node.me").click();
await page.waitForTimeout(500);
ok((await page.locator(".bv-page").count()) === 1, "opened from its node");

await page.goto(`${base}?frame=1&s=invest&v=apres`, { waitUntil: "networkidle" });
const first = await page.locator(".bv-fig-value").first().innerText();
await page.getByRole("button", { name: "Le marché bouge" }).click();
await page.waitForTimeout(600);
ok((await page.locator(".bv-fig-value").first().innerText()) !== first, "a figure ticks to its new value");

await page.goto(`${base}?frame=1&s=projet&v=apres`, { waitUntil: "networkidle" });
await page.getByRole("button", { name: "Étape suivante" }).click();
await page.waitForTimeout(400);
ok((await page.locator(".bv-prog-words b").innerText()).startsWith("3"), "the timeline moves on");

await page.goto(`${base}?frame=1&s=garde-robe&v=apres`, { waitUntil: "networkidle" });
await page.getByRole("tab", { name: /Chaussures/ }).click();
await page.waitForTimeout(500);
ok((await page.locator(".k-Gallery").nth(1).locator(".bv-tile").count()) === 3, "a chip filters the gallery");

await page.goto(`${base}?frame=1&s=premiere&v=apres`, { waitUntil: "networkidle" });
await page.getByRole("button", { name: "Premiers articles" }).click();
await page.waitForTimeout(500);
ok((await page.locator(".bv-sec .bv-row").count()) === 2, "first articles arrive");

ok(errs.length === 0, `no errors ${errs.join(" | ")}`);
await browser.close();
