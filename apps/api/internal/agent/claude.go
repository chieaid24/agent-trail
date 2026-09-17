package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const ClaudeProvider = "claude-code"

// full assistant message with tool inputs far exceeds bufio's 64k default
const maxStreamLine = 8 * 1024 * 1024

type ClaudeCodeOptions struct {
	CLIPath        string
	Model          string
	PermissionMode string
	PinnedVersion  string
	Logger         *slog.Logger
}

type ClaudeCode struct {
	opts ClaudeCodeOptions
}

func NewClaudeCode(opts ClaudeCodeOptions) *ClaudeCode {
	if opts.CLIPath == "" {
		opts.CLIPath = "claude"
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = "acceptEdits"
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ClaudeCode{opts: opts}
}

func (c *ClaudeCode) Name() string { return ClaudeProvider }

func (c *ClaudeCode) ValidateConfiguration(ctx context.Context) error {
	path, err := exec.LookPath(c.opts.CLIPath)
	if err != nil {
		return fmt.Errorf("claude-code: CLI %q not found on PATH: %w", c.opts.CLIPath, err)
	}
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return fmt.Errorf("claude-code: %q --version failed: %w", path, err)
	}
	version := strings.TrimSpace(string(out))
	if c.opts.PinnedVersion != "" && !pinMatches(version, c.opts.PinnedVersion) {
		return fmt.Errorf("claude-code: CLI version %q does not match pinned %q",
			version, c.opts.PinnedVersion)
	}
	return nil
}

// whole-token match: pin "2.1.3" must not accept "2.1.30"
func pinMatches(version, pinned string) bool {
	for _, tok := range strings.FieldsFunc(version, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')'
	}) {
		if tok == pinned {
			return true
		}
	}
	return false
}

// events must be drained before wait
func (c *ClaudeCode) Start(ctx context.Context, req Request) (Session, error) {
	if req.WorkspaceDir == "" {
		return nil, errors.New("claude-code: workspace dir required")
	}
	info, err := os.Stat(req.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("claude-code: workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("claude-code: workspace %q is not a directory", req.WorkspaceDir)
	}

	runCtx, cancel := context.WithCancel(ctx)

	// argv only, never a shell: instructions are never shell-interpreted
	args := []string{
		"--print", req.Instructions,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", c.opts.PermissionMode,
	}
	if c.opts.Model != "" {
		args = append(args, "--model", c.opts.Model)
	}

	cmd := exec.CommandContext(runCtx, c.opts.CLIPath, args...)
	cmd.Dir = req.WorkspaceDir
	cmd.Env = claudeEnv()
	// tool subprocs inherit stdout: kill whole pgroup or the pipe stays open and the session never ends
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude-code: stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("claude-code: start CLI: %w", err)
	}

	s := &claudeSession{
		cmd:    cmd,
		stdout: stdout,
		stderr: &stderr,
		cancel: cancel,
		events: make(chan Event),
		done:   make(chan struct{}),
		logger: c.opts.Logger,
		files:  map[string]struct{}{},
	}
	go s.run(runCtx)
	return s, nil
}

// secret env forwarded to the cli; values redacted from any failure detail
var credentialEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// isolates cli from host config; creds forwarded from process env, never stored
func claudeEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"CI=1",
		"TERM=dumb",
		"DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
	for _, k := range append([]string{"ANTHROPIC_BASE_URL"}, credentialEnv...) {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

type claudeSession struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *bytes.Buffer
	cancel context.CancelFunc
	events chan Event
	done   chan struct{}
	logger *slog.Logger

	mu        sync.Mutex
	files     map[string]struct{}
	sessionID string
	summary   string
	failed    bool
	result    Result
	err       error
}

func (s *claudeSession) Events() <-chan Event { return s.events }

func (s *claudeSession) Send(ctx context.Context, message string) error {
	return errors.New("claude-code: session does not accept follow-up input")
}

func (s *claudeSession) Cancel(ctx context.Context) error {
	s.cancel()
	return nil
}

func (s *claudeSession) Wait(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-s.done:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.err
}

func (s *claudeSession) emit(t EventType, payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("{}")
	}
	s.events <- Event{Type: t, Timestamp: time.Now().UTC(), Payload: raw}
}

func (s *claudeSession) run(runCtx context.Context) {
	defer close(s.done)
	defer close(s.events)

	toolNames := map[string]string{}
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLine)

	sawResult := false
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		s.normalize(line, toolNames, &sawResult)
	}
	scanErr := scanner.Err()

	// stdout drained so wait can't deadlock; grab ctx err before self-cancel or finish reads it as external stop
	waitErr := s.cmd.Wait()
	ctxErr := runCtx.Err()
	s.cancel()
	s.finish(ctxErr, sawResult, scanErr, waitErr)
}

type claudeLine struct {
	Type         string          `json:"type"`
	Subtype      string          `json:"subtype"`
	Model        string          `json:"model"`
	SessionID    string          `json:"session_id"`
	Message      json.RawMessage `json:"message"`
	Result       string          `json:"result"`
	IsError      bool            `json:"is_error"`
	TotalCostUSD *float64        `json:"total_cost_usd"`
	DurationMS   int64           `json:"duration_ms"`
	NumTurns     int             `json:"num_turns"`
	Usage        json.RawMessage `json:"usage"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func (s *claudeSession) normalize(line []byte, toolNames map[string]string, sawResult *bool) {
	var l claudeLine
	if err := json.Unmarshal(line, &l); err != nil {
		s.emit(EventWarning, map[string]any{"reason": "unparsable provider line"})
		return
	}
	switch l.Type {
	case "system":
		// only init opens a session; other system records are housekeeping
		if l.Subtype == "init" {
			s.mu.Lock()
			s.sessionID = l.SessionID
			s.mu.Unlock()
			s.emit(EventSessionStarted, map[string]any{
				"adapter": ClaudeProvider, "model": l.Model, "session_id": l.SessionID,
			})
		}
	case "assistant":
		s.normalizeContent(l.Message, toolNames)
	case "user":
		s.normalizeContent(l.Message, toolNames)
	case "rate_limit_event":
	case "result":
		*sawResult = true
		cost := map[string]any{
			"duration_ms": l.DurationMS, "num_turns": l.NumTurns, "usage": rawOrNull(l.Usage),
		}
		if l.TotalCostUSD != nil {
			cost["total_cost_usd"] = *l.TotalCostUSD
		}
		s.emit(EventCostUpdate, cost)
		s.mu.Lock()
		s.summary = l.Result
		s.mu.Unlock()
		if l.IsError || (l.Subtype != "" && l.Subtype != "success") {
			reason := l.Subtype
			if reason == "" {
				reason = "error"
			}
			s.fail(reason, l.Result)
			return
		}
		s.emit(EventSessionCompleted, map[string]any{"summary": l.Result})
	default:
		s.emit(EventWarning, map[string]any{"unhandled_type": l.Type})
	}
}

func (s *claudeSession) normalizeContent(raw json.RawMessage, toolNames map[string]string) {
	if len(raw) == 0 {
		return
	}
	var m struct {
		Content []claudeBlock `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				s.emit(EventAssistantMessage, map[string]any{"message": b.Text})
			}
		case "tool_use":
			toolNames[b.ID] = b.Name
			s.emitToolUse(b)
		case "tool_result":
			s.emitToolResult(b, toolNames)
		}
	}
}

// invocation of these is the plan, not a generic tool request
var planTools = map[string]bool{"ExitPlanMode": true, "TodoWrite": true}

var (
	fileReadTools  = map[string]bool{"Read": true}
	fileWriteTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true}
)

func (s *claudeSession) emitToolUse(b claudeBlock) {
	if planTools[b.Name] {
		s.emit(EventPlan, map[string]any{"tool": b.Name, "plan": rawOrNull(b.Input)})
		return
	}
	s.emit(EventToolRequested, map[string]any{
		"tool": b.Name, "tool_use_id": b.ID, "input": rawOrNull(b.Input),
	})
	path := toolFilePath(b.Input)
	if path == "" {
		return
	}
	switch {
	case fileReadTools[b.Name]:
		s.emit(EventFileRead, map[string]any{"path": path})
	case fileWriteTools[b.Name]:
		s.emit(EventFileWritten, map[string]any{"path": path})
		s.mu.Lock()
		s.files[path] = struct{}{}
		s.mu.Unlock()
	}
}

func (s *claudeSession) emitToolResult(b claudeBlock, toolNames map[string]string) {
	name := toolNames[b.ToolUseID]
	if len(b.Content) > 0 {
		s.emit(EventToolOutput, map[string]any{
			"tool": name, "tool_use_id": b.ToolUseID, "output": rawOrNull(b.Content),
		})
	}
	s.emit(EventToolCompleted, map[string]any{
		"tool": name, "tool_use_id": b.ToolUseID, "is_error": b.IsError,
	})
}

func (s *claudeSession) fail(reason, detail string) {
	s.emit(EventSessionFailed, map[string]any{"reason": reason, "detail": s.redact(detail)})
	s.mu.Lock()
	if s.err == nil {
		s.err = fmt.Errorf("claude-code: %s", reason)
	}
	s.failed = true
	s.mu.Unlock()
}

// ctxErr captured before self-cancel: non-nil only on external cancel/timeout
func (s *claudeSession) finish(ctxErr error, sawResult bool, scanErr, waitErr error) {
	s.mu.Lock()
	files := make([]string, 0, len(s.files))
	for f := range s.files {
		files = append(files, f)
	}
	sort.Strings(files)
	sessionID := s.sessionID
	summary := s.summary
	alreadyFailed := s.failed
	s.mu.Unlock()

	setResult := func(err error) {
		s.mu.Lock()
		s.result = Result{Summary: summary, FilesChanged: files}
		if err != nil {
			s.err = err
		}
		s.mu.Unlock()
	}

	// surface ctx err so executor tells a shutdown from an agent fault
	if ctxErr != nil && !alreadyFailed {
		reason := "cancelled"
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			reason = "timeout"
		}
		s.emit(EventSessionFailed, map[string]any{"reason": reason})
		s.mu.Lock()
		s.result = Result{}
		s.err = ctxErr
		s.mu.Unlock()
		s.logger.LogAttrs(context.Background(), slog.LevelWarn, "claude session stopped",
			slog.String("event", "agent_session_stopped"), slog.String("reason", reason),
			slog.String("session_id", sessionID))
		return
	}
	if alreadyFailed {
		setResult(nil)
		return
	}
	// crash / read error / truncated stream: fail safe with redacted reason
	if !sawResult || scanErr != nil || waitErr != nil {
		reason := "cli exited without a result"
		detail := ""
		if scanErr != nil {
			detail = scanErr.Error()
		}
		if waitErr != nil {
			reason = "cli process error"
			detail = strings.TrimSpace(waitErr.Error() + " " + s.stderr.String())
		}
		s.emit(EventSessionFailed, map[string]any{"reason": reason, "detail": s.redact(detail)})
		setResult(fmt.Errorf("claude-code: %s", reason))
		return
	}
	setResult(nil)
}

// url userinfo (token in clone url) stripped before logs/events
var credentialInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// best-effort: only strips values it knows
func (s *claudeSession) redact(msg string) string {
	msg = credentialInURL.ReplaceAllString(msg, "$1***@")
	for _, k := range credentialEnv {
		if v := os.Getenv(k); v != "" {
			msg = strings.ReplaceAll(msg, v, "***")
		}
	}
	if len(msg) > 4096 {
		msg = msg[:4096] + "..."
	}
	return strings.TrimSpace(msg)
}

func toolFilePath(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "notebook_path"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func rawOrNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}
