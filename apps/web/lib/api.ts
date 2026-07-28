// Typed client for the control-plane API. All calls go through the
// same-origin /backend proxy (next.config.ts), so this runs in the browser
// with no CORS setup.

import type {
  ActivityEvent,
  StoredEvidence,
  Task,
  TaskStatus,
  ValidationResult,
} from "./types";

export const API_PREFIX = "/backend/api/v1";

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${API_PREFIX}${path}`, init);
  } catch {
    throw new ApiError(0, "control plane unreachable");
  }
  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const message =
      body !== null &&
      typeof body === "object" &&
      "error" in body &&
      typeof body.error === "string"
        ? body.error
        : `request failed with status ${res.status}`;
    throw new ApiError(res.status, message);
  }
  return body as T;
}

export async function listTasks(options?: {
  status?: TaskStatus;
  limit?: number;
}): Promise<Task[]> {
  const params = new URLSearchParams();
  if (options?.status) params.set("status", options.status);
  if (options?.limit) params.set("limit", String(options.limit));
  const query = params.size > 0 ? `?${params}` : "";
  const body = await request<{ tasks: Task[] }>(`/tasks${query}`);
  return body.tasks;
}

export function getTask(taskId: string): Promise<Task> {
  return request<Task>(`/tasks/${taskId}`);
}

export function cancelTask(taskId: string, reason?: string): Promise<Task> {
  return request<Task>(`/tasks/${taskId}/cancel`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(reason ? { reason } : {}),
  });
}

export async function listEvents(taskId: string): Promise<ActivityEvent[]> {
  const body = await request<{ events: ActivityEvent[] }>(
    `/tasks/${taskId}/events?limit=1000`,
  );
  return body.events;
}

export async function listValidations(
  taskId: string,
): Promise<ValidationResult[]> {
  const body = await request<{ validations: ValidationResult[] }>(
    `/tasks/${taskId}/validations`,
  );
  return body.validations;
}

// Evidence is null until the runner generates a report; a 404 is that
// normal empty state, not an error.
export async function getEvidence(
  taskId: string,
): Promise<StoredEvidence | null> {
  try {
    return await request<StoredEvidence>(`/tasks/${taskId}/evidence`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

// The SSE resume cursor of an event (docs/architecture/api.md: Streaming).
export function eventCursor(e: ActivityEvent): string {
  return `${e.attempt_number}:${e.sequence_number}`;
}

export function streamUrl(taskId: string, lastEventId?: string): string {
  const suffix = lastEventId
    ? `?last_event_id=${encodeURIComponent(lastEventId)}`
    : "";
  return `${API_PREFIX}/tasks/${taskId}/stream${suffix}`;
}
