import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Timeline } from "./Timeline";
import type { ActivityEvent } from "@/lib/types";

let seq = 0;
function event(
  type: string,
  payload: Record<string, unknown> = {},
  redaction: ActivityEvent["redaction_status"] = "none",
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
    redaction_status: redaction,
    created_at: "2026-07-28T12:00:00Z",
  };
}

afterEach(cleanup);

test("renders newest first", () => {
  render(<Timeline events={[event("task.created"), event("task.queued")]} />);
  const rows = screen.getAllByRole("listitem");
  expect(rows[0].textContent).toContain("status: queued");
  expect(rows[1].textContent).toContain("task created");
});

test("redacted events show a marker instead of content", () => {
  render(
    <Timeline
      events={[event("agent.message", { message: "secret" }, "redacted")]}
    />,
  );
  expect(screen.getByText("redacted")).toBeDefined();
  expect(screen.queryByText("secret")).toBeNull();
});

test("empty timeline explains itself", () => {
  render(<Timeline events={[]} />);
  expect(screen.getByText(/No activity yet/)).toBeDefined();
});
