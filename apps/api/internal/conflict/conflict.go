// Package conflict detects overlap between active task diffs.
package conflict

import "time"

// Kind identifies an overlap detector.
type Kind string

const (
	// KindFileOverlap marks a shared changed file.
	KindFileOverlap Kind = "file_overlap"
	// KindAdjacentLines marks nearby base-side hunks.
	KindAdjacentLines Kind = "adjacent_lines"
	// KindMergeConflict marks a failed temporary merge.
	KindMergeConflict Kind = "merge_conflict"
	// KindMigration marks concurrent migration changes.
	KindMigration Kind = "migration"
	// KindDependency marks a shared dependency file.
	KindDependency Kind = "dependency"
)

// TaskConflict is a stored warning oriented toward the other task.
type TaskConflict struct {
	ID             string    `json:"id"`
	OtherTaskID    string    `json:"other_task_id"`
	OtherTaskTitle string    `json:"other_task_title"`
	Kinds          []Kind    `json:"kinds"`
	Files          []string  `json:"files"`
	DetectedAt     time.Time `json:"detected_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Sibling is an active repository task with a published diff.
type Sibling struct {
	TaskID   string
	Title    string
	BaseSHA  string
	FinalSHA string
}

// Detection is one conflicting task pair.
type Detection struct {
	OtherTaskID    string
	OtherTaskTitle string
	Kinds          []Kind
	Files          []string
}
