import { expect, test } from "./fixtures";

// A photo sent from the bar becomes a node's picture (#748), against the
// test core of TestEspaceScenario: « Ajoute cette chemise à ma
// garde-robe » with a photo; the scripted brain opens the shirt's node
// and keeps the photo with it (node_set, the attachment's id as its
// property photo). Its page opens in front with the picture, loaded from
// eriphe-core, its tag and its other property beside it.

// A real 2×2 PNG: eriphe-core tells an image by its content.
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEElEQVR4nGP4z8AARAwQCgAf7gP9i18U1AAAAABJRU5ErkJggg==", "base64");

test("a photo sent with a message becomes a node's picture", async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.goto("/espace");
  const input = page.locator(".cs-bar input");
  await expect(input).toBeVisible();

  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Joindre un fichier" }).click();
  await (await chooser).setFiles([{ name: "chemise.png", mimeType: "image/png", buffer: png }]);
  await expect(page.locator(".cs-file .cs-file-name")).toHaveText(["chemise.png"]);
  await input.fill("Ajoute cette chemise à ma garde-robe.");
  await input.press("Enter");

  const pg = page.locator("article.pg").filter({ has: page.locator(".pg-title-tx", { hasText: "Chemise bleue" }) });
  const picture = pg.locator("img.pg-picture");
  await expect(picture).toBeVisible();
  await expect(picture).toHaveAttribute("alt", "photo");
  await expect.poll(() => picture.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth)).toBe(2);
  await expect(pg.locator(".pg-tag")).toHaveText(["#vêtement"]);
  await expect(pg.locator(".pg-prop")).toHaveText(["couleurbleu"]);
  await expect(pg).not.toContainText("media:");
  expect(problems).toEqual([]);
});
