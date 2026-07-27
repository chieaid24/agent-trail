package validation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// Status classifies one trusted check outcome. Failed means the command ran
// and exited nonzero (a real check failure); timed_out and error are
// infrastructure outcomes and are never conflated with failed.
type Status string

const (
	StatusPassed   Status = "passed"
	StatusFailed   Status = "failed"
	StatusTimedOut Status = "timed_out"
	StatusError    Status = "error"
)

// maxCapturedOutput bounds the output kept per check for the summary.
const maxCapturedOutput = 64 << 10

// maxSummaryLen bounds the stored one-line summary.
const maxSummaryLen = 500

// Result is one executed check's outcome. ExitCode is nil when the process
// produced none (start failure, killed at timeout).
type Result struct {
	Name             string
	Category         string
	Command          []string
	Status           Status
	ExitCode         *int
	DurationMS       int64
	Summary          string
	TrustedExecution bool
}

// Runner executes a validation file's checks sequentially inside a
// workspace. Commands are argument arrays run without a shell; exit codes
// are recorded as measured and nothing downgrades a failure.
type Runner struct {
	Logger *slog.Logger
}

// Run executes every check and returns the results in order. observe (may
// be nil) is called after each check completes, for activity events. A
// cancelled parent context stops the loop; completed results are returned.
func (r *Runner) Run(ctx context.Context, workspaceDir string, f File, observe func(Result)) []Result {
	results := make([]Result, 0, len(f.Validation))
	for _, c := range f.Validation {
		if ctx.Err() != nil {
			break
		}
		res := r.runCheck(ctx, workspaceDir, c)
		results = append(results, res)
		if observe != nil {
			observe(res)
		}
	}
	return results
}

func (r *Runner) runCheck(ctx context.Context, dir string, c Check) Result {
	res := Result{
		Name:             c.Name,
		Category:         c.Category,
		Command:          c.Command,
		TrustedExecution: true,
	}
	timeout := time.Duration(c.EffectiveTimeoutSeconds()) * time.Second
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var out bytes.Buffer
	cmd := exec.CommandContext(cctx, c.Command[0], c.Command[1:]...)
	cmd.Dir = dir
	cmd.Stdout = &boundedWriter{buf: &out, limit: maxCapturedOutput}
	cmd.Stderr = cmd.Stdout

	start := time.Now()
	err := cmd.Run()
	res.DurationMS = time.Since(start).Milliseconds()

	switch {
	case err == nil:
		code := 0
		res.ExitCode = &code
		res.Status = StatusPassed
		res.Summary = summarize(out.Bytes(), "passed")
	case cctx.Err() != nil && ctx.Err() == nil:
		// The check's own deadline killed it; the exit code of a killed
		// process measures nothing, so none is recorded.
		res.Status = StatusTimedOut
		res.Summary = fmt.Sprintf("timed out after %s", timeout)
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
			code := exitErr.ExitCode()
			res.ExitCode = &code
			res.Status = StatusFailed
			res.Summary = summarize(out.Bytes(), err.Error())
		} else {
			// The command never ran (missing binary, permission, cancel):
			// an infrastructure failure, distinct from a check failure.
			res.Status = StatusError
			res.Summary = truncate(sanitize(err.Error()), maxSummaryLen)
		}
	}

	if r.Logger != nil {
		r.Logger.LogAttrs(ctx, slog.LevelInfo, "trusted check finished",
			slog.String("event", "validation_check_finished"),
			slog.String("check", c.Name),
			slog.String("status", string(res.Status)),
			slog.Int64("duration_ms", res.DurationMS),
		)
	}
	return res
}

// summarize returns the last non-empty output line, or fallback.
func summarize(out []byte, fallback string) string {
	lines := strings.Split(strings.TrimSpace(sanitize(string(out))), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return truncate(line, maxSummaryLen)
		}
	}
	return truncate(sanitize(fallback), maxSummaryLen)
}

// sanitize makes untrusted process output storable: Postgres TEXT rejects
// NUL bytes and invalid UTF-8.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.ToValidUTF8(s, string(utf8.RuneError))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	// Never split a rune: back off any partial encoding at the cut.
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "..."
}

// boundedWriter keeps the first limit bytes and discards the rest, so a
// chatty check cannot balloon memory; the process is never blocked.
type boundedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			w.buf.Write(p[:remaining])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}
