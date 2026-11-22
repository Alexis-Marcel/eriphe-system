import { test as base, expect } from "@playwright/test";

// What every test starts from, against the one test core of a run
// (TestEspaceScenario): signed in, and the workspace as the user would
// find it with nothing left by the test before, no page open and nothing
// left to show. Each test stands alone: it passes run by itself.

const password = "correct horse battery"; // testPassword, core/web_test.go

type Session = Awaited<ReturnType<import("@playwright/test").BrowserContext["storageState"]>>;

export const test = base.extend<{ fresh: void }, { session: Session }>({
  // Signed in once for the worker, with the tests' password.
  session: [
    async ({ browser }, use, info) => {
      const ctx = await browser.newContext({ baseURL: info.project.use.baseURL });
      const page = await ctx.newPage();
      await page.goto("/login");
      await page.locator('input[name="password"]').fill(password);
      await page.locator('button[type="submit"]').click();
      await page.waitForURL((u) => u.pathname !== "/login");
      await use(await ctx.storageState());
      await ctx.close();
    },
    { scope: "worker" },
  ],
  storageState: ({ session }, use) => use(session),
  // No page open, and the last page shown seen already: the stream says it
  // when it connects, and the workspace would open it again.
  fresh: [
    async ({ request }, use) => {
      expect((await request.post("/e2e/view")).ok()).toBe(true);
      await use();
    },
    { auto: true },
  ],
});

export { expect };
