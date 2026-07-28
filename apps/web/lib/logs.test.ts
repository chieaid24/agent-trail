import { describe, expect, it } from "vitest";
import { deriveLogLines, filterLogLines } from "./logs";
import type { ActivityEvent } from "./types";

let seq = 0;
function event(
  type: string,
  payload: Record<string, unknown>,
  redaction: ActivityEvent["redaction_status"] = "none",
): ActivityEvent {
  seq += 1;
  return {
    id: `event-${seq}`,
    task_attempt_id: "attempt-1",
    attempt_number: 1,
    sequence_number: seq,
    event_type: type,
    source: "agent",
    timestamp: "2026-07-28T12:00:00Z",
    payload,
    redaction_status: redaction,
    created_at: "2026-07-28T12:00:00Z",
  };
}

describe("deriveLogLines", () => {
  it("rebuilds a command transcript", () => {
    const lines = deriveLogLines([
      event("command.started", { command: "go", args: ["test", "./..."] }),
      event("command.output", { stream: "stdout", chunk: "ok\tpkg\n" }),
      event("command.output", { stream: "stderr", chunk: "warn: slow" }),
      event("command.completed", { command: "go", exit_code: 0 }),
    ]);
    expect(lines.map((l) => [l.stream, l.text])).toEqual([
      ["command", "$ go test ./..."],
      ["stdout", "ok\tpkg"],
      ["stderr", "warn: slow"],
      ["system", "exit 0"],
    ]);
  });

  it("splits multi-line chunks and drops only the trailing newline", () => {
    const lines = deriveLogLines([
      event("command.output", { stream: "stdout", chunk: "a\n\nb\n" }),
    ]);
    expect(lines.map((l) => l.text)).toEqual(["a", "", "b"]);
  });

  it("ignores events that are not part of the transcript", () => {
    const lines = deriveLogLines([
      event("agent.message", { message: "hello" }),
      event("task.executing", {}),
      event("command.requested", { command: "ls" }),
    ]);
    expect(lines).toEqual([]);
  });

  it("marks redacted events as visible redacted lines", () => {
    const lines = deriveLogLines([
      event(
        "command.output",
        { stream: "stdout", chunk: "secret" },
        "redacted",
      ),
    ]);
    expect(lines).toHaveLength(1);
    expect(lines[0].redacted).toBe(true);
    expect(lines[0].text).toBe("");
  });
});

describe("filterLogLines", () => {
  const lines = deriveLogLines([
    event("command.started", { command: "npm", args: ["run", "Build"] }),
    event("command.output", { stream: "stdout", chunk: "compiled OK\n" }),
    event("command.output", { stream: "stdout", chunk: "hidden" }, "redacted"),
  ]);

  it("returns everything for an empty query", () => {
    expect(filterLogLines(lines, "  ")).toEqual(lines);
  });

  it("matches case-insensitively", () => {
    const hits = filterLogLines(lines, "build");
    expect(hits).toHaveLength(1);
    expect(hits[0].text).toBe("$ npm run Build");
  });

  it("never matches redacted content", () => {
    expect(filterLogLines(lines, "hidden")).toEqual([]);
  });
});
