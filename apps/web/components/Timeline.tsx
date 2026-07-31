"use client";

import { useState } from "react";
import { formatTime } from "@/lib/format";
import { describeEvent } from "@/lib/timeline";
import type { ActivityEvent } from "@/lib/types";

// Live activity timeline, newest first so the latest state is visible
// without scrolling. Rows that arrive after mount fade in with a background
// tint (globals.css .row-appear).
export function Timeline({ events }: { events: ActivityEvent[] }) {
  // Cursor of the newest event present at first render; anything after it
  // animates. The boundary must not move on re-render.
  const [initialCursor] = useState(() => {
    const last = events[events.length - 1];
    return last ? `${last.attempt_number}:${last.sequence_number}` : "";
  });

  if (events.length === 0) {
    return (
      <p className="py-8 text-sm text-muted">
        No activity yet. Events appear here the moment the platform records
        them.
      </p>
    );
  }

  const newest = [...events].reverse();
  return (
    <ol className="mt-2">
      {newest.map((e) => (
        <TimelineRow
          key={e.id}
          event={e}
          animate={initialCursor !== "" && isAfter(e, initialCursor)}
        />
      ))}
    </ol>
  );
}

function isAfter(e: ActivityEvent, cursor: string): boolean {
  const [attempt, sequence] = cursor.split(":").map(Number);
  return (
    e.attempt_number > attempt ||
    (e.attempt_number === attempt && e.sequence_number > sequence)
  );
}

function TimelineRow({
  event,
  animate,
}: {
  event: ActivityEvent;
  animate: boolean;
}) {
  const row = describeEvent(event);
  const redacted = event.redaction_status === "redacted";
  return (
    <li
      className={`grid grid-cols-[4.5rem_3.5rem_1fr] gap-x-3 border-b border-border py-1.5 ${
        animate ? "row-appear" : ""
      }`}
    >
      <span className="pt-0.5 font-mono text-sm text-muted">
        {formatTime(event.timestamp)}
      </span>
      <span className="pt-0.5 text-sm text-muted">{event.source}</span>
      {redacted ? (
        <span className="justify-self-start rounded bg-surface px-1.5 text-sm text-muted">
          redacted
        </span>
      ) : (
        <span className="min-w-0">
          <span className="text-sm font-semibold text-foreground">
            {row.label}
          </span>
          {row.detail && (
            <span
              className={`mt-0.5 block max-w-[72ch] break-words whitespace-pre-wrap text-sm text-muted ${
                row.monoDetail ? "font-mono" : ""
              }`}
            >
              {row.detail}
            </span>
          )}
        </span>
      )}
    </li>
  );
}
