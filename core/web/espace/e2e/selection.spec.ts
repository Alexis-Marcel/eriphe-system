import { expect, test } from "./fixtures";

// Picking items in a list and running the action its block declares
// (list_actions.go), against the test core of TestEspaceScenario: a node
// whose block lists the faked mailbox (mail/boite) with an action that
// archives what the user picks (mail/move, on approval: the click is the
// user's yes). The block reads a server that is not trusted: the author's
// label is not shown, the button says code's words. Seeded at
// /e2e/actions, on its own node.


test("pick two rows and archive them", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  expect((await request.post("/e2e/actions")).ok()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Tri de la boîte"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".a2-pickable > li");
  await expect(rows).toHaveCount(4);

  // Nothing but the list before something is picked.
  const bar = pg.locator(".a2-pickbar");
  await expect(bar).toHaveCount(0);

  await pg.getByRole("checkbox", { name: "Choisir « Facture d'octobre »" }).check();
  await pg.getByRole("checkbox", { name: "Choisir « Réunion de lundi »" }).check();
  await expect(bar).toContainText("2 sélectionné(s)");
  const button = bar.locator(".a2-pickact");
  await expect(button).toHaveCount(1);
  // code's words; the author's label of an external block beside them, marked
  await expect(button).toHaveText("Déplacer les 2 → archiveRanger ⚠");
  await expect(button.locator(".a2-pickhint.ext")).toHaveAttribute("title", "Écrit après une lecture extérieure");
  await button.click();

  await expect(pg.locator(".a2-pickdone")).toHaveText("2 déplacés");
  // The mailbox's moves only: the scenario, on the same test core, moves mails of its own.
  await expect
    .poll(async () => ((await (await request.get("/e2e/tools")).json()).moved as string[][]).filter((ids) => ids.some((id) => /^b\d$/.test(id))))
    .toEqual([["b1", "b3"]]);
  // The source read again: what was moved left the list.
  await expect(rows).toHaveCount(2);
  await expect(bar).toHaveCount(0);
  expect(problems).toEqual([]);
});
