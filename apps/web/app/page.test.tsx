import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import Home from "./page";
import type { Task } from "@/lib/types";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function demoTask(overrides: Partial<Task>): Task {
  return {
    id: "3b241101-e2bb-4255-8caf-4136c566a962",
    organization_id: null,
    repository_id: null,
    source_type: "api",
    source_issue_number: null,
    source_comment_id: null,
    title: "Demo task",
    instructions: "do it",
    status: "queued",
    phase: "pending",
    priority: 0,
    base_branch: "main",
    base_commit_sha: null,
    working_branch: null,
    agent_provider: null,
    agent_model: null,
    policy_id: null,
    requested_by_user_id: null,
    max_runtime_seconds: null,
    max_cost_usd: null,
    started_at: null,
    completed_at: null,
    cancel_requested_at: null,
    failure_code: null,
    failure_message: null,
    created_at: "2026-07-28T12:00:00Z",
    updated_at: "2026-07-28T12:00:00Z",
    version: 1,
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("renders the shell and empty state when no tasks exist", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(jsonResponse({ tasks: [] })),
  );
  render(<Home />);

  expect(screen.getByText("Agent Trail")).toBeDefined();
  expect(screen.getByRole("navigation", { name: "Primary" })).toBeDefined();
  expect(await screen.findByText("/agent-trail run")).toBeDefined();

  const docs = screen.getByRole("link", { name: "Read the docs" });
  expect(docs.getAttribute("href")).toContain("github.com");
});

test("groups tasks by attention and links to detail", async () => {
  const tasks = [
    demoTask({ id: "3b241101-e2bb-4255-8caf-4136c566a901", title: "Waiting" }),
    demoTask({
      id: "3b241101-e2bb-4255-8caf-4136c566a902",
      title: "Working",
      status: "executing",
      phase: "running",
      started_at: "2026-07-28T12:01:00Z",
    }),
  ];
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ tasks })));
  render(<Home />);

  expect(await screen.findByText("Working")).toBeDefined();
  expect(screen.getByRole("region", { name: "Running" })).toBeDefined();
  expect(screen.getByRole("region", { name: "Queued" })).toBeDefined();

  const link = screen.getByRole("link", { name: /Working/ });
  expect(link.getAttribute("href")).toBe(
    "/tasks/3b241101-e2bb-4255-8caf-4136c566a902",
  );
});

test("shows the error state when the control plane is unreachable", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
  render(<Home />);

  expect(
    await screen.findByText(/Could not load tasks: control plane unreachable/),
  ).toBeDefined();
  expect(screen.getByRole("button", { name: "Retry" })).toBeDefined();
});
