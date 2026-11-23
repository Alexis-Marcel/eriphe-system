// Prototype only (proto/eriphe-coche): the screenshots of PROTO.md, each
// moment of the scenarios played in a browser.
// Run the dev server (npx vite --port 5317), then from core/web/espace:
//   node proto-shots/coche.mjs [light|dark] [desktop|phone] [only]
import { chromium } from "@playwright/test";

const [theme = "light", size = "desktop", only = ""] = process.argv.slice(2);
const U = process.env.U ?? "http://localhost:5317";
const phone = size === "phone";
const vp = phone ? { width: 390, height: 844 } : { width: 1280, height: 1000 };
const b = await chromium.launch();

async function page(query) {
  const p = await b.newPage({ locale: "fr-FR", viewport: vp, colorScheme: theme, deviceScaleFactor: 2, reducedMotion: "reduce" });
  p.on("pageerror", (e) => console.log("PAGEERR", e.message));
  p.on("console", (m) => m.type() === "error" && console.log("CONSOLE", m.text()));
  await p.goto(`${U}/?open=node-mails${query}`);
  await p.addStyleTag({ content: ".dev-coche { display: none !important; }" });
  await p.waitForTimeout(1500);
  // the page enlarged, as one reads a long list
  if (!phone && process.env.SMALL === undefined) {
    await p.locator(".pg-big").first().click();
    await p.waitForTimeout(700);
  }
  return p;
}
const name = (k) => `proto-shots/${k}-${size}-${theme}.png`;
// shot: the screen as the user sees it; whole: the page, all of it.
const shot = (p, k) => p.screenshot({ path: name(k) });
async function whole(p, k) {
  if (phone) {
    await p.addStyleTag({ content: ".ls-page { position: static !important; overflow: visible !important; } .cs, .espace-note, .dev-coche, .tk-bar { position: static !important; } .bar, .orb { display: none !important; }" });
    await p.waitForTimeout(300);
    await p.locator(".ls-page article.pg").first().screenshot({ path: name(k) });
  } else {
    await p.addStyleTag({ content: ".gr-page { height: auto !important; max-height: none !important; } .pg, .gr-page > .pg { max-height: none !important; height: auto !important; } .pg-body { overflow: visible !important; } .tk-bar { position: static !important; }" });
    await p.waitForTimeout(500);
    await p.locator("article.pg").first().screenshot({ path: name(k) });
  }
}
const untick = (p, subject) => p.locator(`.tk-rows li:has-text("${subject}") .a2-tick`).first().click();
const want = (k) => !only || only.split(",").includes(k);

// Today's main (?avant): the request answered with cards, one per account.
if (want("avant")) {
  const p = await page("&avant&coche=a");
  await shot(p, "avant");
  await whole(p, "avant-page");
  await p.close();
}
// A: the request, ticked; the orb's one line.
if (want("a")) {
  const p = await page("&coche=a");
  await shot(p, "a1-asked");
  // the user read Eriphe's line and closes it (on a phone it covers the bar)
  if (phone) await p.locator('.orb-say button[aria-label="Fermer"], button[aria-label="Fermer"]').first().click().catch(() => {});
  await untick(p, "La lettre de la semaine n° 122");
  await untick(p, "Votre reçu B2 — novembre");
  await p.waitForTimeout(300);
  await shot(p, "a2-unticked");
  await p.locator(".tk-go.tk-a-corbeille").click();
  await p.waitForTimeout(1600);
  await shot(p, "a3-trash");
  await p.locator(".tk-go.tk-a-archiver").click();
  await p.waitForTimeout(1600);
  await shot(p, "a4-archive");
  await p.close();
  const q = await page("&coche=a");
  await whole(q, "a-page");
  await q.close();
}
// B: a mail arrives, the skill ticks it; nothing else moves.
if (want("b")) {
  const p = await page("&coche=a");
  await p.evaluate(() => document.querySelector(".pg-body")?.scrollTo(0, 0));
  await p.evaluate(() => window.cocheArrive());
  await p.waitForTimeout(500);
  await shot(p, "b-arrived");
  await p.close();
}
await b.close();
