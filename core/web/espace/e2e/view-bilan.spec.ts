import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { rest } from "./rest";

// A page as a bilan (docs/node-format.md), against the test core of
// TestEspaceScenario: the bilan on top, drawn larger; the mails of two
// accounts (mail/bilan) as references, the page written after an external
// read (one sign in the page's head, nothing on its lines); « À traiter »
// whose rows say the mails, Sam's with its one Oui (a reply its skill
// drafted, a card on the mail); « À jeter » whose three rows share one
// card (a trash per mail, folded): a Oui on any answers it. The user reads
// Sam's mail as a letter with what Oui sends, says yes on the row, says
// yes to the trash, then enlarges and resizes the page. Seeded at
// /e2e/bilan, on its own node.


test("a page as a bilan: a proposal's Oui, a shared card, an item read as a letter, a page as a window", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const seeded = await request.post("/e2e/bilan");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Mes mails du jour"]');
  await expect(pg).toBeVisible();

  // The bilan: its first paragraph larger than the « Pour info » after it.
  const lead = pg.locator(".pg-doc .md-p").first();
  await expect(lead).toHaveText("1 réponse attendue : Sam, avant vendredi. 1 hameçonnage dans le lot à jeter.");
  const size = (n: number) => pg.locator(".pg-doc .md-p").nth(n).evaluate((p) => parseFloat(getComputedStyle(p).fontSize));
  expect(await size(0)).toBeGreaterThan(await size(1));
  // Someone else's words: one sign in the head (its ⚠, no word), nothing on the lines.
  await expect(pg.locator(".pg-title .ext-mark")).toHaveCount(1);
  await expect(pg.locator(".a2-inline-ext")).toHaveCount(0);
  await expect(pg.getByText("extérieur", { exact: true })).toHaveCount(0);

  // « À traiter »: Sam's row with its one yes, its verb.
  const rows = pg.locator(".md-item");
  const todo = rows.filter({ hasText: "Devis terrasse" });
  const lease = rows.filter({ hasText: "Avenant au bail" });
  await expect(todo).toHaveCount(1);
  await expect(todo.locator(".a2-inline-yes")).toHaveText("Envoyer");
  await expect(todo.locator(".a2-inline-yes")).toHaveAttribute("title", "Envoyer la réponse · « Devis terrasse » · Sam");
  await expect(lease.locator(".a2-inline-yes")).toHaveCount(0);
  await expect(pg.locator(".pg-foot")).toHaveCount(0); // the cards are on their rows, not at the foot
  await shots(page, "1-bilan");

  // Sam's mail, opened: a letter, then what Oui sends, the reply itself.
  await todo.locator(".md-title").click();
  const read = todo.locator(".a2-read");
  await expect(read.locator(".a2-read-from")).toContainText("Sam");
  await expect(read.locator(".a2-read-addr")).toHaveText("sam@example.org");
  await expect(read.locator(".a2-read-title")).toHaveText("Devis terrasse");
  await expect(read.locator(".a2-read-text")).toContainText("Peux-tu me dire avant vendredi");
  const onYes = read.locator(".a2-onyes");
  await expect(onYes.locator(".a2-onyes-kicker")).toHaveText("Si tu acceptes");
  await expect(onYes.locator("figcaption")).toHaveText("Texte");
  await expect(onYes.locator(".a2-onyes-text")).toContainText("tu peux lancer la commande");
  await expect(read.locator("summary")).toHaveText(["détails"]);
  await expect(read.locator(".a2-taint")).toHaveCount(0); // the page's head says it
  await shots(page, "2-mail-opened");
  await todo.locator(".md-title").click();
  await expect(read).toHaveCount(0);

  // Its Oui on the row: the reply goes, the row says it is done.
  await todo.locator(".a2-inline-yes").first().click();
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).replied).toEqual(["a1"]);
  await expect(todo.locator(".a2-inline-yes")).toHaveCount(0);
  await expect(lease).toHaveCount(1);

  // « À jeter »: three rows share one card, each says how many share it;
  // a Oui on any of them answers it: the three mails go.
  const junk = rows.filter({ has: page.locator('.a2-inline-shared') });
  await expect(junk).toHaveCount(3);
  await expect(junk.first().locator(".a2-inline-shared")).toContainText("3");
  await expect(junk.first().locator(".a2-inline-yes")).toHaveText("Mettre 3 mails à la corbeille");
  await shots(page, "3-shared");
  await junk.first().locator(".a2-inline-yes").click();
  // Its own calls, each as it was kept: the test core is shared with the
  // other files (view-calls.spec.ts trashes too).
  const mine = (all: string[]) => all.filter((x) => /:[aij]\d/.test(x)).sort();
  await expect.poll(async () => mine((await (await request.get("/e2e/tools")).json()).trashed)).toEqual(["perso:j1", "perso:j2", "perso:j3"]);
  // Decided, the rows ask nothing more.
  await expect(pg.locator(".a2-inline-yes")).toHaveCount(0);
  await shots(page, "5-after-actions");

  // The ⋯ menu: what was decided.
  await pg.getByRole("button", { name: "Plus" }).click();
  await expect(page.locator(".pg-menu .pg-settled li").first()).toContainText("Fait");
  await shots(page, "6-menu-settled");
  await page.keyboard.press("Escape");

  // A page as a window: enlarged (its button, its head's double click),
  // its text at a measure that reads; resized from the keyboard.
  const box = page.locator(".gr-page", { has: pg });
  const w0 = (await box.boundingBox())!.width;
  // its own width, in the graph's units: what the camera's scale leaves out
  const own = () => box.evaluate((e) => (e as HTMLElement).offsetWidth);
  const own0 = await own();
  await pg.getByRole("button", { name: "Agrandir" }).click();
  await expect(box).toHaveClass(/\bbig\b/);
  await expect.poll(async () => (await box.boundingBox())!.width).toBeGreaterThan(w0 + 200);
  const text = await pg.locator(".pg-doc .md-p").first().boundingBox();
  expect(text!.width).toBeLessThanOrEqual(761);
  await rest(page); // the camera frames it
  // No bubble rests behind the bar: the page keeps the bar's room clear.
  await expect
    .poll(async () =>
      page.evaluate(() => {
        const bar = document.querySelector(".cs-bar")!.getBoundingClientRect();
        return [...document.querySelectorAll(".gr-node .gr-core")].filter((n) => {
          const b = n.getBoundingClientRect();
          return b.right > bar.left && b.left < bar.right && b.bottom > bar.top && b.top < bar.bottom;
        }).length;
      }),
    )
    .toBe(0);
  await shots(page, "7-enlarged");
  await pg.locator(".pg-head .pg-title-tx").dblclick();
  await expect(box).not.toHaveClass(/\bbig\b/);
  // Back to its size. On screen it may differ a little: the bubbles the
  // enlarged page moved aside come back their own way, and the camera
  // frames the graph as it is then.
  await expect.poll(own).toBe(own0);
  const resize = pg.locator(".pg-resize");
  await resize.focus();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  // Two steps of 40, in the graph's units: on screen it depends on how
  // the camera frames the wider page then.
  await expect.poll(own).toBe(own0 + 80);
  await rest(page);
  await shots(page, "8-resized");
  // Closed.
  await pg.locator(".pg-x").click();
  await expect(pg).toHaveCount(0);
  expect(problems).toEqual([]);
});

// shots saves the screen in both themes when SHOTS names a folder, as the
// scenario does.
async function shots(page: Page, name: string) {
  const dir = process.env.SHOTS;
  if (!dir) return;
  // The page come to rest: opened, the camera framing it.
  await rest(page);
  for (const theme of ["light", "dark"]) {
    await page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
    await page.screenshot({ path: `${dir}/${name}-${theme}.png` });
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}
