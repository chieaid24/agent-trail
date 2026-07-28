"use client";

import { useState } from "react";
import { ApiError, cancelTask } from "@/lib/api";
import type { Task } from "@/lib/types";

type Phase = "idle" | "confirming" | "cancelling" | "failed";

// Cancellation with inline confirmation (DESIGN.md: not a modal). The
// confirm step swaps in place; an optional reason lands in the timeline.
export function CancelButton({
  task,
  onCancelled,
}: {
  task: Task;
  onCancelled: (t: Task) => void;
}) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");

  if (phase === "idle" || phase === "failed") {
    return (
      <div className="flex items-baseline gap-3">
        {phase === "failed" && (
          <span className="text-sm text-danger">Cancel failed: {error}.</span>
        )}
        <button
          type="button"
          onClick={() => setPhase("confirming")}
          className="rounded border border-border px-3 py-1.5 text-sm font-semibold text-danger hover:bg-surface"
        >
          Cancel task
        </button>
      </div>
    );
  }

  const busy = phase === "cancelling";
  return (
    <div className="flex items-center gap-2">
      <span className="text-sm text-foreground">Cancel this task?</span>
      <input
        type="text"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="Reason (optional)"
        aria-label="Cancellation reason"
        disabled={busy}
        className="w-48 rounded border border-border bg-surface px-2 py-1 text-sm text-foreground placeholder:text-muted"
      />
      <button
        type="button"
        disabled={busy}
        onClick={async () => {
          setPhase("cancelling");
          try {
            const cancelled = await cancelTask(
              task.id,
              reason.trim() || undefined,
            );
            onCancelled(cancelled);
            setPhase("idle");
          } catch (err) {
            setError(
              err instanceof ApiError ? err.message : "unexpected failure",
            );
            setPhase("failed");
          }
        }}
        className="rounded bg-danger px-3 py-1.5 text-sm font-semibold text-background disabled:opacity-60"
      >
        {busy ? "Cancelling" : "Confirm cancel"}
      </button>
      <button
        type="button"
        disabled={busy}
        onClick={() => setPhase("idle")}
        className="rounded border border-border px-3 py-1.5 text-sm font-semibold text-muted hover:bg-surface"
      >
        Keep running
      </button>
    </div>
  );
}
