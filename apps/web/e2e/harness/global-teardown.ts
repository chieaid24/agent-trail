import { execFileSync } from "node:child_process";
import fs from "node:fs";
import { E2E, repoRoot, stateFile } from "./env";
import { stopProcess } from "./procs";

export default async function globalTeardown(): Promise<void> {
  if (fs.existsSync(stateFile)) {
    const state = JSON.parse(fs.readFileSync(stateFile, "utf8")) as {
      apiPid?: number;
      workerPid?: number;
    };
    // Only pids this harness spawned and recorded; never kill by name.
    if (state.apiPid) await stopProcess(state.apiPid);
    if (state.workerPid) await stopProcess(state.workerPid);
  }
  execFileSync(
    "docker",
    ["compose", "-p", E2E.project, "down", "-v", "--remove-orphans"],
    {
      cwd: repoRoot,
      env: { ...process.env, POSTGRES_PORT: String(E2E.postgresPort) },
      stdio: "inherit",
    },
  );
}
