package runner

import "context"

// Backend runs the configured attempt execution strategy.
type Backend interface {
	Run(context.Context) error
}

// ProcessBackend executes attempts in the current process.
type ProcessBackend struct {
	Host *Host
}

func (b *ProcessBackend) Run(ctx context.Context) error {
	return b.Host.Run(ctx)
}
