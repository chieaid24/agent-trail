"use client";

import Link from "next/link";
import { StatusBadge } from "./StatusBadge";
import { formatDateTime, formatDuration, runtimeMs } from "@/lib/format";
import type { Task } from "@/lib/types";

// One dense row per task (DESIGN.md: table rows are rows, not cards).
export function TaskRows({ tasks }: { tasks: Task[] }) {
  return (
    <ul>
      {tasks.map((t) => {
        const runtime = runtimeMs(t.started_at, t.completed_at);
        return (
          <li key={t.id} className="border-b border-border">
            <Link
              href={`/tasks/${t.id}`}
              className="grid grid-cols-[10rem_1fr_auto] items-baseline gap-x-4 px-2 py-2 hover:bg-surface"
            >
              <StatusBadge status={t.status} />
              <span className="truncate text-base text-foreground">
                {t.title}
              </span>
              <span className="flex items-baseline gap-4 text-sm text-muted">
                {t.source_issue_number !== null && (
                  <span>issue #{t.source_issue_number}</span>
                )}
                {t.agent_provider && (
                  <span>
                    {t.agent_provider}
                    {t.agent_model ? ` / ${t.agent_model}` : ""}
                  </span>
                )}
                {runtime !== null && <span>{formatDuration(runtime)}</span>}
                <span className="font-mono text-sm">
                  {formatDateTime(t.created_at)}
                </span>
              </span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
