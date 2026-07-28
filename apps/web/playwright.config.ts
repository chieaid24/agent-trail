import { defineConfig } from "@playwright/test";

// Ports mirror e2e/harness/env.ts, which cannot be imported here: Playwright
// loads this config before cwd guarantees hold. Keep the defaults in sync.
const webPort = Number(process.env.E2E_WEB_PORT ?? 3057);
const apiPort = Number(process.env.E2E_API_PORT ?? 8097);

export default defineConfig({
  testDir: "./e2e",
  // The suite mutates shared backend state (stops the worker, restarts the
  // api), so specs run strictly in file order.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  // First hits compile routes in the dev server; absorb that latency.
  expect: { timeout: 15_000 },
  globalSetup: "./e2e/harness/global-setup.ts",
  globalTeardown: "./e2e/harness/global-teardown.ts",
  outputDir: "./e2e/.artifacts/test-results",
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "./e2e/.artifacts/report" }],
  ],
  use: {
    baseURL: `http://127.0.0.1:${webPort}`,
    viewport: { width: 1280, height: 800 },
    screenshot: "only-on-failure",
  },
  webServer: {
    command: `npm run dev -- -p ${webPort} -H 127.0.0.1`,
    url: `http://127.0.0.1:${webPort}`,
    reuseExistingServer: false,
    timeout: 120_000,
    env: { API_PROXY_TARGET: `http://127.0.0.1:${apiPort}` },
  },
});
