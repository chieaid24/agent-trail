import { expect, test } from "@playwright/test";
import { apiCreateTask, apiWaitForStatus } from "./harness/api";

// Live updates without refresh: a task created through the API appears on
// the open dashboard, and its detail page streams the execution as the
// fake worker drives it to completion.
test("dashboard and timeline update live while the worker executes", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Finished" })).toBeVisible();

  const created = await apiCreateTask(
    "E2E: live streaming task",
    "Demonstrate live streaming in the dashboard e2e suite.",
  );

  // No reload: the overview polls its way to the new row.
  await expect(page.getByText("E2E: live streaming task")).toBeVisible({
    timeout: 15_000,
  });

  // Navigate by URL: the row hops between groups as the worker races
  // through states, so clicking it is inherently flaky. Row navigation is
  // covered by 01-dashboard.
  await page.goto(`/tasks/${created.id}`);

  // The stream delivers the run as it happens and closes with done once
  // the fake worker finishes.
  await expect(page.getByText("plan created")).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByText("status: completed").first()).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByText("stream ended")).toBeVisible({
    timeout: 15_000,
  });

  await apiWaitForStatus(created.id, "completed", 30_000);
});
