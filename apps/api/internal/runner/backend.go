package runner

import "context"

// RunnerBackend runs the configured attempt execution strategy.
type RunnerBackend interface {
	Run(context.Context) error
}

// ProcessBackend executes attempts in the current process.
type ProcessBackend struct {
	Host *Host
}

func (b *ProcessBackend) Run(ctx context.Context) error {
	return b.Host.Run(ctx)
}
