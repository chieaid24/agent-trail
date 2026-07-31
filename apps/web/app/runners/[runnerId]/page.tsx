"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { DashboardTaskRows } from "@/components/DashboardTaskRows";
import { RunnerStatus } from "@/components/RunnerStatus";
import { ApiError, getRunner } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import type { RunnerDetail } from "@/lib/types";

type LoadState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | { phase: "ready"; runner: RunnerDetail };

export default function RunnerPage({
  params,
}: {
  params: Promise<{ runnerId: string }>;
}) {
  const { runnerId } = use(params);
  const [state, setState] = useState<LoadState>({ phase: "loading" });

  const load = useCallback(async () => {
    try {
      setState({ phase: "ready", runner: await getRunner(runnerId) });
    } catch (err) {
      setState({
        phase: "error",
        message: err instanceof ApiError ? err.message : "unexpected failure",
      });
    }
  }, [runnerId]);

  useEffect(() => void load(), [load]);

  return (
    <AppShell>
      <div className="px-8 py-6">
        <nav aria-label="Breadcrumb" className="text-sm text-muted">
          <Link href="/" className="hover:text-foreground">
            Overview
          </Link>
          <span aria-hidden className="px-2">
            /
          </span>
          <span>Runner</span>
        </nav>
        {state.phase === "loading" && <DetailSkeleton />}
        {state.phase === "error" && (
          <ErrorNotice message={state.message} onRetry={load} />
        )}
        {state.phase === "ready" && <RunnerView runner={state.runner} />}
      </div>
    </AppShell>
  );
}

function RunnerView({ runner }: { runner: RunnerDetail }) {
  return (
    <article className="mt-2">
      <header>
        <div className="flex items-start justify-between gap-6">
          <div className="min-w-0">
            <h1 className="truncate font-mono text-xl font-semibold">
              {runner.hostname_or_pod}
            </h1>
            <p className="mt-1 text-sm text-muted">
              {runner.runner_type} runner
            </p>
          </div>
          <RunnerStatus status={runner.status} />
        </div>

        <dl className="mt-6 grid grid-cols-2 gap-4 text-sm xl:grid-cols-3">
          <Meta
            label="capacity"
            value={`${runner.active_task_count} of ${runner.capacity} slots`}
          />
          <Meta
            label="heartbeat"
            value={formatDateTime(runner.last_heartbeat_at)}
          />
          <Meta label="runner id" value={runner.id} mono />
        </dl>
      </header>

      <section aria-labelledby="resources-heading" className="mt-8">
        <h2 id="resources-heading" className="text-base font-semibold">
          Resources
        </h2>
        <div className="mt-3 grid grid-cols-3 gap-6 border-y border-border py-4">
          <ResourceGauge label="CPU" value={runner.resources.cpu_percent} />
          <ResourceGauge
            label="Memory"
            value={runner.resources.memory_percent}
          />
          <ResourceGauge label="Disk" value={runner.resources.disk_percent} />
        </div>
      </section>

      <TaskSection
        id="current-task"
        title="Current task"
        tasks={runner.current_tasks}
        empty="No task is assigned to this runner."
      />
      <TaskSection
        id="recent-failures"
        title="Recent failures"
        tasks={runner.recent_failures}
        empty="No recent failures."
      />
    </article>
  );
}

function ResourceGauge({
  label,
  value,
}: {
  label: string;
  value: number | null;
}) {
  const tone =
    value === null
      ? "bg-muted"
      : value >= 90
        ? "bg-danger"
        : value >= 75
          ? "bg-warning"
          : "bg-success";
  return (
    <div>
      <div className="flex items-baseline justify-between text-sm">
        <span className="text-muted">{label}</span>
        <span className="font-mono">
          {value === null ? "Not reported" : `${Math.round(value)}%`}
        </span>
      </div>
      <div
        role={value === null ? undefined : "progressbar"}
        aria-label={value === null ? undefined : `${label} utilization`}
        aria-valuemin={value === null ? undefined : 0}
        aria-valuemax={value === null ? undefined : 100}
        aria-valuenow={value === null ? undefined : value}
        className="mt-2 h-1.5 overflow-hidden rounded-full bg-border"
      >
        <div
          className={`h-full rounded-full ${tone}`}
          style={{ width: `${value ?? 0}%` }}
        />
      </div>
    </div>
  );
}

function Meta({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-muted">{label}</dt>
      <dd className={`mt-1 ${mono ? "font-mono break-all" : "break-words"}`}>
        {value}
      </dd>
    </div>
  );
}

function TaskSection({
  id,
  title,
  tasks,
  empty,
}: {
  id: string;
  title: string;
  tasks: RunnerDetail["current_tasks"];
  empty: string;
}) {
  return (
    <section aria-labelledby={id} className="mt-8">
      <h2
        id={id}
        className="border-b border-border pb-2 text-base font-semibold"
      >
        {title}
      </h2>
      {tasks.length === 0 ? (
        <p className="px-2 py-6 text-sm text-muted">{empty}</p>
      ) : (
        <DashboardTaskRows tasks={tasks} />
      )}
    </section>
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
    <div className="mt-24 text-center">
      <p className="text-sm text-danger">Could not load runner: {message}.</p>
      <button
        type="button"
        onClick={onRetry}
        className="mt-4 rounded border border-border px-3 py-1.5 text-sm font-semibold hover:bg-surface"
      >
        Retry
      </button>
    </div>
  );
}

function DetailSkeleton() {
  return (
    <div aria-hidden className="mt-4 animate-pulse">
      <div className="h-7 w-2/5 rounded bg-surface" />
      <div className="mt-6 h-16 rounded bg-surface" />
      <div className="mt-8 h-20 rounded bg-surface" />
      <div className="mt-8 h-32 rounded bg-surface" />
    </div>
  );
}
