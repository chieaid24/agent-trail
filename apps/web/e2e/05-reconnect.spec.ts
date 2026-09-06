import { expect, test } from "@playwright/test";
import { apiBaseUrl, readState, writeState } from "./harness/env";
import { apiCreateTask } from "./harness/api";
import { shoot } from "./harness/shots";
import { spawnDaemon, stopProcess, waitFor } from "./harness/procs";

test("the timeline stream survives an api restart", async ({ page }) => {
  const created = await apiCreateTask(
    "E2E: reconnect probe",
    "Hold a live stream open while the api restarts underneath it.",
  );
  await page.goto(`/tasks/${created.id}`);

  await expect(page.getByText("live", { exact: true })).toBeVisible();
  await expect(page.getByText("task created")).toBeVisible();

  const state = readState();
  await stopProcess(state.apiPid);

  await expect(page.getByText("reconnecting")).toBeVisible({
    timeout: 15_000,
  });
  await shoot(page, "task-detail-reconnecting");

  // recorded env keeps the session layer configured across the restart
  const api = spawnDaemon(state.apiBin, "api.log", state.apiEnv);
  writeState({ ...state, apiPid: api.pid ?? 0 });
  await waitFor("api readiness after restart", 30_000, async () => {
    const res = await fetch(`${apiBaseUrl}/readyz`).catch(() => null);
    return res?.ok ?? false;
  });

  await expect(page.getByText("live", { exact: true })).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByText("task created")).toHaveCount(1);
});
