import { expect, test } from "@playwright/test";
import { apiBaseUrl, readState, writeState } from "./harness/env";
import { apiCreateTask } from "./harness/api";
import { shoot } from "./harness/shots";
import { spawnDaemon, stopProcess, waitFor } from "./harness/procs";

// The Milestone 8 acceptance criterion: SSE reconnects correctly after a
// dropped connection. The drop is real - the api process is stopped and
// restarted under the open page - and recovery must show no duplicate and
// no missing timeline rows (the client resumes from Last-Event-ID).
test("the timeline stream survives an api restart", async ({ page }) => {
  const created = await apiCreateTask(
    "E2E: reconnect probe",
    "Hold a live stream open while the api restarts underneath it.",
  );
  await page.goto(`/tasks/${created.id}`);

  // Queued task, no worker: the stream stays open and live.
  await expect(page.getByText("live", { exact: true })).toBeVisible();
  await expect(page.getByText("task created")).toBeVisible();

  const state = readState();
  await stopProcess(state.apiPid);

  await expect(page.getByText("reconnecting")).toBeVisible({
    timeout: 15_000,
  });
  await shoot(page, "task-detail-reconnecting");

  const api = spawnDaemon(state.apiBin, "api.log", {
    API_ADDR: state.apiAddr,
    DATABASE_URL: state.databaseUrl,
  });
  writeState({ ...state, apiPid: api.pid ?? 0 });
  await waitFor("api readiness after restart", 30_000, async () => {
    const res = await fetch(`${apiBaseUrl}/readyz`).catch(() => null);
    return res?.ok ?? false;
  });

  // EventSource reconnects on its own and the server replays from the
  // cursor: back to live, still exactly one creation row.
  await expect(page.getByText("live", { exact: true })).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByText("task created")).toHaveCount(1);
});
