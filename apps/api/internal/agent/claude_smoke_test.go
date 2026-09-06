package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// opt-in, spends tokens: REAL_CLAUDE_SMOKE=1 go test ./internal/agent -run RealCLISmoke -v
func TestClaudeCodeRealCLISmoke(t *testing.T) {
	if os.Getenv("REAL_CLAUDE_SMOKE") == "" {
		t.Skip("REAL_CLAUDE_SMOKE not set (spends provider tokens)")
	}
	ws := t.TempDir()
	ad := NewClaudeCode(ClaudeCodeOptions{
		Model: "haiku",
	})
	if err := ad.ValidateConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sess, err := ad.Start(runCtx, Request{
		WorkspaceDir: ws,
		Instructions: "Create a file named SMOKE.md whose entire content is the single line: smoke test passed",
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drain(t, sess)
	types := eventTypes(events)
	if types[len(types)-1] != EventSessionCompleted {
		t.Fatalf("last event = %s, want %s (all: %v)", types[len(types)-1], EventSessionCompleted, types)
	}

	result, err := sess.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FilesChanged) == 0 {
		t.Errorf("FilesChanged empty, want SMOKE.md")
	}
	content, err := os.ReadFile(filepath.Join(ws, "SMOKE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "smoke test passed") {
		t.Fatalf("SMOKE.md = %q", content)
	}
	t.Logf("summary=%q files=%v events=%v", result.Summary, result.FilesChanged, types)
}
