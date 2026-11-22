import { expect, test } from "./fixtures";

// Each item its one Oui, against the test core of TestEspaceScenario: a
// node whose block lists the faked mailbox (mail/boite), plain rows; the
// brain's three calls, each on one row and each a card of its own (each
// moves to another folder). The block read a server that is not trusted:
// the rows still show the cards code made, and nothing else says yes (no
// box, no bar). Seeded at /e2e/rows, on its own node.

test("three items, each with its Oui", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const seeded = await request.post("/e2e/rows");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Tri de la boîte"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".a2-list > li");
  await expect(rows).toHaveCount(4);
  const row = (title: string) => rows.filter({ hasText: title });

  // Three rows, three cards, one Oui each; the fourth row asks nothing.
  for (const title of ["Facture d'octobre", "Lettre d'information", "Réunion de lundi"]) {
    await expect(row(title).locator(".a2-inline-yes")).toHaveCount(1);
    await expect(row(title).locator(".a2-inline-shared")).toHaveCount(0);
  }
  await expect(row("Promotion de la semaine").locator(".a2-inline-yes")).toHaveCount(0);
  const refs = await rows.locator(".a2-inline").evaluateAll((ns) => ns.map((n) => n.getAttribute("data-row-card")));
  expect(new Set(refs).size).toBe(3);
  // Nothing else in the block says yes: no box, no bar of actions.
  await expect(pg.locator("input[type=checkbox], [role=toolbar]")).toHaveCount(0);

  // Each Oui runs its own call only, and its row leaves.
  const mine = async () => ((await (await request.get("/e2e/tools")).json()).moved as string[][]).filter((ids) => ids.some((id) => /^b\d$/.test(id)));
  await row("Lettre d'information").locator(".a2-inline-yes").click();
  await expect.poll(mine).toEqual([["b2"]]);
  await expect(rows).toHaveCount(3);
  await row("Facture d'octobre").locator(".a2-inline-yes").click();
  await expect.poll(mine).toEqual([["b2"], ["b1"]]);
  await row("Réunion de lundi").locator(".a2-inline-yes").click();
  await expect.poll(mine).toEqual([["b2"], ["b1"], ["b3"]]);
  await expect(rows).toHaveCount(1);
  expect(problems).toEqual([]);
});
