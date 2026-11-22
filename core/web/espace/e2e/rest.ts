import { expect, type Locator, type Page } from "@playwright/test";

// The one wait for what must be measured once the workspace stopped
// moving. The tests ask for less motion (playwright.config.ts), so the
// graph and the camera are at rest at once; the tests tagged @motion play
// with motion and wait here for real.

// rest waits until the graph is at rest and the camera still, as the
// viewport says (data-still); false when no graph is drawn (a phone's
// list).
export async function rest(page: Page): Promise<boolean> {
  if (!(await page.locator(".gr-viewport").count())) return false;
  await expect(page.locator(".gr-viewport[data-still]")).toHaveCount(1, { timeout: 60_000 });
  return true;
}

// still waits until el stopped moving and gives its box: in the graph,
// once it is at rest; on a phone's list, once its box stays. Null when el
// left meanwhile (a page fading out when it was found).
export async function still(el: Locator): Promise<{ x: number; y: number; width: number; height: number } | null> {
  if (await rest(el.page())) return el.boundingBox();
  let last = "";
  for (let i = 0; i < 60; i++) {
    if (!(await el.count())) return null;
    const b = await el.boundingBox({ timeout: 2_000 }).catch(() => null);
    const now = JSON.stringify(b && [Math.round(b.x), Math.round(b.y), Math.round(b.width), Math.round(b.height)]);
    if (b && now === last) return b;
    last = now;
    await el.page().waitForTimeout(150);
  }
  throw new Error("it does not settle");
}
