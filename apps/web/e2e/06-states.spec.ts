import { expect, test, type Page } from "@playwright/test";
import { shoot, shootBothViewports } from "./harness/shots";

// UI states that live data cannot hold still (empty, loading, error, a
// full-spectrum grouped board) are pinned with route mocks. Everything
// else in this suite runs against the real stack.

function wireTask(overrides: Record<string, unknown>): Record<string, unknown> {
  return {
    id: "3b241101-e2bb-4255-8caf-4136c566a900",
    organization_id: null,
    repository_id: null,
    source_type: "api",
    source_issue_number: null,
    source_comment_id: null,
    title: "Task",
    instructions: "",
    status: "queued",
    phase: "pending",
    priority: 0,
    base_branch: "main",
    base_commit_sha: null,
    working_branch: null,
    agent_provider: null,
    agent_model: null,
    policy_id: null,
    requested_by_user_id: null,
    max_runtime_seconds: null,
    max_cost_usd: null,
    started_at: null,
    completed_at: null,
    cancel_requested_at: null,
    failure_code: null,
    failure_message: null,
    created_at: "2026-07-28T12:00:00Z",
    updated_at: "2026-07-28T12:00:00Z",
    version: 1,
    ...overrides,
  };
}

async function mockTaskList(page: Page, tasks: unknown[]): Promise<void> {
  await page.route("**/backend/api/v1/tasks*", (route) =>
    route.fulfill({ json: { tasks } }),
  );
}

test("empty state", async ({ page }) => {
  await mockTaskList(page, []);
  await page.goto("/");
  await expect(page.getByText("No tasks yet.", { exact: false })).toBeVisible();
  await shootBothViewports(page, "dashboard-empty");
});

test("error state", async ({ page }) => {
  await page.route("**/backend/api/v1/tasks*", (route) => route.abort());
  await page.goto("/");
  await expect(page.getByText(/Could not load tasks/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();
  await shoot(page, "dashboard-error");
});

test("loading skeleton", async ({ page }) => {
  await page.route("**/backend/api/v1/tasks*", async (route) => {
    await new Promise((r) => setTimeout(r, 3_000));
    await route.fulfill({ json: { tasks: [] } });
  });
  await page.goto("/");
  // The skeleton owns the screen while the request is in flight.
  await shoot(page, "dashboard-loading");
  await expect(page.getByText("No tasks yet.", { exact: false })).toBeVisible();
});

test("full-spectrum grouped board", async ({ page }) => {
  await mockTaskList(page, [
    wireTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a901",
      title: "Fix the flaky login integration test",
      status: "executing",
      phase: "running",
      agent_provider: "claude-code",
      agent_model: "claude-sonnet-5",
      source_issue_number: 118,
      started_at: "2026-07-28T11:58:20Z",
    }),
    wireTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a902",
      title: "Add cursor pagination to the audit log endpoint",
      status: "validating",
      phase: "running",
      agent_provider: "claude-code",
      agent_model: "claude-sonnet-5",
      source_issue_number: 121,
      started_at: "2026-07-28T11:55:00Z",
    }),
    wireTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a903",
      title: "Upgrade the TLS dependency",
      status: "awaiting_review",
      phase: "review",
      agent_provider: "claude-code",
      agent_model: "claude-sonnet-5",
      source_issue_number: 116,
      started_at: "2026-07-28T11:20:00Z",
    }),
    wireTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a904",
      title: "Rename the billing module to invoicing",
      status: "queued",
      priority: 10,
      source_issue_number: 124,
    }),
    wireTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a905",
      title: "Backfill missing webhook deliveries",
      status: "failed",
      phase: "terminal",
      source_issue_number: 112,
      started_at: "2026-07-28T10:00:00Z",
      completed_at: "2026-07-28T10:04:12Z",
      failure_code: "validation_failed",
      failure_message: "2 of 148 tests failed",
    }),
    wireTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a906",
      title: "Document the evidence report schema",
      status: "completed",
      phase: "terminal",
      source_issue_number: 109,
      started_at: "2026-07-28T09:30:00Z",
      completed_at: "2026-07-28T09:33:40Z",
    }),
  ]);
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Running" })).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Awaiting review" }),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "Queued" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Finished" })).toBeVisible();
  await shootBothViewports(page, "dashboard-grouped");
});
