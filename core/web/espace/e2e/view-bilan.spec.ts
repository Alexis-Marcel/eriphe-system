import { expect, test, type Page } from "@playwright/test";

// A view as a bilan (docs/node-format.md), against the test core of
// TestEspaceScenario: the bilan on top, drawn larger; one block that reads
// the mails of two accounts (mail/bilan) once, written after an external
// read (one sign in the page's head, a quiet edge on the block); « À
// traiter » whose rows say what Eriphe proposes, Sam's with its one Oui
// (a reply its skill drafted, a card on the mail); « À jeter » as a batch
// whose trash takes each mail's account; « Pour info » folded. The user
// reads Sam's mail as a letter with what Oui sends, says yes on the row,
// unticks one mail of the batch and trashes the others, then enlarges and
// resizes the page. Seeded at /e2e/bilan, on its own node.

const password = "correct horse battery"; // testPassword, core/web_test.go

test("a view as a bilan: a proposal's Oui, a batch, an item read as a letter, a page as a window", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toBeVisible();

  const seeded = await request.post("/e2e/bilan");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Mes mails du jour"]');
  await expect(pg).toBeVisible();

  // The bilan: its first paragraph larger than the « Pour info » after it.
  const lead = pg.locator(".pg-doc > .a2-markdown:first-child > p").first();
  await expect(lead).toHaveText("1 réponse attendue : Sam, avant vendredi. 1 hameçonnage dans le lot à jeter.");
  const size = (n: number) => pg.locator(".pg-doc > .a2-markdown:first-child > p").nth(n).evaluate((p) => parseFloat(getComputedStyle(p).fontSize));
  expect(await size(0)).toBeGreaterThan(await size(1));
  // Someone else's words: one sign in the head, the block's quiet edge.
  await expect(pg.locator(".pg-title .pg-badge.warn")).toHaveCount(1);
  await expect(pg.locator(".a2-block.ext .a2-block-mark")).toHaveCount(1);
  await expect(pg.getByText("extérieur", { exact: true })).toHaveCount(1);

  // « À traiter »: each row what Eriphe proposes; Sam's with its one Oui.
  const todo = pg.locator(".a2-openrow").filter({ hasText: "Répondre à Sam" });
  const lease = pg.locator(".a2-openrow").filter({ hasText: "À lire : l'avenant au bail" });
  await expect(todo).toHaveCount(1);
  await expect(todo.locator(".a2-inline-yes")).toHaveText("Oui");
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
  await expect(onYes.locator(".a2-onyes-kicker")).toHaveText("Sur Oui");
  await expect(onYes.locator("figcaption")).toHaveText("Texte");
  await expect(onYes.locator(".a2-onyes-text")).toContainText("tu peux lancer la commande");
  await expect(read.locator("summary")).toHaveText(["détails"]);
  await expect(read.locator(".a2-taint")).toHaveCount(0); // its block's edge says it
  await shots(page, "2-mail-opened");
  await todo.locator(".a2-openclose").click();

  // Its Oui on the row: the reply goes, the mail is settled and leaves.
  await todo.locator(".a2-inline-yes").click();
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).replied).toEqual(["a1"]);
  await expect(todo).toHaveCount(0);
  await expect(lease).toHaveCount(1);

  // « À jeter »: a batch line, all ticked; opened, one mail kept.
  const batch = pg.locator(".a2-batch").filter({ hasText: "à jeter" });
  // « Pour info »: a batch with no action, its line only.
  await expect(pg.locator(".a2-batch").filter({ hasText: "pour info" }).locator(".a2-fold-label")).toHaveText("Les 2 mails pour info");
  await expect(batch.locator(".a2-fold-label")).toHaveText("3 mails inutiles à jeter");
  const act = batch.locator(".a2-batch-act");
  await expect(act).toHaveText("Tout mettre à la corbeille");
  await batch.locator(".a2-fold-head").click();
  await batch.getByRole("checkbox", { name: "Décocher « Lettre du dimanche » : le garder" }).uncheck();
  await expect(batch.locator(".a2-batch-kept")).toHaveText("1 gardé(s)");
  await expect(act).toHaveText("Mettre les 2 à la corbeille");
  await shots(page, "3-batch-open");
  await act.click();
  // A trash asks once more, with its exact call: one call per account.
  const ask = pg.locator(".a2-pickask");
  await expect(ask.locator("dt")).toHaveText("mail/trash");
  await expect(ask.locator("dd")).toContainText(["account : perso, travail (un appel par account)"]);
  await expect(ask.locator("li")).toHaveText(["Soldes d'hiver · promo@example.org", "Votre colis est bloqué · colis@example.net"]);
  await shots(page, "4-batch-confirm");
  await ask.getByRole("button", { name: "Lancer" }).click();
  // Its own calls: the test core is shared with the other files (view-calls.spec.ts, run after, trashes too).
  const mine = (all: string[]) => all.filter((x) => /:[aij]\d/.test(x));
  await expect.poll(async () => mine((await (await request.get("/e2e/tools")).json()).trashed)).toEqual(["perso:j1,j3"]);
  await expect(pg.locator(".a2-batch-done")).toContainText("1 gardé(s)");
  await shots(page, "5-after-actions");

  // The ⋯ menu: what was decided, and what the view was asked for.
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
  const text = await pg.locator(".pg-doc > .a2-markdown").first().boundingBox();
  expect(text!.width).toBeLessThanOrEqual(761);
  await page.waitForTimeout(600); // the camera frames it
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
  const w1 = (await box.boundingBox())!.width;
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  // Two steps of 40 in the graph's units: wider on screen, whatever the camera's scale.
  await expect.poll(async () => (await box.boundingBox())!.width).toBeGreaterThan(w1 + 30);
  await page.waitForTimeout(600);
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
  let last = "";
  for (let i = 0; i < 60; i++) {
    const b = await page.locator(".gr-page").first().boundingBox();
    const now = JSON.stringify(b && [Math.round(b.x), Math.round(b.y), Math.round(b.width), Math.round(b.height)]);
    if (b && now === last) break;
    last = now;
    await page.waitForTimeout(200);
  }
  await page.waitForTimeout(300);
  for (const theme of ["light", "dark"]) {
    await page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
    await page.screenshot({ path: `${dir}/${name}-${theme}.png` });
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}
