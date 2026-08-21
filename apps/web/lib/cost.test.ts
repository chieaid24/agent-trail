import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./types";
import { aggregateCost } from "./cost";

function costEvent(
  attempt: number,
  sequence: number,
  payload: Record<string, unknown>,
): ActivityEvent {
  return {
    id: `${attempt}-${sequence}`,
    task_attempt_id: `attempt-${attempt}`,
    attempt_number: attempt,
    sequence_number: sequence,
    event_type: "agent.cost_update",
    source: "agent",
    timestamp: "2026-08-21T12:00:00Z",
    payload,
    redaction_status: "none",
    created_at: "2026-08-21T12:00:00Z",
  };
}

describe("aggregateCost", () => {
  it("uses cumulative provider totals without double counting updates", () => {
    expect(
      aggregateCost([
        costEvent(1, 1, { total_cost_usd: 0.01 }),
        costEvent(1, 2, { total_cost_usd: 0.025 }),
      ]),
    ).toEqual({
      totalUsd: 0.025,
      attempts: [{ attemptNumber: 1, totalUsd: 0.025, updateCount: 2 }],
    });
  });

  it("adds incremental updates and totals attempts", () => {
    expect(
      aggregateCost([
        costEvent(1, 1, { cost_usd: 0.01 }),
        costEvent(1, 2, { cost_usd: 0.015 }),
        costEvent(2, 1, { total_cost_usd: 0.04 }),
      ]),
    ).toEqual({
      totalUsd: 0.065,
      attempts: [
        { attemptNumber: 1, totalUsd: 0.025, updateCount: 2 },
        { attemptNumber: 2, totalUsd: 0.04, updateCount: 1 },
      ],
    });
  });

  it("ignores malformed and unrelated events", () => {
    const malformed = costEvent(1, 1, { cost_usd: -2 });
    malformed.event_type = "agent.message";
    expect(aggregateCost([malformed])).toEqual({ totalUsd: 0, attempts: [] });
  });
});
