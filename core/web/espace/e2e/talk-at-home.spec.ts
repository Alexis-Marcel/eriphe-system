import { expect, test, type Page } from "@playwright/test";

// An answer given at the home, no subject opened, more than a line: the
// bar holds it whole, under the question, and it does not go; a line an
// event made Eriphe say later shows under it and goes, without taking its
// place; a reload shows it again; a new message, or its ✕, moves on.
// Played on the scenario's test core (TestEspaceScenario), after the
// other files (its name sorts last): the scripted brain answers « mes
// films » in three sentences, and /e2e/catalog brings an event of the
// home's.

const password = "correct horse battery"; // testPassword, core/web_test.go

// atHome opens the workspace with no page open, the bar talking to the
// home: the view as the scenario left it, its pages closed.
async function atHome(page: Page) {
  await page.goto("/espace");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe");
}

test("an answer given at home stays readable until you move on", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.route("**/espace/boot", async (route) => {
    const r = await route.fetch();
    const b = await r.json();
    if (b.view) b.view.open = [];
    delete b.view?.talk;
    await route.fulfill({ response: r, json: b });
  });

  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  await atHome(page);

  const say = async (text: string) => {
    const input = page.locator(".cs-bar input");
    await input.fill(text);
    await input.press("Enter");
  };
  const answer = page.locator(".cs-answer");
  const said = page.locator(".cs-said");
  const asked = page.locator(".cs-asked");

  // Three sentences at the home: held whole, its Markdown, under the question.
  await say("Tu as accès à mes films normalement ?");
  await expect(answer).toContainText("Tu peux mettre la recherche et la consultation en accès libre dans Réglages → Outils → films.");
  await expect(answer.locator("strong")).toHaveText("Seerr");
  await expect(asked).toContainText("Tu as accès à mes films normalement ?");
  expect((await asked.boundingBox())!.y).toBeLessThan((await answer.boundingBox())!.y);
  await expect(page.locator("article.pg")).toHaveCount(0);
  // Long after a line would have gone, it is still there.
  await page.waitForTimeout(10_000);
  await expect(answer).toBeVisible();
  await expect(answer).toContainText("chaque recherche te demande ton accord");

  // An event alone (the catalog changed): its line shows and goes, the answer stays.
  expect((await request.post("/e2e/catalog")).ok()).toBe(true);
  await expect(said).toContainText("Un outil de plus : films.");
  await expect(answer).toContainText("Réglages → Outils → films.");
  await expect(said).toHaveCount(0, { timeout: 10_000 });
  await expect(answer).toContainText("Réglages → Outils → films.");

  // A reload shows it again.
  await atHome(page);
  await expect(answer).toContainText("Réglages → Outils → films.");
  await expect(asked).toContainText("Tu as accès à mes films normalement ?");

  // A new message moves on from it.
  await say("Merci.");
  await expect(answer).toHaveCount(0);
  await expect(asked).toContainText("Merci.");
  await expect(said).toContainText("D'accord.");
  await expect(answer).toHaveCount(0);

  // Asked again, then closed: gone, and a reload does not bring it back.
  await say("Et mes films, tu y as accès ?");
  await expect(answer).toContainText("Réglages → Outils → films.");
  await answer.getByRole("button", { name: "Fermer cette réponse" }).click();
  await expect(answer).toHaveCount(0);
  await expect(asked).toContainText("Et mes films, tu y as accès ?");
  await atHome(page);
  await expect(asked).toContainText("Et mes films, tu y as accès ?");
  await expect(answer).toHaveCount(0);
  expect(problems).toEqual([]);
});
