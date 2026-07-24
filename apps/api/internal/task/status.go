// Package task implements the task domain: the validated state machine,
// optimistic versioning, task attempts, and the append-only activity
// timeline. Spec: docs/architecture/task-state-machine.md and
// docs/architecture/data-model.md. All state changes go through
// Store.Transition; nothing else may assign task states.
package task

// Status is a task state-machine state.
type Status string

const (
	StatusCreated           Status = "created"
	StatusQueued            Status = "queued"
	StatusProvisioning      Status = "provisioning"
	StatusPlanning          Status = "planning"
	StatusExecuting         Status = "executing"
	StatusValidating        Status = "validating"
	StatusPublishing        Status = "publishing"
	StatusAwaitingReview    Status = "awaiting_review"
	StatusRevisionRequested Status = "revision_requested"
	StatusCompleted         Status = "completed"
	StatusFailed            Status = "failed"
	StatusCancelled         Status = "cancelled"
	StatusTimedOut          Status = "timed_out"
)

// Phase is the coarse grouping of a status, stored alongside it for cheap
// filtering and always derived via Phase().
type Phase string

const (
	PhasePending  Phase = "pending"
	PhaseRunning  Phase = "running"
	PhaseReview   Phase = "review"
	PhaseTerminal Phase = "terminal"
)

// AllStatuses lists every valid status, for validation and tests.
func AllStatuses() []Status {
	return []Status{
		StatusCreated, StatusQueued, StatusProvisioning, StatusPlanning,
		StatusExecuting, StatusValidating, StatusPublishing,
		StatusAwaitingReview, StatusRevisionRequested,
		StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut,
	}
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusCreated, StatusQueued, StatusProvisioning, StatusPlanning,
		StatusExecuting, StatusValidating, StatusPublishing,
		StatusAwaitingReview, StatusRevisionRequested,
		StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	}
	return false
}

// Terminal reports whether s is a terminal status.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	}
	return false
}

// Phase returns the coarse grouping of s.
func (s Status) Phase() Phase {
	switch s {
	case StatusCreated, StatusQueued:
		return PhasePending
	case StatusProvisioning, StatusPlanning, StatusExecuting,
		StatusValidating, StatusPublishing:
		return PhaseRunning
	case StatusAwaitingReview, StatusRevisionRequested:
		return PhaseReview
	default:
		return PhaseTerminal
	}
}

// happyPath holds the forward edges of the state machine diagram.
var happyPath = map[Status][]Status{
	StatusCreated:           {StatusQueued},
	StatusQueued:            {StatusProvisioning},
	StatusProvisioning:      {StatusPlanning},
	StatusPlanning:          {StatusExecuting},
	StatusExecuting:         {StatusValidating},
	StatusValidating:        {StatusPublishing},
	StatusPublishing:        {StatusAwaitingReview},
	StatusAwaitingReview:    {StatusCompleted, StatusRevisionRequested},
	StatusRevisionRequested: {StatusQueued},
}

// CanTransition reports whether from -> to is a legal transition:
// the happy-path edges, cancellation from any non-terminal status, and
// failed/timed_out from any running status (safe failure).
func CanTransition(from, to Status) bool {
	if !from.Valid() || !to.Valid() || from.Terminal() {
		return false
	}
	if to == StatusCancelled {
		return true
	}
	if to == StatusFailed || to == StatusTimedOut {
		return from.Phase() == PhaseRunning
	}
	for _, next := range happyPath[from] {
		if next == to {
			return true
		}
	}
	return false
}

// EventTypeCreated is the first event on every task timeline.
const EventTypeCreated = "task.created"

// TransitionEventType returns the activity event type a transition to
// status s emits, e.g. "task.queued".
func TransitionEventType(s Status) string {
	return "task." + string(s)
}
