package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func spanContextTraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /metrics = %d", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCounterExpositionKeepsExactNames(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("agent_trail_webhook_received_total", "Webhook deliveries received.")
	c.Inc()
	c.Add(2)

	body := scrape(t, r)
	if !strings.Contains(body, "# TYPE agent_trail_webhook_received_total counter") {
		t.Fatalf("missing counter TYPE line:\n%s", body)
	}
	if !strings.Contains(body, "agent_trail_webhook_received_total 3") {
		t.Fatalf("missing counter sample:\n%s", body)
	}
	if strings.Contains(body, "otel_scope") || strings.Contains(body, "target_info") {
		t.Fatalf("exporter metadata leaked into exposition:\n%s", body)
	}
	if c.Value() != 3 {
		t.Fatalf("Value() = %d, want 3", c.Value())
	}
}

func TestCounterLabels(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("agent_trail_task_status_total", "Task status transitions.")
	c.Inc(Label{"status", "completed"})
	c.Inc(Label{"status", "failed"})
	c.Inc(Label{"status", "failed"})

	body := scrape(t, r)
	if !strings.Contains(body, `agent_trail_task_status_total{status="completed"} 1`) {
		t.Fatalf("missing completed sample:\n%s", body)
	}
	if !strings.Contains(body, `agent_trail_task_status_total{status="failed"} 2`) {
		t.Fatalf("missing failed sample:\n%s", body)
	}
	if c.Value() != 3 {
		t.Fatalf("Value() = %d, want 3 across label sets", c.Value())
	}
}

func TestCounterSameNameReturnsSameInstance(t *testing.T) {
	r := NewRegistry()
	a := r.Counter("agent_trail_task_created_total", "help")
	b := r.Counter("agent_trail_task_created_total", "help")
	if a != b {
		t.Fatal("same name returned distinct counters")
	}
}

func TestHistogramExposition(t *testing.T) {
	r := NewRegistry()
	h := r.Histogram("agent_trail_task_queue_wait_seconds", "Queue wait.", 1, 10, 60)
	h.Observe(5)
	h.Observe(30)

	body := scrape(t, r)
	if !strings.Contains(body, "# TYPE agent_trail_task_queue_wait_seconds histogram") {
		t.Fatalf("missing histogram TYPE line:\n%s", body)
	}
	if !strings.Contains(body, `agent_trail_task_queue_wait_seconds_bucket{le="10"} 1`) {
		t.Fatalf("missing bucket sample:\n%s", body)
	}
	if !strings.Contains(body, "agent_trail_task_queue_wait_seconds_count 2") {
		t.Fatalf("missing count sample:\n%s", body)
	}
}

func TestUpDownCounterExposition(t *testing.T) {
	r := NewRegistry()
	u := r.UpDown("agent_trail_runner_active_tasks", "Active tasks.")
	u.Add(2)
	u.Add(-1)

	body := scrape(t, r)
	if !strings.Contains(body, "agent_trail_runner_active_tasks 1") {
		t.Fatalf("missing gauge sample:\n%s", body)
	}
}

func TestGaugeExposition(t *testing.T) {
	r := NewRegistry()
	r.Gauge("agent_trail_runner_memory_usage", "RSS bytes.", func() float64 { return 42 })

	body := scrape(t, r)
	if !strings.Contains(body, "agent_trail_runner_memory_usage 42") {
		t.Fatalf("missing observable gauge sample:\n%s", body)
	}
}

func TestNilMetricInstrumentsAreSafe(t *testing.T) {
	var c *Counter
	var h *Histogram
	var u *UpDownCounter
	c.Inc()
	h.Observe(1)
	u.Add(1)
	if v := c.Value(); v != 0 {
		t.Fatalf("nil counter Value() = %d", v)
	}
}

func TestRunnerResourceGauges(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("resource sampling is linux-only")
	}
	r := NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	RegisterRunnerResources(r, t.TempDir(), logger)

	body := scrape(t, r)
	for _, name := range []string{
		"agent_trail_runner_cpu_usage",
		"agent_trail_runner_memory_usage",
		"agent_trail_runner_disk_usage",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("missing %s:\n%s", name, body)
		}
	}
}

func TestWithTraceParentBindsTraceID(t *testing.T) {
	ctx := WithTraceParent(t.Context(), "0123456789abcdef0123456789abcdef")
	got := spanContextTraceID(ctx)
	if got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("trace id = %q", got)
	}
	if spanContextTraceID(WithTraceParent(t.Context(), "nope")) != "" {
		t.Fatal("invalid trace id produced a span context")
	}
}
