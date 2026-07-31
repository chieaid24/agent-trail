import { expect, test } from "@playwright/test";
import { EXECUTED_TASK_TITLE } from "./harness/global-setup";
import { shootBothViewports } from "./harness/shots";

test("overview lists seeded tasks with grouped state and summary", async ({
  page,
}) => {
  await page.goto("/");

  await expect(page.getByRole("region", { name: "Finished" })).toBeVisible();
  await expect(page.getByText(EXECUTED_TASK_TITLE)).toBeVisible();
  await expect(page.getByText("Demo: upgrade the TLS library")).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Runner health" }),
  ).toContainText("online");
  await expect(
    page.getByRole("region", { name: "Recent repositories" }),
  ).toContainText("chieaid24/agent-trail");

  // The summary strip is computed from the visible tasks.
  await expect(page.getByText(/\d+ tasks/)).toBeVisible();
  await expect(page.getByText(/% completed/)).toBeVisible();

  await shootBothViewports(page, "dashboard-overview");
});

test("a task row navigates to its detail page", async ({ page }) => {
  await page.goto("/");
  await page
    .getByRole("link", { name: new RegExp(EXECUTED_TASK_TITLE) })
    .click();
  await expect(
    page.getByRole("heading", { name: EXECUTED_TASK_TITLE }),
  ).toBeVisible();
  await expect(page.getByRole("tab", { name: /Timeline/ })).toBeVisible();
});
