import type {
  ActivityEvent,
  Me,
  Organization,
  Repository,
  RepositoryDetail,
  RepositorySettings,
  Runner,
  RunnerDetail,
  StoredEvidence,
  Task,
  TaskConflict,
  TaskTrace,
  TaskStatus,
  ValidationResult,
} from "./types";

export const BACKEND_PREFIX = "/backend";
export const API_PREFIX = `${BACKEND_PREFIX}/api/v1`;

export const LOGIN_URL = `${BACKEND_PREFIX}/auth/github/start`;

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

function redirectToLogin() {
  if (typeof window !== "undefined" && window.location.pathname !== "/login") {
    window.location.assign("/login");
  }
}

async function backendRequest<T>(url: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(url, init);
  } catch {
    throw new ApiError(0, "control plane unreachable");
  }
  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    if (res.status === 401) redirectToLogin();
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

function request<T>(path: string, init?: RequestInit): Promise<T> {
  return backendRequest<T>(`${API_PREFIX}${path}`, init);
}

export function getMe(): Promise<Me> {
  return backendRequest<Me>(`${BACKEND_PREFIX}/me`);
}

export async function logout(): Promise<void> {
  await backendRequest<null>(`${BACKEND_PREFIX}/auth/logout`, {
    method: "POST",
  });
}

export function setRepositoryEnabled(
  repositoryId: string,
  enabled: boolean,
): Promise<Repository> {
  const action = enabled ? "enable" : "disable";
  return request<Repository>(
    `/repositories/${encodeURIComponent(repositoryId)}/${action}`,
    { method: "POST" },
  );
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

export async function listOrganizations(): Promise<Organization[]> {
  const body = await request<{ organizations: Organization[] }>(
    "/organizations",
  );
  return body.organizations;
}

export async function listRepositories(options?: {
  organizationId?: string;
  limit?: number;
}): Promise<Repository[]> {
  const params = new URLSearchParams();
  if (options?.limit) params.set("limit", String(options.limit));
  const query = params.size > 0 ? `?${params}` : "";
  const path = options?.organizationId
    ? `/organizations/${encodeURIComponent(options.organizationId)}/repositories`
    : "/repositories";
  const body = await request<{ repositories: Repository[] }>(`${path}${query}`);
  return body.repositories;
}

export function getRepository(repositoryId: string): Promise<RepositoryDetail> {
  return request<RepositoryDetail>(
    `/repositories/${encodeURIComponent(repositoryId)}`,
  );
}

export function getRepositorySettings(
  repositoryId: string,
): Promise<RepositorySettings> {
  return request<RepositorySettings>(
    `/repositories/${encodeURIComponent(repositoryId)}/settings`,
  );
}

export async function listRunners(): Promise<Runner[]> {
  const body = await request<{ runners: Runner[] }>("/runners");
  return body.runners;
}

export function getRunner(runnerId: string): Promise<RunnerDetail> {
  return request<RunnerDetail>(`/runners/${encodeURIComponent(runnerId)}`);
}

export function getTask(taskId: string): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(taskId)}`);
}

export function getTaskTrace(taskId: string): Promise<TaskTrace> {
  return request<TaskTrace>(`/tasks/${encodeURIComponent(taskId)}/trace`);
}

export function cancelTask(taskId: string, reason?: string): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(taskId)}/cancel`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(reason ? { reason } : {}),
  });
}

export async function listEvents(taskId: string): Promise<ActivityEvent[]> {
  const body = await request<{ events: ActivityEvent[] }>(
    `/tasks/${encodeURIComponent(taskId)}/events?limit=1000`,
  );
  return body.events;
}

export async function listValidations(
  taskId: string,
): Promise<ValidationResult[]> {
  const body = await request<{ validations: ValidationResult[] }>(
    `/tasks/${encodeURIComponent(taskId)}/validations`,
  );
  return body.validations;
}

export async function listConflicts(taskId: string): Promise<TaskConflict[]> {
  const body = await request<{ conflicts: TaskConflict[] }>(
    `/tasks/${encodeURIComponent(taskId)}/conflicts`,
  );
  return body.conflicts;
}

// 404 = no report yet, not an error
export async function getEvidence(
  taskId: string,
): Promise<StoredEvidence | null> {
  try {
    return await request<StoredEvidence>(
      `/tasks/${encodeURIComponent(taskId)}/evidence`,
    );
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export function eventCursor(e: ActivityEvent): string {
  return `${e.attempt_number}:${e.sequence_number}`;
}

export function streamUrl(taskId: string, lastEventId?: string): string {
  const suffix = lastEventId
    ? `?last_event_id=${encodeURIComponent(lastEventId)}`
    : "";
  return `${API_PREFIX}/tasks/${encodeURIComponent(taskId)}/stream${suffix}`;
}
