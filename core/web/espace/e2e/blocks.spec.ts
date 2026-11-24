import { expect, test } from "./fixtures";

// The blocks a model writes as one component, against the test core of
// TestEspaceScenario: a node whose Sections names its source (five faked
// mails, mail/du_jour), noted into three groups, and a band of Figures. The
// group to act on is open, Eriphe's note first; the three mails to delete
// share the brain's one call: a batch, folded, its yes on its line,
// saying its verb. Unfolded, the user unticks one: the faked server gets
// exactly the two others. Seeded at /e2e/blocks, on its own node.

test("a Sections: the group to act on open, a batch folded with its verb, one mail kept", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const seeded = await request.post("/e2e/blocks");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Courrier du jour"]');
  await expect(pg).toBeVisible();

  // The band: each figure, its label and value.
  await expect(pg.locator(".a2-fig-value")).toHaveText(["5", "3"]);

  // The group to act on, open: Eriphe's note first, no card on it.
  const secs = pg.locator(".a2-secs");
  await expect(secs.locator(".a2-head-title")).toHaveText("Courrier");
  await expect(secs.locator(".a2-sec-head")).toHaveText(["À traiter 1"]);
  await expect(secs.locator(".a2-sec .a2-sec-title")).toHaveText("Le comité attend ton avis");

  // The batch: folded, its count, its card's yes on its line, a verb.
  const batch = secs.locator(".a2-sec-fold.batch");
  await expect(batch.locator(".a2-sec-fold-name")).toHaveText("À supprimer");
  await expect(batch.locator(".a2-sec-fold-head .a2-count")).toHaveText("3");
  const yes = batch.locator(".a2-sec-fold-card .a2-inline-yes");
  await expect(yes).toBeVisible();
  await expect(yes).not.toHaveText(/^(Oui|Yes)$/);
  // « Pour info », folded: nothing waits on it.
  await expect(secs.locator(".a2-sec-fold:not(.batch) .a2-sec-fold-name")).toHaveText("Pour info");

  // Unfolded: three rows, all ticked; the user keeps the second.
  await batch.locator(".a2-sec-fold-head").click();
  const boxes = batch.locator("input.a2-tick");
  await expect(boxes).toHaveCount(3);
  await boxes.nth(1).uncheck();
  await expect(batch.locator(".a2-batch-kept")).toHaveText("1 gardé(s)");
  await yes.click();

  // A call for each mail that goes: the one kept is left out.
  await expect.poll(async () => ((await (await request.get("/e2e/tools")).json()).thrown as string[][]).flat().sort()).toEqual(["s1", "s4"]);
  expect(problems).toEqual([]);
});
