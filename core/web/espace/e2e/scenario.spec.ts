import { expect, test, type Locator, type Page } from "@playwright/test";

// The epic's acceptance scenario (#625), its 13 steps played in the
// workspace against the test core of TestEspaceScenario: faked tools, a
// scripted brain, the real engine, cards, filing and stream. What the world
// outside the user does (a monitor's mail, a task that finishes) is asked
// of the test core at /e2e/. The page must say nothing to the console, and
// break no rule of its Content-Security-Policy.

const password = "correct horse battery"; // testPassword, core/web_test.go
const external = "Contenu extérieur"; // what the page names a node someone else's words wrote

test.describe.configure({ mode: "serial" });

test("the 13 steps of the scenario", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => console.error(`CSP: ${e.violatedDirective} ${e.blockedURI}`));
  });

  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  await page.goto("/espace");

  const said = page.locator(".cs-said");
  const say = async (text: string) => {
    const input = page.locator(".cs-bar input");
    await input.fill(text);
    await input.press("Enter");
  };
  const bubble = (title: string) => page.locator(`.gr-node:not(.external) .gr-core[aria-label^="${title}"]`);
  const pageOf = (title: string) => page.locator(`article.pg[aria-label="${title}"]`);

  // 1. Home.
  await expect(said).toContainText("Bonjour. Rien ne t'attend ce matin.");
  await expect(page.locator("article.pg")).toHaveCount(0);

  // 2. One call, no node opened; it is kept in « Ma journée ».
  await say("Allume la lumière du bureau.");
  await expect(said).toContainText("C'est allumé.");
  await expect(page.locator("article.pg")).toHaveCount(0);
  expect((await (await request.get("/e2e/tools")).json()).lit).toEqual(["bureau"]);
  await bubble("Ma journée").click();
  await expect(pageOf("Ma journée")).toContainText("Lumière du bureau allumée.");
  await pageOf("Ma journée").getByRole("button", { name: "Fermer cette page" }).click();

  // 3. The user opens the standing subject « Substack ».
  await bubble("Substack").click();
  await expect(pageOf("Substack")).toBeVisible();

  // 4. Two steps are born: Style runs, Plan comes after it.
  await say("Analyse le style des articles qui marchent, puis propose-moi un plan du premier article.");
  await expect(said).toContainText("Je lance l'analyse du style");
  await expect(bubble("Style des articles")).toBeVisible();
  await expect(bubble("Plan du premier article")).toBeVisible();
  await expect(page.locator(".gr-node.running .gr-core[aria-label^='Style des articles']")).toBeVisible();
  await expect(page.locator(".gr-node.running .gr-core[aria-label^='Style des articles']")).toHaveAttribute("aria-label", /3 articles lus/);

  // 5. The answer goes into « Ma journée », not into a new node.
  const before = await page.locator(".gr-node").count();
  // Eriphe shows the page: the console then talks to it.
  await say("Qu'est-ce que j'ai de prévu aujourd'hui ?");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Ma journée");
  await expect(pageOf("Ma journée")).toContainText("Point produit");
  await expect(pageOf("Ma journée")).toContainText("Dentiste");
  await expect(page.locator(".gr-node")).toHaveCount(before);

  // 6. A monitor opens a node: an urgent mail. It waits for the user, and
  // its title, someone else's words, never shows.
  const mail = (await (await request.post("/e2e/mail")).json()).id as string;
  const tick = page.locator(".tick.yours", { hasText: external });
  await expect(tick).toBeVisible();
  await expect(page.getByText("Urgent : la prod renvoie des 500")).toHaveCount(0);

  // 7. The mail node opens with its triage and its draft.
  await tick.click();
  const mailPage = page.locator(`article.pg[aria-label="${external}"]`);
  await expect(mailPage).toContainText("Tri");
  await expect(mailPage).toContainText("À écrire.");

  // 8. A draft with its « Envoyer ? » card, the production view beside it,
  // a research child running.
  await say("Réponds que je regarde dans l'heure, ouvre-moi la prod, et lance en arrière-plan une recherche sur l'erreur.");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Production");
  await expect(mailPage).toContainText("Je regarde dans l'heure.");
  await expect(pageOf("Production")).toBeVisible();
  await expect(pageOf("Production")).toContainText("42");
  await expect(page.locator(".gr-node.running .gr-core[aria-label^=\"Recherche sur l'erreur\"]")).toBeVisible();
  const card = mailPage.locator(".pg-foot");
  await expect(card).toBeVisible();

  // 9. The user approves the send; Style finishes, and code starts Plan
  // with no turn of the brain.
  await card.getByRole("button", { name: "Oui" }).click();
  await expect.poll(async () => (await (await request.get("/e2e/tools")).json()).sent).toEqual(["sam@example.org: Je regarde dans l'heure."]);
  await request.post("/e2e/finish/style");
  await expect(page.locator(".gr-node.done .gr-core[aria-label^='Style des articles']")).toBeVisible();
  await expect(page.locator(".gr-node.done .gr-core[aria-label^='Plan du premier article']")).toBeVisible({ timeout: 30_000 });

  // 10. Back on Substack: what happened while away.
  await page.locator(".hud-brand").click();
  await bubble("Substack").click();
  await expect(pageOf("Substack")).toBeVisible();
  await expect(bubble("Premier article")).toBeVisible();

  // 11. The plan waits for the user.
  await bubble("Plan du premier article").click({ modifiers: ["Meta"] });
  const plan = pageOf("Plan du premier article");
  await expect(plan).toContainText("Pourquoi ce métier");
  await expect(plan).toContainText("Ce qui a changé en dix ans");
  const v = plan.locator(".pg-versions button");
  const planVersions = await v.count();

  // 12. Said to the open plan page: a new version, the older one still
  // readable.
  await plan.locator(".pg-head").click({ position: { x: 10, y: 10 } });
  await say("Plus court, et garde le titre 2.");
  await expect(said).toContainText("Plus court, le titre 2 gardé.");
  await expect(plan).not.toContainText("Pourquoi ce métier");
  await expect(plan).toContainText("Ce qui a changé en dix ans");
  await expect(v).toHaveCount(planVersions + 1);
  await v.nth(1).click(); // the one before the last
  await expect(plan).toContainText("Pourquoi ce métier");
  await expect(plan.locator(".pg-old")).toBeVisible();
  await plan.locator(".pg-old button").click();
  await expect(plan).not.toContainText("Pourquoi ce métier");

  // The user edits the plan in its .md tab: saved on the version read.
  await plan.getByRole("tab", { name: ".md" }).click();
  const editor = plan.locator(".cm-content");
  await expect(editor).toContainText("Ce qui vient");
  await editor.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("4. Et après\n");
  await plan.getByRole("button", { name: "Enregistrer" }).click();
  await expect(plan.locator(".pg-md-state")).toContainText("Enregistré");
  await plan.getByRole("tab", { name: "Contenu" }).click();
  await expect(plan).toContainText("Et après");

  // 13. Eriphe leaves the archive to the user, who does it: the mail and
  // its child leave the graph, and stay in the archives.
  await openMail(page, mail);
  await say("C'est réglé avec Sam, on peut archiver.");
  await expect(said).toContainText("C'est toi qui archives");
  await mailPage.locator(".pg-archive").click();
  const ask = mailPage.locator(".pg-ask");
  await expect(ask).toContainText("Une bulle part avec.");
  await ask.getByRole("button", { name: "Archiver" }).click();
  await expect(mailPage).toHaveCount(0);
  await expect(page.locator(`[data-node="${mail}"]`)).toHaveCount(0);
  await expect(page.locator(".gr-core[aria-label^=\"Recherche sur l'erreur\"]")).toHaveCount(0);
  await page.locator(".hud-archives .hud-btn").click();
  const archived = page.locator(".hud-arch li");
  await expect(archived).toHaveCount(1);
  await expect(archived).toContainText(external);
  await expect(archived).toContainText("avec une bulle");
  await page.locator(".hud-archives .hud-btn").click();

  // End: home again, nothing waiting.
  await page.locator(".hud-brand").click();
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(page.locator(".tick.yours")).toHaveCount(0);

  expect(problems).toEqual([]);
});

// What the workspace keeps for the user: a bubble where it was dropped, the
// pages open, the theme, on the next visit.
test("the workspace as the user left it", async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  await page.goto("/espace");

  const node = page.locator(".gr-node", { has: page.locator('.gr-core[aria-label="Mails"]') });
  const core = node.locator(".gr-core");
  await expect(core).toBeVisible();
  await page.waitForTimeout(1500); // the graph settles
  const from = await at(node);
  const box = (await core.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  for (let i = 1; i <= 10; i++) await page.mouse.move(box.x + box.width / 2 + i * 12, box.y + box.height / 2 + i * 6);
  await page.mouse.up();
  await expect.poll(async () => (await at(node))[0]).not.toBeCloseTo(from[0], 0);

  await page.locator('.gr-core[aria-label="Substack"]').click();
  await expect(page.locator('article.pg[aria-label="Substack"]')).toBeVisible();
  await page.getByRole("button", { name: "Thème clair ou sombre" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.waitForTimeout(1500); // what is open is kept a moment after it changes; the graph settles
  const kept = await at(node);

  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator('article.pg[aria-label="Substack"]')).toBeVisible();
  // Where it was dropped, the page open making room around it as before.
  await expect.poll(async () => Math.abs((await at(node))[0] - kept[0]) + Math.abs((await at(node))[1] - kept[1])).toBeLessThan(4);

  await page.getByRole("button", { name: "Thème clair ou sombre" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  expect(problems).toEqual([]);
});

// at is where a bubble is in the graph's own space.
async function at(node: Locator): Promise<[number, number]> {
  return node.evaluate((el) => [parseFloat((el as HTMLElement).style.left), parseFloat((el as HTMLElement).style.top)]);
}

// openMail opens the mail's page from its bubble, the console talking to it.
async function openMail(page: Page, id: string) {
  const mailPage = page.locator(`article.pg[aria-label="${external}"]`);
  if (!(await mailPage.count())) await page.locator(`[data-node="${id}"] .gr-core`).click();
  await mailPage.locator(".pg-head").click({ position: { x: 10, y: 10 } });
}
