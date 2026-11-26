import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { rest } from "./rest";

// A page as a bilan (docs/node-format.md), against the test core of
// TestEspaceScenario: the bilan on top, drawn larger; one block that reads
// the mails of two accounts (mail/bilan) once, written after an external
// read (one sign in the page's head, nothing on the block); « À
// traiter » whose rows say what Eriphe proposes, Sam's with its one Oui
// (a reply its skill drafted, a card on the mail); « À jeter » as a batch
// whose rows share one card (a trash per mail, folded); « Pour info »
// folded. The user reads Sam's mail as a letter with what Oui sends, says
// yes on the row, unticks one mail of the batch and says yes to the
// others on its line, then enlarges and
// resizes the page. Seeded at /e2e/bilan, on its own node.


test("a page as a bilan: a proposal's Oui, a batch, an item read as a letter, a page as a window", async ({ page, request }) => {
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
  const lead = pg.locator(".pg-doc > .md-p").first();
  await expect(lead).toHaveText("1 réponse attendue : Sam, avant vendredi. 1 hameçonnage dans le lot à jeter.");
  const size = (n: number) => pg.locator(".pg-doc > .md-p").nth(n).evaluate((p) => parseFloat(getComputedStyle(p).fontSize));
  expect(await size(0)).toBeGreaterThan(await size(1));
  // Someone else's words: one sign in the head (its ⚠, no word), nothing on the block.
  await expect(pg.locator(".pg-title .ext-mark")).toHaveCount(1);
  await expect(pg.locator(".a2-block.ext")).toHaveCount(1);
  await expect(pg.locator(".a2-block-mark")).toHaveCount(0); // no edge, no dot: the head says it
  await expect(pg.getByText("extérieur", { exact: true })).toHaveCount(0);

  // « À traiter »: each row what Eriphe proposes; Sam's with its one yes, its verb.
  const todo = pg.locator(".a2-openrow").filter({ hasText: "Répondre à Sam" });
  const lease = pg.locator(".a2-openrow").filter({ hasText: "À lire : l'avenant au bail" });
  await expect(todo).toHaveCount(1);
  await expect(todo.locator(".a2-inline-yes")).toHaveText("Envoyer");
  await expect(todo.locator(".a2-inline-yes")).toHaveAttribute("title", "Envoyer la réponse · « Devis terrasse » · Sam");
  await expect(lease.locator(".a2-inline-yes")).toHaveCount(0);
  await expect(pg.locator(".pg-foot")).toHaveCount(0); // the card is on its row, not at the foot
  await shots(page, "1-bilan");

  // Sam's mail, opened: a letter, then what Oui sends, the reply itself.
  await todo.locator(".a2-openbtn").click();
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
  await todo.locator(".a2-openclose").click();

  // Its Oui on the row: the reply goes, the mail is settled and leaves.
  await todo.locator(".a2-inline-yes").click();
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).replied).toEqual(["a1"]);
  await expect(todo).toHaveCount(0);
  await expect(lease).toHaveCount(1);

  // « À jeter »: a batch line carrying its rows' one card, all ticked;
  // opened, one mail kept: its Oui says how many go.
  const batch = pg.locator(".a2-batch").filter({ hasText: "à jeter" });
  // « Pour info »: a batch with no card, its line only.
  const infos = pg.locator(".a2-batch").filter({ hasText: "pour info" });
  await expect(infos.locator(".a2-fold-label")).toHaveText("Les 2 mails pour info");
  await expect(infos.locator(".a2-inline-yes")).toHaveCount(0);
  await expect(batch.locator(".a2-fold-label")).toHaveText("3 mails inutiles à jeter");
  const yes = batch.locator(".a2-fold-card .a2-inline-yes");
  await expect(yes).toHaveText("Mettre 3 mails à la corbeille");
  await batch.locator(".a2-fold-head").click();
  await expect(batch.locator(".a2-batchrows .a2-inline-yes")).toHaveCount(0); // its rows are quiet: the Oui is the line's
  await batch.getByRole("checkbox", { name: "Décocher « Lettre du dimanche » : le garder" }).uncheck();
  await expect(batch.locator(".a2-batch-kept")).toHaveText("1 gardé(s)");
  await expect(yes).toHaveText("Mettre 2 mails à la corbeille");
  await shots(page, "3-batch-open");
  await yes.click();
  // Its own calls, each as it was kept: the test core is shared with the
  // other files (view-calls.spec.ts trashes too).
  const mine = (all: string[]) => all.filter((x) => /:[aij]\d/.test(x)).sort();
  await expect.poll(async () => mine((await (await request.get("/e2e/tools")).json()).trashed)).toEqual(["perso:j1", "perso:j3"]);
  // Decided, the batch asks nothing more: the mail kept stays, no Oui.
  await expect(batch.locator(".a2-fold-label")).toHaveText("1 mails inutiles à jeter");
  await expect(batch.locator(".a2-inline-yes")).toHaveCount(0);
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
  const text = await pg.locator(".pg-doc > .md-p").first().boundingBox();
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
