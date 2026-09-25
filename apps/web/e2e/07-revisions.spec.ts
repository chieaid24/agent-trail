import { expect, test, type Page } from "@playwright/test";
import type {
  ActivityEvent,
  AttemptInsight,
  StoredEvidence,
  Task,
  TaskAttempt,
  TaskInsights,
  ValidationResult,
} from "../lib/types";
import { apiTaskByTitle } from "./harness/api";
import { EXECUTED_TASK_TITLE } from "./harness/global-setup";
import { shoot } from "./harness/shots";

// route mocks pin multi-attempt states the fake pipeline cannot hold still

const taskId = "3b241101-e2bb-4255-8caf-4136c566a971";
const attemptIds = [
  "3b241101-e2bb-4255-8caf-4136c566a972",
  "3b241101-e2bb-4255-8caf-4136c566a973",
];
const baseSha = "9c1f4b2e7a6d5c3b1a0f9e8d7c6b5a4f3e2d1c0b";
const firstFinalSha = "1f2e3d4c5b6a79880716253443526170a1b2c3d4";
const secondBaseSha = "4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c";
const secondFinalSha = "7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a0f9e8d";

type RevisionState =
  "revision-requested" | "running" | "awaiting-review" | "completed";

const TASK_STATUS: Record<RevisionState, Task["status"]> = {
  "revision-requested": "revision_requested",
  running: "executing",
  "awaiting-review": "awaiting_review",
  completed: "completed",
};

const TASK_PHASE: Record<RevisionState, Task["phase"]> = {
  "revision-requested": "review",
  running: "running",
  "awaiting-review": "review",
  completed: "terminal",
};

function wireTask(state: RevisionState): Task {
  const status = TASK_STATUS[state];
  return {
    id: taskId,
    organization_id: null,
    repository_id: null,
    source_type: "github_issue",
    source_issue_number: 51,
    source_comment_id: 900,
    title: "Preserve execution evidence across task attempts",
    instructions:
      "Run the repository checks and preserve their evidence for every attempt.",
    status,
    phase: TASK_PHASE[state],
    priority: 0,
    base_branch: "main",
    base_commit_sha: baseSha,
    working_branch: "agent-trail/51-preserve-execution-evidence",
    agent_provider: "claude-code",
    agent_model: "claude-sonnet-5",
    policy_id: null,
    requested_by_user_id: null,
    max_runtime_seconds: null,
    max_cost_usd: 2,
    started_at: "2026-09-24T12:00:00Z",
    completed_at: state === "completed" ? "2026-09-24T12:40:00Z" : null,
    cancel_requested_at: null,
    failure_code: null,
    failure_message: null,
    status_reason: state === "completed" ? "pull request #7 merged" : null,
    created_at: "2026-09-24T11:59:58Z",
    updated_at: "2026-09-24T12:30:00Z",
    version: 14,
  };
}

function attempts(state: RevisionState): TaskAttempt[] {
  const published = state === "awaiting-review" || state === "completed";
  return [
    {
      id: attemptIds[0],
      task_id: taskId,
      attempt_number: 1,
      status: "superseded",
      base_commit_sha: null,
      final_commit_sha: firstFinalSha,
      pull_request_number: 7,
      instructions: null,
      requested_by_login: "octocat",
      trigger_comment_id: 900,
      trigger_check_run_id: 41,
      trigger_check_run_completed_at: "2026-09-24T12:05:00Z",
      feedback: [],
      failure_code: null,
      failure_message: null,
      started_at: "2026-09-24T12:00:00Z",
      completed_at: "2026-09-24T12:20:00Z",
      created_at: "2026-09-24T11:59:58Z",
    },
    {
      id: attemptIds[1],
      task_id: taskId,
      attempt_number: 2,
      status: state === "completed" ? "completed" : "active",
      base_commit_sha: secondBaseSha,
      final_commit_sha: published ? secondFinalSha : null,
      pull_request_number: 7,
      instructions:
        "Preserve execution evidence across task attempts\n\nRun the repository checks and preserve their evidence for every attempt.\n\nThe branch already holds the previous attempts' work.\n\nReview feedback:\n- inline review comment by alice on internal/evidence/store.go:52: keep the per-attempt read model.\n- revise command by alice: address the review and keep the tests green.",
      requested_by_login: "alice",
      trigger_comment_id: 950,
      trigger_check_run_id: 42,
      trigger_check_run_completed_at: published ? "2026-09-24T12:30:00Z" : null,
      feedback: [
        {
          kind: "review_comment",
          author: "alice",
          posted_at: "2026-09-24T12:18:00Z",
          location: "internal/evidence/store.go:52",
        },
        {
          kind: "revise_command",
          author: "alice",
          posted_at: "2026-09-24T12:20:00Z",
        },
      ],
      failure_code: null,
      failure_message: null,
      started_at:
        state === "revision-requested" ? null : "2026-09-24T12:21:00Z",
      completed_at: state === "completed" ? "2026-09-24T12:40:00Z" : null,
      created_at: "2026-09-24T12:20:00Z",
    },
  ];
}

let sequence = 0;
function event(
  attempt: 1 | 2,
  type: string,
  timestamp: string,
  payload: Record<string, unknown> = {},
): ActivityEvent {
  sequence += 1;
  return {
    id: `3b241101-e2bb-4255-8caf-4136c56${String(sequence).padStart(5, "0")}`,
    task_attempt_id: attemptIds[attempt - 1],
    attempt_number: attempt,
    sequence_number: sequence,
    event_type: type,
    source: type.startsWith("task.") ? "system" : "runner",
    timestamp,
    payload,
    redaction_status: "none",
    created_at: timestamp,
  };
}

function events(state: RevisionState): ActivityEvent[] {
  sequence = 0;
  const first: ActivityEvent[] = [
    event(1, "task.created", "2026-09-24T11:59:58Z"),
    event(1, "task.queued", "2026-09-24T11:59:58Z"),
    event(1, "task.provisioning", "2026-09-24T12:00:00Z"),
    event(1, "task.planning", "2026-09-24T12:00:10Z"),
    event(1, "plan.created", "2026-09-24T12:00:12Z", {
      plan: "1. Read the evidence store\n2. Add the per-attempt read\n3. Run the checks",
    }),
    event(1, "task.executing", "2026-09-24T12:00:14Z"),
    event(1, "file.changed", "2026-09-24T12:03:00Z", {
      path: "apps/api/internal/evidence/store.go",
    }),
    event(1, "agent.cost_update", "2026-09-24T12:04:00Z", {
      total_cost_usd: 0.31,
    }),
    event(1, "task.validating", "2026-09-24T12:05:00Z"),
    event(1, "task.publishing", "2026-09-24T12:06:00Z"),
    event(1, "task.awaiting_review", "2026-09-24T12:06:30Z"),
  ];
  const revised: ActivityEvent[] = [
    event(1, "task.revision_requested", "2026-09-24T12:20:00Z", {
      reason: "revision requested by @alice in pull request #7",
    }),
    event(2, "task.queued", "2026-09-24T12:20:00Z", {
      reason: "revision requested by @alice in pull request #7",
    }),
  ];
  if (state === "revision-requested") {
    // a second revise command on a task already at attempt 2
    return [
      ...first,
      ...revised,
      event(2, "task.provisioning", "2026-09-24T12:21:00Z"),
      event(2, "task.planning", "2026-09-24T12:21:10Z"),
      event(2, "task.executing", "2026-09-24T12:21:20Z"),
      event(2, "task.validating", "2026-09-24T12:28:00Z"),
      event(2, "task.publishing", "2026-09-24T12:29:00Z"),
      event(2, "task.awaiting_review", "2026-09-24T12:30:00Z"),
      event(2, "task.revision_requested", "2026-09-24T12:45:00Z", {
        reason: "revision requested by @alice in pull request #7",
      }),
    ];
  }
  const running: ActivityEvent[] = [
    event(2, "task.provisioning", "2026-09-24T12:21:00Z"),
    event(2, "task.planning", "2026-09-24T12:21:10Z"),
    event(2, "plan.created", "2026-09-24T12:21:12Z", {
      plan: "1. Keep the per-attempt read model\n2. Address the inline review\n3. Re-run the checks",
    }),
    event(2, "task.executing", "2026-09-24T12:21:20Z"),
    event(2, "agent.started", "2026-09-24T12:21:21Z", {
      adapter: "claude-code",
    }),
    event(2, "file.changed", "2026-09-24T12:24:00Z", {
      path: "apps/api/internal/evidence/store.go",
    }),
    event(2, "file.changed", "2026-09-24T12:25:00Z", {
      path: "apps/api/internal/evidence/store_test.go",
    }),
    event(2, "agent.cost_update", "2026-09-24T12:26:00Z", {
      total_cost_usd: 0.27,
    }),
  ];
  if (state === "running") return [...first, ...revised, ...running];
  const published: ActivityEvent[] = [
    event(2, "task.validating", "2026-09-24T12:28:00Z"),
    event(2, "validation.completed", "2026-09-24T12:29:00Z", {
      status: "passed",
      passed: 2,
      failed: 0,
    }),
    event(2, "task.publishing", "2026-09-24T12:29:30Z"),
    event(2, "evidence.generated", "2026-09-24T12:29:40Z"),
    event(2, "task.awaiting_review", "2026-09-24T12:30:00Z"),
  ];
  if (state === "awaiting-review") {
    return [...first, ...revised, ...running, ...published];
  }
  return [
    ...first,
    ...revised,
    ...running,
    ...published,
    event(2, "task.completed", "2026-09-24T12:40:00Z", {
      reason: "pull request #7 merged",
    }),
  ];
}

function validation(attempt: 1 | 2, name: string): ValidationResult {
  return {
    id: `${attemptIds[attempt - 1]}-${name}`,
    task_attempt_id: attemptIds[attempt - 1],
    attempt_number: attempt,
    name,
    category: name === "lint" ? "lint" : "unit_test",
    command: ["go", "test", "./..."],
    status: "passed",
    exit_code: 0,
    duration_ms: attempt === 1 ? 4200 : 3900,
    summary: "",
    trusted_execution: true,
    created_at: "2026-09-24T12:29:00Z",
  };
}

function evidence(attempt: 1 | 2): StoredEvidence {
  return {
    id: `${attemptIds[attempt - 1]}-evidence`,
    task_attempt_id: attemptIds[attempt - 1],
    attempt_number: attempt,
    schema_version: 1,
    summary_markdown: "# Evidence",
    report: {
      schema_version: 1,
      task: {
        id: taskId,
        title: "Preserve execution evidence across task attempts",
      },
      execution: {
        agent_provider: "claude-code",
        agent_model: "claude-sonnet-5",
        base_commit: attempt === 1 ? baseSha : secondBaseSha,
        final_commit: attempt === 1 ? firstFinalSha : secondFinalSha,
        duration_seconds: attempt === 1 ? 390 : 540,
      },
      changes: {
        files_changed: attempt === 1 ? 1 : 2,
        files:
          attempt === 1
            ? ["apps/api/internal/evidence/store.go"]
            : [
                "apps/api/internal/evidence/store.go",
                "apps/api/internal/evidence/store_test.go",
              ],
      },
      validation: [
        {
          name: "unit",
          category: "unit_test",
          status: "passed",
          trusted_execution: true,
          exit_code: 0,
          duration_ms: attempt === 1 ? 4200 : 3900,
        },
      ],
    },
    created_at: attempt === 1 ? "2026-09-24T12:06:00Z" : "2026-09-24T12:29:40Z",
  };
}

function insight(attempt: 1 | 2, state: RevisionState): AttemptInsight {
  const finished =
    attempt === 1 || state === "awaiting-review" || state === "completed";
  return {
    attempt_id: attemptIds[attempt - 1],
    attempt_number: attempt,
    status:
      attempt === 1
        ? "superseded"
        : state === "completed"
          ? "completed"
          : "active",
    started_at: "2026-09-24T12:00:00Z",
    completed_at: finished ? "2026-09-24T12:06:30Z" : null,
    failure_code: null,
    queue_wait_ms: attempt === 1 ? 2000 : 60000,
    provisioning_ms: 8000,
    agent_session_ms: finished ? 240000 : null,
    validation_ms: finished ? 42000 : null,
    publishing_ms: finished ? 30000 : null,
    total_runtime_ms: finished ? 390000 : null,
    event_count: attempt === 1 ? 11 : 14,
    validation: finished
      ? { total: 1, passed: 1, failed: 0, timed_out: 0, error: 0, trusted: 1 }
      : null,
    cost: { total_usd: attempt === 1 ? 0.31 : 0.27, update_count: 1 },
  };
}

function insights(state: RevisionState): TaskInsights {
  const rows = [insight(1, state), insight(2, state)];
  return {
    task_id: taskId,
    task_status: TASK_STATUS[state],
    overall: {
      attempt_count: 2,
      total_runtime_ms: rows.every((r) => r.total_runtime_ms !== null)
        ? 780000
        : null,
      event_count: 25,
      validation: rows[1].validation ?? rows[0].validation,
      cost: { total_usd: 0.58, update_count: 2 },
    },
    attempts: rows,
  };
}

function sse(items: ActivityEvent[], finalStatus: string): string {
  const frames = items.map(
    (e) =>
      `id: ${e.attempt_number}:${e.sequence_number}\ndata: ${JSON.stringify(e)}\n`,
  );
  return (
    [
      ...frames,
      `event: done\ndata: ${JSON.stringify({ status: finalStatus })}\n`,
    ].join("\n") + "\n"
  );
}

async function mockRevisionTask(
  page: Page,
  state: RevisionState,
): Promise<void> {
  const list = attempts(state);
  const published = state === "awaiting-review" || state === "completed";
  await page.route(`**/backend/api/v1/tasks/${taskId}**`, (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const attempt = url.searchParams.get("attempt");
    if (path.endsWith("/attempts"))
      return route.fulfill({ json: { attempts: list } });
    if (path.endsWith("/insights"))
      return route.fulfill({ json: insights(state) });
    if (path.endsWith("/validations")) {
      const results = [validation(1, "unit"), validation(1, "lint")];
      if (published) results.push(validation(2, "unit"), validation(2, "lint"));
      return route.fulfill({ json: { validations: results } });
    }
    if (path.endsWith("/conflicts"))
      return route.fulfill({ json: { conflicts: [] } });
    if (path.endsWith("/trace")) {
      const wanted = attempt === null ? [1, 2] : [Number(attempt)];
      return route.fulfill({
        json: {
          spans: wanted
            .filter((n) => n === 1 || published)
            .map((n) => ({
              trace_id: "0123456789abcdef0123456789abcdef",
              span_id: `000000000000000${n}`,
              parent_span_id: null,
              task_attempt_id: attemptIds[n - 1],
              name: "runner.attempt",
              kind: "internal",
              start_time:
                n === 1 ? "2026-09-24T12:00:00Z" : "2026-09-24T12:21:00Z",
              end_time:
                n === 1 ? "2026-09-24T12:06:30Z" : "2026-09-24T12:30:00Z",
              attributes: {},
              status_code: "Ok",
              status_message: "",
            })),
        },
      });
    }
    if (path.endsWith("/evidence")) {
      const n = attempt === null ? 2 : Number(attempt);
      if (n === 2 && !published) {
        return route.fulfill({
          status: 404,
          json: { error: "no evidence report" },
        });
      }
      return route.fulfill({ json: evidence(n === 1 ? 1 : 2) });
    }
    if (path.endsWith("/stream")) {
      return route.fulfill({
        contentType: "text/event-stream",
        body: sse(events(state), TASK_STATUS[state]),
      });
    }
    return route.fulfill({ json: wireTask(state) });
  });
}

async function expectNoOverflow(page: Page): Promise<void> {
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    ),
  ).toBe(false);
}

async function screenshots(page: Page, name: string): Promise<void> {
  for (const width of [1280, 1024, 390]) {
    await page.setViewportSize({ width, height: 800 });
    await expect(
      page.getByRole("group", { name: "Attempt selector" }),
    ).toBeVisible();
    await expectNoOverflow(page);
    await page.evaluate(() => window.scrollTo(0, 0));
    await shoot(page, `task-revisions-${name}-${width}`);
  }
  await page.setViewportSize({ width: 1280, height: 800 });
}

const header = (page: Page) => page.locator("article > header");

test("revision requested on a multi-attempt task", async ({ page }) => {
  await mockRevisionTask(page, "revision-requested");
  await page.goto(`/tasks/${taskId}`);
  await expect(
    header(page).getByText("revision requested", { exact: true }),
  ).toBeVisible();
  await expect(
    header(page).getByText("revision requested", { exact: true }),
  ).toHaveClass(/text-info/);
  const selector = page.getByRole("group", { name: "Attempt selector" });
  await expect(selector).toContainText("2 attempts");
  await expect(page.getByRole("button", { name: "Attempt 2" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(page.getByLabel("Attempt 2 request")).toContainText(
    "Revision requested by alice",
  );
  await expect(
    page.getByText("status: revision requested").first(),
  ).toBeVisible();
  await screenshots(page, "revision-requested");
});

test("running revision follows attempt 2 while the agent works", async ({
  page,
}) => {
  await mockRevisionTask(page, "running");
  await page.goto(`/tasks/${taskId}`);
  await expect(
    header(page).getByText("executing", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Attempt 2" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(header(page).getByText("not published")).toBeVisible();
  await expect(page.getByText("agent session started")).toBeVisible();
  await expect(page.getByText("status: awaiting review")).toHaveCount(0);
  await page.getByRole("tab", { name: /Files/ }).click();
  await expect(
    page.getByText("apps/api/internal/evidence/store_test.go"),
  ).toBeVisible();
  await page.getByRole("tab", { name: /Evidence/ }).click();
  await expect(page.getByText("No evidence report yet.")).toBeVisible();
  await page.getByRole("tab", { name: /Timeline/ }).click();
  await screenshots(page, "running");
});

test("awaiting review after a revision switches attempts without a reload", async ({
  page,
}) => {
  await mockRevisionTask(page, "awaiting-review");
  await page.goto(`/tasks/${taskId}`);
  await expect(
    header(page).getByText("awaiting review", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Attempt 2" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(
    header(page).getByText(secondBaseSha.slice(0, 10)),
  ).toBeVisible();
  await expect(
    header(page).getByText(secondFinalSha.slice(0, 10)),
  ).toBeVisible();
  await expect(page.getByRole("tab", { name: /Validations/ })).toContainText(
    "2",
  );
  await page.getByRole("tab", { name: /Evidence/ }).click();
  await expect(
    page.getByText(secondFinalSha.slice(0, 10)).last(),
  ).toBeVisible();
  await page.getByRole("tab", { name: /Trace/ }).click();
  await expect(
    page.getByRole("list", { name: "Trace waterfall" }).getByRole("listitem"),
  ).toHaveCount(1);
  await page.getByRole("tab", { name: /Timeline/ }).click();
  await screenshots(page, "awaiting-review");

  // a full reload would drop this marker
  await page.evaluate(() => {
    (window as unknown as { __revisionsMarker: string }).__revisionsMarker =
      "kept";
  });
  const insightsRows = page
    .getByRole("table", { name: "Attempt comparison" })
    .getByRole("row");
  await expect(insightsRows.nth(2)).toHaveAttribute("aria-current", "true");

  await page.getByRole("button", { name: "Attempt 1" }).click();
  await expect(page.getByRole("button", { name: "Attempt 1" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(page.getByLabel("Attempt 1 request")).toContainText(
    "Run requested by octocat",
  );
  await expect(header(page).getByText(baseSha.slice(0, 10))).toBeVisible();
  await expect(
    header(page).getByText(firstFinalSha.slice(0, 10)),
  ).toBeVisible();
  await expect(page.getByText("status: revision requested")).toBeVisible();
  await expect(page.getByText("agent session started")).toHaveCount(0);
  await expect(insightsRows.nth(1)).toHaveAttribute("aria-current", "true");
  await page.getByRole("tab", { name: /Evidence/ }).click();
  await expect(page.getByText(firstFinalSha.slice(0, 10)).last()).toBeVisible();
  await page.getByRole("tab", { name: /Files/ }).click();
  await expect(
    page.getByText("apps/api/internal/evidence/store_test.go"),
  ).toHaveCount(0);
  expect(
    await page.evaluate(
      () =>
        (window as unknown as { __revisionsMarker?: string }).__revisionsMarker,
    ),
  ).toBe("kept");
  await page.getByRole("tab", { name: /Timeline/ }).click();
  await screenshots(page, "awaiting-review-attempt-1");
});

test("completed after a revision shows the merge reason", async ({ page }) => {
  await mockRevisionTask(page, "completed");
  await page.goto(`/tasks/${taskId}`);
  await expect(
    header(page).getByText("completed", { exact: true }),
  ).toBeVisible();
  await expect(header(page).getByText("pull request #7 merged")).toBeVisible();
  await expect(page.getByRole("button", { name: "Attempt 2" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(page.getByText("status: completed")).toBeVisible();
  await screenshots(page, "completed");
});

test("status surfaces show the revision badge and outcome reasons", async ({
  page,
}) => {
  const revising = {
    ...wireTask("revision-requested"),
    id: "3b241101-e2bb-4255-8caf-4136c566a981",
  };
  const merged = {
    ...wireTask("completed"),
    id: "3b241101-e2bb-4255-8caf-4136c566a982",
    title: "Rename the billing module",
  };
  const closed = {
    ...wireTask("completed"),
    id: "3b241101-e2bb-4255-8caf-4136c566a983",
    title: "Upgrade the TLS library",
    status: "cancelled" as const,
    status_reason: "pull request #9 closed without merge",
  };
  await page.route("**/backend/api/v1/tasks?*", (route) =>
    route.fulfill({ json: { tasks: [revising, merged, closed] } }),
  );
  await page.goto("/");
  const review = page.getByRole("region", { name: "Awaiting review" });
  await expect(
    review.getByText("revision requested", { exact: true }),
  ).toHaveClass(/text-info/);
  const finished = page.getByRole("region", { name: "Finished" });
  await expect(finished.getByText("pull request #7 merged")).toBeVisible();
  await expect(
    finished.getByText("pull request #9 closed without merge"),
  ).toBeVisible();
  await expectNoOverflow(page);
  await shoot(page, "task-revisions-overview-1280");

  const repositoryId = "39be2f56-3419-4a0a-a7ad-1a72698c0cc5";
  const summary = (t: Task) => ({
    id: t.id,
    title: t.title,
    status: t.status,
    phase: t.phase,
    source_issue_number: t.source_issue_number,
    started_at: t.started_at,
    completed_at: t.completed_at,
    failure_message: t.failure_message,
    status_reason: t.status_reason,
    created_at: t.created_at,
    updated_at: t.updated_at,
  });
  await page.route(`**/backend/api/v1/repositories/${repositoryId}`, (route) =>
    route.fulfill({
      json: {
        id: repositoryId,
        organization_id: "5b0a6f6e-3c73-4b57-9d5c-0f0f38c4a001",
        github_repository_id: 201,
        owner: "chieaid24",
        name: "agent-trail",
        full_name: "chieaid24/agent-trail",
        default_branch: "main",
        is_private: false,
        is_enabled: true,
        settings: {
          default_policy: "restricted",
          validation_file: ".agent-trail/validation.yaml",
          max_attempts: 5,
        },
        active_task_count: 1,
        recent_task_count: 3,
        created_at: "2026-07-28T12:00:00Z",
        updated_at: "2026-09-24T12:40:00Z",
        metrics: {
          total_tasks: 3,
          active_tasks: 1,
          completed_tasks: 1,
          failed_tasks: 0,
          completion_rate: 1,
          median_runtime_millis: 780000,
        },
        active_tasks: [summary(revising)],
        recent_tasks: [summary(merged), summary(closed), summary(revising)],
      },
    }),
  );
  await page.goto(`/repositories/${repositoryId}`);
  await expect(
    page.getByRole("heading", { name: "chieaid24/agent-trail" }),
  ).toBeVisible();
  await expect(
    page.getByText("revision requested", { exact: true }).first(),
  ).toHaveClass(/text-info/);
  await expect(page.getByText("pull request #7 merged")).toBeVisible();
  await expectNoOverflow(page);
  await shoot(page, "task-revisions-repository-1280");
});

test("persisted tasks keep the single-attempt layout and seeded reasons", async ({
  page,
}) => {
  const executed = await apiTaskByTitle(EXECUTED_TASK_TITLE);
  await page.goto(`/tasks/${executed.id}`);
  await expect(
    page.getByRole("heading", { name: EXECUTED_TASK_TITLE }),
  ).toBeVisible();
  await expect(page.getByText("stream ended")).toBeVisible();
  const attemptsResponse = await page.request.get(
    `/backend/api/v1/tasks/${executed.id}/attempts`,
  );
  expect(attemptsResponse.status()).toBe(200);
  const body = (await attemptsResponse.json()) as { attempts: TaskAttempt[] };
  expect(body.attempts).toHaveLength(1);
  await expect(
    page.getByRole("group", { name: "Attempt selector" }),
  ).toHaveCount(0);
  await expect(header(page).getByText("final commit")).toHaveCount(0);
  const scoped = await page.request.get(
    `/backend/api/v1/tasks/${executed.id}/evidence?attempt=1`,
  );
  expect(scoped.status()).toBe(200);
  const unknown = await page.request.get(
    `/backend/api/v1/tasks/${executed.id}/evidence?attempt=2`,
  );
  expect(unknown.status()).toBe(404);
  await expectNoOverflow(page);
  await shoot(page, "task-revisions-single-attempt-1280");

  const seeded = await apiTaskByTitle("Fix the flaky login test");
  await page.goto(`/tasks/${seeded.id}`);
  await expect(header(page).getByText("pull request #41 merged")).toBeVisible();
  await page.goto("/");
  await expect(
    page
      .getByRole("region", { name: "Finished" })
      .getByText("pull request #41 merged"),
  ).toBeVisible();
});
