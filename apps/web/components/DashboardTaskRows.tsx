import Link from "next/link";
import { StatusBadge } from "./StatusBadge";
import { formatDateTime } from "@/lib/format";
import type { DashboardTask } from "@/lib/types";

export function DashboardTaskRows({ tasks }: { tasks: DashboardTask[] }) {
  return (
    <ul>
      {tasks.map((task) => (
        <li key={task.id} className="border-b border-border">
          <Link
            href={`/tasks/${task.id}`}
            className="grid grid-cols-[9rem_1fr_auto] items-baseline gap-x-4 px-2 py-2 hover:bg-surface"
          >
            <StatusBadge status={task.status} />
            <span className="truncate text-base">{task.title}</span>
            <span className="flex gap-4 text-sm text-muted">
              {task.source_issue_number !== null && (
                <span>issue #{task.source_issue_number}</span>
              )}
              <span className="font-mono">
                {formatDateTime(task.updated_at)}
              </span>
            </span>
          </Link>
          {task.failure_message && (
            <p className="px-2 pb-2 pl-[10.5rem] text-sm text-danger">
              {task.failure_message}
            </p>
          )}
        </li>
      ))}
    </ul>
  );
}
