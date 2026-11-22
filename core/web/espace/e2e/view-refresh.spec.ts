import { expect, test } from "@playwright/test";

// A view reads its sources again right after anything changes them
// (core/source_fresh.go), against the test core of TestEspaceScenario: a
// view lists a mailbox of four mails (mail/promos), read every five
// minutes. Asked in the conversation, Eriphe deletes two of them
// (mail/purger, allowed for good): the view loses them by itself, within
// moments, with no reload and no word to Eriphe. Seeded at /e2e/purge, on
// its own node.

const password = "correct horse battery"; // testPassword, core/web_test.go

test("a view loses what the conversation deleted, without a reload", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  // No page open, whatever the spec before left (one shared test core).
  expect((await request.post("/e2e/view")).ok()).toBe(true);
  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  expect((await request.post("/e2e/purge")).ok()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Boîte à nettoyer"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".a2-list > li");
  await expect(rows).toHaveCount(4);

  // Said in the bar: Eriphe deletes the two promos.
  let reloaded = false;
  page.on("framenavigated", (f) => f === page.mainFrame() && (reloaded = true));
  const input = page.locator(".cs-bar input");
  await input.fill("Supprime les deux promos.");
  await input.press("Enter");
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).purged).toEqual(["p1", "p3"]);

  // The view, as it was opened: the two promos gone within a few seconds.
  await expect(rows).toHaveCount(2, { timeout: 5_000 });
  await expect(pg).toContainText("Compte rendu de la réunion");
  await expect(pg).toContainText("Photos du week-end");
  await expect(pg).not.toContainText("Soldes d'automne");
  await expect(pg).not.toContainText("Dernière chance");
  expect(reloaded).toBe(false);
  expect(problems).toEqual([]);
});
