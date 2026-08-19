// Package bench holds the reproducible benchmarks and failure-injection
// tests behind docs/testing/benchmark-results.md. Every test skips unless
// AGENT_TRAIL_BENCH=1, so the CI gate's plain `go test ./...` never pays
// for a load run; scripts/bench.sh is the entry point that sets the flag,
// boots a dedicated database, and captures the numbers.
package bench
