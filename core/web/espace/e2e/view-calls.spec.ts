import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { rest } from "./rest";

// A call to approve, asked in conversation, against the test core of
// TestEspaceScenario: a view of the mailbox (a plain List, /e2e/courrier)
// is open, alone; the user says « supprime ces 2 » in the bar; the
// scripted brain calls mail/trash on two of its rows. The card says what it does to
// which items in plain words, on those two rows (one card: a Oui on
// either answers it), not at the page's foot; its exact call is behind
// one « détails ». Oui: the mails leave, Eriphe says it is done.


test("a call to approve says what it does to which items, on their rows", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  const input = page.locator(".cs-bar input");
  await expect(input).toBeVisible();

  const seeded = await request.post("/e2e/courrier");
  expect(seeded.ok(), await seeded.text()).toBe(true);
  const pg = page.locator('article.pg[aria-label="Courrier"]');
  await expect(pg).toBeVisible();
  const rows = pg.locator(".a2-list > li");
  await expect(rows).toHaveCount(4);

  // Said in the bar: the brain calls trash on two rows of the view.
  await input.fill("supprime ces 2");
  await input.press("Enter");
  const colis = rows.filter({ hasText: "Votre colis est arrivé" });
  const relais = rows.filter({ hasText: "Colis en point relais" });
  await expect(colis.locator(".a2-inline-yes")).toBeVisible();
  await shots(page, "1-card-on-rows");

  // One card on both rows, in plain words; none at the foot.
  await expect(relais.locator(".a2-inline-yes")).toBeVisible();
  await expect(colis.locator(".a2-inline-yes")).toHaveAttribute("title", "Mettre 2 mails à la corbeille · compte perso");
  await expect(colis.locator(".a2-inline-what")).toHaveText("Mettre 2 mails à la corbeille");
  await expect(colis.locator(".a2-inline-shared")).toHaveCount(1);
  await expect(rows.filter({ hasText: "Billets pour Disney" }).locator(".a2-inline-yes")).toHaveCount(0);
  await expect(pg.locator(".pg-foot")).toHaveCount(0);
  // On a narrow screen, the orb's words stay whole on it: beside the page
  // where there is room for them, else as the bar's line (no free spot
  // left by where the graph put the page).
  const wide = page.viewportSize()!;
  await page.setViewportSize({ width: 1000, height: wide.height });
  await rest(page);
  const words = page.locator(".orb-say.on, .orb-line");
  await expect(words).toContainText("Je les mets à la corbeille dès que tu dis oui.");
  await expect
    .poll(async () => {
      const b = await words.boundingBox();
      return !!b && b.x >= 0 && b.x + b.width <= 1000 && b.y >= 0 && b.y + b.height <= wide.height;
    })
    .toBe(true);
  await shots(page, "4-narrow");
  await page.setViewportSize(wide);
  // Hovered, its sibling row is shown as the same card.
  await colis.locator(".a2-inline-yes").hover();
  await expect(relais).toHaveClass(/a2-card-sibling/);
  await shots(page, "2-hover-siblings");

  // The card as eriphe-core reads it: what it does, to which mails by
  // their titles, the account apart; its exact call under its one
  // « détails »; no line of what Oui does.
  const ref = await colis.locator(".a2-inline").getAttribute("data-row-card");
  expect(ref).toMatch(/^calls:\d+$/);
  const card = await (await page.request.get(`/api/proposals/${ref}`)).json();
  expect(card.text).toBe("Mettre 2 mails à la corbeille");
  expect(card.context).toBe("compte perso");
  expect(card.summary).toEqual(["« Votre colis est arrivé » · Relais ⚠", "« Colis en point relais » · Relais ⚠"]);
  // Its Détails say no id nor account (the rows and the quiet account say
  // them): only its exact call, one click further.
  expect(card.fields).toEqual([{ name: "mail/trash", value: 'account : perso\nids : ["c2","c4"]', detail: true, raw: true }]);
  expect(card.yes ?? "").toBe("");

  // Oui on the second row answers the one card: the call runs once.
  await relais.locator(".a2-inline-yes").click();
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).trashed).toContain("perso:c2,c4");
  await expect(rows).toHaveCount(2);
  // The yes is the user talking to Eriphe: what Eriphe says of it then
  // answers them, and the orb says it, beside the page.
  await expect(page.locator(".orb-say.on, .orb-line")).toContainText("C'est fait : les 2 mails sont à la corbeille.");
  await shots(page, "3-done");
  expect(problems).toEqual([]);
});

// shots saves the screen in both themes when SHOTS names a folder.
async function shots(page: Page, name: string) {
  const dir = process.env.SHOTS;
  if (!dir) return;
  await rest(page);
  for (const theme of ["light", "dark"]) {
    await page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
    await page.waitForTimeout(400); // its colours settled
    await page.screenshot({ path: `${dir}/${name}-${theme}.png` });
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}
