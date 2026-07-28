"use client";

import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { TaskRows } from "@/components/TaskRows";
import { ApiError, listTasks } from "@/lib/api";
import { formatDuration } from "@/lib/format";
import { groupTasks, overviewStats } from "@/lib/overview";
import type { Task } from "@/lib/types";

const POLL_MS = 5000;
const repoUrl = "https://github.com/chieaid24/agent-trail";

type LoadState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | { phase: "ready"; tasks: Task[] };

export default function Home() {
  const [state, setState] = useState<LoadState>({ phase: "loading" });

  const load = useCallback(async () => {
    try {
      const tasks = await listTasks({ limit: 200 });
      setState({ phase: "ready", tasks });
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : "unexpected failure";
      setState((prev) =>
        // Keep showing data through a transient poll failure.
        prev.phase === "ready" ? prev : { phase: "error", message },
      );
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), POLL_MS);
    return () => clearInterval(timer);
  }, [load]);

  return (
    <AppShell>
      <div className="px-8 py-6">
        <header className="flex items-baseline justify-between gap-6">
          <h1 className="text-lg font-semibold">Tasks</h1>
          {state.phase === "ready" && state.tasks.length > 0 && (
            <Summary tasks={state.tasks} />
          )}
        </header>
        <div className="mt-6">
          {state.phase === "loading" && <Skeleton />}
          {state.phase === "error" && (
            <ErrorNotice message={state.message} onRetry={load} />
          )}
          {state.phase === "ready" &&
            (state.tasks.length === 0 ? (
              <Empty />
            ) : (
              <Groups tasks={state.tasks} />
            ))}
        </div>
      </div>
    </AppShell>
  );
}

// One quiet sentence of computed facts; deliberately not metric cards.
function Summary({ tasks }: { tasks: Task[] }) {
  const s = overviewStats(tasks);
  const parts = [
    `${s.total} task${s.total === 1 ? "" : "s"}`,
    s.running > 0 && `${s.running} running`,
    s.review > 0 && `${s.review} awaiting review`,
    s.failed > 0 && `${s.failed} failed`,
    s.completionRate !== null &&
      `${Math.round(s.completionRate * 100)}% completed`,
    s.medianRuntimeMs !== null &&
      `median run ${formatDuration(s.medianRuntimeMs)}`,
  ].filter(Boolean);
  return <p className="text-sm text-muted">{parts.join(" · ")}</p>;
}

function Groups({ tasks }: { tasks: Task[] }) {
  const groups = groupTasks(tasks).filter((g) => g.tasks.length > 0);
  return (
    <div className="flex flex-col gap-8">
      {groups.map((g) => (
        <section key={g.key} aria-label={g.label}>
          <h2 className="flex items-baseline gap-2 border-b border-border pb-2 text-sm font-semibold text-muted">
            {g.label}
            <span className="font-normal">{g.tasks.length}</span>
          </h2>
          <TaskRows tasks={g.tasks} />
        </section>
      ))}
    </div>
  );
}

function Empty() {
  return (
    <div className="mt-24 flex justify-center">
      <div className="max-w-sm text-center">
        <p className="text-sm text-muted">
          No tasks yet. Comment{" "}
          <code className="font-mono text-foreground">/agent-trail run</code> on
          a GitHub issue to start one.
        </p>
        <a
          href={repoUrl}
          className="mt-4 inline-block text-sm text-accent underline-offset-4 hover:underline"
        >
          Read the docs
        </a>
      </div>
    </div>
  );
}

function ErrorNotice({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div className="mt-24 flex justify-center">
      <div className="max-w-sm text-center">
        <p className="text-sm text-danger">Could not load tasks: {message}.</p>
        <button
          type="button"
          onClick={onRetry}
          className="mt-4 rounded border border-border px-3 py-1.5 text-sm font-semibold hover:bg-surface"
        >
          Retry
        </button>
      </div>
    </div>
  );
}

function Skeleton() {
  return (
    <div aria-hidden className="flex animate-pulse flex-col gap-2">
      {[0, 1, 2, 3, 4].map((i) => (
        <div key={i} className="h-8 rounded bg-surface" />
      ))}
    </div>
  );
}
