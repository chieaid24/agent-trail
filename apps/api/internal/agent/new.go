package agent

import (
	"fmt"
	"log/slog"
)

const (
	ProviderFake       = "fake"
	ProviderClaudeCode = ClaudeProvider
)

type Options struct {
	Provider       string
	CLIPath        string
	Model          string
	PermissionMode string
	PinnedVersion  string
	Logger         *slog.Logger
}

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
			Logger:         opts.Logger,
		}), nil
	default:
		return nil, fmt.Errorf("agent: unknown provider %q", opts.Provider)
	}
}
