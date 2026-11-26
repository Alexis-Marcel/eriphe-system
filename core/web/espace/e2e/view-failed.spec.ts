import { expect, test } from "./fixtures";

// A yes whose call fails, against the test core of TestEspaceScenario: a
// page of the mailbox (/e2e/courrier) is open; the user says « supprime
// ces 2 »; the scripted brain calls mail/trash on two rows, whose trash
// now answers with an error (/e2e/trash-fails). Oui: nothing is trashed;
// the card is decided, so it leaves its rows and the page's foot; the
// page's ⋯ menu lists it under « Déjà décidé », failed, with the tool's
// own words; the orb says the failure in plain words. No Retry button.

const refusal = "perso: the mail server refused it (403 Forbidden)";

test("a failed yes leaves the page; the ⋯ menu keeps why, the orb says it", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));

  await page.goto("/espace");
  const input = page.locator(".cs-bar input");
  await expect(input).toBeVisible();
  expect((await request.post("/e2e/trash-fails", { data: refusal })).ok()).toBe(true);
  try {
    const seeded = await request.post("/e2e/courrier");
    expect(seeded.ok(), await seeded.text()).toBe(true);
    const pg = page.locator('article.pg[aria-label="Courrier"]');
    await expect(pg).toBeVisible();
    const rows = pg.locator(".md-item");
    await expect(rows).toHaveCount(4);

    await pg.locator(".md-p").first().click(); // the bar talks to it
    await input.fill("supprime ces 2");
    await input.press("Enter");
    const colis = rows.filter({ hasText: "Votre colis est arrivé" });
    await expect(colis.locator(".a2-inline-yes")).toBeVisible();
    await colis.locator(".a2-inline-yes").click();

    // The orb says what came of it, in plain words.
    await expect(page.locator(".orb-say.on, .orb-line")).toContainText("Échec : le serveur du courrier a refusé");
    // Nothing went; the card left the rows, and is at no foot.
    expect((await (await request.get("/e2e/tools")).json()).trashed).not.toContain("perso:c2,c4");
    await expect(rows).toHaveCount(4);
    await expect(pg.locator(".a2-inline-yes")).toHaveCount(0);
    await expect(pg.locator(".pg-foot")).toHaveCount(0);
    await expect(pg.locator(".a2-proposal")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /réessayer|retry/i })).toHaveCount(0);

    // The ⋯ menu: decided, failed, and why in the tool's own words.
    await pg.getByRole("button", { name: "Plus" }).click();
    const line = page.locator(".pg-menu .pg-settled li.failed");
    await expect(line).toHaveCount(1);
    await expect(line.locator(".pg-settled-word")).toHaveText("Échec");
    await expect(line.locator(".pg-settled-text")).toHaveText("Mettre 2 mails à la corbeille");
    await expect(line.locator(".pg-settled-why")).toContainText("403 Forbidden");
    await page.keyboard.press("Escape");
  } finally {
    await request.post("/e2e/trash-fails", { data: "" });
  }
  expect(problems).toEqual([]);
});
