package runner

import "context"

type RunnerBackend interface {
	Run(context.Context) error
}

type ProcessBackend struct {
	Host *Host
}

func (b *ProcessBackend) Run(ctx context.Context) error {
	return b.Host.Run(ctx)
}
