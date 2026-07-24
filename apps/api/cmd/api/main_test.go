package main

import (
	"net"
	"strings"
	"testing"
)

func TestRunRejectsBadConfig(t *testing.T) {
	t.Setenv("LOG_LEVEL", "loud")
	if err := run(); err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("err = %v, want LOG_LEVEL error", err)
	}
}

func TestRunReportsListenFailure(t *testing.T) {
	// Occupy a port so ListenAndServe fails immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("API_ADDR", ln.Addr().String())
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LOG_LEVEL", "error")
	if err := run(); err == nil {
		t.Fatal("run succeeded on an occupied port")
	}
}
