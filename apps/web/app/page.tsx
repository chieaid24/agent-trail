"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { RunnerStatus } from "@/components/RunnerStatus";
import { TaskRows } from "@/components/TaskRows";
import { ApiError, listRepositories, listRunners, listTasks } from "@/lib/api";
import { formatDateTime, formatDuration } from "@/lib/format";
import { groupTasks, overviewStats } from "@/lib/overview";
import type { Repository, Runner, Task } from "@/lib/types";

const POLL_MS = 5000;
const repoUrl = "https://github.com/chieaid24/agent-trail";

type LoadState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | {
      phase: "ready";
      tasks: Task[];
      runners: Runner[];
      repositories: Repository[];
    };

export default function Home() {
  const [state, setState] = useState<LoadState>({ phase: "loading" });

  const load = useCallback(async () => {
    try {
      const [tasks, runners, repositories] = await Promise.all([
        listTasks({ limit: 200 }),
        listRunners(),
        listRepositories({ limit: 8 }),
      ]);
      setState({ phase: "ready", tasks, runners, repositories });
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
    const initial = setTimeout(() => void load(), 0);
    const timer = setInterval(() => void load(), POLL_MS);
    return () => {
      clearTimeout(initial);
      clearInterval(timer);
    };
  }, [load]);

  return (
    <AppShell>
      <div className="px-8 py-6">
        <header>
          <h1 className="text-lg font-semibold">Overview</h1>
          <p className="mt-1 text-sm text-muted">
            Live control-plane activity and execution capacity.
          </p>
        </header>
        <div className="mt-6">
          {state.phase === "loading" && <OverviewSkeleton />}
          {state.phase === "error" && (
            <ErrorNotice message={state.message} onRetry={load} />
          )}
          {state.phase === "ready" && (
            <>
              <OperationalOverview
                runners={state.runners}
                repositories={state.repositories}
              />
              <section aria-labelledby="tasks-heading" className="mt-8">
                <header className="flex items-baseline justify-between gap-6 border-b border-border pb-2">
                  <h2 id="tasks-heading" className="text-base font-semibold">
                    Tasks
                  </h2>
                  {state.tasks.length > 0 && <Summary tasks={state.tasks} />}
                </header>
                <div className="mt-4">
                  {state.tasks.length === 0 ? (
                    <Empty />
                  ) : (
                    <Groups tasks={state.tasks} />
                  )}
                </div>
              </section>
            </>
          )}
        </div>
      </div>
    </AppShell>
  );
}

function OperationalOverview({
  runners,
  repositories,
}: {
  runners: Runner[];
  repositories: Repository[];
}) {
  return (
    <div className="grid grid-cols-2 gap-8">
      <section aria-labelledby="runners-heading">
        <div className="flex items-baseline justify-between border-b border-border pb-2">
          <h2 id="runners-heading" className="text-base font-semibold">
            Runner health
          </h2>
          {runners.length > 0 && (
            <span className="text-sm text-muted">
              {runners.filter((runner) => runner.status === "online").length} of{" "}
              {runners.length} online
            </span>
          )}
        </div>
        {runners.length === 0 ? (
          <OperationalEmpty
            message="No runners registered."
            action="Start a worker"
            href={`${repoUrl}/blob/main/docs/operations/local-development.md`}
          />
        ) : (
          <ul>
            {runners.map((runner) => (
              <li key={runner.id} className="border-b border-border">
                <Link
                  href={`/runners/${runner.id}`}
                  className="grid grid-cols-[1fr_auto] gap-x-4 px-2 py-2 hover:bg-surface"
                >
                  <span className="min-w-0">
                    <span className="block truncate font-mono text-sm">
                      {runner.hostname_or_pod}
                    </span>
                    <span className="mt-0.5 block text-sm text-muted">
                      {runner.active_task_count} of {runner.capacity} slots in
                      use
                    </span>
                  </span>
                  <RunnerStatus status={runner.status} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="repositories-heading">
        <div className="flex items-baseline justify-between border-b border-border pb-2">
          <h2 id="repositories-heading" className="text-base font-semibold">
            Recent repositories
          </h2>
          {repositories.length > 0 && (
            <span className="text-sm text-muted">
              {repositories.length} synced
            </span>
          )}
        </div>
        {repositories.length === 0 ? (
          <OperationalEmpty
            message="No repositories synced."
            action="Configure the GitHub App"
            href={`${repoUrl}/blob/main/docs/architecture/github-app.md`}
          />
        ) : (
          <ul>
            {repositories.map((repository) => (
              <li key={repository.id} className="border-b border-border">
                <Link
                  href={`/repositories/${repository.id}`}
                  className="grid grid-cols-[1fr_auto] gap-x-4 px-2 py-2 hover:bg-surface"
                >
                  <span className="min-w-0">
                    <span className="block truncate font-mono text-sm">
                      {repository.full_name}
                    </span>
                    <span className="mt-0.5 block text-sm text-muted">
                      {repository.active_task_count} active,{" "}
                      {repository.recent_task_count} changed in 30 days
                    </span>
                  </span>
                  <span className="text-right text-sm text-muted">
                    {formatDateTime(repository.updated_at)}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function OperationalEmpty({
  message,
  action,
  href,
}: {
  message: string;
  action: string;
  href: string;
}) {
  return (
    <p className="px-2 py-6 text-sm text-muted">
      {message}{" "}
      <a href={href} className="text-accent hover:underline">
        {action}
      </a>
      .
    </p>
  );
}

// One quiet sentence of computed facts; deliberately not metric cards.
// At the fetch cap the figures describe a truncated window and say so.
function Summary({ tasks }: { tasks: Task[] }) {
  const s = overviewStats(tasks);
  const atCap = tasks.length >= 200;
  const parts = [
    atCap ? "newest 200 tasks" : `${s.total} task${s.total === 1 ? "" : "s"}`,
    s.running > 0 && `${s.running} running`,
    s.review > 0 && `${s.review} awaiting review`,
    s.failed > 0 && `${s.failed} failed`,
    s.completionRate !== null &&
      `${Math.round(s.completionRate * 100)}% completed`,
    s.medianRuntimeMs !== null &&
      `median run ${formatDuration(s.medianRuntimeMs)}`,
  ].filter(Boolean);
  return <p className="text-sm text-muted">{parts.join(", ")}</p>;
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
    <div className="flex justify-center py-16">
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

function OverviewSkeleton() {
  return (
    <div aria-hidden className="animate-pulse">
      <div className="grid grid-cols-2 gap-8">
        {[0, 1].map((column) => (
          <div key={column}>
            <div className="h-5 w-36 rounded bg-surface" />
            <div className="mt-3 flex flex-col gap-2">
              {[0, 1, 2].map((row) => (
                <div key={row} className="h-10 rounded bg-surface" />
              ))}
            </div>
          </div>
        ))}
      </div>
      <div className="mt-8 h-5 w-20 rounded bg-surface" />
      <div className="mt-3 flex flex-col gap-2">
        {[0, 1, 2, 3].map((row) => (
          <div key={row} className="h-8 rounded bg-surface" />
        ))}
      </div>
    </div>
  );
}
