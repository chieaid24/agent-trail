import type { RunnerStatus as RunnerStatusValue } from "@/lib/types";

const TONES: Record<RunnerStatusValue, { dot: string; text: string }> = {
  online: { dot: "bg-success", text: "text-success" },
  lost: { dot: "bg-danger", text: "text-danger" },
  offline: { dot: "bg-muted", text: "text-muted" },
};

export function RunnerStatus({ status }: { status: RunnerStatusValue }) {
  const tone = TONES[status];
  return (
    <span className={`inline-flex items-center gap-1.5 text-sm ${tone.text}`}>
      <span aria-hidden className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
      {status}
    </span>
  );
}
