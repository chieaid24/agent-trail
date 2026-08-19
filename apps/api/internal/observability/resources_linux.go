//go:build linux

package observability

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const resourceSamplingSupported = true

// processCPUSeconds returns the process's cumulative user+system CPU time.
func processCPUSeconds() (float64, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, err
	}
	toSec := func(tv syscall.Timeval) float64 {
		return float64(tv.Sec) + float64(tv.Usec)/1e6
	}
	return toSec(ru.Utime) + toSec(ru.Stime), nil
}

// processRSSBytes returns the process resident set size from /proc.
func processRSSBytes() (float64, error) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected /proc/self/statm: %q", data)
	}
	pages, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, err
	}
	return pages * float64(os.Getpagesize()), nil
}

// diskUsedFraction returns the used fraction of the filesystem at path.
func diskUsedFraction(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	if total <= 0 {
		return 0, nil
	}
	free := float64(st.Bavail) * float64(st.Bsize)
	return (total - free) / total, nil
}
