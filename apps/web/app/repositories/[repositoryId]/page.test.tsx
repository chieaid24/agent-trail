import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import RepositoryPage from "./page";
import type { RepositoryDetail } from "@/lib/types";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("renders repository settings, metrics, and task sections", async () => {
  const repository = {
    id: "39be2f56-3419-4a0a-a7ad-1a72698c0cc5",
    organization_id: "5b0a6f6e-3c73-4b57-9d5c-0f0f38c4a001",
    owner: "chieaid24",
    name: "agent-trail",
    full_name: "chieaid24/agent-trail",
    github_repository_id: 201,
    default_branch: "main",
    is_enabled: true,
    is_private: false,
    active_task_count: 1,
    recent_task_count: 3,
    created_at: "2026-07-28T12:00:00Z",
    updated_at: "2026-07-28T12:00:00Z",
    settings: {
      default_policy: "restricted",
      validation_file: ".agent-trail/validation.yaml",
      max_attempts: 5,
    },
    metrics: {
      total_tasks: 3,
      active_tasks: 1,
      completed_tasks: 1,
      failed_tasks: 1,
      completion_rate: 0.5,
      median_runtime_millis: 120000,
    },
    active_tasks: [],
    recent_tasks: [],
  } as RepositoryDetail;
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(repository))),
  );

  await act(async () => {
    render(
      <RepositoryPage
        params={Promise.resolve({ repositoryId: repository.id })}
      />,
    );
  });

  expect(await screen.findByText("chieaid24/agent-trail")).toBeDefined();
  expect(screen.getByText("restricted")).toBeDefined();
  expect(screen.getByText(".agent-trail/validation.yaml")).toBeDefined();
  expect(screen.getByText("revision limit")).toBeDefined();
  expect(screen.getByText("5 attempts")).toBeDefined();
  expect(screen.getByText("50%")).toBeDefined();
  expect(screen.getByText("No active tasks.")).toBeDefined();
});
