import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { LogViewer } from "./LogViewer";
import type { ActivityEvent } from "@/lib/types";

let seq = 0;
function outputEvent(
  chunk: string,
  redaction: ActivityEvent["redaction_status"] = "none",
): ActivityEvent {
  seq += 1;
  return {
    id: `event-${seq}`,
    task_attempt_id: "attempt-1",
    attempt_number: 1,
    sequence_number: seq,
    event_type: "command.output",
    source: "agent",
    timestamp: "2026-07-28T12:00:00Z",
    payload: { stream: "stdout", chunk },
    redaction_status: redaction,
    created_at: "2026-07-28T12:00:00Z",
  };
}

afterEach(cleanup);

test("windows long transcripts instead of rendering every line", () => {
  const events = Array.from({ length: 500 }, (_, i) =>
    outputEvent(`line ${i}\n`),
  );
  render(<LogViewer events={events} />);

  expect(screen.getByText("500 lines")).toBeDefined();
  // The window starts at the top: early lines are mounted, the tail is not.
  expect(screen.getByText("line 0")).toBeDefined();
  expect(screen.queryByText("line 499")).toBeNull();
});

test("search narrows to matching lines, case-insensitively", () => {
  render(
    <LogViewer
      events={[outputEvent("Compiled OK\n"), outputEvent("tests passed\n")]}
    />,
  );
  fireEvent.change(screen.getByLabelText("Search logs"), {
    target: { value: "compiled" },
  });
  expect(screen.getByText("1 matching line")).toBeDefined();
  expect(screen.getByText("Compiled OK")).toBeDefined();
  expect(screen.queryByText("tests passed")).toBeNull();
});

test("redacted output renders a visible marker, not a gap", () => {
  render(<LogViewer events={[outputEvent("secret", "redacted")]} />);
  expect(screen.getByText("redacted")).toBeDefined();
  expect(screen.queryByText("secret")).toBeNull();
});

test("empty transcript explains itself", () => {
  render(<LogViewer events={[]} />);
  expect(screen.getByText(/No command output yet/)).toBeDefined();
});
