import { defineConfig, devices } from "@playwright/test";

// The epic's scenario, played in a browser (#633): against a test
// eriphe-core with faked tools and a scripted brain (TestEspaceScenario,
// core/espace_scenario_test.go), on 127.0.0.1 only. Never against a real
// eriphe-core. It serves the bundle as built in ../static/espace: build it
// first (npm run build), as CI does.
// Another port when one is taken (another checkout playing it): ERIPHE_E2E_ADDR.
const addr = process.env.ERIPHE_E2E_ADDR ?? "127.0.0.1:8897";

export default defineConfig({
  testDir: "e2e",
  // The scenario walks some thirty steps in one test: under half a minute
  // without motion, more on a shared CI runner.
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never", outputFolder: "e2e-report" }]] : "list",
  outputDir: "e2e-results",
  use: { baseURL: `http://${addr}`, locale: "fr-FR", colorScheme: "light", trace: "retain-on-failure", ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } },
  // The tests check what the workspace does, not how it moves: they ask
  // for less motion, so the graph and the camera are at rest at once (the
  // layout's and the camera's geometry are unit tests). The few tagged
  // @motion play with motion, after the others: what most users see is
  // still played in a browser.
  projects: [
    { name: "still", grepInvert: /@motion/, use: { reducedMotion: "reduce" } },
    { name: "motion", grep: /@motion/, use: { reducedMotion: "no-preference" }, dependencies: ["still"] },
  ],
  webServer: {
    command: "go test -count=1 -run '^TestEspaceScenario$' -timeout 0 .",
    cwd: "../..",
    env: { ERIPHE_E2E_ADDR: addr },
    url: `http://${addr}/login`,
    timeout: 300_000,
    reuseExistingServer: false,
    stdout: "pipe",
    stderr: "pipe",
  },
});
