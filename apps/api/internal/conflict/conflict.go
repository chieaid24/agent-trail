package conflict

import "time"

type Kind string

const (
	KindFileOverlap   Kind = "file_overlap"
	KindAdjacentLines Kind = "adjacent_lines"
	KindMergeConflict Kind = "merge_conflict"
	KindMigration     Kind = "migration"
	KindDependency    Kind = "dependency"
	KindSemantic      Kind = "semantic"
)

type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// stored warning oriented toward the other task
type TaskConflict struct {
	ID                  string    `json:"id"`
	OtherTaskID         string    `json:"other_task_id"`
	OtherTaskTitle      string    `json:"other_task_title"`
	Kinds               []Kind    `json:"kinds"`
	Files               []string  `json:"files"`
	SemanticSeverity    Severity  `json:"semantic_severity,omitempty"`
	SemanticExplanation string    `json:"semantic_explanation,omitempty"`
	SemanticEvidence    []string  `json:"semantic_evidence"`
	DetectedAt          time.Time `json:"detected_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Sibling struct {
	TaskID   string
	Title    string
	BaseSHA  string
	FinalSHA string
}

type Detection struct {
	OtherTaskID         string
	OtherTaskTitle      string
	Kinds               []Kind
	Files               []string
	SemanticSeverity    Severity
	SemanticExplanation string
	SemanticEvidence    []string
}
