// Direct control-plane calls for specs: arranging fixtures through the real
// API, never through the UI under test.

import { apiBaseUrl } from "./env";

export interface ApiTask {
  id: string;
  title: string;
  status: string;
}

export async function apiListTasks(): Promise<ApiTask[]> {
  const res = await fetch(`${apiBaseUrl}/api/v1/tasks`);
  if (!res.ok) throw new Error(`list tasks: ${res.status}`);
  const body = (await res.json()) as { tasks: ApiTask[] };
  return body.tasks;
}

export async function apiTaskByTitle(title: string): Promise<ApiTask> {
  const match = (await apiListTasks()).find((t) => t.title === title);
  if (!match) throw new Error(`no task titled ${JSON.stringify(title)}`);
  return match;
}

export async function apiCreateTask(
  title: string,
  instructions: string,
): Promise<ApiTask> {
  const res = await fetch(`${apiBaseUrl}/api/v1/tasks`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title, instructions }),
  });
  if (!res.ok) throw new Error(`create task: ${res.status}`);
  return (await res.json()) as ApiTask;
}

export async function apiWaitForStatus(
  taskId: string,
  status: string,
  timeoutMs: number,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const res = await fetch(`${apiBaseUrl}/api/v1/tasks/${taskId}`);
    if (res.ok) {
      const task = (await res.json()) as ApiTask;
      if (task.status === status) return;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`task ${taskId} never reached ${status}`);
}
