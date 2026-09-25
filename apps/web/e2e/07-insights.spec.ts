import { expect, test, type Page } from "@playwright/test";
import type { AttemptInsight, TaskInsights } from "../lib/types";
import { formatDuration } from "../lib/format";
import { apiTaskByTitle } from "./harness/api";
import { EXECUTED_TASK_TITLE } from "./harness/global-setup";
import { shoot } from "./harness/shots";

const taskId = "3b241101-e2bb-4255-8caf-4136c566a951";

function attempt(overrides: Partial<AttemptInsight> = {}): AttemptInsight {
  return {
    attempt_id: "3b241101-e2bb-4255-8caf-4136c566a952",
    attempt_number: 1,
    status: "completed",
    started_at: "2026-08-21T12:00:00Z",
    completed_at: "2026-08-21T12:00:12Z",
    failure_code: null,
    queue_wait_ms: 1500,
    provisioning_ms: 800,
    agent_session_ms: 7000,
    validation_ms: 2000,
    publishing_ms: 450,
    total_runtime_ms: 12000,
    event_count: 14,
    validation: {
      total: 3,
      passed: 3,
      failed: 0,
      timed_out: 0,
      error: 0,
      trusted: 2,
    },
    cost: { total_usd: 0.0425, update_count: 2 },
    ...overrides,
  };
}

function insights(attempts: AttemptInsight[]): TaskInsights {
  return {
    task_id: taskId,
    task_status: "completed",
    overall: {
      attempt_count: attempts.length,
      total_runtime_ms:
        attempts.length > 0 &&
        attempts.every((a) => a.total_runtime_ms !== null)
          ? attempts.reduce((sum, a) => sum + a.total_runtime_ms!, 0)
          : null,
      event_count: attempts.reduce((sum, a) => sum + a.event_count, 0),
      validation: attempts[0]?.validation ?? null,
      cost: attempts[0]?.cost ?? null,
    },
    attempts,
  };
}

async function mockTask(
  page: Page,
  options: { status?: string; long?: boolean } = {},
): Promise<void> {
  const status = options.status ?? "completed";
  await page.route(`**/backend/api/v1/tasks/${taskId}**`, (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/insights")) return route.fallback();
    if (path.endsWith("/attempts"))
      return route.fulfill({
        json: {
          attempts: [
            {
              id: "3b241101-e2bb-4255-8caf-4136c566a952",
              task_id: taskId,
              attempt_number: 1,
              status: status === "executing" ? "active" : status,
              base_commit_sha: null,
              final_commit_sha: null,
              pull_request_number: null,
              instructions: null,
              requested_by_login: null,
              trigger_comment_id: null,
              trigger_check_run_id: null,
              trigger_check_run_completed_at: null,
              feedback: [],
              failure_code: status === "failed" ? "validation_failed" : null,
              failure_message: null,
              started_at: "2026-08-21T12:00:00Z",
              completed_at:
                status === "executing" ? null : "2026-08-21T12:00:12Z",
              created_at: "2026-08-21T11:59:58Z",
            },
          ],
        },
      });
    if (path.endsWith("/validations"))
      return route.fulfill({ json: { validations: [] } });
    if (path.endsWith("/conflicts"))
      return route.fulfill({ json: { conflicts: [] } });
    if (path.endsWith("/trace")) return route.fulfill({ json: { spans: [] } });
    if (path.endsWith("/evidence"))
      return route.fulfill({ status: 404, json: { error: "not found" } });
    if (path.endsWith("/stream"))
      return route.fulfill({
        contentType: "text/event-stream",
        body: `event: done\ndata: ${JSON.stringify({ status })}\n\n`,
      });
    return route.fulfill({
      json: {
        id: taskId,
        title: options.long
          ? "Preserve validation evidence across every attempt while upgrading the dependency scanner and reconciling repository state"
          : "Preserve execution evidence across task attempts",
        instructions: options.long
          ? "Verify every validation outcome and retain the evidence for each execution attempt.\n".repeat(
              8,
            )
          : "Run the repository checks and preserve their evidence.",
        status,
        phase: status === "executing" ? "running" : "terminal",
        base_branch: options.long
          ? "release/2026-platform-hardening-with-complete-execution-evidence"
          : "main",
        source_issue_number: 51,
        base_commit_sha: null,
        working_branch: null,
        agent_provider: "claude-code",
        agent_model: "claude-sonnet-5",
        started_at: "2026-08-21T12:00:00Z",
        completed_at: status === "executing" ? null : "2026-08-21T12:00:12Z",
        cancel_requested_at: null,
        failure_code: status === "failed" ? "validation_failed" : null,
        failure_message:
          status === "failed" ? "Repository validation did not pass." : null,
        max_cost_usd: null,
        created_at: "2026-08-21T11:59:58Z",
      },
    });
  });
}

async function screenshots(page: Page, name: string): Promise<void> {
  for (const width of [1280, 1024, 390]) {
    await page.setViewportSize({ width, height: 800 });
    const section = page.getByRole("region", { name: "Execution insights" });
    await section.scrollIntoViewIfNeeded();
    await expect(section).toBeVisible();
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    );
    expect(overflow).toBe(false);
    const fraction = page.getByLabel("Overall summary").locator("dd > span");
    if ((await fraction.count()) > 0) {
      expect(
        await fraction.evaluate((element) => element.getClientRects().length),
      ).toBe(1);
      expect(
        await fraction.evaluate(
          (element) =>
            element.getBoundingClientRect().width <=
            element.parentElement!.clientWidth,
        ),
      ).toBe(true);
    }
    await page.evaluate(() => window.scrollTo(0, 0));
    await shoot(page, `task-insights-${name}-${width}`);
    const table = page.getByRole("table", { name: "Attempt comparison" });
    if (
      width >= 1024 &&
      (await table.count()) > 0 &&
      ["populated", "multi-attempt", "long-content"].includes(name)
    ) {
      const scrolling = table.locator("..");
      if (
        await scrolling.evaluate(
          (element) => element.scrollWidth > element.clientWidth,
        )
      ) {
        await scrolling.evaluate((element) => {
          element.scrollLeft = element.scrollWidth;
        });
        await shoot(page, `task-insights-${name}-${width}-right`);
        await scrolling.evaluate((element) => {
          element.scrollLeft = 0;
        });
      }
    }
    if (
      width === 390 &&
      (await page.getByRole("list", { name: "Attempt details" }).count()) > 0
    ) {
      await expect(
        page.getByRole("list", { name: "Attempt details" }),
      ).toBeVisible();
      await expect(
        page.getByRole("table", { name: "Attempt comparison" }),
      ).toBeHidden();
    }
  }
}

test("execution insights loading", async ({ page }) => {
  await mockTask(page);
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    `**/backend/api/v1/tasks/${taskId}/insights`,
    async (route) => {
      await held;
      await route.fulfill({ json: insights([]) });
    },
  );
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByLabel("Loading execution insights")).toBeVisible();
  await screenshots(page, "loading");
  release();
  await expect(page.getByText("No attempts recorded yet.")).toBeVisible();
});

test("execution insights error and retry", async ({ page }) => {
  await mockTask(page);
  let failing = true;
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    failing
      ? route.fulfill({ status: 500, json: { error: "insights unavailable" } })
      : route.fulfill({ json: insights([attempt()]) }),
  );
  await page.goto(`/tasks/${taskId}`);
  const alert = page
    .getByRole("alert")
    .filter({ hasText: "Execution insights are unavailable." });
  await expect(alert).toBeVisible();
  await screenshots(page, "error");
  failing = false;
  await alert.getByRole("button", { name: "Retry" }).click();
  await expect(page.getByLabel("Overall summary")).toContainText("$0.0425");
});

test("execution insights empty", async ({ page }) => {
  await mockTask(page);
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    route.fulfill({ json: insights([]) }),
  );
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByText("No attempts recorded yet.")).toBeVisible();
  await screenshots(page, "empty");
});

test("execution insights partial sources", async ({ page }) => {
  await mockTask(page, { status: "executing" });
  const body = insights([
    attempt({
      status: "active",
      completed_at: null,
      provisioning_ms: null,
      agent_session_ms: null,
      validation_ms: null,
      publishing_ms: null,
      total_runtime_ms: null,
      validation: null,
      cost: null,
    }),
  ]);
  body.task_status = "executing";
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    route.fulfill({ json: body }),
  );
  await page.goto(`/tasks/${taskId}`);
  const summary = page.getByLabel("Overall summary");
  await expect(summary).toContainText("not reported");
  await expect(summary).toContainText("none");
  await expect(
    page
      .getByRole("table", { name: "Attempt comparison" })
      .getByText("-", { exact: true }),
  ).toHaveCount(5);
  await screenshots(page, "partial");
});

test("execution insights populated", async ({ page }) => {
  await mockTask(page);
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    route.fulfill({ json: insights([attempt()]) }),
  );
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByLabel("Overall summary")).toContainText("12s");
  await expect(page.getByLabel("Overall summary")).toContainText("3/3 passed");
  await screenshots(page, "populated");
  await page.getByRole("tab", { name: "Trace" }).click();
  await expect(page.getByText("No spans recorded yet")).toBeVisible();
});

test("execution insights failed attempt", async ({ page }) => {
  await mockTask(page, { status: "failed" });
  const body = insights([
    attempt({
      status: "failed",
      failure_code: "validation_failed",
      publishing_ms: null,
      validation: {
        total: 3,
        passed: 1,
        failed: 1,
        timed_out: 0,
        error: 1,
        trusted: 3,
      },
    }),
  ]);
  body.task_status = "failed";
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    route.fulfill({ json: body }),
  );
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByLabel("Overall summary")).toContainText(
    "1/3 passed (1 failed, 1 error)",
  );
  await screenshots(page, "failed");
});

test("execution insights multi-attempt comparison", async ({ page }) => {
  await mockTask(page);
  const body = insights([
    attempt({
      status: "superseded",
      cost: { total_usd: 0.025, update_count: 1 },
    }),
    attempt({
      attempt_id: "3b241101-e2bb-4255-8caf-4136c566a953",
      attempt_number: 2,
      status: "failed",
      failure_code: "validation_failed",
      event_count: 27,
      cost: { total_usd: 0.04, update_count: 1 },
    }),
    attempt({
      attempt_id: "3b241101-e2bb-4255-8caf-4136c566a954",
      attempt_number: 3,
      event_count: 40,
      cost: null,
    }),
  ]);
  body.overall.cost = { total_usd: 0.065, update_count: 2 };
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    route.fulfill({ json: body }),
  );
  await page.goto(`/tasks/${taskId}`);
  const rows = page
    .getByRole("table", { name: "Attempt comparison" })
    .getByRole("row");
  await expect(rows).toHaveCount(4);
  await expect(rows.nth(1)).toContainText("superseded");
  await expect(rows.nth(2)).toContainText("$0.0400");
  await expect(rows.nth(3)).toContainText("not reported");
  await expect(page.getByLabel("Overall summary")).toContainText(
    "Reported cost$0.0650",
  );
  await screenshots(page, "multi-attempt");
});

test("execution insights long failure and validation details", async ({
  page,
}) => {
  await mockTask(page, { status: "failed", long: true });
  const body = insights([
    attempt({
      status: "failed",
      failure_code:
        "validation_failed_after_repository_policy_reconciliation_".repeat(6),
      validation: {
        total: 100000,
        passed: 99997,
        failed: 1,
        timed_out: 1,
        error: 1,
        trusted: 100000,
      },
    }),
  ]);
  body.task_status = "failed";
  await page.route(`**/backend/api/v1/tasks/${taskId}/insights`, (route) =>
    route.fulfill({ json: body }),
  );
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByLabel("Overall summary")).toContainText(
    "99997/100000 passed (1 failed, 1 timed out, 1 error)",
  );
  await screenshots(page, "long-content");
});

test("execution insights match the persisted executed task", async ({
  page,
}) => {
  const task = await apiTaskByTitle(EXECUTED_TASK_TITLE);
  await page.goto(`/tasks/${task.id}`);
  const response = await page.request.get(
    `/backend/api/v1/tasks/${task.id}/insights`,
  );
  expect(response.status()).toBe(200);
  const body = (await response.json()) as TaskInsights;
  expect(body.attempts.length).toBeGreaterThan(0);
  expect(body.overall.total_runtime_ms).not.toBeNull();
  expect(body.overall.event_count).toBeGreaterThan(0);
  const summary = page.getByLabel("Overall summary");
  await expect(
    summary
      .locator("div")
      .filter({ has: page.getByText("Attempts", { exact: true }) }),
  ).toContainText(String(body.overall.attempt_count));
  await expect(
    summary
      .locator("div")
      .filter({ has: page.getByText("Runtime", { exact: true }) }),
  ).toContainText(formatDuration(body.overall.total_runtime_ms!));
  await expect(
    summary
      .locator("div")
      .filter({ has: page.getByText("Events", { exact: true }) }),
  ).toContainText(String(body.overall.event_count));
  await expect(
    summary
      .locator("div")
      .filter({ has: page.getByText("Reported cost", { exact: true }) }),
  ).toContainText(
    body.overall.cost === null
      ? "not reported"
      : `$${body.overall.cost.total_usd.toFixed(4)}`,
  );
  const table = page.getByRole("table", { name: "Attempt comparison" });
  for (const attempt of body.attempts) {
    const row = table.getByRole("row").filter({
      has: page.getByRole("cell", {
        name: `#${attempt.attempt_number}`,
        exact: true,
      }),
    });
    await expect(row).toContainText(String(attempt.event_count));
    await expect(row).toContainText(
      attempt.cost === null
        ? "not reported"
        : `$${attempt.cost.total_usd.toFixed(4)}`,
    );
  }
  await screenshots(page, "persisted");
  await page.goto("/");
  await expect(
    page.getByRole("link", { name: new RegExp(EXECUTED_TASK_TITLE) }),
  ).toBeVisible();
  const taskTitle = page
    .getByRole("link", { name: new RegExp(EXECUTED_TASK_TITLE) })
    .getByText(EXECUTED_TASK_TITLE, { exact: true });
  expect(
    await taskTitle.evaluate(
      (element) => element.getBoundingClientRect().width,
    ),
  ).toBeGreaterThan(100);
  const repositoryNames = page.locator(
    'a[href^="/repositories/"] > span:first-child > span:first-child',
  );
  for (const repositoryName of await repositoryNames.all()) {
    expect(
      await repositoryName.evaluate(
        (element) => element.getBoundingClientRect().width,
      ),
    ).toBeGreaterThan(100);
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  await shoot(page, "task-insights-mobile-navigation-populated-390");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    ),
  ).toBe(false);
});

test("mobile navigation remains visible through dashboard states", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 800 });
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let failing = true;
  await page.route("**/backend/api/v1/tasks*", async (route) => {
    await held;
    await route.fulfill(
      failing
        ? { status: 500, json: { error: "read unavailable" } }
        : { json: { tasks: [] } },
    );
  });
  await page.route("**/backend/api/v1/runners", (route) =>
    route.fulfill({ json: { runners: [] } }),
  );
  await page.route("**/backend/api/v1/repositories*", (route) =>
    route.fulfill({ json: { repositories: [] } }),
  );
  await page.route("**/backend/api/v1/organizations", (route) =>
    route.fulfill({ json: { organizations: [] } }),
  );
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Primary" });
  await expect(nav.getByRole("link", { name: "Tasks" })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Installations" })).toBeVisible();
  await shoot(page, "task-insights-mobile-navigation-loading-390");
  release();
  await expect(page.getByText(/Could not load tasks/)).toBeVisible();
  await shoot(page, "task-insights-mobile-navigation-error-390");
  failing = false;
  await page.getByRole("button", { name: "Retry" }).click();
  await expect(page.getByText("No tasks yet.", { exact: false })).toBeVisible();
  await shoot(page, "task-insights-mobile-navigation-empty-390");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    ),
  ).toBe(false);
  await nav.getByRole("link", { name: "Installations" }).click();
  await expect(
    page.getByRole("heading", { name: "Installations", exact: true }),
  ).toBeVisible();
  await expect(
    nav.getByRole("link", { name: "Installations" }),
  ).toHaveAttribute("aria-current", "page");
  await shoot(page, "task-insights-mobile-navigation-installations-390");
});
