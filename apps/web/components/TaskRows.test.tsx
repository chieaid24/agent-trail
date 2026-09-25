import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { TaskRows } from "./TaskRows";
import type { Task } from "@/lib/types";

afterEach(cleanup);

function task(overrides: Partial<Task>): Task {
  return {
    id: "3b241101-e2bb-4255-8caf-4136c566a962",
    organization_id: null,
    repository_id: null,
    source_type: "api",
    source_issue_number: 12,
    source_comment_id: null,
    title: "Sample task",
    instructions: "do it",
    status: "completed",
    phase: "terminal",
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
    started_at: "2026-07-28T12:00:00Z",
    completed_at: "2026-07-28T12:00:30Z",
    cancel_requested_at: null,
    failure_code: null,
    failure_message: null,
    status_reason: null,
    created_at: "2026-07-28T12:00:00Z",
    updated_at: "2026-07-28T12:00:30Z",
    version: 1,
    ...overrides,
  };
}

test("shows why a task completed or was cancelled", () => {
  render(
    <TaskRows
      tasks={[
        task({ status_reason: "pull request #7 merged" }),
        task({
          id: "3b241101-e2bb-4255-8caf-4136c566a963",
          status: "cancelled",
          status_reason: "pull request #8 closed without merge",
        }),
      ]}
    />,
  );
  expect(screen.getByText("pull request #7 merged")).toBeDefined();
  expect(
    screen.getByText("pull request #8 closed without merge"),
  ).toBeDefined();
  expect(screen.getAllByText("issue #12")).toHaveLength(2);
});

test("keeps non-terminal reasons in the timeline", () => {
  render(
    <TaskRows
      tasks={[
        task({
          status: "queued",
          phase: "pending",
          completed_at: null,
          status_reason: "revision requested by @alice in pull request #7",
        }),
      ]}
    />,
  );
  expect(screen.queryByText(/revision requested by/)).toBeNull();
});
