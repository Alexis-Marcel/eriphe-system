import { expect, test } from "./fixtures";

// The messages that wait for Eriphe (messages_waiting.go), against the
// test core of TestEspaceScenario: the scripted brain is held at work by
// « Prends ton temps » until /e2e/finish/hold; what the user says
// meanwhile shows above the bar with a clock, and one is withdrawn before
// Eriphe reads it. /e2e/turns tells what each of its turns was given.


test("messages wait with a clock, and one is withdrawn", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  try {
    await page.goto("/espace");
    const input = page.locator(".cs-bar input");
    await expect(input).toBeVisible();
    const say = async (text: string) => {
      await input.fill(text);
      await input.press("Enter");
    };
    const turns = async () => ((await (await request.get("/e2e/turns")).json()).turns as string[][]) ?? [];
    const before = (await turns()).length;

    // Eriphe at work.
    await say("Prends ton temps.");
    await expect.poll(async () => (await turns()).length).toBe(before + 1);
    // At work, the orb out of the bar says so.
    const orb = page.locator("button.orb");
    await expect(orb).toHaveClass(/working/);
    await expect(orb).not.toHaveClass(/docked/);
    if (process.env.SHOTS) {
      await page.waitForTimeout(800); // there, at rest
      await page.screenshot({ path: `${process.env.SHOTS}/orb-working-light.png` });
      await page.evaluate(() => (document.documentElement.dataset.theme = "dark"));
      await page.screenshot({ path: `${process.env.SHOTS}/orb-working-dark.png` });
      await page.evaluate(() => (document.documentElement.dataset.theme = "light"));
    }

    // Two messages meanwhile: both wait, in order, a clock each.
    await say("Et pour le budget ?");
    await say("Et pour le planning ?");
    const waits = page.locator(".cs-wait");
    await expect(waits).toHaveCount(2);
    await expect(waits.locator(".cs-wait-text")).toHaveText(["Et pour le budget ?", "Et pour le planning ?"]);
    await expect(waits.locator(".cs-wait-clock")).toHaveCount(2);
    // Eriphe is still at work.
    await expect(orb).toHaveClass(/working/);

    // The second withdrawn.
    await waits.nth(1).getByRole("button", { name: "Retirer ce message" }).click();
    await expect(waits).toHaveCount(1);
    await expect(waits.locator(".cs-wait-text")).toHaveText(["Et pour le budget ?"]);
    await page.waitForTimeout(1500); // a stream round: it does not come back
    await expect(waits).toHaveCount(1);

    // Eriphe free again: it reads the first only, and the clock goes.
    expect((await request.post("/e2e/finish/hold")).ok()).toBe(true);
    await expect.poll(async () => (await turns()).slice(before + 1)).toEqual([["Et pour le budget ?"]]);
    await expect(page.locator(".cs-waiting")).toHaveCount(0);
    await expect(page.locator(".orb-say.on .orb-text, .orb-line .orb-text")).toContainText("D'accord.");
    expect(problems).toEqual([]);
  } finally {
    await request.post("/e2e/finish/hold"); // never leave the brain held
  }
});
