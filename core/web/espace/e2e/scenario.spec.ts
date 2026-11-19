import { expect, test, type Locator, type Page } from "@playwright/test";

// The epic's acceptance scenario (#625), its steps played in the
// workspace against the test core of TestEspaceScenario: faked tools, a
// scripted brain, the real engine, cards, filing and stream. What the world
// outside the user does (a monitor's mail, a task that finishes) is asked
// of the test core at /e2e/. The page must say nothing to the console, and
// break no rule of its Content-Security-Policy.

const password = "correct horse battery"; // testPassword, core/web_test.go
const external = "Contenu extérieur"; // what the page names a node someone else's words wrote

test.describe.configure({ mode: "serial" });

test("the steps of the scenario", async ({ page, request }) => {
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

  // 2. One call, no node opened; it is logged in « Ma journée »'s trace.
  await say("Allume la lumière du bureau.");
  await expect(said).toContainText("C'est allumé.");
  await expect(page.locator("article.pg")).toHaveCount(0);
  expect((await (await request.get("/e2e/tools")).json()).lit).toEqual(["bureau"]);
  await bubble("Ma journée").click();
  await pageOf("Ma journée").getByRole("tab", { name: "Trace" }).click();
  await expect(pageOf("Ma journée").locator(".pg-trace")).toContainText("Appel : maison/allumer.");
  await pageOf("Ma journée").getByRole("tab", { name: "Contenu" }).click();
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
  // Eriphe shows the page beside; the console still talks to Substack, its
  // short answer under the question.
  await say("Qu'est-ce que j'ai de prévu aujourd'hui ?");
  await expect(said).toContainText("Deux rendez-vous");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Substack");
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
  await expect(said).toContainText("Le brouillon est prêt");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", `Écris à Eriphe, sur ${external}`);
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
  // Plan's result is the user's: it waits for them, the work around it done.
  await expect(page.locator(".gr-node.waiting .gr-core[aria-label^='Plan du premier article']")).toBeVisible({ timeout: 30_000 });

  // 10. Back on Substack: what happened while away.
  await home(page);
  await bubble("Substack").click();
  await expect(pageOf("Substack")).toBeVisible();
  await expect(page.locator(".gr-node.done .gr-core[aria-label^='Premier article']")).toBeVisible();

  // 11. The plan waits for the user.
  await bubble("Plan du premier article").click({ modifiers: ["Meta"] });
  const plan = pageOf("Plan du premier article");
  await expect(plan).toContainText("Pourquoi ce métier");
  await expect(plan).toContainText("Ce qui a changé en dix ans");
  // One version as the user counts them (no list of versions yet): the
  // step's empty creation is not one.
  const v = plan.locator(".pg-versions button");
  await expect(v).toHaveCount(0);
  await expect(page.locator(".tick.yours", { hasText: "Plan du premier" })).toBeVisible();

  // 12. Said to the open plan page: a new version, the older one still
  // readable.
  await plan.locator(".pg-head").click({ position: { x: 10, y: 10 } });
  await say("Plus court, et garde le titre 2.");
  await expect(said).toContainText("Plus court, le titre 2 gardé.");
  await expect(plan).not.toContainText("Pourquoi ce métier");
  await expect(plan).toContainText("Ce qui a changé en dix ans");
  await expect(v).toHaveText(["v2", "v1"]);
  await v.nth(1).click();
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
  // The user says the plan suits them: Eriphe, on their word, marks it done.
  await plan.locator(".pg-head").click({ position: { x: 10, y: 10 } });
  await say("Ce plan me va.");
  await expect(said).toContainText("C'est noté, le plan est fait.");
  await expect(page.locator(".gr-node.done .gr-core[aria-label^='Plan du premier article']")).toBeVisible();

  // 13. Eriphe leaves the archive to the user, who does it: the mail and
  // its child leave the graph, and stay in the archives.
  await openMail(page, mail);
  await say("C'est réglé avec Sam, on peut archiver.");
  await expect(said).toContainText("C'est toi qui archives");
  await mailPage.locator(".pg-archive").click();
  const ask = mailPage.locator(".pg-ask");
  await expect(ask).toContainText("2 bulles partent avec.");
  await ask.getByRole("button", { name: "Archiver" }).click();
  await expect(mailPage).toHaveCount(0);
  await expect(page.locator(`[data-node="${mail}"]`)).toHaveCount(0);
  await expect(page.locator(".gr-core[aria-label^=\"Recherche sur l'erreur\"]")).toHaveCount(0);
  await expect(page.locator('.gr-core[aria-label="Production"]')).toHaveCount(0);
  await expect(page.locator(".hud-archives .hud-count")).toHaveText("1");
  await page.locator(".hud-archives .hud-icon").click();
  const archived = page.locator(".hud-arch li");
  await expect(archived).toHaveCount(1);
  await expect(archived).toContainText(external);
  await expect(archived).toContainText("avec 2 bulles");
  await page.locator(".hud-archives .hud-icon").click();

  // 14. Asked at the home, a subject opens its page, the console in it;
  // Eriphe asks there, and the view it proposes waits at the subject's
  // foot. Its yes makes the view, which opens.
  await home(page);
  await say("Garde un œil sur mes mails.");
  const watch = pageOf("Garder un œil sur mes mails");
  await expect(watch).toBeVisible();
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Garder un œil sur mes mails");
  await expect(said).toContainText("Lesquels veux-tu suivre");
  await say("Ceux qui demandent une réponse.");
  await expect(said).toContainText("Voici la vue");
  const viewCard = watch.locator(".pg-foot");
  await expect(viewCard).toContainText("Mes mails");
  await viewCard.getByRole("button", { name: "Oui" }).click();
  await expect(bubble("Mes mails")).toBeVisible();
  await expect(pageOf("Mes mails")).toBeVisible();

  // 15. A card no page holds (proposed at the home, no subject opened)
  // waits in the console, with its Oui; answered, it leaves it.
  await home(page);
  await say("Propose-moi une vue de l'agenda.");
  await expect(said).toContainText("Voici la vue de ton agenda");
  const loose = page.locator(".cs-aside .cs-cards");
  await expect(loose).toContainText("Mon agenda");
  await loose.getByRole("button", { name: "Oui" }).click();
  await expect(page.locator(".cs-cards")).toHaveCount(0);
  await expect(bubble("Mon agenda")).toBeVisible();

  // 16. Asked on the side: its node is born on the graph, not opened, and
  // the console stays at the home.
  await home(page);
  await say("Lance une recherche à part sur les newsletters.");
  await expect(said).toContainText("Je lance la recherche à part.");
  await expect(bubble("Recherche sur les newsletters")).toBeVisible();
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe");

  // End: home again, nothing waiting.
  await home(page);
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

  // Home: no page open, the whole graph in view.
  await home(page);
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect.poll(async () => (await (await page.request.get("/espace/boot")).json()).view.open).toEqual([]);
  const node = page.locator(".gr-node", { has: page.locator('.gr-core[aria-label="Mails"]') });
  const core = node.locator(".gr-core");
  await expect(core).toBeVisible();
  await settled(node);
  const from = await at(node);
  const box = (await core.boundingBox())!;
  const placed = page.waitForResponse((r) => r.url().endsWith("/place") && r.request().method() === "PUT");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  for (let i = 1; i <= 10; i++) await page.mouse.move(box.x + box.width / 2 + i * 12, box.y + box.height / 2 + i * 6);
  await page.mouse.up();
  expect((await placed).status()).toBe(204);
  await expect.poll(async () => (await at(node))[0]).not.toBeCloseTo(from[0], 0);
  const dropped = await settled(node);

  // eriphe-core keeps the place where it was dropped; with no page open,
  // the subject is exactly there on the next visit.
  const id = (await node.getAttribute("data-node"))!;
  const kept = (await (await page.request.get("/espace/boot")).json()).places[id] as { x: number; y: number };
  expect(Math.hypot(kept.x - dropped[0], kept.y - dropped[1])).toBeLessThan(2);
  await page.reload();
  await expect(core).toBeVisible();
  await expect.poll(async () => Math.hypot((await at(node))[0] - kept.x, (await at(node))[1] - kept.y)).toBeLessThan(0.5);

  // The page open and the theme come back too.
  await page.locator('.gr-core[aria-label="Substack"]').click();
  await expect(page.locator('article.pg[aria-label="Substack"]')).toBeVisible();
  const view = page.waitForResponse((r) => r.url().endsWith("/api/espace/view") && r.request().method() === "PUT");
  await page.getByRole("button", { name: "Thème clair ou sombre" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect((await view).status()).toBe(204);
  await expect.poll(async () => (await (await page.request.get("/espace/boot")).json()).view).toMatchObject({ theme: "dark", open: [expect.any(String)] });
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator('article.pg[aria-label="Substack"]')).toBeVisible();

  await page.getByRole("button", { name: "Thème clair ou sombre" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  expect(problems).toEqual([]);
});

// home closes every page open, one by one, the one the camera frames (the
// console talks to it) first: the console then talks to the home.
async function home(page: Page) {
  const pages = page.locator("article.pg");
  for (let n = await pages.count(); n > 0; n = await pages.count()) {
    const framed = page.locator("article.pg.talking");
    await ((await framed.count()) ? framed : pages).first().locator(".pg-x").click();
    await expect(pages).toHaveCount(n - 1);
  }
}

// at is where a bubble is in the graph's own space.
async function at(node: Locator): Promise<[number, number]> {
  return node.evaluate((el) => [parseFloat((el as HTMLElement).style.left), parseFloat((el as HTMLElement).style.top)]);
}

// openMail opens the mail's page alone, from its bubble with the whole graph
// in view: the console talks to it.
async function openMail(page: Page, id: string) {
  await home(page);
  await expect(page.locator("article.pg")).toHaveCount(0);
  await page.locator(`[data-node="${id}"] .gr-core`).click();
  await expect(page.locator(`article.pg[aria-label="${external}"]`)).toBeVisible();
}

// settled is where a bubble is once it stopped moving.
async function settled(node: Locator): Promise<[number, number]> {
  let last = await at(node);
  for (let i = 0; i < 40; i++) {
    await node.page().waitForTimeout(150);
    const now = await at(node);
    if (Math.hypot(now[0] - last[0], now[1] - last[1]) < 0.01) return now;
    last = now;
  }
  return last;
}

// On a phone: the graph as a list, a tap opens one page over it.
test("the workspace on a small screen", async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/login");
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await page.waitForURL((u) => u.pathname !== "/login");
  await page.goto("/espace");

  await home(page);
  await expect(page.locator(".gr-node")).toHaveCount(0);
  const rows = page.locator(".ls-row");
  await expect(rows.filter({ hasText: "Substack" })).toBeVisible();
  // a subject, then its bubbles under it
  const titles = await rows.locator("b").allTextContents();
  expect(titles.indexOf("Premier article")).toBeGreaterThan(titles.indexOf("Substack"));
  expect(titles.indexOf("Plan du premier article")).toBeGreaterThan(titles.indexOf("Premier article"));
  // the archived mail is not there; its title never was
  await expect(page.getByText("Urgent : la prod renvoie des 500")).toHaveCount(0);

  // nor the prod's page, its child
  await expect(rows.filter({ hasText: "Production" })).toHaveCount(0);

  await rows.filter({ hasText: "Ma journée" }).locator("button").click();
  const day = page.locator('article.pg[aria-label="Ma journée"]');
  await expect(day).toBeVisible();
  await expect(day).toContainText("Point produit");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Ma journée");
  await day.getByRole("button", { name: "Fermer cette page" }).click();
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(rows.first()).toBeVisible();
  expect(problems).toEqual([]);
});
