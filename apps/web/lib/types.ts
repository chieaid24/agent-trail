// Wire types for the control-plane API (docs/architecture/api.md). Field
// names and casing mirror the Go JSON tags exactly; every nullable column
// arrives as null, never absent.

export type TaskStatus =
  | "created"
  | "queued"
  | "provisioning"
  | "planning"
  | "executing"
  | "validating"
  | "publishing"
  | "awaiting_review"
  | "revision_requested"
  | "completed"
  | "failed"
  | "cancelled"
  | "timed_out";

export type TaskPhase = "pending" | "running" | "review" | "terminal";

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
  organization_id: string | null;
  repository_id: string | null;
  source_type: string;
  source_issue_number: number | null;
  source_comment_id: number | null;
  title: string;
  instructions: string;
  status: TaskStatus;
  phase: TaskPhase;
  priority: number;
  base_branch: string;
  base_commit_sha: string | null;
  working_branch: string | null;
  agent_provider: string | null;
  agent_model: string | null;
  policy_id: string | null;
  requested_by_user_id: string | null;
  max_runtime_seconds: number | null;
  max_cost_usd: number | null;
  started_at: string | null;
  completed_at: string | null;
  cancel_requested_at: string | null;
  failure_code: string | null;
  failure_message: string | null;
  created_at: string;
  updated_at: string;
  version: number;
}

export type ActivitySource = "api" | "system" | "runner" | "agent";
export type RedactionStatus = "none" | "pending" | "redacted";

// One row of the append-only activity timeline. Sequence numbers restart
// per attempt; (attempt_number, sequence_number) orders the timeline and is
// the SSE resume cursor.
export interface ActivityEvent {
  id: string;
  task_attempt_id: string;
  attempt_number: number;
  sequence_number: number;
  event_type: string;
  source: ActivitySource;
  timestamp: string;
  payload: Record<string, unknown>;
  redaction_status: RedactionStatus;
  created_at: string;
}

export type ValidationStatus = "passed" | "failed" | "timed_out" | "error";

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

export interface ValidationResult {
  id: string;
  task_attempt_id: string;
  attempt_number: number;
  name: string;
  category: ValidationCategory;
  command: string[];
  status: ValidationStatus;
  exit_code: number | null;
  duration_ms: number;
  summary: string;
  // The trust distinction (VISION.md principle 4): true means the platform
  // ran and measured the command; false means the agent merely claimed it.
  trusted_execution: boolean;
  created_at: string;
}

// The evidence report document (apps/api/internal/evidence/report.go).
// Optional fields use omitempty on the Go side, so they may be absent.
export interface EvidenceReportDocument {
  schema_version: number;
  task: {
    id: string;
    source_issue?: number;
    title: string;
    requested_by?: string;
  };
  execution: {
    agent_provider?: string;
    agent_model?: string;
    base_commit?: string;
    final_commit?: string;
    duration_seconds?: number;
  };
  plan?: string[];
  changes: {
    files_changed: number;
    files?: string[];
  };
  validation: {
    name: string;
    category?: string;
    status: string;
    trusted_execution: boolean;
    exit_code?: number;
    duration_ms?: number;
    summary?: string;
  }[];
  risks?: string[];
  unverified?: string[];
}

export interface StoredEvidence {
  id: string;
  task_attempt_id: string;
  attempt_number: number;
  schema_version: number;
  summary_markdown: string;
  report: EvidenceReportDocument;
  created_at: string;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  github_account_login: string;
  github_account_type: string;
  repository_count: number;
  enabled_repository_count: number;
  created_at: string;
  updated_at: string;
}

export interface RepositorySettings {
  default_policy: string;
  validation_file: string;
}

export interface Repository {
  id: string;
  organization_id: string;
  github_repository_id: number;
  owner: string;
  name: string;
  full_name: string;
  default_branch: string;
  is_private: boolean;
  is_enabled: boolean;
  settings: RepositorySettings;
  active_task_count: number;
  recent_task_count: number;
  created_at: string;
  updated_at: string;
}

export interface DashboardTask {
  id: string;
  title: string;
  status: TaskStatus;
  phase: TaskPhase;
  source_issue_number: number | null;
  started_at: string | null;
  completed_at: string | null;
  failure_message: string | null;
  created_at: string;
  updated_at: string;
}

export interface RepositoryMetrics {
  total_tasks: number;
  active_tasks: number;
  completed_tasks: number;
  failed_tasks: number;
  completion_rate: number | null;
  median_runtime_millis: number | null;
}

export interface RepositoryDetail extends Repository {
  metrics: RepositoryMetrics;
  active_tasks: DashboardTask[];
  recent_tasks: DashboardTask[];
}

export interface ResourceUsage {
  cpu_percent: number | null;
  memory_percent: number | null;
  disk_percent: number | null;
}

export type RunnerStatus = "online" | "lost" | "offline";

export interface Runner {
  id: string;
  runner_type: string;
  hostname_or_pod: string;
  status: RunnerStatus;
  capacity: number;
  active_task_count: number;
  labels: Record<string, string>;
  resources: ResourceUsage;
  last_heartbeat_at: string;
  created_at: string;
  updated_at: string;
}

export interface RunnerDetail extends Runner {
  current_tasks: DashboardTask[];
  recent_failures: DashboardTask[];
}

export type ConflictKind =
  | "file_overlap"
  | "adjacent_lines"
  | "merge_conflict"
  | "migration"
  | "dependency";

export interface TaskConflict {
  id: string;
  other_task_id: string;
  other_task_title: string;
  kinds: ConflictKind[];
  files: string[];
  detected_at: string;
  updated_at: string;
}
