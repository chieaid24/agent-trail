// Pure derivation of timeline rows from activity events. Rendering stays in
// components; everything here is testable without a DOM.

import type { ActivityEvent } from "./types";

export interface TimelineRow {
  // Headline for the row; always present.
  label: string;
  // Longer free text under the headline, when the payload carries one.
  detail?: string;
  // Detail renders in mono when it is a shell/git artifact (DESIGN.md).
  monoDetail?: boolean;
}

function str(
  payload: Record<string, unknown>,
  key: string,
): string | undefined {
  const v = payload[key];
  return typeof v === "string" && v.length > 0 ? v : undefined;
}

function num(
  payload: Record<string, unknown>,
  key: string,
): number | undefined {
  const v = payload[key];
  return typeof v === "number" ? v : undefined;
}

// commandLine renders {command, args} as one shell-like line. Display only,
// never executed.
function commandLine(payload: Record<string, unknown>): string | undefined {
  const command = str(payload, "command");
  if (!command) return undefined;
  const args = payload.args;
  if (Array.isArray(args) && args.every((a) => typeof a === "string")) {
    return [command, ...args].join(" ");
  }
  return command;
}

// describeEvent maps every event type the platform emits to a readable row.
// Unknown types fall back to the raw type so nothing is silently dropped.
export function describeEvent(e: ActivityEvent): TimelineRow {
  const p = e.payload;
  const type = e.event_type;

  if (type.startsWith("task.")) {
    const to = type.slice("task.".length);
    const reason = str(p, "reason");
    return {
      label:
        to === "created"
          ? "task created"
          : `status: ${to.replaceAll("_", " ")}`,
      detail: reason,
    };
  }

  switch (type) {
    case "agent.started":
      return {
        label: "agent session started",
        detail: str(p, "adapter") && `adapter: ${str(p, "adapter")}`,
      };
    case "agent.message":
      return { label: "agent message", detail: str(p, "message") };
    case "plan.created":
      return { label: "plan created", detail: str(p, "plan") };
    case "command.requested":
      return {
        label: "command requested",
        detail: commandLine(p),
        monoDetail: true,
      };
    case "command.started":
      return {
        label: "command started",
        detail: commandLine(p),
        monoDetail: true,
      };
    case "command.output": {
      const chunk = str(p, "chunk");
      return {
        label: `output (${str(p, "stream") ?? "stdout"})`,
        detail: chunk,
        monoDetail: true,
      };
    }
    case "command.completed": {
      const exit = num(p, "exit_code");
      const simulated = p.simulated === true;
      const parts = [
        commandLine(p),
        exit !== undefined ? `exit ${exit}` : undefined,
      ]
        .filter(Boolean)
        .join(" -> ");
      return {
        label: simulated
          ? "command completed (simulated)"
          : "command completed",
        detail: parts || undefined,
        monoDetail: true,
      };
    }
    case "file.read":
      return { label: "file read", detail: str(p, "path"), monoDetail: true };
    case "file.changed":
      return {
        label: "file changed",
        detail: str(p, "path"),
        monoDetail: true,
      };
    case "agent.cost_update": {
      const cost = num(p, "cost_usd");
      return {
        label: "cost update",
        detail: cost !== undefined ? `$${cost.toFixed(4)}` : undefined,
      };
    }
    case "agent.warning":
      return {
        label: "agent warning",
        detail: str(p, "message") ?? str(p, "reason"),
      };
    case "agent.completed":
      return { label: "agent session completed", detail: str(p, "summary") };
    case "agent.failed":
      return { label: "agent session failed", detail: str(p, "reason") };
    case "validation.started":
      return { label: "validation started" };
    case "validation.check.completed": {
      const name = str(p, "name") ?? "check";
      const status = str(p, "status") ?? "unknown";
      const trusted = p.trusted_execution === true;
      return {
        label: `check ${status}: ${name}${trusted ? "" : " (agent claim)"}`,
        detail: str(p, "summary"),
      };
    }
    case "validation.completed": {
      const passed = num(p, "passed");
      const failed = num(p, "failed");
      const counts =
        passed !== undefined && failed !== undefined
          ? `${passed} passed, ${failed} failed`
          : undefined;
      return {
        label: `validation ${str(p, "status") ?? "completed"}`,
        detail: counts,
      };
    }
    case "evidence.generated":
      return { label: "evidence report generated" };
    case "publishing.skipped":
      return { label: "publishing skipped", detail: str(p, "reason") };
    case "cleanup.completed":
      return { label: "workspace cleaned up" };
    case "runner.lost":
      return { label: "runner lost", detail: str(p, "reason") };
    case "github.comment.posted":
      return { label: "github comment posted" };
    case "github.check_run.created":
      return { label: "github check run created" };
    default:
      return { label: type };
  }
}

// Unique changed file paths, in first-seen order.
export function changedFiles(events: ActivityEvent[]): string[] {
  const seen = new Set<string>();
  for (const e of events) {
    if (e.event_type !== "file.changed") continue;
    const path = str(e.payload, "path");
    if (path) seen.add(path);
  }
  return [...seen];
}

// The latest plan text, if the agent published one.
export function latestPlan(events: ActivityEvent[]): string | null {
  for (let i = events.length - 1; i >= 0; i--) {
    if (events[i].event_type === "plan.created") {
      return str(events[i].payload, "plan") ?? null;
    }
  }
  return null;
}
