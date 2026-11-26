import { expect, test } from "./fixtures";

// Changed tools wait in « Outils à revoir » like any other item
// (tool_review.go), against the test core of TestEspaceScenario: the news
// watch's server (veille) rewords its tool after it was added and adds
// a new one that changes things and one that only reads; all are held,
// and the node made by code lists them and waits for the user (its
// bubble says « t'attend »).
// Opened: one row per change under its server's heading, the one that
// gives more power first and marked; the detail, only what changed, when
// a row is opened. The rows share one card: a Oui on any accepts them
// all; a row's ✕ refuses that one. Nothing runs past the page. Seeded at
// /e2e/review, on its own node.

test("changed tools wait in a page: one row each, a ✕ each, one Oui for all", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const res = await request.post("/e2e/review");
  expect(res.ok()).toBe(true);
  const id = (await res.json()).id as string;

  // The bubble waits for the user; no message was said about it.
  const bubble = page.locator(`.gr-node[data-node="${id}"]`);
  await expect(bubble).toHaveClass(/\byours\b/);
  await expect(bubble.locator(".gr-halo")).toHaveCount(1);
  await expect(bubble.locator(".gr-label em")).toHaveCount(0);

  const pages = page.locator("article.pg");
  for (let n = await pages.count(); n > 0; n = await pages.count()) {
    await pages.first().locator(".pg-x").dispatchEvent("click");
    await expect(pages).toHaveCount(n - 1);
  }
  await bubble.locator(".gr-core").dispatchEvent("click");
  const pg = page.locator('article.pg[aria-label="Outils à revoir"]');
  await expect(pg).toBeVisible();

  // A row each, under its server: the new tool that changes things first,
  // marked; the others quiet. No text whole, no Avant.
  const rows = pg.locator(".md-item");
  await expect(rows).toHaveCount(3);
  await expect(pg.locator(".md-h")).toHaveText(["veille"]);
  await expect(rows.nth(0).locator(".md-title")).toHaveText("classer — nouvel outil · modification · plus de pouvoir");
  await expect(rows.nth(0).locator(".md-title b, .md-title strong")).toHaveText("plus de pouvoir");
  await expect(rows.nth(1).locator(".md-title")).toHaveText("guetter — description modifiée · envoi");
  await expect(rows.nth(2).locator(".md-title")).toHaveText("resumer — nouvel outil · lecture");
  await expect(pg).not.toContainText("Avant");
  await expect(pg).not.toContainText("Guette les nouvelles du métier.");

  // Opened, a row says only what changed.
  await rows.nth(1).locator(".md-title").click();
  await expect(rows.nth(1).locator(".md-detail")).toHaveText("Ajouté : « Nouveau : en envoie aussi un résumé chaque matin. »");
  await rows.nth(0).locator(".md-title").click();
  await expect(rows.nth(0).locator(".md-detail")).toContainText("Dit : « Classe chaque nouvelle");
  await rows.nth(2).locator(".md-title").click();
  await expect(rows.nth(2).locator(".md-detail")).toContainText("Dit : « Résume une nouvelle. »");
  await expect(rows.nth(2).locator(".md-detail")).toContainText("Paramètres : longueur, sujet");

  // One card on the three rows: its Oui on each, a ✕ each.
  await expect(rows.locator(".a2-inline-yes")).toHaveCount(3);
  await expect(rows.first().locator(".a2-inline-yes")).toHaveText("Accepter les 3");
  await expect(rows.first().locator(".a2-inline-shared")).toContainText("3");
  await expect(rows.locator(".a2-inline-no")).toHaveCount(3);
  await expect(pg.locator("input[type=checkbox], [role=toolbar]")).toHaveCount(0);

  // Nothing runs past the page, a long word in a line opened included.
  const fits = () =>
    pg.evaluate((el) => {
      const box = el.getBoundingClientRect();
      const out = [...el.querySelectorAll<HTMLElement>(".pg-doc *")].filter((x) => x.getBoundingClientRect().right > box.right + 1);
      return { scroll: el.scrollWidth <= el.clientWidth, out: out.map((x) => x.className).slice(0, 3) };
    });
  expect(await fits()).toEqual({ scroll: true, out: [] });

  // ✕ on the new reader: refused, alone (still held: the brain's tools
  // stay as the other tests know them); the others still wait on the
  // card's Oui.
  await rows.nth(2).locator(".a2-inline-no").click();
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(1)).toContainText("guetter");
  await expect(rows.first().locator(".a2-inline-yes")).toHaveText("Accepter les 2");
  await expect(bubble).toHaveClass(/\byours\b/);

  // A Oui: both at once; nothing left, the node rests.
  await rows.first().locator(".a2-inline-yes").click();
  await expect(rows).toHaveCount(0);
  await expect(pg).toContainText("Rien à revoir.");
  await expect(bubble).not.toHaveClass(/\byours\b/);
  expect(problems).toEqual([]);
});
