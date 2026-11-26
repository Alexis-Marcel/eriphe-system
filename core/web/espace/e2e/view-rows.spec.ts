import { expect, test } from "./fixtures";

// Each item its one Oui, against the test core of TestEspaceScenario: a
// node whose page references four faked mails ([subject][b1], with
// [b1]: mail:b1), one row each; the brain's three calls, each on one item
// and each a card of its own (each moves to another folder). The rows
// show the cards code made, and nothing else says yes (no box, no bar). A
// click on a row opens the mail below it, read by eriphe-core with the
// server's read tool (item_open.go). Seeded at /e2e/rows, on its own node.

test("three items, each with its Oui; a row opens its mail", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const seeded = await request.post("/e2e/rows");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Tri de la boîte"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".md-item");
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
  // Nothing else on the page says yes: no box, no bar of actions.
  await expect(pg.locator("input[type=checkbox], [role=toolbar]")).toHaveCount(0);

  // A click on a row opens its mail below it, as the server reads it.
  await row("Promotion de la semaine").locator(".md-row").click();
  await expect(row("Promotion de la semaine").locator(".md-opened .a2-read-title")).toHaveText("Promotion de la semaine");
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).read).toContain("b4");
  await row("Promotion de la semaine").locator(".md-row").click();
  await expect(row("Promotion de la semaine").locator(".md-opened")).toHaveCount(0);

  // Each Oui runs its own call only; its row says it is done.
  const mine = async () => ((await (await request.get("/e2e/tools")).json()).moved as string[][]).filter((ids) => ids.some((id) => /^b\d$/.test(id)));
  await row("Lettre d'information").locator(".a2-inline-yes").click();
  await expect.poll(mine).toEqual([["b2"]]);
  await expect(row("Lettre d'information").locator(".a2-inline-yes")).toHaveCount(0);
  await row("Facture d'octobre").locator(".a2-inline-yes").click();
  await expect.poll(mine).toEqual([["b2"], ["b1"]]);
  await row("Réunion de lundi").locator(".a2-inline-yes").click();
  await expect.poll(mine).toEqual([["b2"], ["b1"], ["b3"]]);
  await expect(pg.locator(".a2-inline-yes")).toHaveCount(0);
  expect(problems).toEqual([]);
});
