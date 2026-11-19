import { expect, test, type Locator, type Page } from "@playwright/test";

// The epic's acceptance scenario (#625), its steps played in the
// workspace against the test core of TestEspaceScenario: faked tools, a
// scripted brain, the real engine, cards, filing and stream. What the world
// outside the user does (a monitor's mail, a task that finishes) is asked
// of the test core at /e2e/. The page must say nothing to the console, and
// break no rule of its Content-Security-Policy. The bar is only a bar: the
// user's last question and one line of Eriphe's, which goes; what Eriphe
// says at length, its questions and their buttons are in the subject's
// document.

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
  // The bar's buttons: none but the fold of the last question (the cards
  // no node holds aside).
  const barButtons = page.locator(".cs-panel button:not(.cs-asked), .cs-bar button");
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
  // The question stays, in grey, just above Eriphe's line, which goes
  // after a few seconds; the question stays. No button in the bar.
  await say("Allume la lumière du bureau.");
  await expect(said).toContainText("C'est allumé.");
  const asked = page.locator(".cs-asked");
  await expect(asked).toContainText("Allume la lumière du bureau.");
  expect((await asked.boundingBox())!.y).toBeLessThan((await said.boundingBox())!.y);
  await shot(page, "bar");
  await expect(barButtons).toHaveCount(0);
  await expect(said).toHaveCount(0, { timeout: 10_000 });
  await expect(asked).toContainText("Allume la lumière du bureau.");
  await expect(page.locator("article.pg")).toHaveCount(0);
  expect((await (await request.get("/e2e/tools")).json()).lit).toEqual(["bureau"]);
  await bubble("Ma journée").click();
  // The trace is behind the page's « ⋯ » menu: no tabs.
  await expect(pageOf("Ma journée").getByRole("tab")).toHaveCount(0);
  await pageOf("Ma journée").getByRole("button", { name: "Plus" }).click();
  await shot(page, "page-menu", false);
  await page.getByRole("menuitemcheckbox", { name: "Trace" }).click();
  await expect(pageOf("Ma journée").locator(".pg-trace")).toContainText("Appel : maison/allumer.");
  await pageOf("Ma journée").getByRole("button", { name: "← Contenu" }).click();
  await expect(pageOf("Ma journée").locator(".pg-trace")).toHaveCount(0);
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

  // 6. A monitor opens a node: an urgent mail. Its bubble says it waits
  // for the user, and its title, someone else's words, never shows.
  const mail = (await (await request.post("/e2e/mail")).json()).id as string;
  const waiting = page.locator(`.gr-node.yours[data-node="${mail}"]`);
  await expect(waiting).toHaveCount(1);
  await expect(waiting.locator(".gr-label em.yours")).toHaveText("t'attend");
  await shot(page, "waiting");
  await expect(page.getByText("Urgent : la prod renvoie des 500")).toHaveCount(0);

  // 7. The mail node opens with its triage and its draft.
  await openMail(page, mail);
  const mailPage = page.locator(`article.pg[aria-label="${external}"]`);
  await expect(mailPage).toContainText("Tri");
  await expect(mailPage).toContainText("À écrire.");

  // 8. A draft with its « Envoyer ? » card, the production view beside it,
  // a research child running.
  await say("Réponds que je regarde dans l'heure, ouvre-moi la prod, et lance en arrière-plan une recherche sur l'erreur.");
  await expect(said).toContainText("Le brouillon est prêt");
  await shot(page, "bar-over-page");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", `Écris à Eriphe, sur ${external}`);
  await expect(mailPage).toContainText("Je regarde dans l'heure.");
  await expect(pageOf("Production")).toBeVisible();
  await expect(pageOf("Production")).toContainText("42");
  // A tall page, a table in it, beside the mail's: neither on the other,
  // the new one whole on screen, none under the bar.
  await expect(pageOf("Production").locator("table")).toBeVisible();
  await expectApart(page, [mailPage, pageOf("Production")]);
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
  await expect(page.locator(".gr-node.yours .gr-core[aria-label^='Plan du premier article']")).toHaveCount(1);

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
  await menu(plan, "Fichier .md");
  const editor = plan.locator(".cm-content");
  await expect(editor).toContainText("Ce qui vient");
  await editor.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("4. Et après\n");
  await plan.getByRole("button", { name: "Enregistrer" }).click();
  await expect(plan.locator(".pg-md-state")).toContainText("Enregistré");
  await plan.getByRole("button", { name: "← Contenu" }).click();
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

  // 14. Asked at the home, a subject opens its page, the bar in it;
  // Eriphe asks there what matters first: its questions, each with its
  // default, are in the subject's document, with the buttons that answer
  // them; the bar has one line. The view it proposes then waits in the
  // document, where Eriphe placed it, recalling the choices. Its yes makes
  // the view, which opens.
  await home(page);
  await say("Garde un œil sur mes mails.");
  const watch = pageOf("Garder un œil sur mes mails");
  await expect(watch).toBeVisible();
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Garder un œil sur mes mails");
  await expect(said).toHaveText("J'ai écrit mes questions dans le sujet.");
  await expect(watch).toContainText("Par défaut au brief, jamais la nuit");
  await expect(barButtons).toHaveCount(0);
  await expect(watch.locator(".pg-foot")).toHaveCount(0);
  const defaults = watch.getByRole("button", { name: "Fais comme tu proposes" });
  await expect(watch.getByRole("button", { name: "Toutes, et réveille-moi la nuit pour la prod" })).toBeVisible();
  await defaults.click();
  await expect(said).toContainText("Voici la vue");
  await expect(page.locator(".cs-asked")).toContainText("Fais comme tu proposes");
  // The document says where things stand: the questions and their
  // buttons gone, the choices written, the card placed under them.
  await expect(defaults).toHaveCount(0);
  await expect(watch).not.toContainText("Par défaut au brief");
  await expect(watch).toContainText("Ce qu'on a décidé");
  // The view's card says plainly what its yes makes; its technical words,
  // the skill's instructions drawn as Markdown, are one click away.
  const viewCard = watch.locator(".pg-body .pg-card");
  await expect(viewCard).toContainText("Mes mails");
  await expect(viewCard).toContainText("prévenu au brief, jamais la nuit");
  await expect(viewCard.locator(".a2-proposal-summary")).toContainText("Une page « Mes mails »");
  await expect(viewCard.locator(".a2-proposal-summary")).toContainText("Regarde toutes les 5 min s'il y a du nouveau");
  await expect(watch.locator(".pg-foot")).toHaveCount(0);
  const details = viewCard.locator("details.a2-proposal-details");
  await expect(details.locator(".a2-proposal-md")).toBeHidden();
  await shot(page, "view-card");
  await details.locator("summary").click();
  await expect(details.locator(".a2-proposal-md h3")).toHaveText("Trier les mails");
  await expect(details).not.toContainText("**chaque**");
  await details.locator(".a2-proposal-md").scrollIntoViewIfNeeded();
  await shot(page, "view-card-details", true);
  // On a laptop's screen, where two pages side by side do not fit.
  await page.setViewportSize({ width: 1180, height: 720 });
  await viewCard.getByRole("button", { name: "Oui" }).click();
  await expect(bubble("Mes mails")).toBeVisible();
  await expect(pageOf("Mes mails")).toBeVisible();
  // Opened beside the subject, the view's page takes free room next to
  // it: the two never overlap, the new one is framed whole, its head on
  // screen, and no page goes under the bar.
  await settledBox(pageOf("Mes mails"));
  await shot(page, "view-beside", false);
  await expectApart(page, [watch, pageOf("Mes mails")]);
  await page.setViewportSize({ width: 1440, height: 900 });
  // Answered, the card stays in the document and says so.
  await expect(viewCard.locator(".a2-proposal-kicker")).toContainText("Validé");
  await expect(viewCard.getByRole("button", { name: "Oui" })).toHaveCount(0);
  // Its pages closed, the view's bubble, made on the subject, sits beside
  // it: no two bubbles on each other. Dropped right on the subject, it stays
  // there and the subject gives way.
  await home(page);
  await expectBubblesApart(page);
  const mine = page.locator(".gr-node:not(.external)", { has: page.locator('.gr-core[aria-label^="Mes mails"]') });
  await settled(mine);
  const src = (await bubble("Mes mails").boundingBox())!, dst = (await bubble("Garder un œil sur mes mails").boundingBox())!;
  const viewPlaced = page.waitForResponse((r) => r.url().endsWith("/place") && r.request().method() === "PUT");
  await page.mouse.move(src.x + src.width / 2, src.y + src.height / 2);
  await page.mouse.down();
  for (let i = 1; i <= 12; i++)
    await page.mouse.move(src.x + src.width / 2 + ((dst.x - src.x + (dst.width - src.width) / 2) * i) / 12, src.y + src.height / 2 + ((dst.y - src.y + (dst.height - src.height) / 2) * i) / 12);
  await page.mouse.up();
  expect((await viewPlaced).status()).toBe(204);
  await settled(mine);
  await expectBubblesApart(page);
  await shot(page, "graph-apart");

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
  // The subjects in a constellation around the centre, the whole graph in view.
  await settled(page.locator(".gr-node:not(.external)", { has: page.locator('.gr-core[aria-label^="Recherche sur les newsletters"]') }));
  await expectBubblesApart(page);
  if (process.env.SHOTS) await page.waitForTimeout(2000); // the camera at rest
  await shot(page, "graph-home");

  // 17. Writing. Asked at the home, the main subject opens with its plan
  // and its questions, their buttons in its document.
  await home(page);
  await say("Écris-moi un article sur le télétravail.");
  const write = pageOf("Écrire l'article");
  await expect(write).toBeVisible();
  await expect(said).toHaveText("J'ouvre le sujet de l'article.");
  await expect(write).toContainText("Ce qui reste à inventer");
  await expect(write).toContainText("Par défaut, des salariés.");
  await expect(barButtons).toHaveCount(0);
  const articleDefaults = write.getByRole("button", { name: "Fais comme tu proposes" });
  await articleDefaults.click();
  await expect(said).toContainText("C'est noté");
  await expect(articleDefaults).toHaveCount(0);
  await expect(write).toContainText("Ce qu'on a décidé");
  await expect(write).toContainText("Pour des salariés, 1 000 mots.");

  // 18. A research on the side: its node is born on the graph, not
  // opened, its task at work; the bar stays on the subject.
  await say("Lance une recherche sur le côté.");
  await expect(said).toHaveText("Je lance la recherche sur le côté.");
  await expect(bubble("Recherche sur le télétravail")).toBeVisible();
  await expect(pageOf("Recherche sur le télétravail")).toHaveCount(0);
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Écrire l'article");
  await expect(page.locator(".gr-node.running .gr-core[aria-label^='Documentation sur le télétravail']")).toBeVisible();

  // 19. The research done, Eriphe updates the main subject (its summary,
  // what comes next) and opens « Choisir l'angle » on the side.
  await request.post("/e2e/finish/docs");
  await expect(write).toContainText("Trois études récentes", { timeout: 15_000 });
  await expect(write).toContainText("Choisir l'angle.");
  await expect(bubble("Choisir l'angle")).toBeVisible();
  // Written before its node was open, the link to it links once it is.
  await expect(write.locator("button.a2-wiki", { hasText: "Choisir l'angle" }).first()).toBeVisible();

  // 20. The angle is chosen by a button of its document: the choice is
  // written there, the article gets its first version, the main subject
  // says where things stand.
  await home(page);
  await bubble("Choisir l'angle").click();
  const angle = pageOf("Choisir l'angle");
  await expect(angle.getByRole("button", { name: "L'angle des salariés" })).toBeVisible();
  await angle.getByRole("button", { name: "L'angle des managers" }).click();
  await expect(said).toHaveText("J'ai écrit une première version de l'article.");
  await expect(angle).toContainText("Ce qu'on a décidé");
  await expect(angle.getByRole("button", { name: "L'angle des managers" })).toHaveCount(0);
  await expect(bubble("L'article")).toBeVisible();

  // 21. Said to the article: a new version; the main subject follows.
  await home(page);
  await bubble("L'article").click();
  const article = pageOf("L'article");
  await expect(article).toContainText("ce qui change pour les managers");
  await say("Ajoute une conclusion.");
  await expect(said).toHaveText("Conclusion ajoutée.");
  await expect(article).toContainText("Le bureau devient un lieu de rencontre.");
  await expect(article.locator(".pg-versions button")).toHaveText(["v2", "v1"]);
  await home(page);
  await bubble("Écrire l'article").click();
  await expect(write).toContainText("L'angle des managers.");
  await expect(write).toContainText("version 2, avec sa conclusion");
  await expect(write).toContainText("Trois études récentes");

  // 22. The main subject links its subjects in its text ([[title]]): the
  // research, « Choisir l'angle », the article. A click opens one beside it.
  const related = write.locator(".a2-markdown li button.a2-wiki");
  await expect(related).toHaveText(["La recherche", "Choisir l'angle", "L'article"]);
  await expect(related.first()).toHaveAttribute("title", "Recherche sur le télétravail");
  await related.nth(1).click();
  await expect(pageOf("Choisir l'angle")).toBeVisible();
  await expect(write).toBeVisible();

  // End: home again, nothing waiting.
  await home(page);
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(page.locator(".gr-node.yours")).toHaveCount(0);

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

// shot saves a screenshot when SHOTS names a folder (a look by hand), in
// light and in dark.
async function shot(page: Page, name: string, both = true) {
  const dir = process.env.SHOTS;
  if (!dir) return;
  await page.screenshot({ path: `${dir}/${name}-light.png` });
  if (!both) return;
  // The theme set on the page alone: the user's choice is not touched.
  const set = (theme: string) => page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
  const was = await page.locator("html").getAttribute("data-theme");
  await set("dark");
  await page.screenshot({ path: `${dir}/${name}-dark.png` });
  await set(was ?? "light");
}

// menu picks an entry of a page's « ⋯ » menu.
async function menu(pg: Locator, name: string) {
  await pg.getByRole("button", { name: "Plus" }).click();
  await pg.page().getByRole("menuitemcheckbox", { name }).click();
}

// home closes every page open, one by one, the one the camera frames (the
// console talks to it) first: the console then talks to the home.
async function home(page: Page) {
  const pages = page.locator("article.pg");
  for (let n = await pages.count(); n > 0; n = await pages.count()) {
    // once they are at rest (the layout and the camera ease them into
    // place), the page the console talks to, else one the camera frames:
    // its close button on screen
    for (let i = 0; i < n; i++) await settledBox(pages.nth(i));
    let x: Locator | null = null;
    await expect
      .poll(async () => {
        const order = [page.locator("article.pg.talking"), ...Array.from({ length: n }, (_, i) => pages.nth(i))];
        for (const pg of order) if ((await pg.count()) && (await inView(pg.first().locator(".pg-x")))) return !!(x = pg.first().locator(".pg-x"));
        return false;
      })
      .toBe(true);
    await x!.click();
    await expect(pages).toHaveCount(n - 1);
  }
}

// inView tells whether an element is whole inside the viewport.
async function inView(el: Locator): Promise<boolean> {
  const b = await el.boundingBox(), v = el.page().viewportSize()!;
  return !!b && b.x >= 0 && b.y >= 0 && b.x + b.width <= v.width && b.y + b.height <= v.height;
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

// settledBox waits until a page stopped moving (the camera and its spring
// at rest).
async function settledBox(el: Locator): Promise<void> {
  let last = await el.boundingBox();
  for (let i = 0; i < 40; i++) {
    await el.page().waitForTimeout(150);
    const now = await el.boundingBox();
    if (last && now && Math.abs(now.x - last.x) + Math.abs(now.y - last.y) + Math.abs(now.width - last.width) < 0.5) return;
    last = now;
  }
}

// expectApart checks open pages, once they are at rest (the layout's
// soft collision and the camera's spring ease them into place): no two
// overlap, the last one opened is whole on screen, its head in view, and
// none goes under the bar.
async function expectApart(page: Page, pages: Locator[]): Promise<void> {
  await expect(async () => {
    for (const p of pages) await settledBox(p);
    const view = page.viewportSize()!;
    const bar = (await page.locator(".cs").boundingBox())!;
    const boxes = await Promise.all(pages.map(async (p) => (await p.boundingBox())!));
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i], b = boxes[j];
        const apart = a.x + a.width <= b.x || b.x + b.width <= a.x || a.y + a.height <= b.y || b.y + b.height <= a.y;
        expect(apart, `pages ${i} and ${j} overlap`).toBe(true);
      }
    }
    const last = boxes.at(-1)!;
    const head = (await pages.at(-1)!.locator(".pg-head").boundingBox())!;
    expect(head.y).toBeGreaterThanOrEqual(0);
    expect(last.x).toBeGreaterThanOrEqual(0);
    expect(last.x + last.width).toBeLessThanOrEqual(view.width);
    // a page in view ends above the bar zone: its last line is not under it
    for (const b of boxes) {
      const seen = b.x + b.width > 0 && b.x < view.width;
      if (seen && b.x < bar.x + bar.width && bar.x < b.x + b.width) expect(b.y + b.height, "a page under the bar").toBeLessThanOrEqual(bar.y + 0.5);
    }
  }).toPass({ timeout: 15_000 });
}

// expectBubblesApart checks the graph once it settled: no two bubbles'
// circles intersect, nor two subjects' labels.
async function expectBubblesApart(page: Page): Promise<void> {
  const overlaps = () =>
    page.locator(".gr-node").evaluateAll((els) => {
      const bs = els.map((el) => {
        const e = el as HTMLElement;
        const core = e.querySelector(".gr-core") as HTMLElement;
        return { id: e.dataset.node, x: parseFloat(e.style.left), y: parseFloat(e.style.top), r: core.offsetWidth / 2 };
      });
      const labels = els.filter((el) => el.classList.contains("d0")).map((el) => ({ id: (el as HTMLElement).dataset.node, b: el.querySelector(".gr-label b")!.getBoundingClientRect() }));
      const out: string[] = [];
      for (let i = 0; i < bs.length; i++)
        for (let j = i + 1; j < bs.length; j++)
          if (Math.hypot(bs[i].x - bs[j].x, bs[i].y - bs[j].y) < bs[i].r + bs[j].r) out.push(`${bs[i].id} on ${bs[j].id}`);
      for (let i = 0; i < labels.length; i++)
        for (let j = i + 1; j < labels.length; j++) {
          const a = labels[i].b, b = labels[j].b;
          if (a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom) out.push(`${labels[i].id}'s label on ${labels[j].id}'s`);
        }
      return out;
    });
  await expect.poll(overlaps).toEqual([]);
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
  await shot(page, "phone-page");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Ma journée");
  await day.getByRole("button", { name: "Fermer cette page" }).click();
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(rows.first()).toBeVisible();
  expect(problems).toEqual([]);
});
