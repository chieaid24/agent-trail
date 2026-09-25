import { expect, test } from "vitest";
import { attemptNumbers } from "./attempts";
import type { ActivityEvent, TaskAttempt } from "./types";

function attempt(n: number): TaskAttempt {
  return { attempt_number: n } as TaskAttempt;
}

function event(n: number): ActivityEvent {
  return { attempt_number: n } as ActivityEvent;
}

test("unions attempts and events, sorted and deduplicated", () => {
  expect(
    attemptNumbers([attempt(2), attempt(1)], [event(1), event(3)]),
  ).toEqual([1, 2, 3]);
  expect(attemptNumbers([], [])).toEqual([]);
  expect(attemptNumbers([], [event(2), event(2)])).toEqual([2]);
});
