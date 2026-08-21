import type { ActivityEvent } from "./types";

export interface AttemptCost {
  attemptNumber: number;
  totalUsd: number;
  updateCount: number;
}

export interface CostSummary {
  totalUsd: number;
  attempts: AttemptCost[];
}

function nonNegativeNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? value
    : null;
}

// Providers may report either a cumulative total or an incremental cost.
export function aggregateCost(events: ActivityEvent[]): CostSummary {
  const attempts = new Map<number, AttemptCost>();
  for (const event of events) {
    if (event.event_type !== "agent.cost_update") continue;
    const cumulative = nonNegativeNumber(event.payload.total_cost_usd);
    const increment = nonNegativeNumber(event.payload.cost_usd);
    if (cumulative === null && increment === null) continue;

    const attempt = attempts.get(event.attempt_number) ?? {
      attemptNumber: event.attempt_number,
      totalUsd: 0,
      updateCount: 0,
    };
    attempt.totalUsd = cumulative ?? attempt.totalUsd + (increment ?? 0);
    attempt.updateCount += 1;
    attempts.set(event.attempt_number, attempt);
  }

  const breakdown = [...attempts.values()].sort(
    (a, b) => a.attemptNumber - b.attemptNumber,
  );
  return {
    totalUsd: breakdown.reduce((sum, attempt) => sum + attempt.totalUsd, 0),
    attempts: breakdown,
  };
}
