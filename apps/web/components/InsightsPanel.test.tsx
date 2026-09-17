import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AttemptInsight, TaskInsights } from "@/lib/types";
import { InsightsPanel } from "./InsightsPanel";

afterEach(cleanup);

function attempt(overrides: Partial<AttemptInsight>): AttemptInsight {
  return {
    attempt_id: "3b241101-e2bb-4255-8caf-4136c566a962",
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

function insights(
  attempts: AttemptInsight[],
  overrides: Partial<TaskInsights> = {},
): TaskInsights {
  const runtimes = attempts.map((a) => a.total_runtime_ms);
  return {
    task_id: "3b241101-e2bb-4255-8caf-4136c566a961",
    task_status: "completed",
    overall: {
      attempt_count: attempts.length,
      total_runtime_ms: runtimes.every((r) => r !== null)
        ? runtimes.reduce((sum, r) => (sum ?? 0) + (r ?? 0), 0)
        : null,
      event_count: attempts.reduce((sum, a) => sum + a.event_count, 0),
      validation: attempts[0]?.validation ?? null,
      cost: attempts[0]?.cost ?? null,
    },
    attempts,
    ...overrides,
  };
}

describe("InsightsPanel", () => {
  it("renders loading, error, and empty states", () => {
    const onRetry = vi.fn();
    const { rerender } = render(
      <InsightsPanel state={{ phase: "loading" }} onRetry={onRetry} />,
    );
    expect(screen.getByLabelText("Loading execution insights")).toBeTruthy();

    rerender(<InsightsPanel state={{ phase: "error" }} onRetry={onRetry} />);
    expect(screen.getByRole("alert").textContent).toContain("unavailable");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);

    rerender(
      <InsightsPanel
        state={{ phase: "ready", insights: insights([]) }}
        onRetry={onRetry}
      />,
    );
    expect(screen.getByText("No attempts recorded yet.")).toBeTruthy();
  });

  it("renders the populated summary and comparison", () => {
    render(
      <InsightsPanel
        state={{ phase: "ready", insights: insights([attempt({})]) }}
        onRetry={() => {}}
      />,
    );
    const summary = screen.getByLabelText("Overall summary");
    expect(within(summary).getByText("12s")).toBeTruthy();
    expect(within(summary).getByText("$0.0425")).toBeTruthy();
    expect(within(summary).getByTitle("3/3 passed").textContent).toBe(
      "3/3 passed",
    );
    expect(within(summary).getByText("14")).toBeTruthy();

    const table = screen.getByRole("table", { name: "Attempt comparison" });
    const row = within(table).getAllByRole("row")[1];
    expect(row.textContent).toContain("#1");
    expect(row.textContent).toContain("completed");
    expect(within(row).getAllByText("2s").length).toBeGreaterThan(0);
    expect(within(row).getByText("7s")).toBeTruthy();
    expect(within(row).getByText("450ms")).toBeTruthy();
    expect(
      within(table).getByRole("columnheader", { name: "Queue wait" }).title,
    ).toContain("runner started");
  });

  it("shows dashes for missing sources instead of zero", () => {
    render(
      <InsightsPanel
        state={{
          phase: "ready",
          insights: insights(
            [
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
            ],
            { task_status: "executing" },
          ),
        }}
        onRetry={() => {}}
      />,
    );
    const table = screen.getByRole("table", { name: "Attempt comparison" });
    const row = within(table).getAllByRole("row")[1];
    expect(within(row).getAllByText("-")).toHaveLength(5);
    expect(within(row).getAllByText("2s").length).toBeGreaterThan(0);
    expect(within(row).getByText("executing")).toBeTruthy();
    expect(within(row).getByText("none")).toBeTruthy();
    expect(within(row).getByText("not reported")).toBeTruthy();
    const summary = screen.getByLabelText("Overall summary");
    expect(within(summary).getByText("-")).toBeTruthy();
    expect(within(summary).getByText("not reported")).toBeTruthy();
  });

  it("keeps failed attempts visible with their failure code", () => {
    render(
      <InsightsPanel
        state={{
          phase: "ready",
          insights: insights(
            [
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
            ],
            { task_status: "failed" },
          ),
        }}
        onRetry={() => {}}
      />,
    );
    const table = screen.getByRole("table", { name: "Attempt comparison" });
    const row = within(table).getAllByRole("row")[1];
    expect(within(row).getByText("failed")).toBeTruthy();
    expect(within(row).getByText("validation_failed")).toBeTruthy();
    expect(
      within(row).getByText("1/3 passed (1 failed, 1 error)").parentElement
        ?.className,
    ).toContain("text-danger");
  });

  it("orders multi-attempt tasks and badges the active attempt", () => {
    render(
      <InsightsPanel
        state={{
          phase: "ready",
          insights: insights(
            [
              attempt({
                attempt_id: "a1",
                attempt_number: 1,
                status: "superseded",
                cost: { total_usd: 0.025, update_count: 2 },
              }),
              attempt({
                attempt_id: "a2",
                attempt_number: 2,
                status: "active",
                completed_at: null,
                total_runtime_ms: null,
                publishing_ms: null,
                cost: { total_usd: 0.04, update_count: 1 },
              }),
            ],
            { task_status: "validating" },
          ),
        }}
        onRetry={() => {}}
      />,
    );
    const table = screen.getByRole("table", { name: "Attempt comparison" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain("#1");
    expect(within(rows[0]).getByText("superseded")).toBeTruthy();
    expect(within(rows[0]).getByText("$0.0250")).toBeTruthy();
    expect(rows[1].textContent).toContain("#2");
    expect(within(rows[1]).getByText("validating")).toBeTruthy();
    expect(within(rows[1]).getByText("$0.0400")).toBeTruthy();
    const summary = screen.getByLabelText("Overall summary");
    expect(within(summary).getByText("2")).toBeTruthy();
    expect(within(summary).getByText("-")).toBeTruthy();
  });
});

it("keeps long failure and check details in the mobile cards", () => {
  const failureCode = "validation_failed_".repeat(12);
  render(
    <InsightsPanel
      state={{
        phase: "ready",
        insights: insights([
          attempt({
            failure_code: failureCode,
            status: "failed",
            validation: {
              total: 100000,
              passed: 99997,
              failed: 1,
              timed_out: 1,
              error: 1,
              trusted: 100000,
            },
          }),
        ]),
      }}
      onRetry={() => {}}
    />,
  );
  const cards = screen.getByRole("list", { name: "Attempt details" });
  expect(within(cards).getByText(failureCode).className).toContain("break-all");
  expect(
    within(cards).getByText(
      "99997/100000 passed (1 failed, 1 timed out, 1 error)",
    ),
  ).toBeTruthy();
  expect(within(cards).getByText("Attempt #1")).toBeTruthy();
});
