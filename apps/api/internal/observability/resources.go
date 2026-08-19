package observability

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

// RegisterRunnerResources samples process CPU, RSS, and workspace disk usage.
func RegisterRunnerResources(reg *Registry, workspaceRoot string, logger *slog.Logger) {
	if !resourceSamplingSupported {
		logger.Warn("runner resource gauges unsupported on this platform",
			slog.String("event", "runner_resources_unsupported"))
		return
	}
	diskPath := workspaceRoot
	if _, err := os.Stat(diskPath); err != nil {
		diskPath = os.TempDir()
	}

	// The first CPU sample establishes the baseline and reports zero.
	var mu sync.Mutex
	lastWall := time.Now()
	lastCPU, _ := processCPUSeconds()
	reg.Gauge("agent_trail_runner_cpu_usage",
		"Fraction of one CPU core used by the runner process.",
		func() float64 {
			mu.Lock()
			defer mu.Unlock()
			cpu, err := processCPUSeconds()
			if err != nil {
				return 0
			}
			now := time.Now()
			wall := now.Sub(lastWall).Seconds()
			used := cpu - lastCPU
			lastWall, lastCPU = now, cpu
			if wall <= 0 || used < 0 {
				return 0
			}
			return used / wall
		})

	reg.Gauge("agent_trail_runner_memory_usage",
		"Resident set size of the runner process in bytes.",
		func() float64 {
			rss, err := processRSSBytes()
			if err != nil {
				return 0
			}
			return rss
		})

	reg.Gauge("agent_trail_runner_disk_usage",
		"Used fraction of the filesystem holding the workspace root.",
		func() float64 {
			frac, err := diskUsedFraction(diskPath)
			if err != nil {
				return 0
			}
			return frac
		})
}
