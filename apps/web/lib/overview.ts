// Pure grouping and stats for the dashboard overview. Every number is
// computed from the fetched task list; nothing is invented.

import type { Task } from "./types";

export type GroupKey = "running" | "review" | "queued" | "finished";

export interface OverviewGroup {
  key: GroupKey;
  label: string;
  tasks: Task[];
}

function groupKey(t: Task): GroupKey {
  switch (t.phase) {
    case "running":
      return "running";
    case "review":
      return "review";
    case "terminal":
      return "finished";
    default:
      return "queued";
  }
}

const mtime = (v: string | null) => (v ? new Date(v).getTime() : 0);

// groupTasks orders groups by operator attention: what runs now, what waits
// on review, what is queued, what finished.
export function groupTasks(tasks: Task[]): OverviewGroup[] {
  const groups: Record<GroupKey, Task[]> = {
    running: [],
    review: [],
    queued: [],
    finished: [],
  };
  for (const t of tasks) groups[groupKey(t)].push(t);

  groups.running.sort((a, b) => mtime(b.started_at) - mtime(a.started_at));
  groups.review.sort((a, b) => mtime(b.updated_at) - mtime(a.updated_at));
  // Queue order: higher priority first, then oldest first.
  groups.queued.sort(
    (a, b) =>
      b.priority - a.priority || mtime(a.created_at) - mtime(b.created_at),
  );
  groups.finished.sort((a, b) => mtime(b.completed_at) - mtime(a.completed_at));

  return [
    { key: "running", label: "Running", tasks: groups.running },
    { key: "review", label: "Awaiting review", tasks: groups.review },
    { key: "queued", label: "Queued", tasks: groups.queued },
    { key: "finished", label: "Finished", tasks: groups.finished },
  ];
}

export interface OverviewStats {
  total: number;
  running: number;
  review: number;
  queued: number;
  failed: number;
  // completed / (completed + failed + timed_out); cancellation is an
  // operator choice, not an outcome, so it is excluded. Null until an
  // outcome exists.
  completionRate: number | null;
  // Median runtime of finished tasks with measured bounds. Null until one
  // exists.
  medianRuntimeMs: number | null;
}

export function overviewStats(tasks: Task[]): OverviewStats {
  let running = 0;
  let review = 0;
  let queued = 0;
  let failed = 0;
  let completed = 0;
  let timedOut = 0;
  const runtimes: number[] = [];

  for (const t of tasks) {
    switch (groupKey(t)) {
      case "running":
        running++;
        break;
      case "review":
        review++;
        break;
      case "queued":
        queued++;
        break;
      case "finished":
        if (t.status === "completed") completed++;
        else if (t.status === "failed") failed++;
        else if (t.status === "timed_out") timedOut++;
        if (t.started_at && t.completed_at) {
          runtimes.push(
            new Date(t.completed_at).getTime() -
              new Date(t.started_at).getTime(),
          );
        }
        break;
    }
  }

  const outcomes = completed + failed + timedOut;
  runtimes.sort((a, b) => a - b);
  const medianRuntimeMs =
    runtimes.length === 0
      ? null
      : runtimes.length % 2 === 1
        ? runtimes[(runtimes.length - 1) / 2]
        : (runtimes[runtimes.length / 2 - 1] + runtimes[runtimes.length / 2]) /
          2;

  return {
    total: tasks.length,
    running,
    review,
    queued,
    failed: failed + timedOut,
    completionRate: outcomes === 0 ? null : completed / outcomes,
    medianRuntimeMs,
  };
}
