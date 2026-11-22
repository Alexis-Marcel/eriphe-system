import { expect, test } from "./fixtures";

// An item opened in place (item_open.go), against the test core of
// TestEspaceScenario: a node whose block lists two faked mails (mail/lus)
// and says how one is read in full (mail/lire, a free read). A click on a
// row shows it below itself as a letter (the page's head saying once it is someone else's words), with the
// item's note; a click again closes it. Seeded at /e2e/open, on its own
// node.


test("a click on a mail shows its body in place", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  expect((await request.post("/e2e/open")).ok()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Mails à lire"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".a2-openrow");
  await expect(rows).toHaveCount(2);

  await rows.first().getByText("Ordre du jour").click();
  const opened = rows.first().locator(".a2-opened");
  await expect(opened.locator(".a2-read-text")).toHaveText("Bonjour,\nvoici l'ordre du jour de l1.");
  await expect(opened.locator(".a2-read-note")).toHaveText("Classé « À traiter » : Sam attend ton avis");
  // Someone else's words: said once by the page's head and the block's edge, not again inside.
  await expect(pg.locator(".pg-title .pg-badge.warn")).toHaveText("extérieur");
  await expect(pg.locator(".a2-block.ext .a2-block-mark")).toHaveCount(1);
  await expect(opened.locator(".a2-taint")).toHaveCount(0);
  await expect(opened.locator(".a2-read-from")).toContainText("Sam");
  await expect(rows.first().getByRole("button", { name: "Fermer" }).first()).toHaveAttribute("aria-expanded", "true");
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).read).toEqual(["l1"]);

  await rows.first().getByText("Ordre du jour").first().click();
  await expect(opened).toHaveCount(0);
  expect(problems).toEqual([]);
});
