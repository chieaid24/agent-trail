import { expect, test } from "@playwright/test";
import { apiTaskByTitle } from "./harness/api";
import { EXECUTED_TASK_TITLE } from "./harness/global-setup";
import { shoot, shootBothViewports } from "./harness/shots";

// The task the fake worker actually executed: full timeline, a trusted
// validation result, and an evidence report.
test("executed task shows timeline, logs, validations, evidence, files", async ({
  page,
}) => {
  const task = await apiTaskByTitle(EXECUTED_TASK_TITLE);
  await page.goto(`/tasks/${task.id}`);

  await expect(
    page.getByRole("heading", { name: EXECUTED_TASK_TITLE }),
  ).toBeVisible();
  await expect(page.getByText("completed", { exact: true })).toBeVisible();

  // Terminal task: the stream replays and closes with done.
  await expect(page.getByText("stream ended")).toBeVisible();

  // Timeline (newest first): terminal transition at the top, session
  // start near the bottom.
  await expect(page.getByText("status: completed").first()).toBeVisible();
  await expect(page.getByText("agent session started")).toBeVisible();
  await expect(page.getByText("plan created")).toBeVisible();
  await shootBothViewports(page, "task-detail-timeline");

  // Logs: rebuilt transcript with prompt, output, and exit code.
  await page.getByRole("tab", { name: /Logs/ }).click();
  await expect(page.getByText("$ echo fake agent validation")).toBeVisible();
  await expect(page.getByText("exit 0")).toBeVisible();
  await shoot(page, "task-detail-logs");

  // Log search narrows the transcript.
  await page.getByLabel("Search logs").fill("echo");
  await expect(page.getByText(/1 matching line/)).toBeVisible();
  await shoot(page, "task-detail-logs-search");
  await page.getByLabel("Search logs").clear();

  // Validations: the platform-executed smoke check is a verified fact.
  await page.getByRole("tab", { name: /Validations/ }).click();
  await expect(page.getByText("smoke")).toBeVisible();
  await expect(page.getByText("verified").first()).toBeVisible();
  await shoot(page, "task-detail-validations");

  // Evidence: provenance, trusted table, and the trust split.
  await page.getByRole("tab", { name: /Evidence/ }).click();
  await expect(page.getByText("Verified by Agent Trail")).toBeVisible();
  await shoot(page, "task-detail-evidence");

  // Files changed during the run.
  await page.getByRole("tab", { name: /Files/ }).click();
  await expect(page.getByText("AGENT_NOTES.md", { exact: true })).toBeVisible();
  await shoot(page, "task-detail-files");
});

test("failed task surfaces its failure loudly", async ({ page }) => {
  const task = await apiTaskByTitle("Demo: upgrade the TLS library");
  await page.goto(`/tasks/${task.id}`);

  await expect(page.getByText("failed", { exact: true })).toBeVisible();
  // The seeded failure carries a code and an actionable message.
  await expect(
    page.getByText(/2 of 148 tests failed after the upgrade/),
  ).toBeVisible();
  await expect(page.getByText("validation_failed")).toBeVisible();
  await shoot(page, "task-detail-failed");
});
