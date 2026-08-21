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

const traceTaskId = "3b241101-e2bb-4255-8caf-4136c566a920";

async function mockTraceTask(page: Page): Promise<void> {
  const task = wireTask({
    id: traceTaskId,
    title: "Trace the release workflow",
    status: "completed",
    phase: "terminal",
    agent_provider: "claude-code",
    agent_model: "claude-sonnet-5",
    started_at: "2026-08-21T12:00:00Z",
    completed_at: "2026-08-21T12:00:12Z",
  });
  await page.route(`**/backend/api/v1/tasks/${traceTaskId}`, (route) =>
    route.fulfill({ json: task }),
  );
  await page.route(
    `**/backend/api/v1/tasks/${traceTaskId}/validations`,
    (route) => route.fulfill({ json: { validations: [] } }),
  );
  await page.route(
    `**/backend/api/v1/tasks/${traceTaskId}/conflicts`,
    (route) => route.fulfill({ json: { conflicts: [] } }),
  );
  await page.route(`**/backend/api/v1/tasks/${traceTaskId}/evidence`, (route) =>
    route.fulfill({ status: 404, json: { error: "not found" } }),
  );
  await page.route(`**/backend/api/v1/tasks/${traceTaskId}/stream*`, (route) =>
    route.fulfill({
      contentType: "text/event-stream",
      body: [
        "id: 1:1",
        `data: ${JSON.stringify({
          id: "3b241101-e2bb-4255-8caf-4136c566a921",
          task_attempt_id: "3b241101-e2bb-4255-8caf-4136c566a922",
          attempt_number: 1,
          sequence_number: 1,
          event_type: "agent.cost_update",
          source: "agent",
          timestamp: "2026-08-21T12:00:10Z",
          payload: { total_cost_usd: 0.0425 },
          redaction_status: "none",
          created_at: "2026-08-21T12:00:10Z",
        })}`,
        "",
        "event: done",
        'data: {"status":"completed"}',
        "",
      ].join("\n"),
    }),
  );
}

test("trace loading state", async ({ page }) => {
  await mockTraceTask(page);
  await page.route(
    `**/backend/api/v1/tasks/${traceTaskId}/trace`,
    async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 3_000));
      await route.fulfill({ json: { spans: [] } });
    },
  );
  await page.goto(`/tasks/${traceTaskId}`);
  await page.getByRole("tab", { name: "Trace" }).click();
  await expect(page.getByLabel("Loading trace")).toBeVisible();
  await shoot(page, "task-trace-loading");
});

test("trace empty state", async ({ page }) => {
  await mockTraceTask(page);
  await page.route(`**/backend/api/v1/tasks/${traceTaskId}/trace`, (route) =>
    route.fulfill({ json: { spans: [] } }),
  );
  await page.goto(`/tasks/${traceTaskId}`);
  await page.getByRole("tab", { name: "Trace" }).click();
  await expect(page.getByText("No spans recorded yet")).toBeVisible();
  await shoot(page, "task-trace-empty");
});

test("trace waterfall and run cost", async ({ page }) => {
  await mockTraceTask(page);
  await page.route(`**/backend/api/v1/tasks/${traceTaskId}/trace`, (route) =>
    route.fulfill({
      json: {
        spans: [
          {
            trace_id: "0123456789abcdef0123456789abcdef",
            span_id: "0123456789abcdef",
            parent_span_id: null,
            task_attempt_id: "3b241101-e2bb-4255-8caf-4136c566a922",
            name: "runner.attempt",
            kind: "internal",
            start_time: "2026-08-21T12:00:00Z",
            end_time: "2026-08-21T12:00:12Z",
            attributes: {},
            status_code: "Ok",
            status_message: "",
          },
          {
            trace_id: "0123456789abcdef0123456789abcdef",
            span_id: "fedcba9876543210",
            parent_span_id: "0123456789abcdef",
            task_attempt_id: "3b241101-e2bb-4255-8caf-4136c566a922",
            name: "agent.session",
            kind: "internal",
            start_time: "2026-08-21T12:00:02Z",
            end_time: "2026-08-21T12:00:09Z",
            attributes: {},
            status_code: "Ok",
            status_message: "",
          },
          {
            trace_id: "0123456789abcdef0123456789abcdef",
            span_id: "aaaaaaaaaaaaaaaa",
            parent_span_id: "0123456789abcdef",
            task_attempt_id: "3b241101-e2bb-4255-8caf-4136c566a922",
            name: "validation.run",
            kind: "internal",
            start_time: "2026-08-21T12:00:09Z",
            end_time: "2026-08-21T12:00:11Z",
            attributes: {},
            status_code: "Ok",
            status_message: "",
          },
        ],
      },
    }),
  );
  await page.goto(`/tasks/${traceTaskId}`);
  await expect(page.getByText("$0.0425", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Cost breakdown")).toContainText(
    "attempt 1: $0.0425",
  );
  await page.getByRole("tab", { name: /Trace/ }).click();
  await expect(
    page.getByRole("list", { name: "Trace waterfall" }),
  ).toBeVisible();
  await expect(page.getByText("agent.session")).toBeVisible();
  await shootBothViewports(page, "task-trace-waterfall");
});

test("empty state", async ({ page }) => {
  await mockTaskList(page, []);
  await page.route("**/backend/api/v1/runners", (route) =>
    route.fulfill({ json: { runners: [] } }),
  );
  await page.route("**/backend/api/v1/repositories*", (route) =>
    route.fulfill({ json: { repositories: [] } }),
  );
  await page.goto("/");
  await expect(page.getByText("No tasks yet.", { exact: false })).toBeVisible();
  await expect(page.getByText("No runners registered.")).toBeVisible();
  await expect(page.getByText("No repositories synced.")).toBeVisible();
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
  await page.route("**/backend/api/v1/runners", (route) =>
    route.fulfill({
      json: {
        runners: [
          {
            id: "3b241101-e2bb-4255-8caf-4136c566a907",
            hostname_or_pod: "runner-local-1",
            status: "online",
            capacity: 4,
            active_task_count: 2,
          },
        ],
      },
    }),
  );
  await page.route("**/backend/api/v1/repositories*", (route) =>
    route.fulfill({
      json: {
        repositories: [
          {
            id: "3b241101-e2bb-4255-8caf-4136c566a908",
            full_name: "example/control-plane",
            active_task_count: 2,
            recent_task_count: 18,
            updated_at: "2026-07-28T12:00:00Z",
          },
          {
            id: "3b241101-e2bb-4255-8caf-4136c566a909",
            full_name: "example/runner-images",
            active_task_count: 0,
            recent_task_count: 3,
            updated_at: "2026-07-28T11:30:00Z",
          },
        ],
      },
    }),
  );
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Running" })).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Awaiting review" }),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "Queued" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Finished" })).toBeVisible();
  await shootBothViewports(page, "dashboard-grouped");
});
