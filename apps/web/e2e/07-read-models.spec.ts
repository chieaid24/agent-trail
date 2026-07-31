import { expect, test, type Page } from "@playwright/test";
import {
  apiListRepositories,
  apiListRunners,
  type ApiRepository,
  type ApiRunner,
} from "./harness/api";
import { shootBothViewports } from "./harness/shots";

let repository: ApiRepository;
let runner: ApiRunner;

test.beforeAll(async () => {
  const repositories = await apiListRepositories();
  const runners = await apiListRunners();
  repository =
    repositories.find((item) => item.full_name === "chieaid24/agent-trail") ??
    repositories[0];
  runner = runners[0];
  if (!repository) throw new Error("no seeded repository");
  if (!runner) throw new Error("no registered runner");
});

test("repository and runner pages show real read models", async ({ page }) => {
  await page.goto(`/repositories/${repository.id}`);
  await expect(
    page.getByRole("heading", { name: repository.full_name }),
  ).toBeVisible();
  await expect(page.getByText("Repository metrics")).toBeVisible();
  await expect(page.getByText(".agent-trail/validation.yaml")).toBeVisible();
  await shootBothViewports(page, "repository-detail");

  await page.goto(`/runners/${runner.id}`);
  await expect(
    page.getByRole("heading", { name: runner.hostname_or_pod }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "Resources" })).toBeVisible();
  await expect(page.getByText("Not reported")).toHaveCount(3);
  await shootBothViewports(page, "runner-detail");
});

test("repository and runner detail loading states", async ({ page }) => {
  await delayedRoute(
    page,
    "**/backend/api/v1/repositories/00000000-0000-0000-0000-000000000001",
    repositoryDetail(),
  );
  await page.goto("/repositories/00000000-0000-0000-0000-000000000001");
  await shootBothViewports(page, "repository-detail-loading");

  await delayedRoute(
    page,
    "**/backend/api/v1/runners/00000000-0000-0000-0000-000000000002",
    runnerDetail(),
  );
  await page.goto("/runners/00000000-0000-0000-0000-000000000002");
  await shootBothViewports(page, "runner-detail-loading");
});

test("repository and runner detail error states", async ({ page }) => {
  await page.route("**/backend/api/v1/repositories/*", (route) =>
    route.fulfill({ status: 500, json: { error: "read model unavailable" } }),
  );
  await page.goto(`/repositories/${repository.id}`);
  await expect(page.getByText(/Could not load repository/)).toBeVisible();
  await shootBothViewports(page, "repository-detail-error");

  await page.unroute("**/backend/api/v1/repositories/*");
  await page.route("**/backend/api/v1/runners/*", (route) =>
    route.fulfill({ status: 500, json: { error: "read model unavailable" } }),
  );
  await page.goto(`/runners/${runner.id}`);
  await expect(page.getByText(/Could not load runner/)).toBeVisible();
  await shootBothViewports(page, "runner-detail-error");
});

test("repository and runner pages handle long content and measured resources", async ({
  page,
}) => {
  await page.route("**/backend/api/v1/repositories/*", (route) =>
    route.fulfill({ json: repositoryDetail() }),
  );
  await page.goto("/repositories/00000000-0000-0000-0000-000000000001");
  await expect(
    page.getByText("Validation failed after 148 checks"),
  ).toBeVisible();
  await shootBothViewports(page, "repository-detail-long");

  await page.route("**/backend/api/v1/runners/*", (route) =>
    route.fulfill({ json: runnerDetail() }),
  );
  await page.goto("/runners/00000000-0000-0000-0000-000000000002");
  await expect(
    page.getByRole("progressbar", { name: "Memory utilization" }),
  ).toHaveAttribute("aria-valuenow", "91");
  await expect(
    page.getByText("Validation failed after 148 checks"),
  ).toBeVisible();
  await shootBothViewports(page, "runner-detail-measured");
});

async function delayedRoute(
  page: Page,
  pattern: string,
  body: unknown,
): Promise<void> {
  await page.route(pattern, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 3_000));
    await route.fulfill({ json: body });
  });
}

function dashboardTask() {
  return {
    id: "00000000-0000-0000-0000-000000000003",
    title:
      "Upgrade the dependency scanner and preserve every existing validation invariant",
    status: "failed",
    phase: "terminal",
    source_issue_number: 142,
    started_at: "2026-07-28T11:45:00Z",
    completed_at: "2026-07-28T12:00:00Z",
    failure_message: "Validation failed after 148 checks",
    created_at: "2026-07-28T11:44:00Z",
    updated_at: "2026-07-28T12:00:00Z",
  };
}

function repositoryDetail() {
  return {
    id: "00000000-0000-0000-0000-000000000001",
    organization_id: "00000000-0000-0000-0000-000000000004",
    github_repository_id: 201,
    owner: "chieaid24",
    name: "agent-trail-control-plane-with-a-deliberately-long-name",
    full_name:
      "chieaid24/agent-trail-control-plane-with-a-deliberately-long-name",
    default_branch: "release/2026-q3-platform-hardening",
    is_private: true,
    is_enabled: true,
    settings: {
      default_policy: "restricted-network-and-filesystem",
      validation_file: ".agent-trail/validation.yaml",
    },
    active_task_count: 0,
    recent_task_count: 1,
    created_at: "2026-07-28T11:00:00Z",
    updated_at: "2026-07-28T12:00:00Z",
    metrics: {
      total_tasks: 42,
      active_tasks: 0,
      completed_tasks: 38,
      failed_tasks: 4,
      completion_rate: 0.9,
      median_runtime_millis: 754000,
    },
    active_tasks: [],
    recent_tasks: [dashboardTask()],
  };
}

function runnerDetail() {
  return {
    id: "00000000-0000-0000-0000-000000000002",
    runner_type: "kubernetes",
    hostname_or_pod: "agent-trail-runner-production-us-east-1-7f84b9cbbd-k2mqp",
    status: "lost",
    capacity: 4,
    active_task_count: 0,
    labels: { pool: "production" },
    resources: {
      cpu_percent: 82,
      memory_percent: 91,
      disk_percent: 67,
    },
    last_heartbeat_at: "2026-07-28T12:00:00Z",
    created_at: "2026-07-28T11:00:00Z",
    updated_at: "2026-07-28T12:00:00Z",
    current_tasks: [],
    recent_failures: [dashboardTask()],
  };
}
