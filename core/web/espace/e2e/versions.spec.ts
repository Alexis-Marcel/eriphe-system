import { expect, test, type Locator, type Page } from "@playwright/test";

// A page with many versions: one chip in its head, the version shown,
// that opens their list; the title stays on one line, cut short, its
// « extérieur » badge beside it, nothing past the page's edge. Played on
// the scenario's test core (TestEspaceScenario), signed in as there, on a
// fresh urgent mail (POST /e2e/mail: an external node a skill opened, the
// one thing this file adds to the core), and on the standing « Mails »;
// their pages are given 17 versions here, and the view is not written.

const password = "correct horse battery"; // testPassword, core/web_test.go
const title = "Urgent : la prod renvoie des 500";
const faked = [title, "Mails"];

// fake gives those pages 17 versions, each its own text,
// and keeps the view in the test.
async function fake(page: Page, asked: number[]) {
  await page.route("**/espace/boot", async (route) => {
    const r = await route.fetch();
    const b = await r.json();
    if (b.view) b.view.open = [];
    delete b.view?.talk;
    await route.fulfill({ response: r, json: b });
  });
  await page.route(/\/api\/nodes\/[^/?]+(\?.*)?$/, async (route) => {
    if (route.request().method() !== "GET") return route.fulfill({ status: 204 });
    const url = new URL(route.request().url());
    const n = Number(url.searchParams.get("version")) || 17;
    url.search = "";
    const r = await route.fetch({ url: url.toString() });
    const p = await r.json();
    if (!faked.includes(p.node?.title)) return route.continue();
    asked.push(n);
    p.version = n;
    p.shift = 0;
    p.versions = Array.from({ length: 17 }, (_, i) => ({ n: i + 1, author: i % 3 ? "brain" : "user", at: new Date(Date.now() - (17 - i) * 3_600_000).toISOString() }));
    p.parts = [{ text: `Le texte de la version ${n}.` }];
    await route.fulfill({ response: r, json: p });
  });
  await page.route("**/api/espace/view", (route) => route.fulfill({ status: 204 }));
}

async function signIn(page: Page) {
  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
}

// shots saves the screen in both themes when SHOTS names a folder.
async function shots(page: Page, name: string) {
  const dir = process.env.SHOTS;
  if (!dir) return;
  for (const theme of ["light", "dark"]) {
    await page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
    await page.screenshot({ path: `${dir}/${name}-${theme}.png` });
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

// still waits until a box stops moving (the page opening, the camera).
async function still(el: Locator) {
  let last = "";
  for (let i = 0; i < 60; i++) {
    const b = await el.boundingBox();
    const now = JSON.stringify(b && [Math.round(b.x), Math.round(b.y), Math.round(b.width), Math.round(b.height)]);
    if (b && now === last) return b;
    last = now;
    await el.page().waitForTimeout(150);
  }
  throw new Error("the page does not settle");
}

// oneLine checks the head: the title on one line, its badge on that line,
// every control of the title's row inside the page.
async function oneLine(pg: Locator, name = title) {
  const box = await still(pg);
  const tx = pg.locator(".pg-title-tx");
  await expect(tx).toHaveAttribute("title", name);
  const line = await tx.evaluate((e) => [e.offsetHeight, parseFloat(getComputedStyle(e).lineHeight)]);
  expect(line[0]).toBeLessThanOrEqual(line[1] + 2);
  const t = (await tx.boundingBox())!;
  // whole, or cut short but never to nothing (in the layout's pixels: the
  // graph's camera may scale the page)
  const [w, full] = await tx.evaluate((e) => [e.offsetWidth, e.scrollWidth]);
  expect(w).toBeGreaterThanOrEqual(Math.min(full, 60));
  for (const sign of await pg.locator(".pg-title h2 .pg-badge.warn, .pg-title .pg-standing").all()) {
    const b = (await sign.boundingBox())!;
    expect(Math.abs(b.y + b.height / 2 - (t.y + t.height / 2))).toBeLessThan(t.height / 2);
    expect(b.x).toBeGreaterThan(t.x);
    expect(b.x - (t.x + t.width)).toBeLessThan(48); // beside it, not pushed away
  }
  for (const b of await pg.locator(".pg-title > *").all()) {
    const x = (await b.boundingBox())!;
    expect(x.x + x.width).toBeLessThanOrEqual(box.x + box.width);
    expect(Math.abs(x.y + x.height / 2 - (t.y + t.height / 2))).toBeLessThan(t.height);
  }
}

test.describe("a page with 17 versions", () => {
  test.use({ viewport: { width: 1100, height: 760 } });

  test("folds them into one chip, a menu to choose", async ({ page, request }) => {
    const id = (await (await request.post("/e2e/mail")).json()).id as string;
    const asked: number[] = [];
    await fake(page, asked);
    await signIn(page);
    await page.goto("/espace");
    await page.locator(`[data-node="${id}"] .gr-core`).click();
    const pg = page.locator("article.pg").filter({ has: page.locator(".pg-title-tx", { hasText: title }) });
    await expect(pg).toContainText("Le texte de la version 17.");

    const chip = pg.locator(".pg-vchip");
    await expect(chip).toHaveCount(1);
    await expect(chip).toHaveText("v17");
    await expect(pg.locator(".pg-title button")).toHaveCount(4); // the chip, archive, ⋯, ✕
    await oneLine(pg);
    await shots(page, "versions-closed");

    // Its menu: the 17, newest first, who made each and when; the last
    // marked so. It scrolls; the one shown is in view.
    await chip.click();
    const list = page.getByRole("listbox", { name: "Versions" });
    const opts = list.getByRole("option");
    await expect(opts).toHaveCount(17);
    await expect(opts.locator("b")).toHaveText(Array.from({ length: 17 }, (_, i) => `v${17 - i}`));
    await expect(opts.first()).toContainText("dernière");
    await expect(opts.first()).toHaveAttribute("aria-selected", "true");
    await expect(opts.first()).toBeFocused();
    expect(await list.evaluate((e) => e.scrollHeight > e.clientHeight)).toBe(true);
    const lb = (await list.boundingBox())!;
    expect(lb.y + lb.height).toBeLessThanOrEqual(760);
    expect(lb.x).toBeGreaterThanOrEqual(0);
    await shots(page, "versions-open");
    // The wheel over it scrolls it.
    await page.mouse.move(lb.x + lb.width / 2, lb.y + lb.height / 2);
    await page.mouse.wheel(0, 300);
    await expect.poll(() => list.evaluate((e) => e.scrollTop)).toBeGreaterThan(0);

    // Escape closes it, the focus back on the chip.
    await page.keyboard.press("Escape");
    await expect(list).toHaveCount(0);
    await expect(chip).toBeFocused();

    // v5, chosen with the keyboard: shown, the chip marked as not the last.
    await page.keyboard.press("ArrowDown");
    await expect(opts.first()).toBeFocused();
    for (let i = 0; i < 12; i++) await page.keyboard.press("ArrowDown");
    await expect(opts.nth(12)).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(pg).toContainText("Le texte de la version 5.");
    expect(asked).toContain(5);
    await expect(chip).toHaveText("v5");
    await expect(chip).toHaveClass(/old/);
    await expect(pg.locator(".pg-old")).toContainText("Tu regardes la v5.");
    await oneLine(pg);

    // A click outside closes the menu; the way back is the last.
    await chip.click();
    await expect(opts.nth(12)).toHaveAttribute("aria-selected", "true");
    await pg.locator(".pg-body").click({ position: { x: 20, y: 20 } });
    await expect(list).toHaveCount(0);
    await pg.locator(".pg-old button").click();
    await expect(chip).toHaveText("v17");
    await expect(chip).not.toHaveClass(/old/);
  });
});

test("pages with 17 versions, two open on a narrow screen", async ({ page, request }) => {
  const id = (await (await request.post("/e2e/mail")).json()).id as string;
  await page.setViewportSize({ width: 800, height: 760 });
  await fake(page, []);
  await signIn(page);
  await page.goto("/espace");
  await page.locator(`[data-node="${id}"] .gr-core`).click();
  const mail = page.locator("article.pg").filter({ has: page.locator(".pg-title-tx", { hasText: title }) });
  await expect(mail).toContainText("Le texte de la version 17.");
  // « Mails », standing, opened beside it: its ∞ by its title.
  await page.locator('.gr-node:not(.external) .gr-core[aria-label^="Mails"]').first().click({ modifiers: ["Meta"] });
  const mails = page.locator("article.pg").filter({ has: page.locator(".pg-title-tx", { hasText: /^Mails$/ }) });
  await expect(mails).toContainText("Le texte de la version 17.");
  await expect(mails.locator(".pg-standing")).toBeVisible();
  await expect(page.locator("article.pg")).toHaveCount(2);
  for (const [pg, name] of [[mail, title], [mails, "Mails"]] as const) {
    await expect(pg.locator(".pg-vchip")).toHaveText("v17");
    await oneLine(pg, name);
  }
  await shots(page, "versions-narrow");
});

test("a page with 17 versions, on a phone", async ({ page, request }) => {
  const id = (await (await request.post("/e2e/mail")).json()).id as string;
  await page.setViewportSize({ width: 390, height: 844 });
  await fake(page, []);
  await signIn(page);
  await page.goto("/espace");
  await page.locator(".ls-row").filter({ hasText: title }).first().locator("button").first().click();
  const pg = page.locator("article.pg").filter({ has: page.locator(".pg-title-tx", { hasText: title }) });
  await expect(pg).toContainText("Le texte de la version 17.");
  const box = (await pg.boundingBox())!;
  // The title on its own line, cut short; the chip and the rest under it,
  // inside the page.
  const tx = pg.locator(".pg-title-tx");
  const line = await tx.evaluate((e) => [e.offsetHeight, parseFloat(getComputedStyle(e).lineHeight)]);
  expect(line[0]).toBeLessThanOrEqual(line[1] + 2);
  for (const b of await pg.locator(".pg-title > *").all()) {
    const x = (await b.boundingBox())!;
    expect(x.x + x.width).toBeLessThanOrEqual(box.x + box.width);
  }
  await pg.locator(".pg-vchip").click();
  const list = page.getByRole("listbox", { name: "Versions" });
  await expect(list.getByRole("option")).toHaveCount(17);
  const lb = (await list.boundingBox())!;
  expect(lb.x).toBeGreaterThanOrEqual(0);
  expect(lb.x + lb.width).toBeLessThanOrEqual(390);
  await list.getByRole("option").nth(12).click();
  await expect(pg).toContainText("Le texte de la version 5.");
  await shots(page, "versions-phone");
});
