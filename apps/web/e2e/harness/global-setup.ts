// Boots the full stack for the e2e suite: a dedicated postgres (compose
// project namespaced away from dev infra), migrations, seed data, and the
// api + fake-adapter worker as real processes. The worker executes the
// seeded queued task, so specs run against a genuinely executed timeline
// with trusted validation results and an evidence report.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import {
  apiBaseUrl,
  apiDir,
  artifactsDir,
  binDir,
  databaseUrl,
  E2E,
  repoRoot,
  screenshotsDir,
  sessionCookieName,
  storageStatePath,
  webDir,
  writeState,
} from "./env";
import { spawnDaemon, waitFor } from "./procs";

// The seeded task the worker picks up and drives to completion.
export const EXECUTED_TASK_TITLE = "Add pagination to the audit log";

function compose(...args: string[]): void {
  execFileSync("docker", ["compose", "-p", E2E.project, ...args], {
    cwd: repoRoot,
    env: { ...process.env, POSTGRES_PORT: String(E2E.postgresPort) },
    stdio: "inherit",
  });
}

export default async function globalSetup(): Promise<void> {
  fs.rmSync(artifactsDir, { recursive: true, force: true });
  fs.mkdirSync(binDir, { recursive: true });
  fs.mkdirSync(screenshotsDir, { recursive: true });

  // Fresh database every run; the seed refuses a non-empty one.
  compose("down", "-v", "--remove-orphans");
  compose("up", "-d", "--wait", "postgres");

  for (const cmd of ["api", "worker", "migrate", "seed"]) {
    execFileSync(
      "go",
      ["build", "-o", path.join(binDir, cmd), `./cmd/${cmd}`],
      {
        cwd: apiDir,
        stdio: "inherit",
      },
    );
  }

  const dbEnv = { ...process.env, DATABASE_URL: databaseUrl };
  execFileSync(path.join(binDir, "migrate"), ["up"], { env: dbEnv });
  execFileSync(path.join(binDir, "seed"), [], { env: dbEnv });

  // The suite runs with the session layer on: the api's GitHub base URLs
  // point at the local fake, and every spec carries the session minted below.
  const fakeGithub = spawnDaemon(process.execPath, "fake-github.log", {}, [
    path.join(webDir, "e2e", "harness", "fake-github.mjs"),
    String(E2E.fakeGithubPort),
  ]);
  const fakeGithubUrl = `http://127.0.0.1:${E2E.fakeGithubPort}`;

  const apiBin = path.join(binDir, "api");
  const apiAddr = `127.0.0.1:${E2E.apiPort}`;
  const apiEnv = {
    API_ADDR: apiAddr,
    DATABASE_URL: databaseUrl,
    GITHUB_OAUTH_CLIENT_ID: "e2e-client-id",
    GITHUB_OAUTH_CLIENT_SECRET: "e2e-client-secret",
    GITHUB_OAUTH_BASE_URL: fakeGithubUrl,
    GITHUB_API_BASE_URL: fakeGithubUrl,
    GITHUB_APP_SLUG: "agent-trail-e2e",
    AUTH_PUBLIC_ORIGIN: `http://127.0.0.1:${E2E.webPort}`,
  };
  const api = spawnDaemon(apiBin, "api.log", apiEnv);
  const worker = spawnDaemon(path.join(binDir, "worker"), "worker.log", {
    DATABASE_URL: databaseUrl,
  });

  await waitFor("api readiness", 30_000, async () => {
    const res = await fetch(`${apiBaseUrl}/readyz`).catch(() => null);
    return res?.ok ?? false;
  });

  const sessionCookie = await loginThroughFakeGitHub();
  writeStorageState(sessionCookie);
  writeState({
    apiPid: api.pid ?? 0,
    workerPid: worker.pid ?? 0,
    fakeGithubPid: fakeGithub.pid ?? 0,
    apiBin,
    apiAddr,
    apiEnv,
    databaseUrl,
    sessionCookie,
  });

  // The fake adapter finishes in seconds. The worker takes the seeded
  // queued task and also recovers the seeded mid-flight one, so wait until
  // Awaiting-review seed tasks are settled and remain unclaimed. The tasks
  // read authenticates like every spec.
  const settled = new Set([
    "completed",
    "failed",
    "cancelled",
    "timed_out",
    "awaiting_review",
  ]);
  await waitFor("worker drives seeded tasks to rest", 60_000, async () => {
    const res = await fetch(`${apiBaseUrl}/api/v1/tasks`, {
      headers: { cookie: `${sessionCookieName}=${sessionCookie}` },
    }).catch(() => null);
    if (!res?.ok) return false;
    const body = (await res.json()) as {
      tasks: { title: string; status: string }[];
    };
    return (
      body.tasks.some(
        (t) => t.title === EXECUTED_TASK_TITLE && t.status === "completed",
      ) && body.tasks.every((t) => settled.has(t.status))
    );
  });
}

// Drives the OAuth flow against the api and the fake GitHub with plain
// fetches (the web server is not up yet), returning the session token.
// The callback is called on the api directly rather than through the
// /backend proxy the browser would use; the api never checks which host
// carried the request, only the state cookie and the code.
async function loginThroughFakeGitHub(): Promise<string> {
  const start = await fetch(`${apiBaseUrl}/auth/github/start`, {
    redirect: "manual",
  });
  const stateCookie = readCookie(start, "agent_trail_oauth_state");
  const authorizeUrl = start.headers.get("location");
  if (stateCookie === null || authorizeUrl === null) {
    throw new Error("auth start returned no state cookie or location");
  }

  const authorize = await fetch(authorizeUrl, { redirect: "manual" });
  const callbackUrl = authorize.headers.get("location");
  if (callbackUrl === null) {
    throw new Error("fake github returned no callback redirect");
  }
  const params = new URL(callbackUrl).searchParams;

  const callback = await fetch(
    `${apiBaseUrl}/auth/github/callback?` +
      new URLSearchParams({
        code: params.get("code") ?? "",
        state: params.get("state") ?? "",
      }).toString(),
    {
      redirect: "manual",
      headers: { cookie: `agent_trail_oauth_state=${stateCookie}` },
    },
  );
  const session = readCookie(callback, sessionCookieName);
  if (session === null || session === "") {
    throw new Error(
      `callback set no session cookie (status ${callback.status}, ` +
        `location ${callback.headers.get("location")})`,
    );
  }
  return session;
}

function readCookie(res: Response, name: string): string | null {
  for (const header of res.headers.getSetCookie()) {
    const [pair] = header.split(";");
    const eq = pair.indexOf("=");
    if (pair.slice(0, eq) === name) return pair.slice(eq + 1);
  }
  return null;
}

// Playwright storage state: every browser context starts signed in; the
// signed-out specs opt out with an empty storageState.
function writeStorageState(sessionCookie: string): void {
  fs.writeFileSync(
    storageStatePath,
    JSON.stringify({
      cookies: [
        {
          name: sessionCookieName,
          value: sessionCookie,
          domain: "127.0.0.1",
          path: "/",
          expires: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
          httpOnly: true,
          secure: false,
          sameSite: "Lax",
        },
      ],
      origins: [],
    }),
  );
}
