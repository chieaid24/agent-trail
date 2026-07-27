package agent

import (
	"fmt"
	"log/slog"
	"time"
)

// Provider names selectable through configuration (AGENT_PROVIDER).
const (
	// ProviderFake is the deterministic no-model adapter (default).
	ProviderFake = "fake"
	// ProviderClaudeCode is the Claude Code CLI adapter.
	ProviderClaudeCode = ClaudeProvider
)

// Options selects and configures an Adapter. The worker builds it from
// config, keeping provider-specific settings out of the core domain.
type Options struct {
	Provider       string
	CLIPath        string
	Model          string
	PermissionMode string
	PinnedVersion  string
	Timeout        time.Duration
	Logger         *slog.Logger
}

// New builds the adapter named by opts.Provider. An empty provider is the
// fake; an unknown one is a configuration error.
func New(opts Options) (Adapter, error) {
	switch opts.Provider {
	case ProviderFake, "":
		return NewFake(), nil
	case ProviderClaudeCode:
		return NewClaudeCode(ClaudeCodeOptions{
			CLIPath:        opts.CLIPath,
			Model:          opts.Model,
			PermissionMode: opts.PermissionMode,
			PinnedVersion:  opts.PinnedVersion,
			Timeout:        opts.Timeout,
			Logger:         opts.Logger,
		}), nil
	default:
		return nil, fmt.Errorf("agent: unknown provider %q", opts.Provider)
	}
}
