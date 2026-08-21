import { formatDuration } from "@/lib/format";
import type { TaskSpan } from "@/lib/types";

export type TraceState =
  | { phase: "loading" }
  | { phase: "ready"; spans: TaskSpan[] }
  | { phase: "error" };

function spanKey(span: Pick<TaskSpan, "trace_id" | "span_id">): string {
  return `${span.trace_id}:${span.span_id}`;
}

function spanDepth(span: TaskSpan, byID: Map<string, TaskSpan>): number {
  let depth = 0;
  let parentID = span.parent_span_id;
  const visited = new Set<string>();
  while (parentID && !visited.has(parentID)) {
    visited.add(parentID);
    const parent = byID.get(`${span.trace_id}:${parentID}`);
    if (!parent) break;
    depth += 1;
    parentID = parent.parent_span_id;
  }
  return depth;
}

export function TraceWaterfall({ state }: { state: TraceState }) {
  if (state.phase === "loading") {
    return (
      <div aria-label="Loading trace" className="animate-pulse space-y-3 py-3">
        {["w-3/4", "w-1/2", "w-2/3"].map((width) => (
          <div key={width} className={`h-8 rounded bg-surface ${width}`} />
        ))}
      </div>
    );
  }
  if (state.phase === "error") {
    return (
      <p role="alert" className="py-8 text-sm text-danger">
        Trace data is unavailable. Retrying automatically.
      </p>
    );
  }
  if (state.spans.length === 0) {
    return (
      <div className="rounded border border-border px-4 py-8 text-center">
        <p className="text-sm font-semibold">No spans recorded yet</p>
        <p className="mt-1 text-sm text-muted">
          Completed spans appear after the runner stores its telemetry batch.
        </p>
      </div>
    );
  }

  const ordered = [...state.spans].sort(
    (a, b) => Date.parse(a.start_time) - Date.parse(b.start_time),
  );
  const traceStart = Math.min(
    ...ordered.map((span) => Date.parse(span.start_time)),
  );
  const traceEnd = Math.max(
    ...ordered.map((span) => Date.parse(span.end_time)),
  );
  const duration = Math.max(traceEnd - traceStart, 1);
  const byID = new Map(ordered.map((span) => [spanKey(span), span]));

  return (
    <div className="overflow-x-auto rounded border border-border">
      <div className="min-w-[680px]">
        <div className="grid grid-cols-[16rem_1fr_5rem] gap-3 border-b border-border bg-surface px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted">
          <span>Operation</span>
          <span>Timeline</span>
          <span className="text-right">Duration</span>
        </div>
        <ol aria-label="Trace waterfall">
          {ordered.map((span) => {
            const start = Date.parse(span.start_time);
            const end = Date.parse(span.end_time);
            const left = ((start - traceStart) / duration) * 100;
            const width = Math.max(((end - start) / duration) * 100, 0.75);
            const depth = spanDepth(span, byID);
            return (
              <li
                key={spanKey(span)}
                className="grid grid-cols-[16rem_1fr_5rem] items-center gap-3 border-b border-border px-3 py-2 last:border-b-0"
              >
                <div
                  className="min-w-0"
                  style={{ paddingLeft: `${Math.min(depth, 6) * 12}px` }}
                >
                  <p className="truncate font-mono text-sm" title={span.name}>
                    {span.name}
                  </p>
                  {span.status_code === "Error" && (
                    <p className="truncate text-xs text-danger">
                      {span.status_message || "error"}
                    </p>
                  )}
                </div>
                <div className="relative h-5 rounded bg-border">
                  <div
                    className={`absolute top-1 h-3 rounded-sm ${
                      span.status_code === "Error" ? "bg-danger" : "bg-accent"
                    }`}
                    style={{
                      left: `${left}%`,
                      width: `${Math.min(width, 100 - left)}%`,
                    }}
                    title={`${span.name}: ${formatDuration(end - start)}`}
                  />
                </div>
                <span className="text-right font-mono text-xs text-muted">
                  {formatDuration(end - start)}
                </span>
              </li>
            );
          })}
        </ol>
      </div>
    </div>
  );
}
