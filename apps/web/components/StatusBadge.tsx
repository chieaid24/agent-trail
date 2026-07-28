import type { TaskStatus } from "@/lib/types";
import { statusLabel } from "@/lib/format";

// DESIGN.md status map: queued/cancelled muted, running accent, completed
// success, failed/timed-out danger, awaiting-review warning.
type Tone = "muted" | "accent" | "success" | "warning" | "danger";

const TONES: Record<TaskStatus, Tone> = {
  created: "muted",
  queued: "muted",
  provisioning: "accent",
  planning: "accent",
  executing: "accent",
  validating: "accent",
  publishing: "accent",
  awaiting_review: "warning",
  revision_requested: "warning",
  completed: "success",
  failed: "danger",
  cancelled: "muted",
  timed_out: "danger",
};

const TONE_CLASSES: Record<Tone, string> = {
  muted: "text-muted",
  accent: "text-accent",
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
};

const DOT_CLASSES: Record<Tone, string> = {
  muted: "bg-muted",
  accent: "bg-accent",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

const RUNNING: readonly TaskStatus[] = [
  "provisioning",
  "planning",
  "executing",
  "validating",
  "publishing",
];

export function StatusBadge({ status }: { status: TaskStatus }) {
  const tone = TONES[status] ?? "muted";
  const breathing = RUNNING.includes(status);
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-sm font-semibold whitespace-nowrap ${TONE_CLASSES[tone]}`}
    >
      <span
        aria-hidden
        className={`h-1.5 w-1.5 shrink-0 rounded-full ${DOT_CLASSES[tone]} ${
          breathing ? "dot-breathe" : ""
        }`}
      />
      {statusLabel(status)}
    </span>
  );
}
