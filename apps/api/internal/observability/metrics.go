package observability

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Registry is the metrics facade for the control plane, backed by the
// OpenTelemetry SDK. Metric names follow docs/operations/observability.md
// exactly: instruments carry no OTel unit and the Prometheus exporter adds
// no suffixes, so the exposition names equal the instrument names. Every
// Registry serves /metrics via Handler(); Setup (otel.go) additionally
// attaches an OTLP push reader.
type Registry struct {
	meter    metric.Meter
	provider *sdkmetric.MeterProvider
	promReg  *prometheus.Registry

	mu         sync.Mutex
	counters   map[string]*Counter
	histograms map[string]*Histogram
	updowns    map[string]*UpDownCounter
	gauges     map[string]struct{}
}

// Label is one metric attribute; a bounded key/value pair.
type Label struct {
	Key   string
	Value string
}

func attrs(labels []Label) metric.MeasurementOption {
	kvs := make([]attribute.KeyValue, len(labels))
	for i, l := range labels {
		kvs[i] = attribute.String(l.Key, l.Value)
	}
	return metric.WithAttributes(kvs...)
}

// NewRegistry returns a standalone registry: /metrics only, no OTLP export.
// Binaries wire export through Setup; tests and tools use this directly.
func NewRegistry() *Registry {
	r, err := newRegistry(nil, nil)
	if err != nil {
		// Construction only fails on exporter misconfiguration, which is
		// impossible with the fixed options below; degrade loudly.
		panic(err)
	}
	return r
}

func newRegistry(res *resource.Resource, extra []sdkmetric.Reader) (*Registry, error) {
	promReg := prometheus.NewRegistry()
	exporter, err := otelprom.New(
		otelprom.WithRegisterer(promReg),
		otelprom.WithoutUnits(),
		otelprom.WithoutCounterSuffixes(),
		otelprom.WithoutScopeInfo(),
		otelprom.WithoutTargetInfo(),
	)
	if err != nil {
		return nil, err
	}
	opts := []sdkmetric.Option{sdkmetric.WithReader(exporter)}
	if res != nil {
		opts = append(opts, sdkmetric.WithResource(res))
	}
	for _, r := range extra {
		opts = append(opts, sdkmetric.WithReader(r))
	}
	provider := sdkmetric.NewMeterProvider(opts...)
	return &Registry{
		meter:      provider.Meter("agent-trail"),
		provider:   provider,
		promReg:    promReg,
		counters:   map[string]*Counter{},
		histograms: map[string]*Histogram{},
		updowns:    map[string]*UpDownCounter{},
		gauges:     map[string]struct{}{},
	}, nil
}

// Counter is a monotonically increasing metric, optionally labelled.
type Counter struct {
	c     metric.Int64Counter
	total atomic.Uint64
}

// Counter returns the named counter, creating it on first use.
func (r *Registry) Counter(name, help string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	inst, err := r.meter.Int64Counter(name, metric.WithDescription(help))
	if err != nil {
		otel.Handle(err)
	}
	c := &Counter{c: inst}
	r.counters[name] = c
	return c
}

// Inc adds one to the counter.
func (c *Counter) Inc(labels ...Label) { c.Add(1, labels...) }

// Add adds n to the counter.
func (c *Counter) Add(n int64, labels ...Label) {
	if c == nil || n < 0 {
		return
	}
	c.total.Add(uint64(n))
	if c.c != nil {
		c.c.Add(context.Background(), n, attrs(labels))
	}
}

// Value returns the count summed across every label set.
func (c *Counter) Value() uint64 {
	if c == nil {
		return 0
	}
	return c.total.Load()
}

// Histogram records a distribution of values, optionally labelled.
type Histogram struct {
	h metric.Float64Histogram
}

// Histogram returns the named histogram, creating it on first use with the
// given bucket boundaries (the unit is part of the name, per the doc).
func (r *Registry) Histogram(name, help string, boundaries ...float64) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	inst, err := r.meter.Float64Histogram(name,
		metric.WithDescription(help),
		metric.WithExplicitBucketBoundaries(boundaries...),
	)
	if err != nil {
		otel.Handle(err)
	}
	h := &Histogram{h: inst}
	r.histograms[name] = h
	return h
}

// Observe records one value.
func (h *Histogram) Observe(v float64, labels ...Label) {
	if h == nil || h.h == nil {
		return
	}
	h.h.Record(context.Background(), v, attrs(labels))
}

// UpDownCounter is an additive metric that can decrease (a live count).
type UpDownCounter struct {
	c metric.Int64UpDownCounter
}

// UpDown returns the named up/down counter, creating it on first use.
func (r *Registry) UpDown(name, help string) *UpDownCounter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.updowns[name]; ok {
		return u
	}
	inst, err := r.meter.Int64UpDownCounter(name, metric.WithDescription(help))
	if err != nil {
		otel.Handle(err)
	}
	u := &UpDownCounter{c: inst}
	r.updowns[name] = u
	return u
}

// Add applies delta to the counter.
func (u *UpDownCounter) Add(delta int64, labels ...Label) {
	if u == nil || u.c == nil {
		return
	}
	u.c.Add(context.Background(), delta, attrs(labels))
}

// Gauge registers an observable gauge sampled by observe at collection time.
// Registering the same name twice keeps the first registration.
func (r *Registry) Gauge(name, help string, observe func() float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.gauges[name]; ok {
		return
	}
	r.gauges[name] = struct{}{}
	_, err := r.meter.Float64ObservableGauge(name,
		metric.WithDescription(help),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(observe())
			return nil
		}),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// Handler serves GET /metrics in the Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{})
}

// shutdown flushes and stops the meter provider.
func (r *Registry) shutdown(ctx context.Context) error {
	return r.provider.Shutdown(ctx)
}
