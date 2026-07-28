"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useMemo, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { CancelButton } from "@/components/CancelButton";
import { EvidencePanel } from "@/components/EvidencePanel";
import { LogViewer } from "@/components/LogViewer";
import { StatusBadge } from "@/components/StatusBadge";
import { Timeline } from "@/components/Timeline";
import { ValidationList } from "@/components/ValidationList";
import { ApiError, getEvidence, getTask, listValidations } from "@/lib/api";
import {
  formatDateTime,
  formatDuration,
  runtimeMs,
  shortSha,
} from "@/lib/format";
import { changedFiles, latestPlan } from "@/lib/timeline";
import { useTaskStream, type StreamState } from "@/lib/useTaskStream";
import type { StoredEvidence, Task, ValidationResult } from "@/lib/types";
import { isTerminal } from "@/lib/types";

const TASK_POLL_MS = 10_000;

type TabKey = "timeline" | "logs" | "validations" | "evidence" | "files";

type TaskState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | { phase: "ready"; task: Task };

// Re-render clock for live runtimes.
function useNow(intervalMs: number, enabled: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!enabled) return;
    const t = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs, enabled]);
  return now;
}

export default function TaskPage({
  params,
}: {
  params: Promise<{ taskId: string }>;
}) {
  const { taskId } = use(params);
  const [state, setState] = useState<TaskState>({ phase: "loading" });
  const [validations, setValidations] = useState<ValidationResult[]>([]);
  const [evidence, setEvidence] = useState<StoredEvidence | null>(null);
  const [tab, setTab] = useState<TabKey>("timeline");
  const stream = useTaskStream(taskId);

  const loadTask = useCallback(async () => {
    try {
      const task = await getTask(taskId);
      setState({ phase: "ready", task });
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : "unexpected failure";
      setState((prev) =>
        prev.phase === "ready" ? prev : { phase: "error", message },
      );
    }
  }, [taskId]);

  const loadValidations = useCallback(async () => {
    try {
      setValidations(await listValidations(taskId));
    } catch {
      // The timeline still tells the story; retried on the next trigger.
    }
  }, [taskId]);

  const loadEvidence = useCallback(async () => {
    try {
      setEvidence(await getEvidence(taskId));
    } catch {
      // Same: non-fatal, retried on the next trigger.
    }
  }, [taskId]);

  useEffect(() => {
    void loadTask();
    void loadValidations();
    void loadEvidence();
  }, [loadTask, loadValidations, loadEvidence]);

  // The stream drives freshness: a lifecycle event refetches the task, a
  // validation or evidence event refetches its view.
  const taskEventCount = stream.events.filter((e) =>
    e.event_type.startsWith("task."),
  ).length;
  const validationEventCount = stream.events.filter((e) =>
    e.event_type.startsWith("validation."),
  ).length;
  const evidenceEventCount = stream.events.filter(
    (e) => e.event_type === "evidence.generated",
  ).length;

  useEffect(() => {
    if (taskEventCount > 0) void loadTask();
  }, [taskEventCount, loadTask]);
  useEffect(() => {
    if (validationEventCount > 0) void loadValidations();
  }, [validationEventCount, loadValidations]);
  useEffect(() => {
    if (evidenceEventCount > 0) void loadEvidence();
  }, [evidenceEventCount, loadEvidence]);

  // Poll fallback while the task runs, in case the stream stalls.
  const running = state.phase === "ready" && !isTerminal(state.task.status);
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void loadTask(), TASK_POLL_MS);
    return () => clearInterval(t);
  }, [running, loadTask]);

  const files = useMemo(() => changedFiles(stream.events), [stream.events]);
  const plan = useMemo(() => latestPlan(stream.events), [stream.events]);

  return (
    <AppShell>
      <div className="px-8 py-6">
        <nav aria-label="Breadcrumb" className="text-sm text-muted">
          <Link href="/" className="hover:text-foreground">
            Tasks
          </Link>
        </nav>
        {state.phase === "loading" && <DetailSkeleton />}
        {state.phase === "error" && (
          <ErrorNotice message={state.message} onRetry={loadTask} />
        )}
        {state.phase === "ready" && (
          <TaskDetail
            task={state.task}
            streamState={stream.state}
            plan={plan}
            onTaskChanged={(t) => setState({ phase: "ready", task: t })}
          >
            <TabBar
              tab={tab}
              onSelect={setTab}
              counts={{
                timeline: stream.events.length,
                logs: null,
                validations: validations.length,
                evidence: null,
                files: files.length,
              }}
            />
            <div role="tabpanel" className="mt-4">
              {tab === "timeline" && <Timeline events={stream.events} />}
              {tab === "logs" && <LogViewer events={stream.events} />}
              {tab === "validations" && (
                <ValidationList results={validations} />
              )}
              {tab === "evidence" && <EvidencePanel evidence={evidence} />}
              {tab === "files" && <FileList files={files} />}
            </div>
          </TaskDetail>
        )}
      </div>
    </AppShell>
  );
}

function TaskDetail({
  task,
  streamState,
  plan,
  onTaskChanged,
  children,
}: {
  task: Task;
  streamState: StreamState;
  plan: string | null;
  onTaskChanged: (t: Task) => void;
  children: React.ReactNode;
}) {
  const terminal = isTerminal(task.status);
  const now = useNow(1000, !terminal);
  const runtime = runtimeMs(task.started_at, task.completed_at);
  void now; // ticking re-render keeps the runtime fresh

  return (
    <article>
      <header className="mt-2">
        <div className="flex items-start justify-between gap-6">
          <h1 className="min-w-0 text-xl font-semibold break-words">
            {task.title}
          </h1>
          {!terminal && (
            <CancelButton task={task} onCancelled={onTaskChanged} />
          )}
        </div>
        <div className="mt-2 flex items-baseline gap-4">
          <StatusBadge status={task.status} />
          <StreamChip state={streamState} />
          {task.cancel_requested_at && !terminal && (
            <span className="text-sm text-warning">
              cancellation requested {formatDateTime(task.cancel_requested_at)}
            </span>
          )}
        </div>
        {task.status === "failed" || task.status === "timed_out" ? (
          <p className="mt-3 max-w-[72ch] text-sm text-danger">
            {task.failure_code && (
              <span className="font-mono">{task.failure_code}: </span>
            )}
            {task.failure_message ?? "No failure detail was recorded."}
          </p>
        ) : null}

        <dl className="mt-4 grid grid-cols-[auto_1fr_auto_1fr] gap-x-4 gap-y-1 text-sm lg:grid-cols-[auto_1fr_auto_1fr_auto_1fr]">
          {task.source_issue_number !== null && (
            <Meta label="issue" value={`#${task.source_issue_number}`} />
          )}
          <Meta label="base" value={task.base_branch} mono />
          {task.base_commit_sha && (
            <Meta
              label="base commit"
              value={shortSha(task.base_commit_sha)}
              mono
            />
          )}
          {task.working_branch && (
            <Meta label="branch" value={task.working_branch} mono />
          )}
          {task.agent_provider && (
            <Meta
              label="agent"
              value={`${task.agent_provider}${task.agent_model ? ` / ${task.agent_model}` : ""}`}
            />
          )}
          {runtime !== null && (
            <Meta label="runtime" value={formatDuration(runtime)} />
          )}
          {task.max_cost_usd !== null && (
            <Meta label="cost cap" value={`$${task.max_cost_usd.toFixed(2)}`} />
          )}
          <Meta label="created" value={formatDateTime(task.created_at)} />
        </dl>

        <Instructions text={task.instructions} />
        {plan && (
          <details className="mt-3 max-w-[72ch]">
            <summary className="cursor-pointer text-sm font-semibold text-muted hover:text-foreground">
              Agent plan
            </summary>
            <p className="mt-1 text-sm whitespace-pre-wrap text-foreground">
              {plan}
            </p>
          </details>
        )}
      </header>

      <div className="mt-8">{children}</div>
    </article>
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
    <>
      <dt className="text-muted">{label}</dt>
      <dd
        className={`min-w-0 truncate text-foreground ${mono ? "font-mono" : ""}`}
      >
        {value}
      </dd>
    </>
  );
}

function Instructions({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const long = text.split("\n").length > 4 || text.length > 400;
  return (
    <div className="mt-4 max-w-[72ch]">
      <h2 className="text-sm font-semibold text-muted">Instructions</h2>
      <p
        className={`mt-1 text-base whitespace-pre-wrap text-foreground ${
          long && !expanded ? "line-clamp-4" : ""
        }`}
      >
        {text}
      </p>
      {long && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-sm font-semibold text-accent hover:underline"
        >
          {expanded ? "Show less" : "Show all"}
        </button>
      )}
    </div>
  );
}

const STREAM_LABELS: Record<StreamState, { text: string; className: string }> =
  {
    connecting: { text: "connecting", className: "text-muted" },
    live: { text: "live", className: "text-accent" },
    reconnecting: { text: "reconnecting", className: "text-warning" },
    done: { text: "stream ended", className: "text-muted" },
  };

function StreamChip({ state }: { state: StreamState }) {
  const { text, className } = STREAM_LABELS[state];
  return <span className={`text-sm ${className}`}>{text}</span>;
}

const TAB_LABELS: Record<TabKey, string> = {
  timeline: "Timeline",
  logs: "Logs",
  validations: "Validations",
  evidence: "Evidence",
  files: "Files",
};

function TabBar({
  tab,
  onSelect,
  counts,
}: {
  tab: TabKey;
  onSelect: (t: TabKey) => void;
  counts: Record<TabKey, number | null>;
}) {
  return (
    <div role="tablist" className="flex gap-1 border-b border-border">
      {(Object.keys(TAB_LABELS) as TabKey[]).map((key) => {
        const active = key === tab;
        return (
          <button
            key={key}
            role="tab"
            type="button"
            aria-selected={active}
            onClick={() => onSelect(key)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm font-semibold ${
              active
                ? "border-accent text-foreground"
                : "border-transparent text-muted hover:text-foreground"
            }`}
          >
            {TAB_LABELS[key]}
            {counts[key] !== null && counts[key] > 0 && (
              <span className="ml-1.5 font-normal text-muted">
                {counts[key]}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

function FileList({ files }: { files: string[] }) {
  if (files.length === 0) {
    return (
      <p className="py-8 text-sm text-muted">No file changes recorded yet.</p>
    );
  }
  return (
    <ul className="mt-2 font-mono text-sm">
      {files.map((f) => (
        <li key={f} className="border-b border-border py-1.5 break-all">
          {f}
        </li>
      ))}
    </ul>
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
        <p className="text-sm text-danger">Could not load task: {message}.</p>
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

function DetailSkeleton() {
  return (
    <div aria-hidden className="mt-4 animate-pulse">
      <div className="h-7 w-2/5 rounded bg-surface" />
      <div className="mt-3 h-4 w-24 rounded bg-surface" />
      <div className="mt-6 flex flex-col gap-2">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="h-6 rounded bg-surface" />
        ))}
      </div>
    </div>
  );
}
