import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import TaskPage from "./page";
import type {
  ActivityEvent,
  Task,
  TaskAttempt,
  TaskInsights,
  ValidationResult,
} from "@/lib/types";

const taskId = "3b241101-e2bb-4255-8caf-4136c566a951";
const attemptIds = [
  "3b241101-e2bb-4255-8caf-4136c566a952",
  "3b241101-e2bb-4255-8caf-4136c566a953",
];

type Listener = (m: MessageEvent<string>) => void;

// jsdom has no EventSource; the stub lets a test push frames in order
class FakeEventSource {
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];
  readonly url: string;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: Listener | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Listener[]>();
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
  emit(events: ActivityEvent[], finalStatus: string) {
    this.onopen?.();
    for (const e of events) {
      this.onmessage?.({ data: JSON.stringify(e) } as MessageEvent<string>);
    }
    for (const fn of this.listeners.get("done") ?? []) {
      fn({
        data: JSON.stringify({ status: finalStatus }),
      } as MessageEvent<string>);
    }
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function task(overrides: Partial<Task>): Task {
  return {
    id: taskId,
    organization_id: null,
    repository_id: null,
    source_type: "github_issue",
    source_issue_number: 12,
    source_comment_id: 100,
    title: "Preserve execution evidence",
    instructions: "Run the checks.",
    status: "awaiting_review",
    phase: "review",
    priority: 0,
    base_branch: "main",
    base_commit_sha: "a".repeat(40),
    working_branch: "agent-trail/task-1",
    agent_provider: "fake",
    agent_model: null,
    policy_id: null,
    requested_by_user_id: null,
    max_runtime_seconds: null,
    max_cost_usd: null,
    started_at: "2026-09-24T12:00:00Z",
    completed_at: null,
    cancel_requested_at: null,
    failure_code: null,
    failure_message: null,
    status_reason: null,
    created_at: "2026-09-24T11:59:00Z",
    updated_at: "2026-09-24T12:05:00Z",
    version: 9,
    ...overrides,
  };
}

function attempt(n: number, overrides: Partial<TaskAttempt> = {}): TaskAttempt {
  return {
    id: attemptIds[n - 1],
    task_id: taskId,
    attempt_number: n,
    status: n === 1 ? "superseded" : "active",
    base_commit_sha: n === 1 ? null : "b".repeat(40),
    final_commit_sha: n === 1 ? "f".repeat(40) : null,
    pull_request_number: 7,
    instructions: n === 1 ? null : "Revise: rename the helper",
    requested_by_login: "alice",
    trigger_comment_id: 100 + n,
    trigger_check_run_id: null,
    trigger_check_run_completed_at: null,
    feedback:
      n === 1
        ? []
        : [
            {
              kind: "review_comment",
              author: "alice",
              posted_at: "2026-09-24T12:03:00Z",
            },
          ],
    failure_code: null,
    failure_message: null,
    started_at: "2026-09-24T12:00:00Z",
    completed_at: null,
    created_at: n === 1 ? "2026-09-24T11:59:00Z" : "2026-09-24T12:04:00Z",
    ...overrides,
  };
}

let seq = 0;
function event(n: number, type: string, payload: Record<string, unknown> = {}) {
  seq += 1;
  return {
    id: `event-${seq}`,
    task_attempt_id: attemptIds[n - 1],
    attempt_number: n,
    sequence_number: seq,
    event_type: type,
    source: "runner",
    timestamp: "2026-09-24T12:00:00Z",
    payload,
    redaction_status: "none",
    created_at: "2026-09-24T12:00:00Z",
  } as ActivityEvent;
}

function validation(n: number, name: string): ValidationResult {
  return {
    id: `${name}-${n}`,
    task_attempt_id: attemptIds[n - 1],
    attempt_number: n,
    name,
    category: "unit_test",
    command: ["npm", "test"],
    status: "passed",
    exit_code: 0,
    duration_ms: 1200,
    summary: "",
    trusted_execution: true,
    created_at: "2026-09-24T12:00:00Z",
  };
}

const emptyInsights: TaskInsights = {
  task_id: taskId,
  task_status: "awaiting_review",
  overall: {
    attempt_count: 0,
    total_runtime_ms: null,
    event_count: 0,
    validation: null,
    cost: null,
  },
  attempts: [],
};

function stubFetch(options: { task: Task; attempts: TaskAttempt[] }) {
  const calls: string[] = [];
  const fetchMock = vi.fn().mockImplementation((input: string) => {
    calls.push(input);
    const url = new URL(input, "http://dashboard");
    const attemptFilter = url.searchParams.get("attempt");
    if (url.pathname.endsWith("/me")) {
      return Promise.resolve(json({ error: "unauthenticated" }, 401));
    }
    if (url.pathname.endsWith("/attempts")) {
      return Promise.resolve(json({ attempts: options.attempts }));
    }
    if (url.pathname.endsWith("/validations")) {
      return Promise.resolve(
        json({ validations: [validation(1, "smoke"), validation(2, "smoke")] }),
      );
    }
    if (url.pathname.endsWith("/conflicts")) {
      return Promise.resolve(json({ conflicts: [] }));
    }
    if (url.pathname.endsWith("/insights")) {
      return Promise.resolve(json(emptyInsights));
    }
    if (url.pathname.endsWith("/trace")) {
      return Promise.resolve(
        json({
          spans: [
            {
              trace_id: "0123456789abcdef0123456789abcdef",
              span_id: attemptFilter ?? "all",
              parent_span_id: null,
              task_attempt_id: null,
              name: `runner.attempt.${attemptFilter ?? "all"}`,
              kind: "internal",
              start_time: "2026-09-24T12:00:00Z",
              end_time: "2026-09-24T12:00:05Z",
              attributes: {},
              status_code: "Ok",
              status_message: "",
            },
          ],
        }),
      );
    }
    if (url.pathname.endsWith("/evidence")) {
      return Promise.resolve(json({ error: "no evidence report" }, 404));
    }
    return Promise.resolve(json(options.task));
  });
  vi.stubGlobal("fetch", fetchMock);
  return calls;
}

async function renderPage() {
  await act(async () => {
    render(<TaskPage params={Promise.resolve({ taskId })} />);
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 5));
  });
}

async function streamEvents(events: ActivityEvent[], finalStatus: string) {
  const source = FakeEventSource.instances[0];
  expect(source).toBeDefined();
  await act(async () => {
    source.emit(events, finalStatus);
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 5));
  });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  seq = 0;
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal("location", { ...window.location, assign: vi.fn() });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("a multi-attempt task follows the selected attempt without a reload", async () => {
  const calls = stubFetch({
    task: task({}),
    attempts: [attempt(1), attempt(2)],
  });
  await renderPage();
  await streamEvents(
    [
      event(1, "task.created"),
      event(1, "agent.started", { adapter: "fake" }),
      event(1, "file.changed", { path: "first.go" }),
      event(1, "task.revision_requested", {
        reason: "revision requested by @alice in pull request #7",
      }),
      event(2, "task.queued"),
      event(2, "file.changed", { path: "second.go" }),
      event(2, "task.awaiting_review"),
    ],
    "awaiting_review",
  );

  const group = await screen.findByRole("group", { name: "Attempt selector" });
  expect(group.textContent).toContain("2 attempts");
  const second = screen.getByRole("button", { name: "Attempt 2" });
  expect(second.getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByLabelText("Attempt 2 request").textContent).toContain(
    "Revision requested by alice",
  );
  expect(screen.getByText("bbbbbbbbbb")).toBeDefined();
  expect(screen.getByText("not published")).toBeDefined();
  expect(screen.getByText("Revise: rename the helper")).toBeDefined();
  expect(screen.getByText("status: queued")).toBeDefined();
  expect(screen.queryByText("agent session started")).toBeNull();
  expect(
    calls.some((c) => c.endsWith(`/tasks/${taskId}/trace?attempt=2`)),
  ).toBe(true);
  expect(
    calls.some((c) => c.endsWith(`/tasks/${taskId}/evidence?attempt=2`)),
  ).toBe(true);
  expect(calls.some((c) => c.endsWith(`/tasks/${taskId}/trace`))).toBe(false);

  const before = calls.length;
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Attempt 1" }));
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 5));
  });
  expect(
    screen
      .getByRole("button", { name: "Attempt 1" })
      .getAttribute("aria-pressed"),
  ).toBe("true");
  expect(screen.getByText("agent session started")).toBeDefined();
  expect(screen.queryByText("status: queued")).toBeNull();
  expect(screen.getByText("aaaaaaaaaa")).toBeDefined();
  expect(screen.getByText("ffffffffff")).toBeDefined();
  expect(screen.getByText("Run the checks.")).toBeDefined();
  expect(screen.getByLabelText("Attempt 1 request").textContent).toContain(
    "Run requested by alice",
  );
  const after = calls.slice(before);
  expect(after.some((c) => c.endsWith(`/trace?attempt=1`))).toBe(true);
  expect(after.some((c) => c.endsWith(`/evidence?attempt=1`))).toBe(true);
  expect(after.some((c) => c.endsWith(`/tasks/${taskId}`))).toBe(false);

  fireEvent.click(screen.getByRole("tab", { name: /Files/ }));
  expect(screen.getByText("first.go")).toBeDefined();
  expect(screen.queryByText("second.go")).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: /Validations/ }));
  expect(screen.getAllByText("smoke")).toHaveLength(1);
  fireEvent.click(screen.getByRole("tab", { name: /Trace/ }));
  expect(await screen.findByText("runner.attempt.1")).toBeDefined();
});

test("a single-attempt task shows no selector and no attempt facts", async () => {
  stubFetch({
    task: task({
      status: "completed",
      phase: "terminal",
      completed_at: "2026-09-24T12:06:00Z",
      status_reason: "pull request #7 merged",
    }),
    attempts: [attempt(1, { status: "completed" })],
  });
  await renderPage();
  await streamEvents(
    [event(1, "task.created"), event(1, "task.completed")],
    "completed",
  );

  expect(screen.queryByRole("group", { name: "Attempt selector" })).toBeNull();
  expect(screen.queryByText("final commit")).toBeNull();
  expect(screen.queryByLabelText(/request$/)).toBeNull();
  expect(screen.getByText("base commit")).toBeDefined();
  expect(screen.getByText("aaaaaaaaaa")).toBeDefined();
  expect(screen.getByText("pull request #7 merged")).toBeDefined();
  expect(screen.getByText("status: completed")).toBeDefined();
});

test("a revision requested task carries its own badge", async () => {
  stubFetch({
    task: task({ status: "revision_requested" }),
    attempts: [
      attempt(1, { status: "active", final_commit_sha: "f".repeat(40) }),
    ],
  });
  await renderPage();
  await streamEvents([event(1, "task.created")], "revision_requested");
  const badge = screen.getAllByText("revision requested")[0];
  expect(badge.className).toContain("text-info");
});
