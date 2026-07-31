import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import RunnerPage from "./page";
import type { RunnerDetail } from "@/lib/types";

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

test("renders runner capacity, resources, and task states", async () => {
  const runner = {
    id: "88f57a9f-0cd6-4fd8-8c14-6d4710a3370f",
    hostname_or_pod: "runner-1",
    runner_type: "process",
    status: "online",
    capacity: 2,
    active_task_count: 1,
    last_heartbeat_at: "2026-07-28T12:00:00Z",
    resources: {
      cpu_percent: 42,
      memory_percent: 78,
      disk_percent: null,
    },
    current_tasks: [],
    recent_failures: [],
  } as RunnerDetail;
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(runner)));

  await act(async () => {
    render(<RunnerPage params={Promise.resolve({ runnerId: runner.id })} />);
  });

  expect(await screen.findByText("runner-1")).toBeDefined();
  expect(screen.getByText("1 of 2 slots")).toBeDefined();
  expect(
    screen.getByRole("progressbar", { name: "CPU utilization" }),
  ).toBeDefined();
  expect(screen.getByText("Not reported")).toBeDefined();
  expect(screen.getByText("No recent failures.")).toBeDefined();
});
