import Link from "next/link";
import type { ConflictKind, TaskConflict } from "@/lib/types";

const KIND_LABELS: Record<ConflictKind, string> = {
  file_overlap: "same files",
  adjacent_lines: "adjacent lines",
  merge_conflict: "merge conflict",
  migration: "migration collision",
  dependency: "dependency file",
};

export function conflictKindLabel(kind: ConflictKind): string {
  return KIND_LABELS[kind] ?? kind;
}

export function ConflictWarning({ conflicts }: { conflicts: TaskConflict[] }) {
  if (conflicts.length === 0) return null;
  return (
    <section
      aria-label="Conflict warnings"
      className="mt-4 rounded border border-border"
    >
      <h2 className="flex items-center gap-1.5 px-3 pt-2.5 text-sm font-semibold text-warning">
        <span
          aria-hidden
          className="h-1.5 w-1.5 shrink-0 rounded-full bg-warning"
        />
        Overlaps {conflicts.length === 1 ? "an active task" : "active tasks"}
      </h2>
      <ul className="mt-1 pb-1">
        {conflicts.map((c) => (
          <li
            key={c.id}
            className="border-t border-border px-3 py-2 text-sm first:border-t-0"
          >
            <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <Link
                href={`/tasks/${c.other_task_id}`}
                className="min-w-0 font-semibold break-words text-accent hover:underline"
              >
                {c.other_task_title}
              </Link>
              <span className="text-warning">
                {c.kinds.map(conflictKindLabel).join(", ")}
              </span>
            </div>
            {c.files.length > 0 && (
              <p className="mt-0.5 font-mono text-xs break-all text-muted">
                {c.files.join("  ")}
              </p>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
