import { describe, expect, it } from "vitest";
import { groupTasks, overviewStats } from "./overview";
import type { Task, TaskPhase, TaskStatus } from "./types";

let n = 0;
function task(
  status: TaskStatus,
  phase: TaskPhase,
  overrides: Partial<Task> = {},
): Task {
  n += 1;
  return {
    id: `task-${n}`,
    organization_id: null,
    repository_id: null,
    source_type: "api",
    source_issue_number: null,
    source_comment_id: null,
    title: `Task ${n}`,
    instructions: "",
    status,
    phase,
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
    created_at: "2026-07-28T10:00:00Z",
    updated_at: "2026-07-28T10:00:00Z",
    version: 1,
    ...overrides,
  };
}

describe("groupTasks", () => {
  it("splits by phase and orders queued by priority then age", () => {
    const low = task("queued", "pending", {
      priority: 0,
      created_at: "2026-07-28T09:00:00Z",
    });
    const high = task("queued", "pending", {
      priority: 5,
      created_at: "2026-07-28T11:00:00Z",
    });
    const running = task("executing", "running");
    const done = task("completed", "terminal");

    const groups = groupTasks([low, done, high, running]);
    expect(groups.map((g) => g.key)).toEqual([
      "running",
      "review",
      "queued",
      "finished",
    ]);
    expect(groups[2].tasks.map((t) => t.id)).toEqual([high.id, low.id]);
    expect(groups[0].tasks).toEqual([running]);
    expect(groups[3].tasks).toEqual([done]);
  });
});

describe("overviewStats", () => {
  it("computes rate and median only from measured outcomes", () => {
    const stats = overviewStats([
      task("completed", "terminal", {
        started_at: "2026-07-28T10:00:00Z",
        completed_at: "2026-07-28T10:01:00Z",
      }),
      task("completed", "terminal", {
        started_at: "2026-07-28T10:00:00Z",
        completed_at: "2026-07-28T10:03:00Z",
      }),
      task("failed", "terminal"),
      task("cancelled", "terminal"),
      task("executing", "running"),
    ]);
    expect(stats.total).toBe(5);
    expect(stats.running).toBe(1);
    expect(stats.failed).toBe(1);
    // Cancelled is excluded: 2 completed of 3 outcomes.
    expect(stats.completionRate).toBeCloseTo(2 / 3);
    expect(stats.medianRuntimeMs).toBe(120_000);
  });

  it("returns nulls with no outcomes", () => {
    const stats = overviewStats([task("queued", "pending")]);
    expect(stats.completionRate).toBeNull();
    expect(stats.medianRuntimeMs).toBeNull();
  });
});
