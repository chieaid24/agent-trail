"use client";

import { formatDateTime } from "@/lib/format";
import type { TaskAttempt } from "@/lib/types";

// hidden for single-attempt tasks: the selector only exists once a revision adds attempt 2
export function AttemptSelector({
  numbers,
  selected,
  attempt,
  onSelect,
}: {
  numbers: number[];
  selected: number | null;
  // read model of the selected attempt when the attempts list has loaded
  attempt?: TaskAttempt;
  onSelect: (attempt: number) => void;
}) {
  if (numbers.length < 2) return null;
  return (
    <div className="mt-4">
      <div
        role="group"
        aria-label="Attempt selector"
        className="flex flex-wrap items-center gap-3"
      >
        <span className="text-sm font-semibold text-muted">Attempt</span>
        <div className="inline-flex flex-wrap overflow-hidden rounded border border-border">
          {numbers.map((n) => {
            const active = n === selected;
            return (
              <button
                key={n}
                type="button"
                aria-pressed={active}
                aria-label={`Attempt ${n}`}
                onClick={() => onSelect(n)}
                className={`border-r border-border px-3 py-1 text-sm font-semibold tabular-nums last:border-r-0 ${
                  active
                    ? "bg-surface text-foreground"
                    : "text-muted hover:bg-surface hover:text-foreground"
                }`}
              >
                #{n}
              </button>
            );
          })}
        </div>
        <span className="text-sm text-muted">{numbers.length} attempts</span>
      </div>
      {attempt && <RequestLine attempt={attempt} />}
    </div>
  );
}

function RequestLine({ attempt }: { attempt: TaskAttempt }) {
  const revision = attempt.attempt_number > 1;
  const by = attempt.requested_by_login
    ? ` by ${attempt.requested_by_login}`
    : "";
  return (
    <p
      aria-label={`Attempt ${attempt.attempt_number} request`}
      className="mt-2 text-sm text-muted"
    >
      {revision ? "Revision" : "Run"} requested{by}{" "}
      <span className="font-mono">{formatDateTime(attempt.created_at)}</span>
      {revision && attempt.feedback.length > 0 && (
        <>
          {" "}
          with {attempt.feedback.length} feedback item
          {attempt.feedback.length === 1 ? "" : "s"}
        </>
      )}
    </p>
  );
}
