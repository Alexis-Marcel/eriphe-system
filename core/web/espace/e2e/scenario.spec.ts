import type { Locator, Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { rest, still } from "./rest";

// The epic's acceptance scenario (#625), its steps played in the
// workspace against the test core of TestEspaceScenario: faked tools, a
// scripted brain, the real engine, cards, filing and stream. What the world
// outside the user does (a monitor's mail, a task that finishes) is asked
// of the test core at /e2e/. The page must say nothing to the console, and
// break no rule of its Content-Security-Policy. The bar is only a bar:
// Eriphe answers with its orb, beside the subject its words belong to, or
// at the home when they belong to none, in a few lines; what Eriphe says
// at length, its questions and their buttons are in the subject's
// document.
//
// Where things sit (bubbles apart, pages apart, the orb beside its
// subject) is the layout's, the camera's and the orb's unit tests
// (src/graph/layout.test.ts, camera.test.ts, src/orb/model.test.ts); here
// it is checked once or twice, where the real sizes of the page meet them.

// A node someone else's words titled (the urgent mail): its title is shown
// as it is, marked with ⚠, never replaced.
const mailTitle = "Urgent : la prod renvoie des 500";
const external = `${mailTitle} ⚠`; // how a page and the bar name it

test.describe.configure({ mode: "serial" });

test("the steps of the scenario", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => console.error(`CSP: ${e.violatedDirective} ${e.blockedURI}`));
  });

  await page.goto("/espace");

  // Eriphe's words: its orb's, out in the space (or, with no free spot
  // there, a line above the bar).
  const words = page.locator(".orb-say.on, .orb-line");
  const said = words.locator(".orb-text");
  const orb = page.locator("button.orb");
  // The bar's buttons: none (the cards no node holds aside).
  const barButtons = page.locator(".cs-bar button");
  const say = async (text: string) => {
    const input = page.locator(".cs-bar input");
    await input.fill(text);
    await input.press("Enter");
  };
  const bubble = (title: string) => page.locator(`.gr-node:not(.external) .gr-core[aria-label^="${title}"]`);
  const pageOf = (title: string) => page.locator(`article.pg[aria-label="${title}"]`);

  // 1. Home. Eriphe's greeting answers nothing the user said: the orb
  // stays in the bar, silent (it only answers).
  await expect(page.locator(".gr-node .gr-core").first()).toBeVisible();
  await expect(orb).toHaveClass(/docked/);
  await expect(said).toHaveCount(0);
  await expect(page.locator("article.pg")).toHaveCount(0);

  // 2. One call, no node opened; it is logged in « Ma journée »'s trace.
  // A turn about no subject: the orb leaves the bar and answers at the
  // home, on a free spot, clear of every bubble, title and the bar; its
  // words stay until the user moves on, here closes them: it goes back to
  // the bar. No button in the bar.
  await say("Allume la lumière du bureau.");
  await expect(said).toContainText("C'est allumé.");
  await expect(orb).not.toHaveClass(/docked/);
  await expect(words).toHaveClass(/answer/);
  await shot(page, "orb-home");
  await expect(barButtons).toHaveCount(0);
  await words.getByRole("button", { name: "Fermer" }).click();
  await expect(orb).toHaveClass(/docked/);
  await expect(said).toHaveCount(0);
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
  // A turn on a subject: the orb beside its page, with what it did.
  await expect(said).toContainText("Je lance l'analyse du style");
  await expect(orb).not.toHaveClass(/docked/);
  await shot(page, "orb-subject");
  await expect(bubble("Style des articles")).toBeVisible();
  await expect(bubble("Plan du premier article")).toBeVisible();
  await expect(page.locator(".gr-node.running .gr-core[aria-label^='Style des articles']")).toBeVisible();
  await expect(page.locator(".gr-node.running .gr-core[aria-label^='Style des articles']")).toHaveAttribute("aria-label", /3 articles lus/);

  // 5. The answer goes into « Ma journée », not into a new node.
  const before = await page.locator(".gr-node").count();
  // Eriphe shows the page beside; the bar still talks to Substack, the
  // orb says its short answer beside it.
  await say("Qu'est-ce que j'ai de prévu aujourd'hui ?");
  await expect(said).toContainText("Deux rendez-vous");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Substack");
  await expect(pageOf("Ma journée")).toContainText("Point produit");
  await expect(pageOf("Ma journée")).toContainText("Dentiste");
  await expect(page.locator(".gr-node")).toHaveCount(before);

  // 6. A monitor opens a node: an urgent mail. One sign per meaning: its
  // bubble says it waits for the user by its violet halo alone, no word
  // under its title (its core says it to a screen reader); its title,
  // someone else's words, shows as it is, never replaced, drawn as any
  // other title (its page's head says it with its ⚠; its core's words, ⚠).
  const mail = (await (await request.post("/e2e/mail")).json()).id as string;
  const waiting = page.locator(`.gr-node.yours[data-node="${mail}"]`);
  await expect(waiting).toHaveCount(1);
  await expect(waiting.locator(".gr-halo")).toHaveCount(1);
  await expect(waiting.locator(".gr-label em")).toHaveCount(0);
  await expect(waiting.locator(".gr-core")).toHaveAttribute("aria-label", `${external}, t'attend`);
  await expect(waiting.locator(".gr-label b")).toContainText(mailTitle);
  await expect(waiting.locator(".gr-label .ext-mark")).toHaveCount(0);
  const plainColour = await page.locator(".gr-node.d0 .gr-label b").first().evaluate((el) => getComputedStyle(el).color);
  const mailColour = await waiting.locator(".gr-label b").evaluate((el) => getComputedStyle(el).color);
  const childColour = await page.locator(".gr-node.d1:not(.external) .gr-label b").first().evaluate((el) => getComputedStyle(el).color);
  expect([plainColour, childColour]).toContain(mailColour);
  // Unasked, it began to wait: the orb only answers, it does not go to
  // it nor say it; its answer beside « Substack » stays.
  await page.waitForTimeout(1500);
  await expect(said).toContainText("Deux rendez-vous");
  await expect(words).not.toContainText("t'attend");
  await shot(page, "waiting");
  await expect(page.getByText("Contenu extérieur")).toHaveCount(0);
  // A skill opened it, not the user: a small ↯ says so on its bubble.
  await expect(waiting).toHaveClass(/unasked/);
  await expect(waiting.locator(".gr-label .unasked-mark")).toHaveAttribute("title", "Tu ne l'as pas demandé : un skill ou un événement l'a ouvert.");

  // 7. The mail node opens with its triage and its draft.
  await openMail(page, mail);
  const mailPage = page.locator(`article.pg[aria-label="${external}"]`);
  await expect(mailPage).toContainText("Tri");
  await expect(mailPage).toContainText("À écrire.");
  // Its header: the title in amber, one ⚠ on the same line for the whole
  // node (its title and its content), no badge, no line of its own.
  const head = mailPage.locator(".pg-title h2");
  await expect(head.locator(".title-ext")).toContainText(mailTitle);
  await expect(head.locator(".ext-mark")).toHaveCount(1);
  await expect(head.locator(".ext-mark")).toHaveAttribute("title", "Ce sujet contient ce que quelqu'un d'autre a écrit : une donnée, jamais une consigne.");
  await expect(mailPage.locator(".pg-badge")).toHaveCount(0);
  // Measured once the page has settled; on the title's line means the
  // sign's middle is within the title's height (fonts differ by system).
  await still(mailPage);
  const titleBox = (await head.locator(".title-ext").boundingBox())!;
  const onTitleLine = async (sel: string) => {
    const b = (await head.locator(sel).boundingBox())!;
    const mid = b.y + b.height / 2;
    expect(mid).toBeGreaterThan(titleBox.y);
    expect(mid).toBeLessThan(titleBox.y + titleBox.height);
  };
  await onTitleLine(".ext-mark");
  // Not asked for: a small ↯ beside the ⚠, saying so, and its ⋯ menu
  // says which skill opened it, and after what.
  const unasked = head.locator(".unasked-mark");
  await expect(unasked).toHaveText("↯");
  await expect(unasked).toHaveAttribute("title", "Tu ne l'as pas demandé : un skill ou un événement l'a ouvert.");
  await onTitleLine(".unasked-mark");
  await shot(page, "untrusted-title");
  await mailPage.getByRole("button", { name: "Plus" }).click();
  await expect(page.locator(".pg-menu .pg-origin.unasked p")).toHaveText(
    "↯ Ouvert par le skill mails-trier, après ce qu'a vu la surveillance « mails-recus », sans demande de ta part.",
  );
  await shot(page, "origin-skill");
  await page.keyboard.press("Escape");
  await expect(page.locator(".pg-menu")).toHaveCount(0);

  // 8. A draft with its « Envoyer ? » card, the production view beside it,
  // a research child running.
  await say("Réponds que je regarde dans l'heure, ouvre-moi la prod, et lance en arrière-plan une recherche sur l'erreur.");
  // Its send waits for the user's yes: the orb only answers, beside the
  // mail's page, with what it did; what waits is the graph's to show, the
  // card in the page and the node's halo.
  await expect(said).toContainText("Le brouillon est prêt");
  await expect(words).toHaveClass(/answer/);
  await expect(said).not.toContainText("Il me faut ton oui ici.");
  await orbBeside(page, mailPage);
  await expect(page.locator(`.gr-node.yours[data-node="${mail}"]`)).toHaveCount(1);
  await shot(page, "orb-card-answer");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", `Écris à Eriphe, sur ${external}`);
  await expect(mailPage).toContainText("Je regarde dans l'heure.");
  await expect(pageOf("Production")).toBeVisible();
  await expect(pageOf("Production")).toContainText("42");
  // A tall page, a table in it, beside the mail's.
  await expect(pageOf("Production").locator("table")).toBeVisible();
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
  await expect(page.locator(".gr-node.waits .gr-core[aria-label^='Plan du premier article']")).toBeVisible({ timeout: 30_000 });

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
  // One version as the user counts them (no chip of versions yet): the
  // step's empty creation is not one.
  const v = plan.locator(".pg-vchip");
  await expect(v).toHaveCount(0);
  await expect(page.locator(".gr-node.yours .gr-core[aria-label^='Plan du premier article']")).toHaveCount(1);

  // 12. Said to the open plan page: a new version, the older one still
  // readable.
  await plan.locator(".pg-head").click({ position: { x: 10, y: 10 } });
  await say("Plus court, et garde le titre 2.");
  await expect(said).toContainText("Plus court, le titre 2 gardé.");
  await expect(plan).not.toContainText("Pourquoi ce métier");
  await expect(plan).toContainText("Ce qui a changé en dix ans");
  // The versions are one chip, the one shown; its list, the newest first.
  await expect(v).toHaveText("v2");
  await v.click();
  const vs = page.getByRole("listbox", { name: "Versions" }).getByRole("option");
  await expect(vs.locator("b")).toHaveText(["v2", "v1"]);
  await vs.nth(1).click();
  await expect(v).toHaveText("v1");
  await expect(v).toHaveClass(/old/);
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
  await expect(archived).toContainText(mailTitle);
  await expect(archived.locator(".ext-mark")).toBeVisible();
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
  // The user asked for it: no sign, and its ⋯ menu quotes their words.
  await expect(watch.locator(".unasked-mark")).toHaveCount(0);
  await expect(page.locator(".gr-node.unasked", { has: page.locator('.gr-core[aria-label^="Garder un œil sur mes mails"]') })).toHaveCount(0);
  await watch.getByRole("button", { name: "Plus" }).click();
  const origin = page.locator(".pg-menu .pg-origin");
  await expect(origin.locator("h3")).toHaveText("D'où il vient");
  await expect(origin.locator("p")).toHaveText("Ouvert à ta demande : « Garde un œil sur mes mails. »");
  await expect(origin).not.toHaveClass(/unasked/);
  await shot(page, "origin-asked");
  await page.keyboard.press("Escape");
  await expect(origin).toHaveCount(0);
  await expect(watch).toContainText("Par défaut au brief, jamais la nuit");
  await expect(barButtons).toHaveCount(0);
  await expect(watch.locator(".pg-foot")).toHaveCount(0);
  const defaults = watch.getByRole("button", { name: "Fais comme tu proposes" });
  await expect(watch.getByRole("button", { name: "Toutes, et réveille-moi la nuit pour la prod" })).toBeVisible();
  await defaults.click();
  // The view is proposed as a draft of its own, opened beside the
  // subject: its card waits on it for the user's yes, its node's halo says
  // so, and the orb says the answer, as any other.
  await expect(said).toContainText("Voici la vue : dis oui sur sa carte et elle s'ouvre.");
  await expect(words).toHaveClass(/answer/);
  const draft = pageOf("Mes mails");
  await expect(draft).toBeVisible();
  await expect(page.locator(`.gr-node.yours[data-node="${await page.locator(".gr-page", { has: draft }).getAttribute("data-page")}"]`)).toHaveCount(1);
  // The subject says where things stand: the questions and their
  // buttons gone, the choices written, the draft linked; no card in it.
  await expect(defaults).toHaveCount(0);
  await expect(watch).not.toContainText("Par défaut au brief");
  await expect(watch).toContainText("Ce qu'on a décidé");
  await expect(watch.locator(".pg-foot")).toHaveCount(0);
  // The draft's card says in one line what will run by itself, the page
  // above it its preview; its technical words, the skill's instructions
  // drawn as Markdown, are one click away.
  const viewCard = draft.locator(".pg-foot .a2-proposal");
  await expect(viewCard).toHaveCount(1);
  await expect(viewCard).toContainText("Mes mails");
  await expect(viewCard).toContainText("prévenu au brief, jamais la nuit");
  await expect(viewCard.locator(".a2-proposal-summary")).toHaveText("Toutes les 5 min");
  await expect(draft).toContainText("Les mails qui demandent une réponse.");
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
  await expect(page.locator(".gr-node.standing", { has: page.locator('.gr-core[aria-label="Mes mails"]') })).toHaveCount(1);
  await expect(pageOf("Mes mails")).toBeVisible();
  // Opened beside the subject, the view's page takes free room next to
  // it: the two never overlap, and the one the bar talks to, the graph's
  // centre, is framed whole, its head on screen, above the bar.
  await still(pageOf("Mes mails"));
  await shot(page, "view-beside", false);
  await expectApart(page, [watch, pageOf("Mes mails")]);
  await page.setViewportSize({ width: 1440, height: 900 });
  // Answered, the card is decided: it leaves the document (the page's ⋯
  // menu lists it).
  await expect(viewCard).toHaveCount(0);
  await expect(watch.getByRole("button", { name: "Oui" })).toHaveCount(0);
  // Its pages closed, the view's bubble, made on the subject, sits beside
  // it. Dropped right on the view, nothing is pinned: the two are set
  // apart and the graph settles again, no bubble on another.
  await home(page);
  const mine = page.locator(".gr-node:not(.external)", { has: page.locator('.gr-core[aria-label^="Mes mails"]') });
  const watching = page.locator(".gr-node:not(.external)", { has: page.locator('.gr-core[aria-label^="Garder un œil sur mes mails"]') });
  await settled(mine);
  await settled(watching);
  await dragOnto(page, bubble("Mes mails"), bubble("Garder un œil sur mes mails"));
  const dropped = await settled(mine);
  const aside = await settled(watching);
  expect(Math.hypot(aside[0] - dropped[0], aside[1] - dropped[1])).toBeGreaterThan(10);
  await expectBubblesApart(page);
  await shot(page, "graph-apart");

  // 14b. The view's skill, woken once per mail, proposed to archive
  // three: they wait as one card on the view's page, in plain
  // words (« Déplacer 3 mails vers les archives »), each mail a line
  // ticked, still signed as someone else's words (⚠), where it comes from
  // under Détails. The user leaves one out: Oui archives the two others,
  // the card, settled, leaves the page's foot.
  await home(page);
  await bubble("Mes mails").click();
  const view = pageOf("Mes mails");
  await expect(view).toBeVisible();
  // A permanent subject shows it: a small sign by its date, a satellite
  // orbiting its bubble; its ⋯ menu says what keeps it alive, in code's
  // words: the monitor that watches, the skill it starts.
  await expect(view.locator(".pg-title .pg-standing")).toHaveAttribute("title", "Sujet permanent");
  await expect(page.locator(".gr-node.standing", { has: page.locator('.gr-core[aria-label="Mes mails"]') }).locator(".gr-sat")).toHaveCount(1);
  await expect(page.locator(".gr-node:not(.standing) .gr-sat")).toHaveCount(0);
  await view.getByRole("button", { name: "Plus" }).click();
  const alive = page.locator(".pg-menu .pg-alive");
  await expect(alive.locator("h3")).toHaveText("Ce qui le fait vivre");
  await expect(alive.locator(".pg-alive-monitors li")).toContainText("mails-recus");
  await expect(alive.locator(".pg-alive-skills li")).toHaveText("mails-trier lancé quand la surveillance « mails-recus » voit quelque chose, au plus une fois toutes les 15 min");
  await expect(alive.locator(".pg-alive-at")).toContainText("Mis à jour");
  await expect(page.locator(".pg-menu .pg-origin p")).toHaveText("Ouvert à ta demande : tu as dit oui à sa carte.");
  await shot(page, "alive-menu");
  await page.keyboard.press("Escape");
  await expect(alive).toHaveCount(0);
  expect((await request.post("/e2e/proposals")).status()).toBe(204);
  const batch = view.locator(".pg-foot .a2-proposal");
  await expect(batch).toHaveCount(1);
  await expect(batch.locator(".a2-proposal-text")).toHaveText("Déplacer 3 mails vers les archives");
  const ticks = batch.locator(".a2-proposal-choices input[type=checkbox]");
  await expect(ticks).toHaveCount(3);
  await expect(batch.locator(".a2-proposal-choices")).toContainText("« La lettre de la semaine » · news@example.org");
  await expect(batch.locator(".a2-proposal-kicker .a2-taint")).toBeVisible();
  await expect(batch.locator(":scope > .a2-proposal-from, :scope > .a2-proposal-yes, :scope > .a2-proposal-why")).toHaveCount(0);
  // Oui is the strong button, in the ink, in either theme; Non a discreet ✕, no fill.
  const [oui, non] = [batch.locator(".a2-proposal-actions .a2-button-primary"), batch.getByRole("button", { name: "Non" })];
  const bg = (b: Locator) => b.evaluate((el) => getComputedStyle(el).backgroundColor);
  // A theme's colour, as the page computes it.
  const token = (name: string) =>
    page.evaluate((n) => {
      const d = document.createElement("i");
      d.style.color = `var(${n})`;
      document.body.append(d);
      const v = getComputedStyle(d).color;
      d.remove();
      return v;
    }, name);
  for (const theme of ["light", "dark"]) {
    await page.evaluate((th) => (document.documentElement.dataset.theme = th), theme);
    const [fg, card] = [await token("--fg"), await token("--card")];
    expect(fg).not.toBe(card);
    await expect.poll(() => bg(oui)).toBe(fg);
    await expect.poll(() => bg(non)).toBe("rgba(0, 0, 0, 0)");
  }
  await page.evaluate(() => (document.documentElement.dataset.theme = "light"));
  await still(view);
  await shot(page, "chosen-calls-pending");
  await ticks.nth(1).uncheck();
  await batch.getByRole("button", { name: "Déplacer 2 mails vers les archives" }).click();
  // Decided and done, it asks nothing more: it leaves the page's foot.
  await expect(batch).toHaveCount(0);
  // Each call as kept, one mail each; the one left out never moved.
  await expect
    .poll(async () => ((await (await request.get("/e2e/tools")).json()).moved as string[][]).filter((ids) => ids.some((id) => /^n\d$/.test(id))).sort())
    .toEqual([["n1"], ["n3"]]);
  // Kept in the « ⋯ » menu, under « Déjà décidé ».
  await view.getByRole("button", { name: "Plus" }).click();
  await expect(page.locator(".pg-menu .pg-settled h3")).toHaveText("Déjà décidé");
  await expect(page.locator(".pg-menu .pg-settled li").first()).toContainText("Fait");
  await page.keyboard.press("Escape");
  await shot(page, "chosen-calls");

  // 15. A view proposed at the home, no subject opened: its draft is the
  // subject, opened in front with its card at its foot, never above the
  // bar; answered, the card leaves and the page stands.
  await home(page);
  await say("Propose-moi une vue de l'agenda.");
  await expect(said).toContainText("Voici la vue de ton agenda");
  const agenda = pageOf("Mon agenda");
  await expect(agenda).toBeVisible();
  await expect(page.locator(".cs-cards")).toHaveCount(0);
  const agendaCard = agenda.locator(".pg-foot .a2-proposal");
  await agendaCard.getByRole("button", { name: "Oui" }).click();
  await expect(agendaCard).toHaveCount(0);
  await expect(page.locator(".gr-node.standing", { has: page.locator('.gr-core[aria-label="Mon agenda"]') })).toHaveCount(1);

  // 16. Asked on the side: its node is born on the graph, not opened, and
  // the bar stays at the home.
  await home(page);
  await say("Lance une recherche à part sur les newsletters.");
  await expect(said).toContainText("Je lance la recherche à part.");
  await expect(bubble("Recherche sur les newsletters")).toBeVisible();
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe");
  // The subjects in a compact disc around the centre, the whole graph in view.
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
  await expect(article.locator(".pg-vchip")).toHaveText("v2");
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

// What the workspace keeps for the user: the pages open, the theme and the
// graph's settings, on the next visit; a bubble dropped is not pinned, and
// where it is is not kept.
// With motion (@motion): a dragged bubble glides to where it is let go.
test("the workspace as the user left it", { tag: "@motion" }, async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.goto("/espace");

  // Home: no page open, the whole graph in view.
  await home(page);
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect.poll(async () => (await (await page.request.get("/espace/boot")).json()).view.open).toEqual([]);
  const node = page.locator(".gr-node", { has: page.locator('.gr-core[aria-label="Mails"]') });
  const core = node.locator(".gr-core");
  await expect(core).toBeVisible();
  const from = await settled(node);
  // Dragged, it stays where it is let go: nothing is sent, nothing kept.
  const sent: string[] = [];
  page.on("request", (r) => r.method() !== "GET" && r.url().includes("/api/nodes/") && sent.push(r.url()));
  const box = (await core.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  for (let i = 1; i <= 12; i++) await page.mouse.move(box.x + box.width / 2 + i * 12, box.y + box.height / 2 + i * 6);
  await page.mouse.up();
  await expect.poll(async () => (await at(node))[0]).not.toBeCloseTo(from[0], 0);
  await settled(node);
  expect(sent).toEqual([]);
  await expectBubblesApart(page);

  // Nothing is pinned: a page opened becomes the graph's centre, the
  // graph settles round it, and once it is closed the graph rests again,
  // no bubble on another, still nothing sent.
  await page.locator('.gr-core[aria-label="Substack"]').click();
  await expect(page.locator('article.pg[aria-label="Substack"]')).toBeVisible();
  await home(page);
  await settled(node);
  await expectBubblesApart(page);
  expect(sent).toEqual([]);

  // The page open and the theme come back on the next visit; where the
  // bubbles were is not kept: the graph lays itself out afresh.
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

  // The graph's settings, under the gear: a force moved and the centring
  // turned off are kept for the next visit, and « Par défaut » gives the
  // forces back.
  const gear = page.getByRole("button", { name: "Réglages" });
  await gear.click();
  const settings = page.getByRole("region", { name: "Réglages" });
  const repulsion = settings.getByRole("slider", { name: "Répulsion" });
  await repulsion.focus();
  await page.keyboard.press("ArrowRight");
  await settings.getByRole("checkbox", { name: "Le node ouvert devient le centre" }).uncheck();
  await expect(settings.getByRole("link", { name: "Tous les réglages" })).toHaveAttribute("href", "/settings");
  await expect.poll(async () => (await (await page.request.get("/espace/boot")).json()).view).toMatchObject({ forces: { repulsion: 2050 }, centring: false });
  await page.reload();
  await page.getByRole("button", { name: "Réglages" }).click();
  await expect(settings.getByRole("slider", { name: "Répulsion" })).toHaveValue("2050");
  await expect(settings.getByRole("checkbox", { name: "Le node ouvert devient le centre" })).not.toBeChecked();
  await settings.getByRole("button", { name: "Par défaut" }).click();
  await settings.getByRole("checkbox", { name: "Le node ouvert devient le centre" }).check();
  await expect.poll(async () => (await (await page.request.get("/espace/boot")).json()).view.forces).toBeUndefined();
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

// menu chooses an entry of a page's « ⋯ » menu.
async function menu(pg: Locator, name: string) {
  await pg.getByRole("button", { name: "Plus" }).click();
  await pg.page().getByRole("menuitemcheckbox", { name }).click();
}

// home closes every page open, one by one, the one the camera frames (the
// bar talks to it) first: the bar then talks to the home.
async function home(page: Page) {
  // The graph come first (its nodes reach the page on the stream, after
  // the view it restores): a page of the view is drawn only once its node
  // is there, so none counted before would be none closed. On a phone, an
  // open page stands in place of the list.
  await expect(page.locator(".gr-node, .ls-row, article.pg")).not.toHaveCount(0);
  const pages = page.locator("article.pg");
  for (let n = await pages.count(); n > 0; n = await pages.count()) {
    // once they are at rest (the layout and the camera ease them into
    // place), the page the bar talks to, else one the camera frames:
    // its close button on screen
    for (let i = 0; i < n; i++) await still(pages.nth(i));
    let x: Locator | null = null;
    let left = false; // a page that was fading out when counted is gone
    await expect
      .poll(async () => {
        if ((await pages.count()) < n) return (left = true);
        const order = [page.locator("article.pg.talking"), ...Array.from({ length: n }, (_, i) => pages.nth(i))];
        for (const pg of order) if ((await pg.count()) && (await inView(pg.first().locator(".pg-x"))) && (await onTop(pg.first().locator(".pg-x")))) return !!(x = pg.first().locator(".pg-x"));
        // Eriphe's words at the home may lie over the neighbours' heads
        // (the page in focus is the centre, the others round it): closed.
        const say = page.locator(".orb-say.on .orb-x");
        if (await say.count()) await say.first().click();
        return false;
      })
      .toBe(true);
    if (left) continue;
    await x!.click();
    await expect(pages).toHaveCount(n - 1);
  }
}

// onTop tells whether nothing lies over an element's middle: a click there
// lands on it.
async function onTop(el: Locator): Promise<boolean> {
  return el.evaluate((e) => {
    const r = e.getBoundingClientRect();
    const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
    return !!hit && e.contains(hit);
  });
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
// in view: the bar talks to it.
async function openMail(page: Page, id: string) {
  await home(page);
  await expect(page.locator("article.pg")).toHaveCount(0);
  await page.locator(`[data-node="${id}"] .gr-core`).click();
  await expect(page.locator(`article.pg[aria-label="${external}"]`)).toBeVisible();
}

// expectApart checks open pages, once they are at rest (the layout's
// soft collision and the camera's spring ease them into place): no two
// overlap, and the one in focus (the bar talks to it: the graph's centre)
// is whole on screen, its head in view, not under the bar; the others may
// go past the screen's edge.
async function expectApart(page: Page, pages: Locator[]): Promise<void> {
  await expect(async () => {
    for (const p of pages) await still(p);
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
    const talking = await Promise.all(pages.map((p) => p.evaluate((el) => el.classList.contains("talking"))));
    const f = talking.includes(true) ? talking.indexOf(true) : pages.length - 1; // none: the last opened
    const focus = boxes[f];
    const head = (await pages[f].locator(".pg-head").boundingBox())!;
    expect(head.y).toBeGreaterThanOrEqual(0);
    expect(focus.x).toBeGreaterThanOrEqual(0);
    expect(focus.x + focus.width).toBeLessThanOrEqual(view.width);
    // the page in focus ends above the bar zone: its last line is not under it
    if (focus.x < bar.x + bar.width && bar.x < focus.x + focus.width) expect(focus.y + focus.height, "the page in focus under the bar").toBeLessThanOrEqual(bar.y + 0.5);
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

// dragOnto drags a bubble by the pointer, in small steps, and lets it go
// right on another.
async function dragOnto(page: Page, what: Locator, onto: Locator): Promise<void> {
  const a = (await what.boundingBox())!, b = (await onto.boundingBox())!;
  const from = [a.x + a.width / 2, a.y + a.height / 2], to = [b.x + b.width / 2, b.y + b.height / 2];
  await page.mouse.move(from[0], from[1]);
  await page.mouse.down();
  for (let i = 1; i <= 12; i++) await page.mouse.move(from[0] + ((to[0] - from[0]) * i) / 12, from[1] + ((to[1] - from[1]) * i) / 12);
  await page.mouse.up();
}

// settled is where a bubble is once the graph is at rest.
async function settled(node: Locator): Promise<[number, number]> {
  await rest(node.page());
  return at(node);
}

// On a phone: the graph as a list, a tap opens one page over it.
test("the workspace on a small screen", async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/espace");

  await home(page);
  await expect(page.locator(".gr-node")).toHaveCount(0);
  const rows = page.locator(".ls-row");
  await expect(rows.filter({ hasText: "Substack" })).toBeVisible();
  // a subject, then its bubbles under it
  const titles = await rows.locator("b").allTextContents();
  expect(titles.indexOf("Premier article")).toBeGreaterThan(titles.indexOf("Substack"));
  expect(titles.indexOf("Plan du premier article")).toBeGreaterThan(titles.indexOf("Premier article"));
  // the archived mail is not there: in the archives, its title marked
  await expect(rows.filter({ hasText: mailTitle })).toHaveCount(0);
  await page.locator(".hud-archives .hud-icon").click();
  await expect(page.locator(".hud-arch li").filter({ hasText: mailTitle }).locator(".ext-mark")).toBeVisible();
  await shot(page, "phone-archives");
  await page.locator(".hud-archives .hud-icon").click();

  // nor the prod's page, its child
  await expect(rows.filter({ hasText: "Production" })).toHaveCount(0);
  // a subject the user asked for: no ↯ in its row
  await expect(rows.filter({ hasText: "Garder un œil sur mes mails" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "Garder un œil sur mes mails" }).locator(".unasked-mark")).toHaveCount(0);

  await rows.filter({ hasText: "Ma journée" }).locator("button").click();
  const day = page.locator('article.pg[aria-label="Ma journée"]');
  await expect(day).toBeVisible();
  await expect(day).toContainText("Point produit");
  await expect(day.locator(".pg-standing")).toBeVisible(); // a permanent subject, on a phone too
  await shot(page, "phone-page");
  await expect(page.locator(".cs-bar input")).toHaveAttribute("aria-label", "Écris à Eriphe, sur Ma journée");
  // The orb on the phone: no gliding, a line above the bar, its subject named.
  await page.locator(".cs-bar input").fill("Merci.");
  await page.locator(".cs-bar input").press("Enter");
  const line = page.locator(".orb-line");
  await expect(line).toContainText("D'accord.");
  await expect(line.locator(".orb-on")).toHaveText("Sur Ma journée");
  await expect(page.locator("button.orb")).toHaveCount(0);
  await shot(page, "orb-phone");
  await day.getByRole("button", { name: "Fermer cette page" }).click();
  await expect(page.locator("article.pg")).toHaveCount(0);
  await expect(rows.first()).toBeVisible();
  expect(problems).toEqual([]);
});

// orbClear checks that Eriphe's orb and its words cover nothing: no
// bubble, no title, no page, not the bar.
async function orbClear(page: Page): Promise<void> {
  await expect
    .poll(
      () =>
        page.evaluate(() => {
          const r = (e: Element) => e.getBoundingClientRect();
          const mine = [document.querySelector("button.orb:not(.docked)"), document.querySelector(".orb-say.on")].filter(Boolean).map((e) => r(e!));
          const seen = (e: Element) => Number(getComputedStyle(e.closest(".gr-node") ?? e).opacity) > 0.05;
          const theirs = [...document.querySelectorAll(".gr-core, .gr-label b, .gr-page, .cs-bar")].filter(seen).map(r);
          const hit = (a: DOMRect, b: DOMRect) => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
          return mine.flatMap((m) => theirs.filter((t) => hit(m, t)).map((t) => `${Math.round(t.left)},${Math.round(t.top)},${Math.round(t.right)},${Math.round(t.bottom)} by ${Math.round(m.left)},${Math.round(m.top)},${Math.round(m.right)},${Math.round(m.bottom)}`));
        }),
      { message: "the orb covers something" },
    )
    .toEqual([]);
}

// orbBeside checks that the orb, out of the bar, is beside what it talks
// about (a page, a bubble and its title: their boxes as one), close to it
// (on a side, else, when the screen is crowded, the nearest free spot
// around it), and covers nothing. When nothing is free out there, it stays
// in the bar, its words a line above it: it covers nothing either.
async function orbBeside(page: Page, ...what: Locator[]): Promise<void> {
  const orb = page.locator("button.orb");
  if (await page.locator(".orb-line").isVisible()) return;
  await expect(orb).not.toHaveClass(/docked/);
  await expect
    .poll(async () => {
      const o = await orb.boundingBox();
      const boxes = await Promise.all(what.map((w) => w.boundingBox()));
      if (!o || boxes.some((b) => !b)) return Infinity;
      const l = Math.min(...boxes.map((b) => b!.x)), r = Math.max(...boxes.map((b) => b!.x + b!.width));
      const t = Math.min(...boxes.map((b) => b!.y)), b = Math.max(...boxes.map((b) => b!.y + b!.height));
      const dx = Math.max(l - (o.x + o.width), 0, o.x - r);
      const dy = Math.max(t - (o.y + o.height), 0, o.y - b);
      return Math.round(Math.hypot(dx, dy));
    }, { message: "the orb is not beside it" })
    .toBeLessThan(200);
  await orbClear(page);
}
