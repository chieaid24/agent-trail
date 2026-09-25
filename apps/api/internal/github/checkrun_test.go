package github

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

type fakeCheckRunAPI struct {
	mu      sync.Mutex
	updates map[int64][]CheckRunParams
	err     error
}

func (f *fakeCheckRunAPI) UpdateCheckRun(_ context.Context, _ int64, _, _ string, id int64, p CheckRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.updates == nil {
		f.updates = map[int64][]CheckRunParams{}
	}
	f.updates[id] = append(f.updates[id], p)
	return nil
}

func TestTaskCancelledCompletesQueuedTriggerCheckRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.recordAndProcess(t, "d-install", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-run", "issue_comment", issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))
	tasks, err := f.tasks.List(ctx, task.ListParams{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("list: %v (%d tasks)", err, len(tasks))
	}
	cancelled, err := f.tasks.Cancel(ctx, tasks[0].ID, "operator cancelled")
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeCheckRunAPI{}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	runs := NewTriggerCheckRuns(f.store, f.tasks, api, logger)

	runs.TaskCancelled(ctx, cancelled)

	updates := api.updates[777]
	if len(updates) != 1 || updates[0].Conclusion != "cancelled" || updates[0].Status != "completed" {
		t.Fatalf("updates for the trigger check run = %+v", updates)
	}
	attempts := f.attempts(t, cancelled.ID)
	if attempts[0].TriggerCheckRunCompletedAt == nil {
		t.Fatalf("completion not recorded: %+v", attempts[0])
	}
	events, err := f.tasks.Events(ctx, cancelled.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "github.check_run.updated" || last.Source != "system" {
		t.Fatalf("last event = %s from %s", last.EventType, last.Source)
	}

	// second delivery of the same cancellation leaves the completed run alone
	runs.TaskCancelled(ctx, cancelled)
	if len(api.updates[777]) != 1 {
		t.Fatalf("completed run updated again: %+v", api.updates[777])
	}
}

func TestTaskCancelledSkipsTasksWithoutTriggerOrRepository(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	api := &fakeCheckRunAPI{err: errors.New("must not be called")}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	runs := NewTriggerCheckRuns(f.store, f.tasks, api, logger)

	// api task: no repository, no trigger check run
	created, err := f.tasks.Create(ctx, task.CreateParams{Title: "t", Instructions: "i"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.tasks.Cancel(ctx, created.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	runs.TaskCancelled(ctx, cancelled)

	// still-running task: not cancelled, nothing to complete
	running, err := f.tasks.Create(ctx, task.CreateParams{Title: "t", Instructions: "i"})
	if err != nil {
		t.Fatal(err)
	}
	runs.TaskCancelled(ctx, running)
	if len(api.updates) != 0 {
		t.Fatalf("updates = %+v, want none", api.updates)
	}
}

func TestTaskCancelledLogsAndKeepsRunPendingOnAPIFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.recordAndProcess(t, "d-install", "installation", installationJSON(t, "created"))
	f.recordAndProcess(t, "d-run", "issue_comment", issueCommentJSON(t, commentOpts{body: "/agent-trail run"}))
	tasks, _ := f.tasks.List(ctx, task.ListParams{})
	cancelled, err := f.tasks.Cancel(ctx, tasks[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	runs := NewTriggerCheckRuns(f.store, f.tasks, &fakeCheckRunAPI{err: errors.New("github down")},
		slog.New(slog.NewJSONHandler(&log, nil)))

	runs.TaskCancelled(ctx, cancelled)

	if attempts := f.attempts(t, cancelled.ID); attempts[0].TriggerCheckRunCompletedAt != nil {
		t.Fatal("completion recorded despite the api failure")
	}
	if !bytes.Contains(log.Bytes(), []byte("github_trigger_check_run_failed")) {
		t.Fatalf("failure not logged: %s", log.String())
	}
}
