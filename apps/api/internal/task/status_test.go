package task

import "testing"

// specEdges is the full legal edge set, written out literally so the test is
// an independent statement of docs/architecture/task-state-machine.md rather
// than a re-derivation from the implementation.
var specEdges = map[Status][]Status{
	StatusCreated: {StatusQueued, StatusCancelled},
	StatusQueued:  {StatusProvisioning, StatusCancelled},
	StatusProvisioning: {StatusPlanning, StatusFailed, StatusTimedOut,
		StatusCancelled},
	StatusPlanning: {StatusExecuting, StatusFailed, StatusTimedOut,
		StatusCancelled},
	StatusExecuting: {StatusValidating, StatusFailed, StatusTimedOut,
		StatusCancelled},
	StatusValidating: {StatusPublishing, StatusFailed, StatusTimedOut,
		StatusCancelled},
	StatusPublishing: {StatusAwaitingReview, StatusFailed, StatusTimedOut,
		StatusCancelled},
	StatusAwaitingReview: {StatusCompleted, StatusRevisionRequested,
		StatusCancelled},
	StatusRevisionRequested: {StatusQueued, StatusCancelled},
	StatusCompleted:         {},
	StatusFailed:            {},
	StatusCancelled:         {},
	StatusTimedOut:          {},
}

func TestCanTransitionMatchesSpecExactly(t *testing.T) {
	for _, from := range AllStatuses() {
		allowed := map[Status]bool{}
		for _, to := range specEdges[from] {
			allowed[to] = true
		}
		for _, to := range AllStatuses() {
			if got, want := CanTransition(from, to), allowed[to]; got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestCanTransitionRejectsUnknownStatuses(t *testing.T) {
	for _, pair := range [][2]Status{
		{"bogus", StatusQueued},
		{StatusQueued, "bogus"},
		{"", StatusQueued},
		{StatusQueued, ""},
	} {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("CanTransition(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}

func TestCancellationAcceptedFromEveryNonTerminalStatus(t *testing.T) {
	for _, from := range AllStatuses() {
		want := !from.Terminal()
		if got := CanTransition(from, StatusCancelled); got != want {
			t.Errorf("CanTransition(%s, cancelled) = %v, want %v", from, got, want)
		}
	}
}

func TestTerminalStatusesRejectEverything(t *testing.T) {
	for _, from := range AllStatuses() {
		if !from.Terminal() {
			continue
		}
		for _, to := range AllStatuses() {
			if CanTransition(from, to) {
				t.Errorf("terminal %s allows transition to %s", from, to)
			}
		}
	}
}

func TestPhaseIsTotalOverStatuses(t *testing.T) {
	want := map[Status]Phase{
		StatusCreated:           PhasePending,
		StatusQueued:            PhasePending,
		StatusProvisioning:      PhaseRunning,
		StatusPlanning:          PhaseRunning,
		StatusExecuting:         PhaseRunning,
		StatusValidating:        PhaseRunning,
		StatusPublishing:        PhaseRunning,
		StatusAwaitingReview:    PhaseReview,
		StatusRevisionRequested: PhaseReview,
		StatusCompleted:         PhaseTerminal,
		StatusFailed:            PhaseTerminal,
		StatusCancelled:         PhaseTerminal,
		StatusTimedOut:          PhaseTerminal,
	}
	for _, s := range AllStatuses() {
		if got := s.Phase(); got != want[s] {
			t.Errorf("%s.Phase() = %s, want %s", s, got, want[s])
		}
	}
}

func TestValid(t *testing.T) {
	for _, s := range AllStatuses() {
		if !s.Valid() {
			t.Errorf("%s.Valid() = false", s)
		}
	}
	for _, s := range []Status{"", "bogus", "CREATED", "Completed"} {
		if s.Valid() {
			t.Errorf("%q.Valid() = true", s)
		}
	}
}

func TestTransitionEventType(t *testing.T) {
	if got := TransitionEventType(StatusQueued); got != "task.queued" {
		t.Errorf("TransitionEventType(queued) = %q", got)
	}
	if got := TransitionEventType(StatusTimedOut); got != "task.timed_out" {
		t.Errorf("TransitionEventType(timed_out) = %q", got)
	}
}
