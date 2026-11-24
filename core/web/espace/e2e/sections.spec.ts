import { expect, test } from "./fixtures";

// A view in sections (list_sections.go), against the test core of
// TestEspaceScenario: a node whose block lists five faked mails
// (mail/tri), noted into two groups, in two Lists that filter them by
// their group. The brain's one call that deletes the first section's
// three mails is one card on their three rows: its Oui, once at the
// section's head, runs it, and the faked server gets exactly those. Seeded at /e2e/sections,
// on its own node.

test("one card on a section's rows runs on exactly those", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const seeded = await request.post("/e2e/sections");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Mails en sections"]');
  await expect(pg).toBeVisible();
  const drop = pg.locator(".a2-card").filter({ hasText: "À supprimer" });
  const info = pg.locator(".a2-card").filter({ hasText: "Pour info" });
  await expect(drop.locator(".a2-list > li")).toHaveCount(3);
  await expect(info.locator(".a2-list > li")).toHaveCount(2);
  await expect(info).toContainText("Compte rendu du comité");
  await expect(info).not.toContainText("Soldes d'hiver");
  await expect(info.locator(".a2-inline-yes")).toHaveCount(0); // nothing waits on its rows

  // One card on the three rows: its Oui once, at their head, never on
  // each row.
  const yeses = drop.locator(".a2-inline-yes");
  await expect(yeses).toHaveCount(1);
  await expect(drop.locator(".a2-listhead .a2-inline-yes")).toHaveCount(1);
  await expect(drop.locator(".a2-list > li .a2-inline-yes")).toHaveCount(0);
  await expect(drop.locator(".a2-inline-shared")).toHaveCount(0);
  await yeses.first().click();

  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).deleted).toEqual([["s1", "s3", "s4"]]);
  // The source read again: the section is empty, the other as it was.
  await expect(drop.locator(".a2-empty")).toHaveText("Rien à supprimer");
  await expect(info.locator(".a2-list > li")).toHaveCount(2);
  expect(problems).toEqual([]);
});
