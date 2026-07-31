import { expect, test } from "@playwright/test";
import { apiTaskByTitle } from "./harness/api";
import { shootBothViewports } from "./harness/shots";

// The seeded conflict-demo pair: two active repository tasks with a stored
// overlap warning between them (cmd/seed). The warning must render on both
// task detail pages, each naming the other task.

const TASK_A = "Demo: extract the payment client";
const TASK_B = "Demo: add retries to the payment client";

test("task detail warns about the overlapping active task", async ({
  page,
}) => {
  const a = await apiTaskByTitle(TASK_A);
  await page.goto(`/tasks/${a.id}`);

  const warning = page.getByLabel("Conflict warnings");
  await expect(warning).toBeVisible();
  await expect(warning.getByText("Overlaps an active task")).toBeVisible();
  await expect(warning.getByRole("link", { name: TASK_B })).toBeVisible();
  await expect(
    warning.getByText("same files, adjacent lines, merge conflict"),
  ).toBeVisible();
  await expect(warning.getByText("internal/payments/client.go")).toBeVisible();

  await shootBothViewports(page, "07-conflict-warning");
});

test("the warning is symmetric: the sibling names this task", async ({
  page,
}) => {
  const a = await apiTaskByTitle(TASK_A);
  await page.goto(`/tasks/${a.id}`);

  // Follow the warning's link to the sibling and expect the mirror warning.
  await page
    .getByLabel("Conflict warnings")
    .getByRole("link", { name: TASK_B })
    .click();
  const warning = page.getByLabel("Conflict warnings");
  await expect(warning.getByRole("link", { name: TASK_A })).toBeVisible();
});

test("a task without stored conflicts shows no warning", async ({ page }) => {
  const clean = await apiTaskByTitle("Demo: fix flaky login test");
  await page.goto(`/tasks/${clean.id}`);

  await expect(page.getByText("Instructions")).toBeVisible();
  await expect(page.getByLabel("Conflict warnings")).toHaveCount(0);
});
