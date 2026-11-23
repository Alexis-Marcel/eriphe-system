// Prototype only (#730): screenshots of the gallery and of sample pages.
// Run the dev server (npx vite --port 5317), then from core/web/espace:
//   node proto-shots/shoot.mjs <url> <out.png> [light|dark] [desktop|phone] [selector] [click]
// selector: shoot that element only; click: a selector to click first.
import { chromium } from "@playwright/test";

const [url, out, theme = "light", size = "desktop", sel = "", click = ""] = process.argv.slice(2);
const b = await chromium.launch();
const vp = size === "phone" ? { width: 390, height: Number(process.env.H ?? 844) } : { width: 1280, height: Number(process.env.H ?? 900) };
const p = await b.newPage({ locale: "fr-FR", viewport: vp, colorScheme: theme, deviceScaleFactor: 2, reducedMotion: "reduce" });
p.on("pageerror", (e) => console.log("PAGEERR", e.message));
p.on("console", (m) => m.type() === "error" && console.log("CONSOLE", m.text()));
// The clock at 11:05 today (CLOCK=HH:MM for another time), so the
// agenda's line is on its grid whenever the shots are taken.
const [hh, mm] = (process.env.CLOCK ?? "11:05").split(":").map(Number);
const fixed = new Date();
fixed.setHours(hh, mm, 0, 0);
await p.clock.setFixedTime(fixed);
await p.goto(url);
await p.waitForTimeout(1200);
if (click) {
  await p.locator(click).first().click();
  await p.waitForTimeout(1200);
}
// A page shot whole: its height not capped by the screen (shots only).
if (sel === "article.pg") {
  await p.addStyleTag({ content: ".gr-page { height: auto !important; max-height: none !important; } .pg, .gr-page > .pg { max-height: none !important; height: auto !important; } .pg-body { overflow: visible !important; }" });
  await p.waitForTimeout(600);
}
// A phone page (the list's page, which scrolls): the whole of it, without
// the bar over it.
if (sel === "phone-page") {
  await p.addStyleTag({ content: ".ls-page { position: static !important; overflow: visible !important; padding-bottom: 24px !important; } .cs, .espace-note { display: none !important; }" });
  await p.waitForTimeout(400);
  await p.locator(".ls-page article.pg").first().screenshot({ path: out });
} else if (sel) {
  await p.locator(sel).first().screenshot({ path: out });
} else {
  // a scrolling box (the gallery is fixed): grow the window to its height
  const h = await p.evaluate(() => Math.max(...[...document.querySelectorAll(".gallery, .proto-blocks")].map((e) => e.scrollHeight), document.body.scrollHeight));
  await p.setViewportSize({ width: vp.width, height: Math.min(h + 40, 12000) });
  await p.waitForTimeout(400);
  await p.screenshot({ path: out, fullPage: true });
}
await b.close();
