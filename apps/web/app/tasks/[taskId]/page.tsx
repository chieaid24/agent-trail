"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { AttemptSelector } from "@/components/AttemptSelector";
import { CancelButton } from "@/components/CancelButton";
import { ConflictWarning } from "@/components/ConflictWarning";
import { EvidencePanel } from "@/components/EvidencePanel";
import { InsightsPanel } from "@/components/InsightsPanel";
import { LogViewer } from "@/components/LogViewer";
import { StatusBadge } from "@/components/StatusBadge";
import { Timeline } from "@/components/Timeline";
import { TraceWaterfall, type TraceState } from "@/components/TraceWaterfall";
import { ValidationList } from "@/components/ValidationList";
import {
  ApiError,
  getEvidence,
  getTask,
  getTaskTrace,
  listAttempts,
  listConflicts,
  listValidations,
} from "@/lib/api";
import { attemptNumbers } from "@/lib/attempts";
import { aggregateCost, type CostSummary } from "@/lib/cost";
import {
  formatDateTime,
  formatDuration,
  outcomeReason,
  runtimeMs,
  shortSha,
} from "@/lib/format";
import { changedFiles, latestPlan } from "@/lib/timeline";
import { useTaskInsights } from "@/lib/useTaskInsights";
import { useTaskStream, type StreamState } from "@/lib/useTaskStream";
import type {
  StoredEvidence,
  Task,
  TaskAttempt,
  TaskConflict,
  ValidationResult,
} from "@/lib/types";
import { isTerminal } from "@/lib/types";

const TASK_POLL_MS = 10_000;

type TabKey =
  "timeline" | "trace" | "logs" | "validations" | "evidence" | "files";

type TaskState =
  | { phase: "loading" }
  | { phase: "error"; message: string }
  | { phase: "ready"; task: Task };

type ConflictState =
  | { phase: "loading" }
  | { phase: "ready"; items: TaskConflict[] }
  | { phase: "error" };

type AttemptsState =
  | { phase: "loading" }
  | { phase: "ready"; items: TaskAttempt[] }
  | { phase: "error" };

const ATTEMPTS_LOADING: AttemptsState = { phase: "loading" };

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
  const [conflicts, setConflicts] = useState<ConflictState>({
    phase: "loading",
  });
  const [trace, setTrace] = useState<TraceState>({ phase: "loading" });
  const [tab, setTab] = useState<TabKey>("timeline");
  // both keyed by task so navigating between tasks never shows the previous task's attempts
  const [attempts, setAttempts] = useState<{
    taskId: string;
    state: AttemptsState;
  } | null>(null);
  const [chosen, setChosen] = useState<{
    taskId: string;
    attempt: number;
  } | null>(null);
  const stream = useTaskStream(taskId);
  const running = state.phase === "ready" && !isTerminal(state.task.status);
  const { state: insights, retry: retryInsights } = useTaskInsights(
    taskId,
    stream.events.length,
    stream.state === "done",
    running,
  );

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
      // non-fatal; retried on next trigger
    }
  }, [taskId]);

  const attemptsState =
    attempts?.taskId === taskId ? attempts.state : ATTEMPTS_LOADING;
  const attemptItems = useMemo(
    () => (attemptsState.phase === "ready" ? attemptsState.items : []),
    [attemptsState],
  );
  const numbers = useMemo(
    () => attemptNumbers(attemptItems, stream.events),
    [attemptItems, stream.events],
  );
  const latestAttempt = numbers.length > 0 ? numbers[numbers.length - 1] : null;
  const chosenAttempt =
    chosen?.taskId === taskId && numbers.includes(chosen.attempt)
      ? chosen.attempt
      : null;
  // the latest attempt is selected until the user picks another one
  const selectedAttempt = chosenAttempt ?? latestAttempt;
  const selectedRef = useRef<number | null>(null);
  useEffect(() => {
    selectedRef.current = selectedAttempt;
  }, [selectedAttempt]);
  const selectAttempt = useCallback(
    (attempt: number) =>
      setChosen(attempt === latestAttempt ? null : { taskId, attempt }),
    [taskId, latestAttempt],
  );

  const loadAttempts = useCallback(async () => {
    try {
      const items = await listAttempts(taskId);
      if (!Array.isArray(items)) throw new Error("malformed attempts list");
      setAttempts({ taskId, state: { phase: "ready", items } });
    } catch {
      setAttempts((prev) =>
        prev?.taskId === taskId && prev.state.phase === "ready"
          ? prev
          : { taskId, state: { phase: "error" } },
      );
    }
  }, [taskId]);

  // evidence and trace are attempt-scoped reads; a response for a stale selection is dropped
  const loadEvidence = useCallback(async () => {
    const attempt = selectedRef.current ?? undefined;
    try {
      const result = await getEvidence(taskId, attempt);
      if ((selectedRef.current ?? undefined) === attempt) setEvidence(result);
    } catch {
      // non-fatal
    }
  }, [taskId]);

  const loadConflicts = useCallback(async () => {
    try {
      setConflicts({ phase: "ready", items: await listConflicts(taskId) });
    } catch {
      setConflicts({ phase: "error" });
    }
  }, [taskId]);

  const loadTrace = useCallback(async () => {
    const attempt = selectedRef.current ?? undefined;
    try {
      const result = await getTaskTrace(taskId, attempt);
      if ((selectedRef.current ?? undefined) === attempt) {
        setTrace({ phase: "ready", spans: result.spans });
      }
    } catch {
      setTrace({ phase: "error" });
    }
  }, [taskId]);

  useEffect(() => {
    const initial = setTimeout(() => {
      void loadTask();
      void loadValidations();
      void loadConflicts();
      void loadAttempts();
    }, 0);
    return () => clearTimeout(initial);
  }, [loadTask, loadValidations, loadConflicts, loadAttempts]);

  // wait for the attempts list so the first read already targets the latest attempt
  const attemptsSettled = attemptsState.phase !== "loading";
  useEffect(() => {
    if (!attemptsSettled) return;
    const refresh = setTimeout(() => {
      void loadEvidence();
      void loadTrace();
    }, 0);
    return () => clearTimeout(refresh);
  }, [attemptsSettled, selectedAttempt, loadEvidence, loadTrace]);

  const taskEventCount = stream.events.filter((e) =>
    e.event_type.startsWith("task."),
  ).length;
  const validationEventCount = stream.events.filter((e) =>
    e.event_type.startsWith("validation."),
  ).length;
  const evidenceEventCount = stream.events.filter(
    (e) => e.event_type === "evidence.generated",
  ).length;
  const conflictEventCount = stream.events.filter(
    (e) => e.event_type === "conflict.detected",
  ).length;

  useEffect(() => {
    if (taskEventCount === 0) return;
    const refresh = setTimeout(
      () => void Promise.all([loadTask(), loadConflicts(), loadAttempts()]),
      0,
    );
    return () => clearTimeout(refresh);
  }, [taskEventCount, loadTask, loadConflicts, loadAttempts]);
  useEffect(() => {
    if (validationEventCount === 0) return;
    const refresh = setTimeout(() => void loadValidations(), 0);
    return () => clearTimeout(refresh);
  }, [validationEventCount, loadValidations]);
  useEffect(() => {
    if (evidenceEventCount === 0) return;
    const refresh = setTimeout(() => void loadEvidence(), 0);
    return () => clearTimeout(refresh);
  }, [evidenceEventCount, loadEvidence]);
  useEffect(() => {
    if (conflictEventCount === 0) return;
    const refresh = setTimeout(() => void loadConflicts(), 0);
    return () => clearTimeout(refresh);
  }, [conflictEventCount, loadConflicts]);

  useEffect(() => {
    if (stream.state !== "done") return;
    let refresh: ReturnType<typeof setTimeout>;
    let remaining = 6;
    const poll = () => {
      void loadTrace();
      remaining -= 1;
      if (remaining > 0) refresh = setTimeout(poll, 1000);
    };
    refresh = setTimeout(poll, 1000);
    return () => clearTimeout(refresh);
  }, [stream.state, loadTrace]);

  // poll for sibling publishes; they never reach this task's stream
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => {
      void loadTask();
      void loadConflicts();
      void loadAttempts();
      void loadTrace();
    }, TASK_POLL_MS);
    return () => clearInterval(t);
  }, [running, loadTask, loadConflicts, loadAttempts, loadTrace]);

  // the tabs follow the selected attempt; cost stays task-wide with its per-attempt breakdown
  const attemptEvents = useMemo(
    () =>
      selectedAttempt === null
        ? stream.events
        : stream.events.filter((e) => e.attempt_number === selectedAttempt),
    [stream.events, selectedAttempt],
  );
  const attemptValidations = useMemo(
    () =>
      selectedAttempt === null
        ? validations
        : validations.filter((v) => v.attempt_number === selectedAttempt),
    [validations, selectedAttempt],
  );
  const selected = attemptItems.find(
    (a) => a.attempt_number === selectedAttempt,
  );
  const files = useMemo(() => changedFiles(attemptEvents), [attemptEvents]);
  const plan = useMemo(() => latestPlan(attemptEvents), [attemptEvents]);
  const cost = useMemo(() => aggregateCost(stream.events), [stream.events]);

  return (
    <AppShell>
      <div className="px-4 py-6 sm:px-8">
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
            conflicts={conflicts}
            cost={cost}
            attemptNumbers={numbers}
            selectedAttempt={selectedAttempt}
            attempt={selected}
            onSelectAttempt={selectAttempt}
            onRetryConflicts={loadConflicts}
            onTaskChanged={(t) => setState({ phase: "ready", task: t })}
          >
            <InsightsPanel
              state={insights}
              onRetry={retryInsights}
              selectedAttempt={selectedAttempt}
            />
            <div className="mt-6">
              <TabBar
                tab={tab}
                onSelect={setTab}
                counts={{
                  timeline: attemptEvents.length,
                  trace: trace.phase === "ready" ? trace.spans.length : null,
                  logs: null,
                  validations: attemptValidations.length,
                  evidence: null,
                  files: files.length,
                }}
              />
              <div role="tabpanel" className="mt-4">
                {tab === "timeline" && <Timeline events={attemptEvents} />}
                {tab === "trace" && <TraceWaterfall state={trace} />}
                {tab === "logs" && <LogViewer events={attemptEvents} />}
                {tab === "validations" && (
                  <ValidationList results={attemptValidations} />
                )}
                {tab === "evidence" && <EvidencePanel evidence={evidence} />}
                {tab === "files" && <FileList files={files} />}
              </div>
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
  conflicts,
  cost,
  attemptNumbers,
  selectedAttempt,
  attempt,
  onSelectAttempt,
  onRetryConflicts,
  onTaskChanged,
  children,
}: {
  task: Task;
  streamState: StreamState;
  plan: string | null;
  conflicts: ConflictState;
  cost: CostSummary;
  attemptNumbers: number[];
  selectedAttempt: number | null;
  attempt: TaskAttempt | undefined;
  onSelectAttempt: (attempt: number) => void;
  onRetryConflicts: () => void;
  onTaskChanged: (t: Task) => void;
  children: React.ReactNode;
}) {
  const terminal = isTerminal(task.status);
  const now = useNow(1000, !terminal);
  const runtime = runtimeMs(task.started_at, task.completed_at);
  const reason = outcomeReason(task);
  // attempt 1 inherits the task's base and instructions; a revision records its own
  const multiAttempt = attemptNumbers.length > 1;
  const baseCommit = attempt?.base_commit_sha ?? task.base_commit_sha;
  void now; // tick forces re-render for live runtime

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
        <div className="mt-2 flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <StatusBadge status={task.status} />
          <StreamChip state={streamState} />
          {reason && (
            <span className="max-w-[72ch] text-sm break-words text-muted">
              {reason}
            </span>
          )}
          {task.cancel_requested_at && !terminal && (
            <span className="text-sm text-warning">
              cancellation requested {formatDateTime(task.cancel_requested_at)}
            </span>
          )}
        </div>
        {task.status === "failed" || task.status === "timed_out" ? (
          <p className="mt-3 max-w-[72ch] text-sm break-words text-danger">
            {task.failure_code && (
              <span className="font-mono">{task.failure_code}: </span>
            )}
            {task.failure_message ?? "No failure detail was recorded."}
          </p>
        ) : null}
        <AttemptSelector
          numbers={attemptNumbers}
          selected={selectedAttempt}
          attempt={attempt}
          onSelect={onSelectAttempt}
        />
        <div className="mt-4 h-40 xl:h-32">
          {conflicts.phase === "loading" && (
            <p className="h-full rounded border border-border px-3 py-3 text-sm text-muted">
              Checking task overlaps...
            </p>
          )}
          {conflicts.phase === "error" && (
            <div
              role="alert"
              className="flex h-full items-start gap-3 rounded border border-border px-3 py-2 text-sm"
            >
              <span className="py-1 text-warning">
                Conflict warnings unavailable.
              </span>
              <button
                type="button"
                onClick={onRetryConflicts}
                className="rounded px-2 py-1 font-semibold text-accent hover:bg-surface hover:underline"
              >
                Retry
              </button>
            </div>
          )}
          {conflicts.phase === "ready" && conflicts.items.length === 0 && (
            <div className="h-full rounded border border-border px-3 py-3 text-sm">
              <p className="text-muted">No active task overlaps detected.</p>
              <Link
                href="/"
                className="mt-2 inline-block rounded px-2 py-1 font-semibold text-accent hover:bg-surface hover:underline"
              >
                View active tasks
              </Link>
            </div>
          )}
          {conflicts.phase === "ready" && conflicts.items.length > 0 && (
            <ConflictWarning conflicts={conflicts.items} />
          )}
        </div>

        <dl className="mt-4 grid grid-cols-[auto_minmax(0,1fr)] sm:grid-cols-[auto_1fr_auto_1fr] gap-x-4 gap-y-1 text-sm lg:grid-cols-[auto_1fr_auto_1fr_auto_1fr]">
          {task.source_issue_number !== null && (
            <Meta label="issue" value={`#${task.source_issue_number}`} />
          )}
          <Meta label="base" value={task.base_branch} mono />
          {baseCommit && (
            <Meta label="base commit" value={shortSha(baseCommit)} mono />
          )}
          {multiAttempt && (
            <Meta
              label="final commit"
              value={
                attempt?.final_commit_sha
                  ? shortSha(attempt.final_commit_sha)
                  : "not published"
              }
              mono={attempt?.final_commit_sha !== null}
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
          <Meta
            label="cost"
            value={
              cost.attempts.length > 0
                ? `$${cost.totalUsd.toFixed(4)}`
                : "not reported"
            }
          />
          {task.max_cost_usd !== null && (
            <Meta label="cost cap" value={`$${task.max_cost_usd.toFixed(2)}`} />
          )}
          <Meta label="created" value={formatDateTime(task.created_at)} />
        </dl>
        {cost.attempts.length > 0 && (
          <p aria-label="Cost breakdown" className="mt-2 text-xs text-muted">
            {cost.attempts
              .map(
                (attempt) =>
                  `attempt ${attempt.attemptNumber}: $${attempt.totalUsd.toFixed(4)}`,
              )
              .join(" / ")}
          </p>
        )}

        <Instructions text={attempt?.instructions ?? task.instructions} />
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
  trace: "Trace",
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
    <div
      role="tablist"
      className="flex gap-1 overflow-x-auto border-b border-border"
    >
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
