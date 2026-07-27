package validation

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runOne(t *testing.T, c Check) Result {
	t.Helper()
	results := (&Runner{}).Run(context.Background(), t.TempDir(),
		File{Version: 1, Validation: []Check{c}}, nil)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	return results[0]
}

// TestRunPreservesExitCodes: measured outcomes only - exit codes are
// recorded as observed and every trusted result carries the flag.
func TestRunPreservesExitCodes(t *testing.T) {
	pass := runOne(t, Check{Name: "ok", Category: "custom", Command: []string{"true"}})
	if pass.Status != StatusPassed || pass.ExitCode == nil || *pass.ExitCode != 0 ||
		!pass.TrustedExecution {
		t.Fatalf("pass = %+v", pass)
	}

	fail := runOne(t, Check{Name: "bad", Category: "unit_test", Command: []string{"false"}})
	if fail.Status != StatusFailed || fail.ExitCode == nil || *fail.ExitCode != 1 ||
		!fail.TrustedExecution {
		t.Fatalf("fail = %+v", fail)
	}
}

// TestRunDistinguishesInfrastructureFailures: a command that never ran is
// error, and a timeout is timed_out - neither is a check failure.
func TestRunDistinguishesInfrastructureFailures(t *testing.T) {
	missing := runOne(t, Check{Name: "gone", Category: "build",
		Command: []string{"agent-trail-no-such-binary"}})
	if missing.Status != StatusError || missing.ExitCode != nil {
		t.Fatalf("missing = %+v, want error with no exit code", missing)
	}

	slow := runOne(t, Check{Name: "slow", Category: "custom",
		Command: []string{"sleep", "5"}, TimeoutSeconds: 1})
	if slow.Status != StatusTimedOut || slow.ExitCode != nil {
		t.Fatalf("slow = %+v, want timed_out with no exit code", slow)
	}
	if !strings.Contains(slow.Summary, "timed out") {
		t.Fatalf("slow summary = %q", slow.Summary)
	}
}

func TestRunSummarizesLastOutputLine(t *testing.T) {
	echo := runOne(t, Check{Name: "echo", Category: "custom",
		Command: []string{"echo", "42 tests passed"}})
	if echo.Summary != "42 tests passed" {
		t.Fatalf("summary = %q", echo.Summary)
	}
}

// TestRunStopsOnCancelledContext: a cancelled parent stops the loop so a
// lost lease cannot keep spending workspace time.
func TestRunStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := (&Runner{}).Run(ctx, t.TempDir(), File{Version: 1, Validation: []Check{
		{Name: "never", Category: "custom", Command: []string{"true"}},
	}}, nil)
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none after cancel", results)
	}
}

func TestRunObserverSeesEveryResult(t *testing.T) {
	var seen []string
	(&Runner{}).Run(context.Background(), t.TempDir(), File{Version: 1, Validation: []Check{
		{Name: "one", Category: "custom", Command: []string{"true"}},
		{Name: "two", Category: "custom", Command: []string{"false"}},
	}}, func(r Result) { seen = append(seen, r.Name) })
	if len(seen) != 2 || seen[0] != "one" || seen[1] != "two" {
		t.Fatalf("observed = %v", seen)
	}
}

func TestBoundedWriterTruncates(t *testing.T) {
	var buf bytes.Buffer
	w := &boundedWriter{buf: &buf, limit: 4}
	for i := 0; i < 3; i++ {
		n, err := w.Write([]byte("abc"))
		if n != 3 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if buf.String() != "abca" {
		t.Fatalf("buffer = %q, want first 4 bytes", buf.String())
	}
}
