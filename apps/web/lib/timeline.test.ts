import { describe, expect, it } from "vitest";
import { changedFiles, describeEvent, latestPlan } from "./timeline";
import type { ActivityEvent } from "./types";

let seq = 0;
function event(
  type: string,
  payload: Record<string, unknown> = {},
): ActivityEvent {
  seq += 1;
  return {
    id: `event-${seq}`,
    task_attempt_id: "attempt-1",
    attempt_number: 1,
    sequence_number: seq,
    event_type: type,
    source: "runner",
    timestamp: "2026-07-28T12:00:00Z",
    payload,
    redaction_status: "none",
    created_at: "2026-07-28T12:00:00Z",
  };
}

describe("describeEvent", () => {
  it("labels lifecycle transitions with their target status", () => {
    expect(describeEvent(event("task.awaiting_review")).label).toBe(
      "status: awaiting review",
    );
    expect(describeEvent(event("task.created")).label).toBe("task created");
    expect(
      describeEvent(event("task.cancelled", { reason: "operator request" }))
        .detail,
    ).toBe("operator request");
  });

  it("renders commands as mono shell lines", () => {
    const row = describeEvent(
      event("command.started", { command: "go", args: ["vet", "./..."] }),
    );
    expect(row.detail).toBe("go vet ./...");
    expect(row.monoDetail).toBe(true);
  });

  it("marks simulated command completions", () => {
    const row = describeEvent(
      event("command.completed", {
        command: "go",
        exit_code: 0,
        simulated: true,
      }),
    );
    expect(row.label).toBe("command completed (simulated)");
    expect(row.detail).toBe("go -> exit 0");
  });

  it("flags untrusted validation checks as agent claims", () => {
    const row = describeEvent(
      event("validation.check.completed", {
        name: "unit tests",
        status: "passed",
        trusted_execution: false,
      }),
    );
    expect(row.label).toBe("check passed: unit tests (agent claim)");
  });

  it("falls back to the raw type for unknown events", () => {
    expect(describeEvent(event("something.new")).label).toBe("something.new");
  });
});

describe("changedFiles", () => {
  it("dedupes paths in first-seen order", () => {
    const files = changedFiles([
      event("file.changed", { path: "a.go" }),
      event("file.changed", { path: "b.go" }),
      event("file.changed", { path: "a.go" }),
      event("file.read", { path: "c.go" }),
    ]);
    expect(files).toEqual(["a.go", "b.go"]);
  });
});

describe("latestPlan", () => {
  it("returns the newest plan text", () => {
    const plan = latestPlan([
      event("plan.created", { plan: "old plan" }),
      event("agent.message", { message: "working" }),
      event("plan.created", { plan: "new plan" }),
    ]);
    expect(plan).toBe("new plan");
  });

  it("returns null without a plan", () => {
    expect(latestPlan([event("agent.message", { message: "hi" })])).toBeNull();
  });
});
