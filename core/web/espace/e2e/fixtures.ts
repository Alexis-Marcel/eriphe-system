import { test as base, expect, type Request } from "@playwright/test";

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
  // when it connects, and the workspace would open it again. At its end,
  // the test's view still to be kept is kept before the next test begins:
  // data/keep.ts sends it a moment after it changes, else as the page
  // leaves, which would land after the next test's reset.
  fresh: [
    async ({ request, page }, use) => {
      expect((await request.post("/e2e/view")).ok()).toBe(true);
      await use();
      const kept = (r: Request) => r.method() === "PUT" && r.url().endsWith("/api/espace/view");
      for (;;) {
        const sent = await page.waitForRequest(kept, { timeout: 800 }).catch(() => null);
        if (!sent) break;
        await sent.response().catch(() => null);
      }
    },
    { auto: true },
  ],
});

export { expect };
