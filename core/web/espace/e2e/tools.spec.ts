import { expect, test } from "@playwright/test";

// A tool that changed waits in « Outils à revoir » like any other item
// (tool_review.go), against the test core of TestEspaceScenario: the news
// watch's server (veille) changes its tool after it was added; the tool is
// held, and the node made by code lists the change and waits for the user
// (its bubble says « t'attend »). Opened, the change is picked and
// accepted with code's button: the list empties, the node rests. Seeded at
// /e2e/review; this file runs after selection.spec.ts, on its own node.

const password = "correct horse battery"; // testPassword, core/web_test.go

test("a changed tool waits in a view, and is accepted there", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const res = await request.post("/e2e/review");
  expect(res.ok()).toBe(true);
  const id = (await res.json()).id as string;

  // The bubble waits for the user; no message was said about it.
  const bubble = page.locator(`.gr-node[data-node="${id}"]`);
  await expect(bubble).toHaveClass(/\byours\b/);
  await expect(bubble.locator(".gr-ring-yours")).toHaveCount(1);
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
  const rows = pg.locator(".a2-pickable > li");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("veille · guetter");
  await expect(rows.first()).toContainText("changé");
  await expect(rows.first()).toContainText("Guette les nouvelles du métier.");
  await expect(rows.first()).toContainText("en envoie aussi un résumé chaque matin");

  // Picked and accepted, with code's words.
  await pg.getByRole("checkbox", { name: "Choisir « veille · guetter »" }).check();
  const bar = pg.locator(".a2-pickbar");
  const accept = bar.locator(".a2-pickact", { hasText: "Accepter" });
  await expect(accept).toHaveText("Accepter 1 élément");
  await expect(bar.locator(".a2-pickact", { hasText: "Refuser" })).toHaveText("Refuser 1 élément");
  await accept.click();

  // Nothing left: the list empties (its line goes with its rows), the
  // node rests.
  await expect(rows).toHaveCount(0);
  await expect(pg).toContainText("Rien à revoir.");
  await expect(bubble).not.toHaveClass(/\byours\b/);
  expect(problems).toEqual([]);
});
