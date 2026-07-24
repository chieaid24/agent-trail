package main

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestRunLoopStopsOnCancel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runLoop(ctx, logger, time.Millisecond)
		close(done)
	}()

	// Let at least one heartbeat fire, then cancel.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLoop did not stop on cancel")
	}
	if !bytes.Contains(buf.Bytes(), []byte("worker_heartbeat")) {
		t.Error("no heartbeat logged")
	}
}
