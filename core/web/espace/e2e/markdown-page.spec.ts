import { expect, test } from "./fixtures";

// A node's page written in Markdown, against the test core of
// TestEspaceScenario (seeded at /e2e/page): its tasks as rows, a task's
// detail folded, its callouts drawn by the workspace's components
// (figures, steps, a chart). The user ticks a task: eriphe-core writes
// [x] in the file, a version by the user, and the tick is still there
// once the page is read again.

test("a Markdown page: lists, callouts and a chart; a tick survives a reload", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();
  const seeded = await request.post("/e2e/page");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const { id } = (await seeded.json()) as { id: string };
  const pg = page.locator('article.pg[aria-label="Terrasse en Markdown"]');
  await expect(pg).toBeVisible();

  // When it was written, its heading with what is left to do, its tasks.
  await expect(pg.locator(".md-written")).toHaveText("Écrit le 26/11/2026");
  await expect(pg.locator(".md-h")).toHaveText("À faire1");
  const rows = pg.locator(".md-item");
  await expect(rows).toHaveCount(2);
  await expect(rows.first().locator(".md-title")).toHaveText("Répondre au devis de Sam");
  await expect(rows.first().locator(".md-comment")).toHaveText("avant vendredi");
  await expect(rows.first().locator(".md-due")).toHaveAttribute("datetime", "2026-11-28");
  await expect(rows.nth(1)).toHaveClass(/md-done/);
  // Its detail, folded: a click opens it.
  await expect(pg.locator(".md-detail")).toHaveCount(0);
  await rows.first().locator(".md-row").click();
  await expect(pg.locator(".md-detail")).toContainText("4 200 €, la pose comprise");

  // The callouts: figures (a change in its own unit), steps, a chart.
  await expect(pg.locator(".a2-fig-label")).toHaveText(["Prévu", "Dépensé"]);
  await expect(pg.locator(".a2-fig-delta").first()).toContainText("120");
  await expect(pg.locator(".a2-step")).toHaveCount(3);
  await expect(pg.locator(".a2-step-current")).toContainText("Répondre au devis");
  await expect(pg.locator(".a2-head-title", { hasText: "Budget par poste" })).toBeVisible();
  await expect(pg.locator(".a2-chart svg path.a2-bar")).toHaveCount(4); // two posts, two series

  // The tick: the file says [x], in one version by the user.
  const box = rows.first().locator("input.a2-tick");
  await expect(box).toBeEnabled();
  const ticked = page.waitForResponse((r) => r.url().endsWith(`/api/nodes/${id}/task`) && r.request().method() === "POST");
  await box.check();
  expect((await ticked).status()).toBe(200);
  await expect(box).toBeChecked();
  const md = await (await request.get(`/api/nodes/${id}/md`)).json();
  expect(md.body).toContain("- [x] Répondre au devis de Sam");
  const p = await (await request.get(`/api/nodes/${id}`)).json();
  expect(p.versions.at(-1).author).toBe("user");

  // Read again: still ticked (its page opened from its bubble, if the
  // view had not kept it open yet).
  await page.reload();
  const core = page.locator(`.gr-node[data-node="${id}"] .gr-core`);
  await expect(pg.or(core).first()).toBeVisible();
  if (!(await pg.isVisible())) await core.click();
  await expect(pg).toBeVisible();
  await expect(pg.locator(".md-item").first().locator("input.a2-tick")).toBeChecked();
  await expect(pg.locator(".md-item").first()).toHaveClass(/md-done/);

  expect(problems).toEqual([]);
});
