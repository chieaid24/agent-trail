// Domain types for the dashboard, mirroring docs/architecture/data-model.md
// and the task state machine. The dashboard is read-mostly, so these carry the
// fields the UI renders, not the full persistence schema.

export type TaskStatus =
  | "queued"
  | "provisioning"
  | "planning"
  | "executing"
  | "validating"
  | "publishing"
  | "awaiting_review"
  | "completed"
  | "failed"
  | "cancelled"
  | "timed_out";

// Terminal statuses accept no further transitions; cancellation is offered only
// for the rest (task-state-machine.md: cancellation from any non-terminal state).
export const TERMINAL_STATUSES: readonly TaskStatus[] = [
  "completed",
  "failed",
  "cancelled",
  "timed_out",
];

export function isTerminal(status: TaskStatus): boolean {
  return TERMINAL_STATUSES.includes(status);
}

export interface Task {
  id: string;
  repositoryId: string;
  repositoryFullName: string;
  sourceIssueNumber: number;
  title: string;
  instructions: string;
  status: TaskStatus;
  agentProvider: string;
  agentModel: string;
  baseCommitSha: string;
  finalCommitSha: string | null;
  workingBranch: string | null;
  pullRequestNumber: number | null;
  pullRequestUrl: string | null;
  requestedBy: string;
  maxRuntimeSeconds: number;
  maxCostUsd: number;
  costUsd: number;
  createdAt: string;
  startedAt: string | null;
  completedAt: string | null;
  failureCode: string | null;
  failureMessage: string | null;
  filesChanged: FileChange[];
  deniedActions: DeniedAction[];
}

export interface FileChange {
  path: string;
  additions: number;
  deletions: number;
}

export interface DeniedAction {
  action: string;
  reason: string;
  timestamp: string;
}

// One row of the append-only activity timeline (data-model.md: Activity event).
export interface ActivityEvent {
  id: string;
  sequence: number;
  type: string;
  source: "runner" | "agent" | "platform";
  timestamp: string;
  message: string;
  redacted: boolean;
}

export type LogStream = "stdout" | "stderr";

export interface LogLine {
  sequence: number;
  stream: LogStream;
  text: string;
  // Redacted lines render a visible marker, never a silent gap (DESIGN.md).
  redacted: boolean;
}

export type ValidationCategory =
  | "unit_test"
  | "integration_test"
  | "lint"
  | "format"
  | "typecheck"
  | "security"
  | "dependency"
  | "migration"
  | "build"
  | "custom";

export type ValidationStatus = "passed" | "failed" | "skipped";

export interface ValidationResult {
  id: string;
  name: string;
  category: ValidationCategory;
  command: string;
  status: ValidationStatus;
  exitCode: number | null;
  durationMs: number | null;
  summary: string;
  // The trust distinction: platform-verified runs are trusted; agent-reported
  // ones are claims. Never blurred (DESIGN.md, spec requirement).
  trustedExecution: boolean;
}

export interface EvidenceReport {
  schemaVersion: string;
  summaryMarkdown: string;
  baseCommitSha: string;
  finalCommitSha: string | null;
  runnerImage: string;
  agentProvider: string;
  agentModel: string;
  policyVersion: string;
  runtimeSeconds: number;
  createdAt: string;
}

export type RunnerStatus = "idle" | "busy" | "draining" | "offline";

export interface Runner {
  id: string;
  runnerType: string;
  hostnameOrPod: string;
  status: RunnerStatus;
  capacity: number;
  activeTasks: number;
  currentTaskId: string | null;
  lastHeartbeatAt: string;
  cpuPercent: number;
  memoryPercent: number;
  diskPercent: number;
  recentFailures: number;
}

export interface Repository {
  id: string;
  fullName: string;
  owner: string;
  name: string;
  defaultBranch: string;
  isPrivate: boolean;
  isEnabled: boolean;
  defaultPolicy: string;
  validationConfig: string[];
  activeTaskIds: string[];
  completedTaskCount: number;
  failedTaskCount: number;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  accountLogin: string;
  accountType: "Organization" | "User";
}
