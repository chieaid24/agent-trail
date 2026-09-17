import { StatusBadge } from "./StatusBadge";
import { formatDuration, statusLabel } from "@/lib/format";
import type {
  AttemptInsight,
  AttemptStatus,
  CostSummary,
  TaskInsights,
  TaskStatus,
  ValidationSummary,
} from "@/lib/types";

export type InsightsState =
  | { phase: "loading" }
  | { phase: "ready"; insights: TaskInsights }
  | { phase: "error" };

const NOT_RECORDED = "-";

const ATTEMPT_TONES: Record<AttemptStatus, string> = {
  active: "text-accent",
  superseded: "text-muted",
  completed: "text-success",
  failed: "text-danger",
  cancelled: "text-muted",
  timed_out: "text-danger",
};

type DurationKey =
  | "queue_wait_ms"
  | "provisioning_ms"
  | "agent_session_ms"
  | "validation_ms"
  | "publishing_ms"
  | "total_runtime_ms";

const DURATION_COLUMNS: {
  key: DurationKey;
  label: string;
  definition: string;
}[] = [
  {
    key: "queue_wait_ms",
    label: "Queue wait",
    definition: "Attempt created until a runner started it",
  },
  {
    key: "provisioning_ms",
    label: "Provisioning",
    definition: "Sum of runner.provisioning spans",
  },
  {
    key: "agent_session_ms",
    label: "Agent",
    definition: "Sum of agent.session spans",
  },
  {
    key: "validation_ms",
    label: "Validation",
    definition: "Sum of validation.run spans",
  },
  {
    key: "publishing_ms",
    label: "Publishing",
    definition: "task.publishing until task.awaiting_review",
  },
  {
    key: "total_runtime_ms",
    label: "Runtime",
    definition: "Completed runner.attempt spans after execution ends",
  },
];

function duration(ms: number | null): string {
  return ms === null ? NOT_RECORDED : formatDuration(ms);
}

function cost(summary: CostSummary | null): string {
  return summary === null ? "not reported" : `$${summary.total_usd.toFixed(4)}`;
}

function checks(summary: ValidationSummary | null): {
  text: string;
  tone: string;
} {
  if (summary === null) return { text: "none", tone: "text-muted" };
  const problems: string[] = [];
  if (summary.failed > 0) problems.push(`${summary.failed} failed`);
  if (summary.timed_out > 0) problems.push(`${summary.timed_out} timed out`);
  if (summary.error > 0) problems.push(`${summary.error} error`);
  const text =
    `${summary.passed}/${summary.total} passed` +
    (problems.length > 0 ? ` (${problems.join(", ")})` : "");
  const tone =
    summary.failed > 0
      ? "text-danger"
      : problems.length > 0
        ? "text-warning"
        : "text-success";
  return { text, tone };
}

export function InsightsPanel({
  state,
  onRetry,
}: {
  state: InsightsState;
  onRetry: () => void;
}) {
  return (
    <section aria-labelledby="execution-insights-heading">
      <h2
        id="execution-insights-heading"
        className="text-sm font-semibold text-muted"
      >
        Execution insights
      </h2>
      <div className="mt-2">
        {state.phase === "loading" && <InsightsSkeleton />}
        {state.phase === "error" && (
          <div
            role="alert"
            className="flex items-start gap-3 rounded border border-border px-3 py-2 text-sm"
          >
            <span className="py-1 text-warning">
              Execution insights are unavailable.
            </span>
            <button
              type="button"
              onClick={onRetry}
              className="rounded px-2 py-1 font-semibold text-accent hover:bg-surface hover:underline"
            >
              Retry
            </button>
          </div>
        )}
        {state.phase === "ready" && state.insights.attempts.length === 0 && (
          <p className="rounded border border-border px-3 py-3 text-sm text-muted">
            No attempts recorded yet.
          </p>
        )}
        {state.phase === "ready" && state.insights.attempts.length > 0 && (
          <>
            <OverallSummary insights={state.insights} />
            <AttemptCards
              attempts={state.insights.attempts}
              taskStatus={state.insights.task_status}
            />
            <AttemptTable
              attempts={state.insights.attempts}
              taskStatus={state.insights.task_status}
            />
          </>
        )}
      </div>
    </section>
  );
}

function OverallSummary({ insights }: { insights: TaskInsights }) {
  const { overall } = insights;
  const validation = checks(overall.validation);
  const tiles: { label: string; value: string; tone?: string }[] = [
    { label: "Attempts", value: String(overall.attempt_count) },
    { label: "Runtime", value: duration(overall.total_runtime_ms) },
    { label: "Reported cost", value: cost(overall.cost) },
    { label: "Checks", value: validation.text, tone: validation.tone },
    { label: "Events", value: String(overall.event_count) },
  ];
  return (
    <dl
      aria-label="Overall summary"
      className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5"
    >
      {tiles.map((tile) => (
        <div
          key={tile.label}
          className="min-w-0 rounded border border-border bg-surface px-3 py-2"
        >
          <dt className="text-xs font-semibold tracking-wide text-muted uppercase">
            {tile.label}
          </dt>
          <dd
            className={`mt-0.5 text-lg font-semibold break-words tabular-nums ${tile.tone ?? "text-foreground"}`}
            title={tile.value}
          >
            {tile.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function AttemptCards({
  attempts,
  taskStatus,
}: {
  attempts: AttemptInsight[];
  taskStatus: TaskStatus;
}) {
  return (
    <div className="mt-3 lg:hidden">
      <ul aria-label="Attempt details" className="space-y-3">
        {attempts.map((attempt) => {
          const validation = checks(attempt.validation);
          return (
            <li
              key={attempt.attempt_id}
              className="rounded border border-border"
            >
              <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-border bg-surface px-3 py-2">
                <h3 className="text-sm font-semibold">
                  Attempt #{attempt.attempt_number}
                </h3>
                {attempt.status === "active" ? (
                  <span className="flex flex-wrap items-baseline gap-2">
                    <span className="text-sm font-semibold text-accent">
                      active
                    </span>
                    <StatusBadge status={taskStatus} />
                  </span>
                ) : (
                  <span
                    className={`text-sm font-semibold ${ATTEMPT_TONES[attempt.status]}`}
                  >
                    {statusLabel(attempt.status)}
                  </span>
                )}
              </div>
              {attempt.failure_code && (
                <p className="px-3 pt-2 font-mono text-xs break-all text-danger">
                  {attempt.failure_code}
                </p>
              )}
              <dl className="grid grid-cols-2 gap-x-4 gap-y-3 px-3 py-3 text-sm">
                {DURATION_COLUMNS.map((column) => (
                  <div key={column.key} className="min-w-0">
                    <dt
                      className="text-xs text-muted"
                      title={column.definition}
                    >
                      {column.label}
                    </dt>
                    <dd className="mt-0.5 font-mono tabular-nums">
                      {duration(attempt[column.key])}
                    </dd>
                  </div>
                ))}
                <div className="min-w-0">
                  <dt className="text-xs text-muted">Events</dt>
                  <dd className="mt-0.5 font-mono tabular-nums">
                    {attempt.event_count}
                  </dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-xs text-muted">Cost</dt>
                  <dd className="mt-0.5 font-mono break-words tabular-nums">
                    {cost(attempt.cost)}
                  </dd>
                </div>
                <div className="col-span-2 min-w-0">
                  <dt className="text-xs text-muted">Checks</dt>
                  <dd className={`mt-0.5 break-words ${validation.tone}`}>
                    {validation.text}
                  </dd>
                </div>
              </dl>
            </li>
          );
        })}
      </ul>
      <p className="mt-2 text-xs text-muted">
        {NOT_RECORDED} not recorded: the attempt is unfinished or the source was
        never stored.
      </p>
    </div>
  );
}

function AttemptTable({
  attempts,
  taskStatus,
}: {
  attempts: AttemptInsight[];
  taskStatus: TaskStatus;
}) {
  return (
    <div className="mt-3 hidden overflow-x-auto rounded border border-border lg:block">
      <table
        aria-label="Attempt comparison"
        className="w-full min-w-[880px] text-sm"
      >
        <thead>
          <tr className="border-b border-border bg-surface text-xs font-semibold tracking-wide text-muted uppercase">
            <th scope="col" className="px-3 py-2 text-left font-semibold">
              Attempt
            </th>
            <th scope="col" className="px-3 py-2 text-left font-semibold">
              Status
            </th>
            {DURATION_COLUMNS.map((column) => (
              <th
                key={column.key}
                scope="col"
                title={column.definition}
                className="px-3 py-2 text-right font-semibold"
              >
                {column.label}
              </th>
            ))}
            <th scope="col" className="px-3 py-2 text-right font-semibold">
              Events
            </th>
            <th scope="col" className="px-3 py-2 text-left font-semibold">
              Checks
            </th>
            <th scope="col" className="px-3 py-2 text-right font-semibold">
              Cost
            </th>
          </tr>
        </thead>
        <tbody>
          {attempts.map((attempt) => (
            <AttemptRow
              key={attempt.attempt_id}
              attempt={attempt}
              taskStatus={taskStatus}
            />
          ))}
        </tbody>
      </table>
      <p className="border-t border-border px-3 py-1.5 text-xs text-muted">
        {NOT_RECORDED} not recorded: the attempt is unfinished or the source was
        never stored.
      </p>
    </div>
  );
}

function AttemptRow({
  attempt,
  taskStatus,
}: {
  attempt: AttemptInsight;
  taskStatus: TaskStatus;
}) {
  const validation = checks(attempt.validation);
  return (
    <tr className="border-b border-border last:border-b-0">
      <td className="px-3 py-2 font-semibold whitespace-nowrap">
        #{attempt.attempt_number}
      </td>
      <td className="px-3 py-2 whitespace-nowrap">
        {attempt.status === "active" ? (
          <span className="flex flex-wrap items-baseline gap-2">
            <span className="text-sm font-semibold text-accent">active</span>
            <StatusBadge status={taskStatus} />
          </span>
        ) : (
          <span
            className={`text-sm font-semibold ${ATTEMPT_TONES[attempt.status] ?? "text-muted"}`}
          >
            {statusLabel(attempt.status)}
          </span>
        )}
        {attempt.failure_code && (
          <p className="max-w-48 font-mono text-xs break-all whitespace-normal text-danger">
            {attempt.failure_code}
          </p>
        )}
      </td>
      {DURATION_COLUMNS.map((column) => (
        <td
          key={column.key}
          className="px-3 py-2 text-right font-mono whitespace-nowrap tabular-nums"
        >
          {duration(attempt[column.key])}
        </td>
      ))}
      <td className="px-3 py-2 text-right font-mono tabular-nums">
        {attempt.event_count}
      </td>
      <td className={`px-3 py-2 whitespace-nowrap ${validation.tone}`}>
        {validation.text}
      </td>
      <td className="px-3 py-2 text-right font-mono whitespace-nowrap tabular-nums">
        {cost(attempt.cost)}
      </td>
    </tr>
  );
}

function InsightsSkeleton() {
  return (
    <div aria-label="Loading execution insights" className="animate-pulse">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        {[0, 1, 2, 3, 4].map((i) => (
          <div key={i} className="h-14 rounded bg-surface" />
        ))}
      </div>
      <div className="mt-3 h-20 rounded bg-surface" />
    </div>
  );
}
