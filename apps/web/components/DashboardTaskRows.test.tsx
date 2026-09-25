import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { DashboardTaskRows } from "./DashboardTaskRows";
import type { DashboardTask } from "@/lib/types";

afterEach(cleanup);

function summary(overrides: Partial<DashboardTask>): DashboardTask {
  return {
    id: "3b241101-e2bb-4255-8caf-4136c566a962",
    title: "Sample task",
    status: "completed",
    phase: "terminal",
    source_issue_number: 5,
    started_at: "2026-07-28T12:00:00Z",
    completed_at: "2026-07-28T12:00:30Z",
    failure_message: null,
    status_reason: null,
    created_at: "2026-07-28T12:00:00Z",
    updated_at: "2026-07-28T12:00:30Z",
    ...overrides,
  };
}

test("shows the outcome reason beside the row and failures beneath it", () => {
  render(
    <DashboardTaskRows
      tasks={[
        summary({ status_reason: "pull request #7 merged" }),
        summary({
          id: "3b241101-e2bb-4255-8caf-4136c566a963",
          status: "failed",
          failure_message: "2 of 148 tests failed",
          status_reason: "validation failed",
        }),
      ]}
    />,
  );
  expect(screen.getByText("pull request #7 merged")).toBeDefined();
  expect(screen.getByText("2 of 148 tests failed")).toBeDefined();
  expect(screen.queryByText("validation failed")).toBeNull();
  expect(screen.getAllByRole("link")).toHaveLength(2);
});
