// all state changes go through store.transition; nothing else may assign task states
package task

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

type Phase string

const (
	PhasePending  Phase = "pending"
	PhaseRunning  Phase = "running"
	PhaseReview   Phase = "review"
	PhaseTerminal Phase = "terminal"
)

func AllStatuses() []Status {
	return []Status{
		StatusCreated, StatusQueued, StatusProvisioning, StatusPlanning,
		StatusExecuting, StatusValidating, StatusPublishing,
		StatusAwaitingReview, StatusRevisionRequested,
		StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut,
	}
}

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

func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	}
	return false
}

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

func CanTransition(from, to Status) bool {
	if !from.Valid() || !to.Valid() || from.Terminal() {
		return false
	}
	if to == StatusCancelled {
		return true
	}
	if to == StatusFailed {
		return from.Phase() == PhaseRunning
	}
	if to == StatusTimedOut {
		return from.Phase() == PhaseRunning || from == StatusAwaitingReview
	}
	for _, next := range happyPath[from] {
		if next == to {
			return true
		}
	}
	return false
}

const EventTypeCreated = "task.created"

func TransitionEventType(s Status) string {
	return "task." + string(s)
}
