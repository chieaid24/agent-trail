package runner

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/agent"
	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// stubClaudeCLI writes an executable /bin/sh stub standing in for the Claude
// Code CLI, so the executor runs the real adapter code path end to end without
// model cost, and returns its path.
func stubClaudeCLI(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeExecutor(db *sql.DB, s *Store, ts *task.Store, cliPath string) *Executor {
	return &Executor{
		Tasks:         ts,
		Store:         s,
		Validations:   validation.NewStore(db),
		Evidence:      evidence.NewStore(db),
		Adapter:       agent.NewClaudeCode(agent.ClaudeCodeOptions{CLIPath: cliPath}),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		LeaseDuration: time.Minute,
	}
}

// claudeStub emits a plan, edits a workspace file, and reports success.
const claudeStub = `printf '%s\n' '{"type":"system","subtype":"init","model":"claude-test","session_id":"s1"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"ExitPlanMode","input":{"plan":"edit the file"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Write","input":{"file_path":"CHANGE.md"}}]}}'
printf 'change\n' > CHANGE.md
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"File created"}]}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"Edited CHANGE.md.","total_cost_usd":0.02,"num_turns":2}'
`

// TestExecuteCompletesClaudeTaskEndToEnd is the Milestone 5 acceptance
// criterion: the real Claude Code adapter (backed by a stub CLI) completes a
// fixture task end to end, with its normalized events on the timeline.
func TestExecuteCompletesClaudeTaskEndToEnd(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if err := claudeExecutor(db, s, ts, stubClaudeCLI(t, claudeStub)).Execute(ctx, r.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("task status = %s, want completed", got.Status)
	}

	assertSubsequence(t, timelineTypes(t, ts, tk.ID), []string{
		"task.provisioning", "workspace.provisioning", "workspace.ready",
		"task.planning", "agent.started", "plan.created", "task.executing",
		"command.requested", "file.changed", "command.output",
		"command.completed", "agent.cost_update", "agent.completed",
		"task.validating", "validation.completed", "task.publishing",
		"task.awaiting_review", "task.completed",
	})
}

// planlessClaudeStub reports success without ever emitting a plan.
const planlessClaudeStub = `printf '%s\n' '{"type":"system","subtype":"init","model":"m","session_id":"s"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"nothing to change"}]}}'
printf '%s\n' '{"type":"result","subtype":"success","result":"no changes needed"}'
`

// TestExecuteCompletesClaudeTaskWithoutPlan proves the executor does not
// strand a task in planning when the provider emits no plan event.
func TestExecuteCompletesClaudeTaskWithoutPlan(t *testing.T) {
	db, s, ts := testStores(t)
	ctx := context.Background()
	r := mustRegister(t, s)
	tk := mustCreateTask(t, ts)

	c, err := s.Claim(ctx, r.ID, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if err := claudeExecutor(db, s, ts, stubClaudeCLI(t, planlessClaudeStub)).Execute(ctx, r.ID, c); err != nil {
		t.Fatal(err)
	}

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("task status = %s, want completed", got.Status)
	}
	assertSubsequence(t, timelineTypes(t, ts, tk.ID), []string{
		"task.planning", "agent.started", "agent.completed",
		"task.executing", "task.validating", "task.completed",
	})
}
