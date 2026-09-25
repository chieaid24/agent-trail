import { expect, test } from "@playwright/test";
import { apiCreateTask } from "./harness/api";
import { readState, writeState } from "./harness/env";
import { shoot } from "./harness/shots";
import { stopProcess } from "./harness/procs";

// worker stopped so the task stays queued; later specs run workerless on purpose
test("a queued task cancels after inline confirmation", async ({ page }) => {
  const state = readState();
  await stopProcess(state.workerPid);
  writeState({ ...state, workerPid: 0 });

  const created = await apiCreateTask(
    "E2E: cancel me",
    "Stay queued so the cancellation flow can be exercised.",
  );
  await page.goto(`/tasks/${created.id}`);
  await expect(
    page.locator("article header").getByText("queued", { exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Cancel task" }).click();
  await expect(page.getByText("Cancel this task?")).toBeVisible();
  await shoot(page, "task-detail-cancel-confirm");

  await page.getByRole("button", { name: "Keep running" }).click();
  await expect(page.getByRole("button", { name: "Cancel task" })).toBeVisible();

  await page.getByRole("button", { name: "Cancel task" }).click();
  await page.getByLabel("Cancellation reason").fill("e2e cancellation drill");
  await page.getByRole("button", { name: "Confirm cancel" }).click();

  await expect(
    page.locator("article header").getByText("cancelled", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel task" })).toHaveCount(
    0,
  );
  await expect(page.getByText("status: cancelled")).toBeVisible({
    timeout: 15_000,
  });
  await expect(
    page.locator("article > header").getByText("e2e cancellation drill"),
  ).toBeVisible();
  await expect(
    page.getByRole("list").getByText("e2e cancellation drill"),
  ).toBeVisible();
  await shoot(page, "task-detail-cancelled");
});
