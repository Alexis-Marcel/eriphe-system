import { expect, test } from "./fixtures";

// A view in sections (list_sections.go), against the test core of
// TestEspaceScenario: a node whose block lists five faked mails
// (mail/tri), noted into two groups, in two Lists that filter them by
// their group, and an action that deletes (mail/delete) on the first.
// « Tout supprimer » asks once more, naming the three, and the faked
// server gets exactly those. Seeded at /e2e/sections, on its own node.


test("a section's action runs on exactly the rows it showed", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  expect((await request.post("/e2e/sections")).ok()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Mails en sections"]');
  await expect(pg).toBeVisible();
  const drop = pg.locator(".a2-card").filter({ hasText: "À supprimer" });
  const info = pg.locator(".a2-card").filter({ hasText: "Pour info" });
  await expect(drop.locator(".a2-pickable > li")).toHaveCount(3);
  await expect(info.locator(".a2-list > li")).toHaveCount(2);
  await expect(info).toContainText("Compte rendu du comité");
  await expect(info).not.toContainText("Soldes d'hiver");
  await expect(info.locator(".a2-pickwhole")).toHaveCount(0); // no action of its own

  const whole = drop.locator(".a2-pickwhole");
  await expect(whole).toHaveText("Tout supprimer");
  await whole.click();
  const ask = drop.locator(".a2-pickask");
  await expect(ask).toBeVisible();
  await expect(ask.locator("li")).toHaveText(["Soldes d'hiver · promo@example.org", "Votre colis est en route · colis@example.org", "Lettre du dimanche · news@example.org"]);
  await ask.getByRole("button", { name: "Lancer" }).click();

  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).deleted).toEqual([["s1", "s3", "s4"]]);
  // The source read again: the section is empty, the other as it was.
  await expect(drop.locator(".a2-empty")).toHaveText("Rien à supprimer");
  await expect(info.locator(".a2-list > li")).toHaveCount(2);
  expect(problems).toEqual([]);
});
