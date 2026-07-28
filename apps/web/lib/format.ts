// Presentation helpers shared across screens.

export function formatTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-GB", { hour12: false });
}

export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  return `${d.toLocaleDateString("en-CA")} ${formatTime(iso)}`;
}

// Compact duration: 999ms, 12s, 3m 24s, 1h 08m.
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60)
    return `${minutes}m ${String(seconds % 60).padStart(2, "0")}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${String(minutes % 60).padStart(2, "0")}m`;
}

// Runtime between two instants; open-ended tasks run until now.
export function runtimeMs(
  startedAt: string | null,
  completedAt: string | null,
): number | null {
  if (!startedAt) return null;
  const end = completedAt ? new Date(completedAt).getTime() : Date.now();
  return end - new Date(startedAt).getTime();
}

export function shortSha(sha: string): string {
  return sha.slice(0, 10);
}

// Task status -> human label ("awaiting_review" -> "awaiting review").
export function statusLabel(status: string): string {
  return status.replaceAll("_", " ");
}
