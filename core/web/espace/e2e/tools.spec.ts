import { expect, test } from "./fixtures";

// A tool that changed waits in « Outils à revoir » like any other item
// (tool_review.go), against the test core of TestEspaceScenario: the news
// watch's server (veille) changes its tool after it was added; the tool is
// held, and the node made by code lists the change and waits for the user
// (its bubble says « t'attend »). Opened, the change waits on its row as
// a card code made (accept it): its Oui empties the list, the node rests. Seeded at
// /e2e/review, on its own node.


test("a changed tool waits in a view, and is accepted there", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

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

  // Opened: the change, its kind, before and now.
  const pages = page.locator("article.pg");
  for (let n = await pages.count(); n > 0; n = await pages.count()) {
    await pages.first().locator(".pg-x").dispatchEvent("click");
    await expect(pages).toHaveCount(n - 1);
  }
  await bubble.locator(".gr-core").dispatchEvent("click");
  const pg = page.locator('article.pg[aria-label="Outils à revoir"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".a2-list > li");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("veille · guetter");
  await expect(rows.first()).toContainText("changé");
  await expect(rows.first()).toContainText("Guette les nouvelles du métier.");
  await expect(rows.first()).toContainText("en envoie aussi un résumé chaque matin");

  // Its one card, on its row: Oui accepts it. Nothing else says yes.
  await expect(pg.locator("input[type=checkbox], [role=toolbar]")).toHaveCount(0);
  const yes = rows.first().locator(".a2-inline-yes");
  await expect(yes).toHaveCount(1);
  await expect(rows.first().locator(".a2-inline")).toHaveAttribute("data-row-card", /^calls:\d+$/);
  await yes.click();

  // Nothing left: the list empties (its line goes with its rows), the
  // node rests.
  await expect(rows).toHaveCount(0);
  await expect(pg).toContainText("Rien à revoir.");
  await expect(bubble).not.toHaveClass(/\byours\b/);
  expect(problems).toEqual([]);
});
