import { expect, test } from "./fixtures";

// A PDF sent from the bar is kept with a node and drawn in its page
// (#820), against the test core of TestEspaceScenario: « Garde cette
// recette » with a PDF; the scripted brain opens the bread's node and
// keeps the PDF with it (node_set, the attachment's id as its property
// recette). The page draws its page with the bundled pdf.js, under the
// workspace's own Content-Security-Policy, while eriphe-core still serves
// the file itself as a download; a link to download it stays.

// A real one-page PDF, 200×100 points: a black rectangle in its middle.
const pdf = Buffer.from(
  "JVBERi0xLjQKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCjIgMCBvYmoKPDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2JqCjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2UgL1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCAyMDAgMTAwXSAvQ29udGVudHMgNCAwIFIgPj4KZW5kb2JqCjQgMCBvYmoKPDwgL0xlbmd0aCAyNiA+PgpzdHJlYW0KMCAwIDAgcmcgMjAgMjAgMTYwIDYwIHJlIGYKZW5kc3RyZWFtCmVuZG9iagp4cmVmCjAgNQowMDAwMDAwMDAwIDY1NTM1IGYgCjAwMDAwMDAwMDkgMDAwMDAgbiAKMDAwMDAwMDA1OCAwMDAwMCBuIAowMDAwMDAwMTE1IDAwMDAwIG4gCjAwMDAwMDAyMDIgMDAwMDAgbiAKdHJhaWxlcgo8PCAvU2l6ZSA1IC9Sb290IDEgMCBSID4+CnN0YXJ0eHJlZgoyNzgKJSVFT0YK",
  "base64",
);

test("a PDF sent with a message is drawn in its node's page", async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => m.type() === "error" && problems.push(`console: ${m.text()}`));
  page.on("pageerror", (e) => problems.push(`page: ${e.message}`));
  await page.goto("/espace");
  const input = page.locator(".cs-bar input");
  await expect(input).toBeVisible();

  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Joindre un fichier" }).click();
  await (await chooser).setFiles([{ name: "pain.pdf", mimeType: "application/pdf", buffer: pdf }]);
  await expect(page.locator(".cs-file .cs-file-name")).toHaveText(["pain.pdf"]);
  await input.fill("Garde cette recette avec le pain.");
  await input.press("Enter");

  const pg = page.locator("article.pg").filter({ has: page.locator(".pg-title-tx", { hasText: "Pain de campagne" }) });
  const fig = pg.locator("figure.pg-pdf");
  await expect(fig).toHaveAttribute("data-state", "shown");
  const canvas = fig.locator("canvas.pg-pdf-page");
  await expect(canvas).toHaveCount(1);
  await expect(canvas).toHaveAttribute("data-drawn", "1");
  // drawn as the PDF says: its proportions, white paper, black in the middle
  const drawn = await canvas.evaluate((c: HTMLCanvasElement) => {
    const g = c.getContext("2d")!;
    const at = (x: number, y: number) => Array.from(g.getImageData(Math.floor(c.width * x), Math.floor(c.height * y), 1, 1).data.slice(0, 3));
    return { ratio: c.width / c.height, middle: at(0.5, 0.5), corner: at(0.02, 0.05) };
  });
  expect(drawn.ratio).toBeCloseTo(2, 1);
  expect(drawn.middle).toEqual([0, 0, 0]);
  expect(drawn.corner).toEqual([255, 255, 255]);

  const link = fig.locator("a.pg-file");
  await expect(link).toHaveText("recette · télécharger");
  await expect(link).toHaveAttribute("download", "recette.pdf");
  // eriphe-core still serves the file itself as a download, in a sandbox
  const res = await page.request.get((await link.getAttribute("href"))!);
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toBe("application/pdf");
  expect(res.headers()["content-disposition"]).toBe("attachment");
  expect(res.headers()["content-security-policy"]).toBe("default-src 'none'; sandbox");
  await expect(pg).not.toContainText("media:");
  expect(problems).toEqual([]);
});
