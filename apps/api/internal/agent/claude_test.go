package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
}

// writes an executable sh script standing in for the claude cli
func stubCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const happyStub = `printf '%s\n' '{"type":"system","subtype":"init","model":"claude-test","session_id":"s1"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Working on it."}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":["edit NOTES.md"]}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Write","input":{"file_path":"NOTES.md"}}]}}'
printf 'stub wrote this\n' > NOTES.md
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"File created"}]}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"Edited NOTES.md.","total_cost_usd":0.0123,"num_turns":2}'
`

func reasonOf(t *testing.T, e Event) string {
	t.Helper()
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("payload %s: %v", e.Payload, err)
	}
	return p.Reason
}

func TestClaudeCodeHappyPath(t *testing.T) {
	requireSh(t)
	ws := t.TempDir()
	ad := NewClaudeCode(ClaudeCodeOptions{CLIPath: stubCLI(t, happyStub)})
	if ad.Name() != ClaudeProvider {
		t.Fatalf("Name() = %q, want %q", ad.Name(), ClaudeProvider)
	}

	sess, err := ad.Start(context.Background(), Request{
		WorkspaceDir: ws, Instructions: "Edit the notes.",
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drain(t, sess)
	want := []EventType{
		EventSessionStarted, EventAssistantMessage, EventPlan,
		EventToolRequested, EventFileWritten, EventToolOutput,
		EventToolCompleted, EventCostUpdate, EventSessionCompleted,
	}
	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
	for _, e := range events {
		if e.Timestamp.IsZero() {
			t.Fatalf("event %s has zero timestamp", e.Type)
		}
		if !json.Valid(e.Payload) {
			t.Fatalf("event %s payload is not valid JSON", e.Type)
		}
	}

	result, err := sess.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Edited NOTES.md." {
		t.Fatalf("summary = %q", result.Summary)
	}
	if len(result.FilesChanged) != 1 || result.FilesChanged[0] != "NOTES.md" {
		t.Fatalf("FilesChanged = %v", result.FilesChanged)
	}
	content, err := os.ReadFile(filepath.Join(ws, "NOTES.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "stub wrote this\n" {
		t.Fatalf("NOTES.md = %q", content)
	}
}

func TestClaudeCodeResultError(t *testing.T) {
	requireSh(t)
	stub := `printf '%s\n' '{"type":"system","subtype":"init","model":"m","session_id":"s"}'
printf '%s\n' '{"type":"result","subtype":"error_max_turns","is_error":true,"result":"gave up"}'
`
	sess, err := NewClaudeCode(ClaudeCodeOptions{CLIPath: stubCLI(t, stub)}).
		Start(context.Background(), Request{WorkspaceDir: t.TempDir(), Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, sess)
	last := events[len(events)-1]
	if last.Type != EventSessionFailed {
		t.Fatalf("last event = %s, want %s", last.Type, EventSessionFailed)
	}
	if r := reasonOf(t, last); r != "error_max_turns" {
		t.Fatalf("reason = %q, want error_max_turns", r)
	}
	if _, err := sess.Wait(context.Background()); err == nil {
		t.Fatal("Wait err = nil after error result")
	}
}

func TestClaudeCodeDeadline(t *testing.T) {
	requireSh(t)
	runCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	sess, err := NewClaudeCode(ClaudeCodeOptions{
		CLIPath: stubCLI(t, "exec sleep 30\n"),
	}).Start(runCtx, Request{WorkspaceDir: t.TempDir(), Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	events := drain(t, sess)
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("timeout took %v; process not killed", d)
	}
	last := events[len(events)-1]
	if last.Type != EventSessionFailed || reasonOf(t, last) != "timeout" {
		t.Fatalf("last event = %s (%s), want session_failed timeout", last.Type, last.Payload)
	}
	if _, err := sess.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait err = %v, want DeadlineExceeded", err)
	}
}

func TestClaudeCodeDeadlineKillsToolSubprocesses(t *testing.T) {
	requireSh(t)
	runCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	stub := `sleep 30 &
exec sleep 30
`
	sess, err := NewClaudeCode(ClaudeCodeOptions{
		CLIPath: stubCLI(t, stub),
	}).Start(runCtx, Request{WorkspaceDir: t.TempDir(), Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	events := drain(t, sess)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("session ended after %v; orphaned subprocess held it open", d)
	}
	last := events[len(events)-1]
	if last.Type != EventSessionFailed || reasonOf(t, last) != "timeout" {
		t.Fatalf("last event = %s (%s), want session_failed timeout", last.Type, last.Payload)
	}
	if _, err := sess.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait err = %v, want DeadlineExceeded", err)
	}
}

func TestClaudeCodeCancel(t *testing.T) {
	requireSh(t)
	stub := `printf '%s\n' '{"type":"system","subtype":"init","model":"m","session_id":"s"}'
exec sleep 30
`
	sess, err := NewClaudeCode(ClaudeCodeOptions{CLIPath: stubCLI(t, stub)}).
		Start(context.Background(), Request{WorkspaceDir: t.TempDir(), Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}

	first := <-sess.Events()
	if first.Type != EventSessionStarted {
		t.Fatalf("first event = %s, want %s", first.Type, EventSessionStarted)
	}
	if err := sess.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := drain(t, sess)
	last := events[len(events)-1]
	if last.Type != EventSessionFailed || reasonOf(t, last) != "cancelled" {
		t.Fatalf("last event = %s (%s), want session_failed cancelled", last.Type, last.Payload)
	}
	if _, err := sess.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want Canceled", err)
	}
}

func TestClaudeCodeNoShellInterpolation(t *testing.T) {
	requireSh(t)
	ws := t.TempDir()
	stub := `printf '%s\n' '{"type":"result","subtype":"success","result":"done"}'
`
	sess, err := NewClaudeCode(ClaudeCodeOptions{CLIPath: stubCLI(t, stub)}).
		Start(context.Background(), Request{
			WorkspaceDir: ws,
			Instructions: "$(touch INJECTED) `touch INJECTED` ; touch INJECTED",
		})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, sess)
	if _, err := sess.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "INJECTED")); !os.IsNotExist(err) {
		t.Fatal("instructions were shell-interpreted: sentinel file created")
	}
}

func TestClaudeCodeValidateConfiguration(t *testing.T) {
	requireSh(t)
	missing := NewClaudeCode(ClaudeCodeOptions{CLIPath: filepath.Join(t.TempDir(), "nope")})
	if err := missing.ValidateConfiguration(context.Background()); err == nil {
		t.Fatal("missing CLI validated ok")
	}

	cli := stubCLI(t, "printf '%s\\n' '2.1.3 (Claude Code)'\n")
	if err := NewClaudeCode(ClaudeCodeOptions{CLIPath: cli}).
		ValidateConfiguration(context.Background()); err != nil {
		t.Fatalf("runnable CLI: %v", err)
	}
	if err := NewClaudeCode(ClaudeCodeOptions{CLIPath: cli, PinnedVersion: "2.1.3"}).
		ValidateConfiguration(context.Background()); err != nil {
		t.Fatalf("matching pin: %v", err)
	}
	if err := NewClaudeCode(ClaudeCodeOptions{CLIPath: cli, PinnedVersion: "9.9.9"}).
		ValidateConfiguration(context.Background()); err == nil {
		t.Fatal("mismatched pin validated ok")
	}
	if err := NewClaudeCode(ClaudeCodeOptions{CLIPath: cli, PinnedVersion: "2.1"}).
		ValidateConfiguration(context.Background()); err == nil {
		t.Fatal("prefix pin validated ok")
	}
}

func TestClaudeCodeRedactsCredentials(t *testing.T) {
	requireSh(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-key-123")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-token-456")
	stub := `echo "auth failed key=$ANTHROPIC_API_KEY token=$CLAUDE_CODE_OAUTH_TOKEN" >&2
exit 1
`
	sess, err := NewClaudeCode(ClaudeCodeOptions{CLIPath: stubCLI(t, stub)}).
		Start(context.Background(), Request{WorkspaceDir: t.TempDir(), Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, sess)
	last := events[len(events)-1]
	if last.Type != EventSessionFailed {
		t.Fatalf("last event = %s, want %s", last.Type, EventSessionFailed)
	}
	payload := string(last.Payload)
	for _, secret := range []string{"sk-test-key-123", "oauth-token-456"} {
		if strings.Contains(payload, secret) {
			t.Fatalf("failure detail leaked credential %q: %s", secret, payload)
		}
	}
	if !strings.Contains(payload, "***") {
		t.Fatalf("failure detail carries no redaction marker: %s", payload)
	}
	if _, err := sess.Wait(context.Background()); err == nil {
		t.Fatal("Wait err = nil after CLI failure")
	}
}

func TestClaudeCodeRejectsBadWorkspace(t *testing.T) {
	ad := NewClaudeCode(ClaudeCodeOptions{CLIPath: "claude"})
	if _, err := ad.Start(context.Background(), Request{}); err == nil {
		t.Fatal("empty workspace dir accepted")
	}
	if _, err := ad.Start(context.Background(), Request{
		WorkspaceDir: filepath.Join(t.TempDir(), "missing"),
	}); err == nil {
		t.Fatal("missing workspace dir accepted")
	}
}

func TestClaudeCodePreservesCostPresence(t *testing.T) {
	requireSh(t)
	for _, tc := range []struct {
		name, cost string
		reported   bool
	}{
		{"absent", "", false}, {"null", `,"total_cost_usd":null`, false}, {"zero", `,"total_cost_usd":0`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := `printf '%s\n' '{"type":"result","subtype":"success","result":"done"` + tc.cost + `}'`
			sess, err := NewClaudeCode(ClaudeCodeOptions{CLIPath: stubCLI(t, stub)}).Start(t.Context(), Request{WorkspaceDir: t.TempDir(), Instructions: "x"})
			if err != nil {
				t.Fatal(err)
			}
			events := drain(t, sess)
			found := false
			for _, event := range events {
				if event.Type != EventCostUpdate {
					continue
				}
				found = true
				var payload map[string]any
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				value, reported := payload["total_cost_usd"]
				if reported != tc.reported || (reported && value != float64(0)) {
					t.Fatalf("cost payload = %s", event.Payload)
				}
			}
			if !found {
				t.Fatal("missing usage event")
			}
			if _, err := sess.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
