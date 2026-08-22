import { defineConfig } from "@playwright/test";

// ports mirror e2e/harness/env.ts; not importable before cwd guarantees hold
const webPort = Number(process.env.E2E_WEB_PORT ?? 3057);
const apiPort = Number(process.env.E2E_API_PORT ?? 8097);

export default defineConfig({
  testDir: "./e2e",
  // suite mutates shared backend state; specs run strictly in file order
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
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
    storageState: "./e2e/.artifacts/storage-state.json",
  },
  webServer: {
    command: `node -e "require('node:fs').rmSync('.next',{recursive:true,force:true})" && npm run dev -- -p ${webPort} -H 127.0.0.1`,
    url: `http://127.0.0.1:${webPort}`,
    reuseExistingServer: false,
    timeout: 120_000,
    env: { API_PROXY_TARGET: `http://127.0.0.1:${apiPort}` },
  },
});
