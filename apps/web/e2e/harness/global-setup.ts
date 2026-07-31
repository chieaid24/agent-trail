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
  writeState,
} from "./env";
import { spawnDaemon, waitFor } from "./procs";

// The seeded task the worker picks up and drives to completion.
export const EXECUTED_TASK_TITLE = "Demo: add pagination to the audit log";

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

  const apiBin = path.join(binDir, "api");
  const apiAddr = `127.0.0.1:${E2E.apiPort}`;
  const api = spawnDaemon(apiBin, "api.log", {
    API_ADDR: apiAddr,
    DATABASE_URL: databaseUrl,
  });
  const worker = spawnDaemon(path.join(binDir, "worker"), "worker.log", {
    DATABASE_URL: databaseUrl,
  });
  writeState({
    apiPid: api.pid ?? 0,
    workerPid: worker.pid ?? 0,
    apiBin,
    apiAddr,
    databaseUrl,
  });

  await waitFor("api readiness", 30_000, async () => {
    const res = await fetch(`${apiBaseUrl}/readyz`).catch(() => null);
    return res?.ok ?? false;
  });

  // The fake adapter finishes in seconds. The worker takes the seeded
  // queued task and also recovers the seeded mid-flight one, so wait until
  // every seeded task is settled and specs see stable timelines.
  // awaiting_review counts as settled: it is a resting state the runner
  // never claims, where the seeded conflict-demo pair deliberately stays.
  const settled = new Set([
    "completed",
    "failed",
    "cancelled",
    "timed_out",
    "awaiting_review",
  ]);
  await waitFor("worker drives seeded tasks to rest", 60_000, async () => {
    const res = await fetch(`${apiBaseUrl}/api/v1/tasks`).catch(() => null);
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
