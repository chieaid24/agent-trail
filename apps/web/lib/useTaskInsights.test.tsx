import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { getTaskInsights } from "./api";
import type { TaskInsights } from "./types";
import { useTaskInsights } from "./useTaskInsights";

vi.mock("./api", () => ({ getTaskInsights: vi.fn() }));
const getInsights = vi.mocked(getTaskInsights);

function insights(taskId: string): TaskInsights {
  return {
    task_id: taskId,
    task_status: "completed",
    overall: {
      attempt_count: 0,
      event_count: 0,
      total_runtime_ms: null,
      validation: null,
      cost: null,
    },
    attempts: [],
  };
}

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  vi.useRealTimers();
});

test("loads insights and retries a failed request", async () => {
  getInsights.mockRejectedValueOnce(new Error("unavailable"));
  getInsights.mockResolvedValue(insights("task-a"));
  const { result } = renderHook(() =>
    useTaskInsights("task-a", 0, false, false),
  );
  expect(result.current.state.phase).toBe("loading");
  await waitFor(() => expect(result.current.state.phase).toBe("error"));
  act(() => result.current.retry());
  await waitFor(() => expect(result.current.state.phase).toBe("ready"));
  expect(getInsights).toHaveBeenCalledTimes(2);
});

test("discards stale responses when navigating to another task", async () => {
  let resolveOld: (value: TaskInsights) => void = () => {};
  getInsights.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveOld = resolve;
      }),
  );
  getInsights.mockResolvedValue(insights("task-b"));
  const { result, rerender } = renderHook(
    ({ id }) => useTaskInsights(id, 0, false, false),
    { initialProps: { id: "task-a" } },
  );
  rerender({ id: "task-b" });
  await waitFor(() =>
    expect(result.current.state).toEqual({
      phase: "ready",
      insights: insights("task-b"),
    }),
  );
  await act(async () => resolveOld(insights("task-a")));
  expect(result.current.state).toEqual({
    phase: "ready",
    insights: insights("task-b"),
  });
});

test("coalesces stream refreshes behind an in-flight request", async () => {
  vi.useFakeTimers();
  let resolveInitial: (value: TaskInsights) => void = () => {};
  getInsights.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveInitial = resolve;
      }),
  );
  const updated = { ...insights("task-a"), task_status: "failed" as const };
  getInsights.mockResolvedValue(updated);
  const { result, rerender } = renderHook(
    ({ events }) => useTaskInsights("task-a", events, false, false),
    { initialProps: { events: 0 } },
  );
  rerender({ events: 1 });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250);
  });
  rerender({ events: 2 });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(getInsights).toHaveBeenCalledTimes(1);
  await act(async () => resolveInitial(insights("task-a")));
  expect(getInsights).toHaveBeenCalledTimes(2);
  expect(result.current.state).toEqual({ phase: "ready", insights: updated });
});

test("polls running tasks and drains completion spans before stopping", async () => {
  vi.useFakeTimers();
  getInsights.mockResolvedValue(insights("task-a"));
  const { rerender, unmount } = renderHook(
    ({ running, done }) => useTaskInsights("task-a", 0, done, running),
    { initialProps: { running: true, done: false } },
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000);
  });
  expect(getInsights).toHaveBeenCalledTimes(2);
  rerender({ running: false, done: true });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(6_000);
  });
  expect(getInsights).toHaveBeenCalledTimes(8);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20_000);
  });
  expect(getInsights).toHaveBeenCalledTimes(8);
  unmount();
  expect(vi.getTimerCount()).toBe(0);
});

test("renders an initial response while stream events keep arriving", async () => {
  vi.useFakeTimers();
  let resolveInitial: (value: TaskInsights) => void = () => {};
  getInsights.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveInitial = resolve;
      }),
  );
  getInsights.mockResolvedValue(insights("task-a"));
  const { result, rerender } = renderHook(
    ({ events }) => useTaskInsights("task-a", events, false, true),
    { initialProps: { events: 0 } },
  );
  for (let events = 1; events <= 10; events++) {
    rerender({ events });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20);
    });
  }
  expect(getInsights).toHaveBeenCalledTimes(1);
  await act(async () => resolveInitial(insights("task-a")));
  expect(result.current.state.phase).toBe("ready");
  rerender({ events: 11 });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(getInsights).toHaveBeenCalledTimes(2);
  expect(result.current.state.phase).toBe("ready");
});

test("keeps rendering responses when events arrive faster than the API", async () => {
  vi.useFakeTimers();
  getInsights.mockImplementation(
    () =>
      new Promise((resolve) => {
        setTimeout(() => resolve(insights("task-a")), 600);
      }),
  );
  const { result, rerender } = renderHook(
    ({ events }) => useTaskInsights("task-a", events, false, true),
    { initialProps: { events: 0 } },
  );
  for (let events = 1; events <= 12; events++) {
    rerender({ events });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    if (events >= 2) expect(result.current.state.phase).toBe("ready");
  }
  expect(getInsights).toHaveBeenCalledTimes(7);
});
