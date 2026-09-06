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

func NewRegistry() *Registry {
	r, err := newRegistry(nil, nil)
	if err != nil {
		// fixed exporter options make this a programmer error
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

type Counter struct {
	c     metric.Int64Counter
	total atomic.Uint64
}

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

func (c *Counter) Inc(labels ...Label) { c.Add(1, labels...) }

func (c *Counter) Add(n int64, labels ...Label) {
	if c == nil || n < 0 {
		return
	}
	c.total.Add(uint64(n))
	if c.c != nil {
		c.c.Add(context.Background(), n, attrs(labels))
	}
}

// summed across every label set
func (c *Counter) Value() uint64 {
	if c == nil {
		return 0
	}
	return c.total.Load()
}

type Histogram struct {
	h metric.Float64Histogram
}

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

func (h *Histogram) Observe(v float64, labels ...Label) {
	if h == nil || h.h == nil {
		return
	}
	h.h.Record(context.Background(), v, attrs(labels))
}

type UpDownCounter struct {
	c metric.Int64UpDownCounter
}

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

func (u *UpDownCounter) Add(delta int64, labels ...Label) {
	if u == nil || u.c == nil {
		return
	}
	u.c.Add(context.Background(), delta, attrs(labels))
}

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

func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{})
}

func (r *Registry) shutdown(ctx context.Context) error {
	return r.provider.Shutdown(ctx)
}
