import { expect, test } from "./fixtures";

// An action on a heading's items, against the test core of
// TestEspaceScenario (seeded at /e2e/do): « Jeter ces mails » under
// « À jeter » is said in eriphe-core's words, each mail under the heading
// has its box, ticked; the user unticks one and says yes: the others go to
// the trash, one call, and the line shows it done. The mail of the next
// heading is not touched.

test("a do: under a heading: untick one, yes, the others are trashed", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();
  const seeded = await request.post("/e2e/do");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Tri des mails"]');
  await expect(pg).toBeVisible();

  const head = pg.locator(".md-do[data-line='2']");
  await expect(head.locator(".a2-inline-what")).toHaveText("Mettre 3 mails à la corbeille");
  await expect(head).not.toContainText("Jeter ces mails");
  const rows = pg.locator(".md-item");
  await expect(rows).toHaveCount(4);
  for (const i of [0, 1, 2]) await expect(rows.nth(i).locator("input.md-do-tick")).toBeChecked();
  await expect(rows.nth(3).locator("input.md-do-tick")).toHaveCount(0);

  // The bill stays.
  await rows.nth(2).locator("input.md-do-tick").uncheck();
  await expect(head.locator(".a2-inline-what")).toHaveText("Mettre 2 mails à la corbeille");
  await head.locator(".a2-inline-yes").click();

  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).trashed as string[]).toContainEqual(":d1,d2");
  const trashed = (await (await request.get("/e2e/tools")).json()).trashed as string[];
  expect(trashed.join(" ")).not.toMatch(/d3|d4/);
  await expect(pg.locator(".md-do[data-line='2'] .a2-inline-state")).toBeVisible();

  expect(problems).toEqual([]);
});
