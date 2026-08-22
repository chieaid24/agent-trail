import { expect, test } from "@playwright/test";
import { apiTaskByTitle } from "./harness/api";
import { EXECUTED_TASK_TITLE } from "./harness/global-setup";
import { shoot, shootBothViewports } from "./harness/shots";

test("executed task shows timeline, logs, validations, evidence, files", async ({
  page,
}) => {
  const task = await apiTaskByTitle(EXECUTED_TASK_TITLE);
  await page.goto(`/tasks/${task.id}`);

  await expect(
    page.getByRole("heading", { name: EXECUTED_TASK_TITLE }),
  ).toBeVisible();
  await expect(page.getByText("completed", { exact: true })).toBeVisible();

  await expect(page.getByText("stream ended")).toBeVisible();

  await expect(page.getByText("status: completed").first()).toBeVisible();
  await expect(page.getByText("agent session started")).toBeVisible();
  await expect(page.getByText("plan created")).toBeVisible();
  await shootBothViewports(page, "task-detail-timeline");

  await page.getByRole("tab", { name: /Logs/ }).click();
  await expect(page.getByText("$ echo fake agent validation")).toBeVisible();
  await expect(page.getByText("exit 0")).toBeVisible();
  await shoot(page, "task-detail-logs");

  await page.getByLabel("Search logs").fill("echo");
  await expect(page.getByText(/1 matching line/)).toBeVisible();
  await shoot(page, "task-detail-logs-search");
  await page.getByLabel("Search logs").clear();

  await page.getByRole("tab", { name: /Validations/ }).click();
  await expect(page.getByText("smoke")).toBeVisible();
  await expect(page.getByText("verified").first()).toBeVisible();
  await shoot(page, "task-detail-validations");

  await page.getByRole("tab", { name: /Evidence/ }).click();
  await expect(page.getByText("Verified by Agent Trail")).toBeVisible();
  await shoot(page, "task-detail-evidence");

  await page.getByRole("tab", { name: /Files/ }).click();
  await expect(page.getByText("AGENT_NOTES.md", { exact: true })).toBeVisible();
  await shoot(page, "task-detail-files");
});

test("failed task surfaces its failure loudly", async ({ page }) => {
  const task = await apiTaskByTitle("Upgrade the TLS library");
  await page.goto(`/tasks/${task.id}`);

  await expect(page.getByText("failed", { exact: true })).toBeVisible();
  await expect(
    page.getByText(/2 of 148 tests failed after the upgrade/),
  ).toBeVisible();
  await expect(page.getByText("validation_failed")).toBeVisible();
  await shoot(page, "task-detail-failed");
});
