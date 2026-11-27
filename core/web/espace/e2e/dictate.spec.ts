import { expect, test } from "./fixtures";

// Dictation (orb/dictate.ts), against the test core of TestEspaceScenario:
// with Settings › Voice set to a fake service (/e2e/voice), the bar has a
// microphone; Chrome's fake microphone speaks, a tap stops, eriphe-core
// has it written and the words join the draft, after what was typed,
// without being sent. Without the setting, no microphone.

test("what is said is written into the draft, not sent", async ({ page, request, context }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  try {
    // Off: no microphone.
    expect((await request.post("/e2e/voice?on=0")).ok()).toBe(true);
    await page.goto("/espace");
    const input = page.locator(".cs-bar input");
    await expect(input).toBeVisible();
    await expect(page.locator(".cs-mic")).toHaveCount(0);

    // On: the microphone shows.
    expect((await request.post("/e2e/voice?on=1")).ok()).toBe(true);
    await context.grantPermissions(["microphone"]);
    await page.goto("/espace");
    const mic = page.getByRole("button", { name: "Dicter" });
    await expect(mic).toBeVisible();

    await input.fill("Note :");
    let sent = false;
    page.on("request", (r) => r.method() === "POST" && new URL(r.url()).pathname === "/messages" && (sent = true));
    await mic.click();
    const stop = page.getByRole("button", { name: "Arrêter et écrire" });
    await expect(stop).toBeVisible();
    await expect(stop).toHaveAttribute("aria-pressed", "true");
    await expect(input).toHaveAttribute("placeholder", /Je t'écoute/);
    await page.waitForTimeout(1200); // a second of the fake microphone's tone
    const posted = page.waitForResponse((r) => new URL(r.url()).pathname === "/api/voice/transcribe");
    await stop.click();
    expect((await posted).status()).toBe(200);

    await expect(input).toHaveValue("Note : Rappelle-moi d'acheter du pain.");
    await expect(input).toBeFocused();
    await expect(page.getByRole("button", { name: "Dicter" })).toBeEnabled();
    expect(sent).toBe(false);
    const heard = (await (await request.get("/e2e/voice")).json()).heard as string[];
    expect(heard.at(-1)).toMatch(/^speech\.webm [1-9]\d*$/);

    // Escape drops a dictation: nothing is sent to be written.
    const before = heard.length;
    await page.getByRole("button", { name: "Dicter" }).click();
    await expect(page.getByRole("button", { name: "Arrêter et écrire" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Dicter" })).toBeVisible();
    await page.waitForTimeout(300);
    expect(((await (await request.get("/e2e/voice")).json()).heard as string[]).length).toBe(before);
    await expect(input).toHaveValue("Note : Rappelle-moi d'acheter du pain.");
  } finally {
    await request.post("/e2e/voice?on=0");
  }
  expect(problems).toEqual([]);
});
