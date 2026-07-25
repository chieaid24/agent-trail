package observability

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

// Registry holds process-local counters and serves them in the Prometheus
// text exposition format. Counter names follow
// docs/operations/observability.md. A real client library can replace this
// when histograms or labels are needed; plain counters do not justify the
// dependency yet.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
}

// Counter is a monotonically increasing metric.
type Counter struct {
	name string
	help string
	v    atomic.Uint64
}

// NewRegistry returns an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{counters: map[string]*Counter{}}
}

// Counter returns the named counter, creating it on first use.
func (r *Registry) Counter(name, help string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c := &Counter{name: name, help: help}
	r.counters[name] = c
	return c
}

// Inc adds one to the counter.
func (c *Counter) Inc() { c.v.Add(1) }

// Value returns the current count.
func (c *Counter) Value() uint64 { return c.v.Load() }

// Handler serves GET /metrics in the Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		names := make([]string, 0, len(r.counters))
		for name := range r.counters {
			names = append(names, name)
		}
		sort.Strings(names)
		counters := make([]*Counter, 0, len(names))
		for _, name := range names {
			counters = append(counters, r.counters[name])
		}
		r.mu.Unlock()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		for _, c := range counters {
			fmt.Fprintf(w, "# HELP %s %s\n", c.name, c.help)
			fmt.Fprintf(w, "# TYPE %s counter\n", c.name)
			fmt.Fprintf(w, "%s %d\n", c.name, c.Value())
		}
	})
}
