import { expect, test } from "@playwright/test";
import { apiCreateTask, apiWaitForStatus } from "./harness/api";

test("dashboard and timeline update live while the worker executes", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Finished" })).toBeVisible();

  const created = await apiCreateTask(
    "E2E: live streaming task",
    "Demonstrate live streaming in the dashboard e2e suite.",
  );

  await expect(page.getByText("E2E: live streaming task")).toBeVisible({
    timeout: 15_000,
  });

  // navigate by url; row hops groups as worker races, clicking it is flaky
  await page.goto(`/tasks/${created.id}`);

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
