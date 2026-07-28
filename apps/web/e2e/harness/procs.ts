// Process helpers for the harness: daemons logging into the artifacts dir,
// polling waits, and the restart used by the reconnection spec.

import { spawn, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { artifactsDir } from "./env";

export function spawnDaemon(
  bin: string,
  logName: string,
  env: Record<string, string>,
): ChildProcess {
  const log = fs.openSync(path.join(artifactsDir, logName), "a");
  const child = spawn(bin, [], {
    env: { ...process.env, ...env },
    stdio: ["ignore", log, log],
    detached: false,
  });
  child.unref();
  return child;
}

export async function waitFor(
  what: string,
  timeoutMs: number,
  check: () => Promise<boolean> | boolean,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await check()) return;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`timed out after ${timeoutMs}ms waiting for ${what}`);
}

export function processAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

// SIGTERM and wait for exit; the pid belongs to this harness alone. A
// process that ignores the term deadline is force-killed so one hung
// daemon cannot wedge the suite.
export async function stopProcess(pid: number): Promise<void> {
  if (!processAlive(pid)) return;
  try {
    process.kill(pid, "SIGTERM");
  } catch {
    return;
  }
  try {
    await waitFor(`pid ${pid} exit`, 10_000, () => !processAlive(pid));
  } catch {
    process.kill(pid, "SIGKILL");
    await waitFor(`pid ${pid} force exit`, 5_000, () => !processAlive(pid));
  }
}
