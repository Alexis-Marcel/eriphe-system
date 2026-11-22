import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

// An answer given at the home, no subject opened, more than a line: the
// orb holds it whole, out in the space, and it does not go; words an event
// made Eriphe say later are never the orb's (it only answers); a reload
// shows it again; a new message, or its ✕, moves on.
// Played on the scenario's test core (TestEspaceScenario), after the
// other files (its name sorts last): the scripted brain answers « mes
// films » in three sentences, and /e2e/catalog brings an event of the
// home's. And a question said in a subject's bar about something else:
// the card it needs is in front at once.


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

  await atHome(page);

  const say = async (text: string) => {
    const input = page.locator(".cs-bar input");
    await input.fill(text);
    await input.press("Enter");
  };
  const words = page.locator(".orb-say.on, .orb-line");
  const answer = words.locator(".orb-answer");
  const said = words.locator(".orb-text");
  const orb = page.locator("button.orb");

  // Three sentences at the home: held whole by the orb, its Markdown, out
  // in the space; on a graph too full to hold it without covering a
  // bubble (the scenario's, all of them), in a line above the bar.
  await say("Tu as accès à mes films normalement ?");
  await expect(answer).toContainText("Tu peux mettre la recherche et la consultation en accès libre dans Réglages → Outils → films.");
  await expect(answer.locator("strong")).toHaveText("Seerr");
  await expect(page.locator("button.orb:not(.docked), .orb-line")).toHaveCount(1);
  await expect(page.locator("article.pg")).toHaveCount(0);
  // Long after a line would have gone, it is still there.
  await page.waitForTimeout(10_000);
  await expect(answer).toBeVisible();
  await expect(answer).toContainText("chaque recherche te demande ton accord");

  // An event alone (the catalog changed): its words are not the orb's,
  // the answer stays.
  expect((await request.post("/e2e/catalog")).ok()).toBe(true);
  await page.waitForTimeout(3000);
  await expect(answer).toContainText("Réglages → Outils → films.");
  await expect(said).toHaveCount(0);

  // A reload shows it again.
  await atHome(page);
  await expect(answer).toContainText("Réglages → Outils → films.");

  // Closed: the orb goes back to the bar, the event's words never come;
  // the answer does not come back.
  await words.getByRole("button", { name: "Fermer" }).click();
  await expect(answer).toHaveCount(0);
  await expect(orb).toHaveClass(/docked/);
  await page.waitForTimeout(1000);
  await expect(said).toHaveCount(0);
  await expect(orb).toHaveClass(/docked/);
  await atHome(page);
  await expect(answer).toHaveCount(0);

  // Asked again; a new message moves on from it.
  await say("Et mes films, tu y as accès ?");
  await expect(answer).toContainText("Réglages → Outils → films.");
  await say("Merci.");
  await expect(answer).toHaveCount(0);
  await expect(said).toContainText("D'accord.");
  expect(problems).toEqual([]);
});

// Said in a subject's bar, about something else, a question that needs a
// card: the brain answers as at the home (turn_home), and the card waits
// above the bar at once, in front of the user, not in that subject; the
// orb says why beside the subject it was asked in.
test("a card raised for a question about something else is in front at once", async ({ page, request }) => {
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
  await atHome(page);

  await page.locator('.gr-node:not(.external) .gr-core[aria-label^="Substack"]').click();
  const substack = page.locator('article.pg[aria-label="Substack"]');
  await expect(substack).toBeVisible();
  const input = page.locator(".cs-bar input");
  await expect(input).toHaveAttribute("aria-label", "Écris à Eriphe, sur Substack");
  await input.fill("Dis à Lou que je serai en retard.");
  await input.press("Enter");

  // No click: the card is above the bar, not at Substack's foot.
  const loose = page.locator(".cs-aside .cs-cards");
  await expect(loose.getByRole("button", { name: "Non" })).toBeVisible();
  await expect(substack.locator(".pg-foot")).toHaveCount(0);
  await loose.getByRole("button", { name: "Non" }).click();
  await expect(page.locator(".cs-cards")).toHaveCount(0);
  await expect(page.locator(".orb-say.on .orb-text, .orb-line .orb-text")).toContainText("Tu me donnes ton accord pour l'envoyer ?");
  expect((await (await request.get("/e2e/tools")).json()).sent ?? []).not.toContain("lou@example.org: Je serai en retard.");
  expect(problems).toEqual([]);
});
