import { expect, test } from "@playwright/test";

// A node's glance (core/node_glance.go), against the test core of
// TestEspaceScenario: asked in the bar, Eriphe gives the node that reads
// the house's consumption a glance (threshold 10 % from the day's first
// value). The figure is moved at /e2e/conso, and the node reads it again,
// with no word to Eriphe. Past its threshold, its bubble becomes a tile
// that shows it, live; opened, it is a circle again, after a reload too.

const password = "correct horse battery"; // testPassword, core/web_test.go

test("a figure that moved past its threshold shows at a glance, until it is opened", async ({ page, request }) => {
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
  const input = page.locator(".cs-bar input");
  await expect(input).toBeVisible();

  // Asked: the glance is set, on a node opened on the side.
  await input.fill("Garde un œil sur la consommation de la maison.");
  await input.press("Enter");
  let id = "";
  await expect
    .poll(async () => {
      const r = await request.post("/e2e/conso?kw=2");
      if (!r.ok()) return "";
      id = (await r.json()).id;
      return id;
    })
    .not.toBe("");
  const bubble = page.locator(`.gr-node[data-node="${id}"]`);
  const core = bubble.locator(".gr-core");
  await expect(core).toBeVisible();

  // Under its threshold: a circle, no tile.
  expect((await (await request.post("/e2e/conso?kw=2.1")).json()).glance.shown).toBeFalsy();
  await expect(bubble).not.toHaveClass(/tiled/);

  // Past it (+30 %): a tile with the figure, its words for a screen reader.
  let reloaded = false;
  page.on("framenavigated", (f) => f === page.mainFrame() && (reloaded = true));
  expect((await (await request.post("/e2e/conso?kw=2.6")).json()).glance.shown).toBe(true);
  await expect(bubble).toHaveClass(/tiled/);
  await expect(bubble.locator(".gr-core.tile .gl-label")).toHaveText("Conso");
  await expect(bubble.locator(".gl-delta")).toHaveText("▲30 %");
  await expect(bubble.locator(".gl-spark .gl-line")).toHaveCount(1);
  await expect(core).toHaveAttribute("aria-label", /Énergie maison.*Conso : en hausse de 30 %/);
  // a 148 × 60 rectangle, whatever the camera's scale
  const ratio = async () => {
    const b = (await core.boundingBox())!;
    return Math.round((b.width / b.height) * 10) / 10;
  };
  await expect.poll(ratio).toBe(2.5);
  await expect(bubble.locator(".gl-face.good, .gl-face.bad")).toHaveCount(0);

  // It moves again: the tile follows, live.
  await request.post("/e2e/conso?kw=2.8");
  await expect(bubble.locator(".gl-delta")).toHaveText("▲40 %");
  expect(reloaded).toBe(false);

  // Opened: seen; closed, a circle again.
  await core.click();
  const pg = page.locator('article.pg[aria-label="Énergie maison"]');
  await expect(pg).toBeVisible();
  await pg.getByRole("button", { name: "Fermer cette page" }).click();
  await expect(pg).toHaveCount(0);
  await expect(core).toBeVisible();
  await expect(bubble).not.toHaveClass(/tiled/);
  await expect.poll(ratio).toBe(1);

  // Seen on eriphe-core's side: a reload, and a new value past the
  // threshold still, keep it a circle.
  await request.post("/e2e/conso?kw=2.9");
  await page.reload();
  await expect(core).toBeVisible();
  await expect(bubble).not.toHaveClass(/tiled/);
  expect((await (await request.post("/e2e/conso?kw=2.9")).json()).glance.seen).toBe(true);

  // Back under, then past again: a tile once more.
  await request.post("/e2e/conso?kw=2.05");
  await request.post("/e2e/conso?kw=2.7");
  await expect(bubble).toHaveClass(/tiled/);
  await expect(bubble.locator(".gl-delta")).toHaveText("▲35 %");
  expect(problems).toEqual([]);
});
