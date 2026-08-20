// Shared configuration for the e2e harness. Everything is overridable so
// parallel checkouts can run side by side; defaults avoid the common dev
// ports.

import fs from "node:fs";
import path from "node:path";

// Playwright runs with cwd = apps/web (the config directory; `make e2e`
// guarantees it). Fail loudly if invoked from anywhere else.
export const webDir = process.cwd();
if (!fs.existsSync(path.join(webDir, "playwright.config.ts"))) {
  throw new Error(
    `e2e harness must run from apps/web (cwd is ${webDir}); use make e2e`,
  );
}

export const repoRoot = path.resolve(webDir, "../..");
export const apiDir = path.join(repoRoot, "apps", "api");
export const artifactsDir = path.join(webDir, "e2e", ".artifacts");
export const binDir = path.join(artifactsDir, "bin");
export const stateFile = path.join(artifactsDir, "state.json");
export const screenshotsDir = path.join(webDir, "e2e", "screenshots");

export const E2E = {
  project: process.env.E2E_PROJECT ?? "agent-trail-e2e",
  postgresPort: Number(process.env.E2E_POSTGRES_PORT ?? 5457),
  apiPort: Number(process.env.E2E_API_PORT ?? 8097),
  webPort: Number(process.env.E2E_WEB_PORT ?? 3057),
  fakeGithubPort: Number(process.env.E2E_FAKE_GITHUB_PORT ?? 7057),
};

export const databaseUrl = `postgres://agent_trail:agent_trail@127.0.0.1:${E2E.postgresPort}/agent_trail?sslmode=disable`;
export const apiBaseUrl = `http://127.0.0.1:${E2E.apiPort}`;
export const storageStatePath = path.join(artifactsDir, "storage-state.json");

// The one session cookie the whole suite runs under; global-setup mints it
// through the fake GitHub OAuth flow.
export const sessionCookieName = "agent_trail_session";

export interface HarnessState {
  apiPid: number;
  workerPid: number;
  fakeGithubPid: number;
  apiBin: string;
  apiAddr: string;
  // Full daemon env, so a spec restarting the api keeps auth configured.
  apiEnv: Record<string, string>;
  databaseUrl: string;
  sessionCookie: string;
}

export function readState(): HarnessState {
  return JSON.parse(fs.readFileSync(stateFile, "utf8")) as HarnessState;
}

export function writeState(state: HarnessState): void {
  fs.mkdirSync(artifactsDir, { recursive: true });
  fs.writeFileSync(stateFile, JSON.stringify(state, null, 2));
}
