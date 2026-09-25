import type { ActivityEvent, TaskAttempt } from "./types";

// every attempt number the page knows about, ascending; events cover a task whose
// attempts list has not loaded (or failed), the list covers attempts without events yet
export function attemptNumbers(
  attempts: TaskAttempt[],
  events: ActivityEvent[],
): number[] {
  const numbers = new Set<number>();
  for (const attempt of attempts) numbers.add(attempt.attempt_number);
  for (const event of events) numbers.add(event.attempt_number);
  return [...numbers].sort((a, b) => a - b);
}
