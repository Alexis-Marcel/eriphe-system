import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { still } from "./rest";

// A page scrolls as one: its head stays, its document, its blocks and the
// cards waiting at its foot scroll together, and the wheel over it scrolls
// it, never the graph. Played on the scenario's test core
// (TestEspaceScenario), signed in as there; the page shown is « Ma
// journée »'s, its content made long here (a document, a long list of
// mails, two cards that wait), and nothing it does is written back: the
// answers and the view are kept in this test.


const mails = Array.from({ length: 40 }, (_, i) => ({ label: `Message ${i + 1} : point de la semaine, relance et suite à donner` }));
const longText = Array.from({ length: 8 }, (_, i) => `Paragraphe ${i + 1}. Ce qui est arrivé ce matin, ce qui attend une réponse et ce qui peut attendre demain, en quelques lignes.`).join("\n\n");

const cards = ["e2e-scroll-1", "e2e-scroll-2"].map((ref, i) => ({
  ref,
  card: 9001 + i,
  state: "pending",
  text: `Envoyer la réponse ${i + 1} ?`,
  verb: "Envoyer",
  why: "Un brouillon attend dans la page.",
}));

// fake makes the shown page long and keeps every write in the test.
async function fake(page: Page, answered: number[]) {
  // The view as the scenario left it: no page open.
  await page.route("**/espace/boot", async (route) => {
    const r = await route.fetch();
    const b = await r.json();
    if (b.view) b.view.open = [];
    delete b.view?.talk;
    await route.fulfill({ response: r, json: b });
  });
  await page.route(/\/api\/nodes\/[^/?]+(\?.*)?$/, async (route) => {
    if (route.request().method() !== "GET") return route.fulfill({ status: 204 });
    const r = await route.fetch();
    const p = await r.json();
    p.parts = [
      { text: longText },
      {
        block: {
          name: "mails",
          components: [
            { id: "root", component: "Column", children: ["items"] },
            { id: "items", component: "List", children: { componentId: "item", path: "/items" } },
            { id: "item", component: "Text", text: { "@path": "label" } },
          ],
          data: { items: mails },
        },
      },
    ];
    p.cards = cards.map((c) => ({ ...c, state: answered.includes(c.card) ? "approved" : "pending" }));
    await route.fulfill({ response: r, json: p });
  });
  await page.route(/\/api\/proposals\/e2e-scroll-\d$/, (route) => {
    const c = cards.find((x) => route.request().url().endsWith(x.ref))!;
    return route.fulfill({ json: { ...c, state: answered.includes(c.card) ? "approved" : "pending" } });
  });
  await page.route(/\/cards\/\d+\/(yes|no)$/, (route) => {
    answered.push(Number(route.request().url().split("/").at(-2)));
    return route.fulfill({ status: 204 });
  });
  await page.route("**/api/espace/view", (route) => route.fulfill({ status: 204 }));
}

async function signIn(page: Page) {
}

// shots saves the screen in both themes when SHOTS names a folder, as the
// scenario does.
async function shots(page: Page, name: string) {
  const dir = process.env.SHOTS;
  if (!dir) return;
  for (const theme of ["dark", "light"]) {
    await page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
    await page.screenshot({ path: `${dir}/${name}-${theme}.png` });
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

test.describe("a long page with its cards", () => {
  test.use({ viewport: { width: 1240, height: 680 }, colorScheme: "dark" });

  test("scrolls as one, its last card reachable", async ({ page }) => {
    const answered: number[] = [];
    await fake(page, answered);
    await signIn(page);
    await page.goto("/espace");

    await page.locator('.gr-node:not(.external) .gr-core[aria-label^="Ma journée"]').click();
    const pg = page.locator('article.pg[aria-label="Ma journée"]');
    await expect(pg.locator(".pg-foot .a2-proposal")).toHaveCount(2);
    await expect(pg).toContainText("Message 40");
    const body = pg.locator(".pg-body");
    // The cards are in the body, which is the page's one scroll; no block
    // of it scrolls on its own.
    await expect(body.locator(".pg-foot")).toHaveCount(1);
    const nested = await body.evaluate((b) =>
      [...b.querySelectorAll("*")].filter((e) => {
        const s = getComputedStyle(e);
        return /(auto|scroll)/.test(s.overflowY) && e.scrollHeight > e.clientHeight + 1;
      }).length,
    );
    expect(nested).toBe(0);
    // The page stays above the bar.
    const box = (await still(page.locator('article.pg[aria-label="Ma journée"]')))!;
    expect(box.y + box.height).toBeLessThanOrEqual(680);
    // The camera, at rest: its scale and where the world is. A wheel that
    // moved it would move it by a hundred.
    const world = () =>
      page.locator(".gr-world").evaluate((e) => {
        const m = new DOMMatrix(getComputedStyle(e).transform);
        return { k: Math.round(m.a * 1000) / 1000, x: m.e, y: m.f };
      });
    const before = await world();
    const unmoved = async () => {
      const now = await world();
      expect(now.k).toBe(before.k);
      expect(Math.abs(now.x - before.x)).toBeLessThan(5);
      expect(Math.abs(now.y - before.y)).toBeLessThan(5);
    };

    // The wheel over the body scrolls it; the graph does not move.
    expect(await body.evaluate((b) => b.scrollHeight > b.clientHeight)).toBe(true);
    const b = (await body.boundingBox())!;
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
    await page.mouse.wheel(0, 300);
    await expect.poll(() => body.evaluate((e) => e.scrollTop)).toBeGreaterThan(0);
    // Over its head too, it is the body that scrolls.
    const top = await body.evaluate((e) => e.scrollTop);
    const head = (await pg.locator(".pg-head h2").boundingBox())!;
    await page.mouse.move(head.x + 10, head.y + head.height / 2);
    await page.mouse.wheel(0, 200);
    await expect.poll(() => body.evaluate((e) => e.scrollTop)).toBeGreaterThan(top);
    // A pinch over the page zooms nothing.
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
    await page.keyboard.down("Control");
    await page.mouse.wheel(0, -200);
    await page.keyboard.up("Control");
    await page.waitForTimeout(300);
    await unmoved();

    // The keyboard scrolls it too, once it has the focus.
    await body.evaluate((e) => (e.scrollTop = 0));
    await body.focus();
    await page.keyboard.press("PageDown");
    await expect.poll(() => body.evaluate((e) => e.scrollTop)).toBeGreaterThan(0);

    // The last card's yes, scrolled to, answers it.
    const last = pg.locator(".pg-foot .a2-proposal").last();
    const yes = last.getByRole("button", { name: "Envoyer", exact: true });
    await yes.scrollIntoViewIfNeeded();
    const y = (await yes.boundingBox())!;
    expect(y.y + y.height).toBeLessThanOrEqual(680);
    await shots(page, "page-scrolled");
    await yes.click();
    await expect.poll(() => answered).toEqual([9002]);
    // Decided, it leaves the foot; the other still waits.
    await expect(pg.locator(".pg-foot .a2-proposal")).toHaveCount(1);
    await expect(pg.locator(".pg-foot")).not.toContainText("Envoyer la réponse 2 ?");
    await unmoved();
  });
});

test("a long page with its cards, on a phone", async ({ page }) => {
  const answered: number[] = [];
  await page.setViewportSize({ width: 390, height: 844 });
  await fake(page, answered);
  await signIn(page);
  await page.goto("/espace");
  await page.locator(".ls-row").filter({ hasText: "Ma journée" }).locator("button").click();
  const pg = page.locator('article.pg[aria-label="Ma journée"]');
  await expect(pg.locator(".pg-foot .a2-proposal")).toHaveCount(2);
  // The list scrolls the whole page: its body has no scroll of its own.
  expect(await pg.locator(".pg-body").evaluate((b) => b.scrollHeight <= b.clientHeight + 1)).toBe(true);
  const yes = pg.locator(".pg-foot .a2-proposal").last().getByRole("button", { name: "Envoyer", exact: true });
  await yes.scrollIntoViewIfNeeded();
  await yes.click();
  await expect.poll(() => answered).toEqual([9002]);
});
