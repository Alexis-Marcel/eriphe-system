import { expect, test } from "./fixtures";

// Files attached from the bar (orb/Bar.tsx), against the test core of
// TestEspaceScenario: an image and two texts picked, one taken off, the
// message sent with the other two while the scripted brain is held at
// work by « Prends ton temps »; the message waits with its files' names;
// eriphe-core keeps them (/e2e/attachments), and once free the brain's
// turn tells it of each, to read it with read_attachment.

// A real 1×1 PNG: eriphe-core tells an image by its content.
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==", "base64");
const note = Buffer.from("Une ligne de notes.\n");

type Kept = { name: string; type: string; size: number; stored: number; told: boolean };

test("files attached from the bar reach eriphe-core and Eriphe", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  try {
    await page.goto("/espace");
    const input = page.locator(".cs-bar input");
    await expect(input).toBeVisible();
    const turns = async () => ((await (await request.get("/e2e/turns")).json()).turns as string[][]) ?? [];
    const before = (await turns()).length;

    // Eriphe at work: the message waits, and shows it.
    await input.fill("Prends ton temps.");
    await input.press("Enter");
    await expect.poll(async () => (await turns()).length).toBe(before + 1);

    // Three files picked with the paperclip; one taken off.
    const chooser = page.waitForEvent("filechooser");
    await page.getByRole("button", { name: "Joindre un fichier" }).click();
    await (
      await chooser
    ).setFiles([
      { name: "capture.png", mimeType: "image/png", buffer: png },
      { name: "notes.txt", mimeType: "text/plain", buffer: note },
      { name: "brouillon.md", mimeType: "text/markdown", buffer: Buffer.from("# Brouillon\n") },
    ]);
    const chips = page.locator(".cs-file .cs-file-name");
    await expect(chips).toHaveText(["capture.png", "notes.txt", "brouillon.md"]);
    await expect(page.locator(".cs-file img")).toHaveCount(1); // the image's thumbnail
    await page.getByRole("button", { name: "Retirer brouillon.md" }).click();
    await expect(chips).toHaveText(["capture.png", "notes.txt"]);

    // Sent: one multipart request (the browser does not show its files'
    // bytes here: what eriphe-core kept, below, says which went).
    const posted = page.waitForRequest((r) => r.method() === "POST" && new URL(r.url()).pathname === "/messages");
    await input.fill("Regarde ces fichiers.");
    await input.press("Enter");
    const req = await posted;
    expect(req.headers()["content-type"]).toContain("multipart/form-data");
    expect((await req.response())?.status()).toBe(204);
    await expect(page.locator(".cs-file")).toHaveCount(0);

    // It waits for Eriphe, its files named.
    const wait = page.locator(".cs-wait").filter({ hasText: "Regarde ces fichiers." });
    await expect(wait.locator(".cs-wait-files")).toHaveText("capture.png, notes.txt");

    // eriphe-core kept them, whole.
    const kept = async () => ((await (await request.get("/e2e/attachments")).json()).files as Kept[]) ?? [];
    expect((await kept()).map((k) => [k.name, k.type, k.stored])).toEqual([
      ["capture.png", "image/png", png.length],
      ["notes.txt", "text/plain", note.length],
    ]);

    // Eriphe free again: its turn tells it of both files.
    expect((await request.post("/e2e/finish/hold")).ok()).toBe(true);
    await expect.poll(async () => (await turns()).slice(before + 1)).toEqual([["Regarde ces fichiers."]]);
    await expect.poll(async () => (await kept()).map((k) => k.told)).toEqual([true, true]);
    await expect(page.locator(".cs-waiting")).toHaveCount(0);
    expect(problems).toEqual([]);
  } finally {
    await request.post("/e2e/finish/hold"); // never leave the brain held
  }
});
