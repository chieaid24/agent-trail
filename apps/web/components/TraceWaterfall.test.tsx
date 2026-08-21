import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TaskSpan } from "@/lib/types";
import { TraceWaterfall } from "./TraceWaterfall";

const spans: TaskSpan[] = [
  {
    trace_id: "0123456789abcdef0123456789abcdef",
    span_id: "0123456789abcdef",
    parent_span_id: null,
    task_attempt_id: "3b241101-e2bb-4255-8caf-4136c566a962",
    name: "task.execute",
    kind: "internal",
    start_time: "2026-08-21T12:00:00.000Z",
    end_time: "2026-08-21T12:00:02.000Z",
    attributes: { "task.id": "3b241101-e2bb-4255-8caf-4136c566a961" },
    status_code: "Ok",
    status_message: "",
  },
  {
    trace_id: "0123456789abcdef0123456789abcdef",
    span_id: "fedcba9876543210",
    parent_span_id: "0123456789abcdef",
    task_attempt_id: "3b241101-e2bb-4255-8caf-4136c566a962",
    name: "task.validate",
    kind: "internal",
    start_time: "2026-08-21T12:00:00.500Z",
    end_time: "2026-08-21T12:00:01.250Z",
    attributes: {},
    status_code: "Error",
    status_message: "tests failed",
  },
];

describe("TraceWaterfall", () => {
  it("renders loading, error, and empty states", () => {
    const { rerender } = render(
      <TraceWaterfall state={{ phase: "loading" }} />,
    );
    expect(screen.getByLabelText("Loading trace")).toBeTruthy();

    rerender(<TraceWaterfall state={{ phase: "error" }} />);
    expect(screen.getByRole("alert").textContent).toContain("unavailable");

    rerender(<TraceWaterfall state={{ phase: "ready", spans: [] }} />);
    expect(screen.getByText("No spans recorded yet")).toBeTruthy();
  });

  it("renders ordered spans, durations, and error detail", () => {
    render(<TraceWaterfall state={{ phase: "ready", spans }} />);
    expect(screen.getByRole("list", { name: "Trace waterfall" })).toBeTruthy();
    expect(screen.getByText("task.execute")).toBeTruthy();
    expect(screen.getByText("task.validate")).toBeTruthy();
    expect(screen.getByText("tests failed")).toBeTruthy();
    expect(screen.getByText("2s")).toBeTruthy();
    expect(screen.getByText("750ms")).toBeTruthy();
  });
});
