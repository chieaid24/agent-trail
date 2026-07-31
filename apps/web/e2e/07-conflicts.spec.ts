import { expect, test } from "@playwright/test";
import { apiTaskByTitle } from "./harness/api";
import { shootBothViewports } from "./harness/shots";

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
  await shootBothViewports(page, "07-conflict-empty");
});

test("conflict warning loading state", async ({ page }) => {
  const a = await apiTaskByTitle(TASK_A);
  await page.route("**/backend/api/v1/tasks/*/conflicts", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 3_000));
    await route.fulfill({ json: { conflicts: [] } });
  });
  await page.goto(`/tasks/${a.id}`);

  await expect(page.getByText("Checking task overlaps...")).toBeVisible();
  await shootBothViewports(page, "07-conflict-loading");
  await expect(page.getByText("Checking task overlaps...")).toHaveCount(0);
});

test("conflict warning error state", async ({ page }) => {
  const a = await apiTaskByTitle(TASK_A);
  await page.route("**/backend/api/v1/tasks/*/conflicts", (route) =>
    route.abort(),
  );
  await page.goto(`/tasks/${a.id}`);

  await expect(page.getByText("Conflict warnings unavailable.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();
  await shootBothViewports(page, "07-conflict-error");
});

test("conflict warning handles long content", async ({ page }) => {
  const a = await apiTaskByTitle(TASK_A);
  await page.route("**/backend/api/v1/tasks/*/conflicts", (route) =>
    route.fulfill({
      json: {
        conflicts: [
          {
            id: "3b241101-e2bb-4255-8caf-4136c566a970",
            other_task_id: "3b241101-e2bb-4255-8caf-4136c566a971",
            other_task_title:
              "Replace the authentication gateway while preserving every legacy integration contract and regional rollout safeguard",
            kinds: ["file_overlap", "adjacent_lines", "merge_conflict"],
            files: [
              "apps/api/internal/authentication/legacy-integrations/regional-rollouts/extremely-long-policy-filename.go",
            ],
            detected_at: "2026-07-31T12:00:00Z",
            updated_at: "2026-07-31T12:00:00Z",
          },
        ],
      },
    }),
  );
  await page.goto(`/tasks/${a.id}`);

  const warning = page.getByLabel("Conflict warnings");
  await expect(warning).toBeVisible();
  expect(
    await warning.evaluate(
      (element) => element.scrollWidth <= element.clientWidth,
    ),
  ).toBe(true);
  await shootBothViewports(page, "07-conflict-long-content");
});
