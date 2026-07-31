// Package conflict implements phase-1 conflict detection
// (docs/architecture/conflict-detection.md): after an attempt publishes, the
// worker compares its diff against every other active task in the same
// repository and records deterministic overlaps - shared files, adjacent
// lines, a failed temporary merge, migration collisions, and shared
// dependency manifests - as one row per task pair. The dashboard surfaces
// the stored warnings; nothing here blocks a task.
package conflict

import "time"

// Kind names one deterministic detector that fired for a task pair.
type Kind string

const (
	// KindFileOverlap: both diffs change at least one common file.
	KindFileOverlap Kind = "file_overlap"
	// KindAdjacentLines: both diffs touch lines of a common file within
	// adjacencyWindow lines of each other (base-side coordinates).
	KindAdjacentLines Kind = "adjacent_lines"
	// KindMergeConflict: a temporary merge of the two final commits
	// (git merge-tree) reports content conflicts.
	KindMergeConflict Kind = "merge_conflict"
	// KindMigration: both diffs add or change schema migration files, which
	// collide on ordering even when the files differ.
	KindMigration Kind = "migration"
	// KindDependency: both diffs change the same dependency manifest or
	// lockfile.
	KindDependency Kind = "dependency"
)

// TaskConflict is one stored warning as the API serves it, oriented from the
// requested task toward the other member of the pair. JSON tags are the wire
// shape (docs/architecture/api.md).
type TaskConflict struct {
	ID             string    `json:"id"`
	OtherTaskID    string    `json:"other_task_id"`
	OtherTaskTitle string    `json:"other_task_title"`
	Kinds          []Kind    `json:"kinds"`
	Files          []string  `json:"files"`
	DetectedAt     time.Time `json:"detected_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Sibling is another active task in the same repository with a published
// diff to compare against: its latest attempt's base and final commits.
type Sibling struct {
	TaskID   string
	Title    string
	BaseSHA  string
	FinalSHA string
}

// Detection is the outcome of comparing one pair, returned so the caller can
// record it on the activity timeline.
type Detection struct {
	OtherTaskID    string
	OtherTaskTitle string
	Kinds          []Kind
	Files          []string
}
