"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { DashboardTaskRows } from "@/components/DashboardTaskRows";
import { ApiError, getRepository } from "@/lib/api";
import { formatDateTime, formatDuration } from "@/lib/format";
import type { RepositoryDetail } from "@/lib/types";

type LoadState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | { phase: "ready"; repository: RepositoryDetail };

export default function RepositoryPage({
  params,
}: {
  params: Promise<{ repositoryId: string }>;
}) {
  const { repositoryId } = use(params);
  const [state, setState] = useState<LoadState>({ phase: "loading" });

  const load = useCallback(async () => {
    try {
      setState({
        phase: "ready",
        repository: await getRepository(repositoryId),
      });
    } catch (err) {
      setState({
        phase: "error",
        message: err instanceof ApiError ? err.message : "unexpected failure",
      });
    }
  }, [repositoryId]);

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
          <span>Repository</span>
        </nav>
        {state.phase === "loading" && <DetailSkeleton />}
        {state.phase === "error" && (
          <ErrorNotice message={state.message} onRetry={load} />
        )}
        {state.phase === "ready" && (
          <RepositoryView repository={state.repository} />
        )}
      </div>
    </AppShell>
  );
}

function RepositoryView({ repository }: { repository: RepositoryDetail }) {
  const metrics = repository.metrics;
  return (
    <article className="mt-2">
      <header>
        <div className="flex items-start justify-between gap-6">
          <div className="min-w-0">
            <h1 className="truncate font-mono text-xl font-semibold">
              {repository.full_name}
            </h1>
            <p className="mt-1 text-sm text-muted">
              GitHub repository {repository.github_repository_id}
            </p>
          </div>
          <div className="flex gap-2">
            <FactBadge
              label={repository.is_enabled ? "enabled" : "disabled"}
              tone={repository.is_enabled ? "success" : "muted"}
            />
            <FactBadge label={repository.is_private ? "private" : "public"} />
          </div>
        </div>

        <dl className="mt-6 grid grid-cols-2 gap-4 text-sm xl:grid-cols-4">
          <Meta label="default branch" value={repository.default_branch} mono />
          <Meta
            label="default policy"
            value={repository.settings.default_policy}
          />
          <Meta
            label="validation"
            value={repository.settings.validation_file}
            mono
          />
          <Meta label="synced" value={formatDateTime(repository.updated_at)} />
        </dl>
      </header>

      <section aria-labelledby="metrics-heading" className="mt-8">
        <h2 id="metrics-heading" className="text-base font-semibold">
          Repository metrics
        </h2>
        <dl className="mt-3 grid grid-cols-5 border-y border-border">
          <Metric label="total tasks" value={String(metrics.total_tasks)} />
          <Metric label="active" value={String(metrics.active_tasks)} />
          <Metric label="completed" value={String(metrics.completed_tasks)} />
          <Metric label="failed" value={String(metrics.failed_tasks)} />
          <Metric
            label="completion"
            value={
              metrics.completion_rate === null
                ? "No terminal tasks"
                : `${Math.round(metrics.completion_rate * 100)}%`
            }
            detail={
              metrics.median_runtime_millis === null
                ? undefined
                : `median ${formatDuration(metrics.median_runtime_millis)}`
            }
          />
        </dl>
      </section>

      <TaskSection
        id="active-tasks"
        title="Active tasks"
        tasks={repository.active_tasks}
        empty="No active tasks."
      />
      <TaskSection
        id="recent-tasks"
        title="Recent tasks"
        tasks={repository.recent_tasks}
        empty="No task history for this repository."
      />
    </article>
  );
}

function FactBadge({
  label,
  tone = "muted",
}: {
  label: string;
  tone?: "success" | "muted";
}) {
  const classes =
    tone === "success"
      ? "border-success/40 bg-success/10 text-success"
      : "border-border bg-surface text-muted";
  return (
    <span className={`rounded border px-2 py-1 text-sm ${classes}`}>
      {label}
    </span>
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

function Metric({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="border-r border-border px-3 py-3 last:border-r-0">
      <dt className="text-sm text-muted">{label}</dt>
      <dd className="mt-1 text-base font-semibold">{value}</dd>
      {detail && <dd className="mt-0.5 text-sm text-muted">{detail}</dd>}
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
  tasks: RepositoryDetail["active_tasks"];
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
      <p className="text-sm text-danger">
        Could not load repository: {message}.
      </p>
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
