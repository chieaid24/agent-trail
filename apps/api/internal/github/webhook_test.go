package github

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/dbtest"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

var testSecret = []byte("test-webhook-secret")

func webhookRequest(body []byte, deliveryID, eventType, signature string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	if deliveryID != "" {
		r.Header.Set("X-GitHub-Delivery", deliveryID)
	}
	if eventType != "" {
		r.Header.Set("X-GitHub-Event", eventType)
	}
	if signature != "" {
		r.Header.Set("X-Hub-Signature-256", signature)
	}
	return r
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

// newWebhookOnly builds a handler whose store and processor are never
// reached (rejection-path tests).
func newWebhookOnly(metrics *observability.Registry) *Webhook {
	return NewWebhook(testSecret, nil, nil, testLogger(), metrics)
}

func TestWebhookRejectsInvalidSignatureAndCounts(t *testing.T) {
	metrics := observability.NewRegistry()
	h := newWebhookOnly(metrics)
	body := []byte(`{"action":"created"}`)

	for _, sig := range []string{"", "sha256=deadbeef", sign([]byte("wrong"), body)} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, webhookRequest(body, "d-1", "ping", sig))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("signature %q: status %d, want 401", sig, rec.Code)
		}
	}
	invalid := metrics.Counter("agent_trail_webhook_invalid_signature_total", "")
	if invalid.Value() != 3 {
		t.Fatalf("invalid signature counter = %d, want 3", invalid.Value())
	}
	received := metrics.Counter("agent_trail_webhook_received_total", "")
	if received.Value() != 3 {
		t.Fatalf("received counter = %d, want 3", received.Value())
	}
}

func TestWebhookRejectsOversizedBody(t *testing.T) {
	h := newWebhookOnly(observability.NewRegistry())
	body := bytes.Repeat([]byte("a"), maxWebhookBody+1)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, webhookRequest(body, "d-1", "ping", sign(testSecret, body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestWebhookRejectsMissingDeliveryHeaders(t *testing.T) {
	h := newWebhookOnly(observability.NewRegistry())
	body := []byte(`{}`)
	sig := sign(testSecret, body)

	for _, tc := range [][2]string{{"", "ping"}, {"d-1", ""}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, webhookRequest(body, tc[0], tc[1], sig))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("delivery=%q event=%q: status %d, want 400", tc[0], tc[1], rec.Code)
		}
	}
}

func TestWebhookAcceptsProcessesAndDedupes(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	api := &fakeAPI{permission: "write"}
	proc := NewProcessor(store, task.NewStore(db), api, testLogger(),
		observability.NewRegistry())
	h := NewWebhook(testSecret, store, proc, testLogger(),
		observability.NewRegistry())

	body := []byte(`{"zen":"Anything added dilutes everything else."}`)
	req := func() *http.Request {
		return webhookRequest(body, "delivery-abc", "ping", sign(testSecret, body))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req())
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "accepted") {
		t.Fatalf("first delivery: status %d body %s", rec.Code, rec.Body.String())
	}

	// The replay is acked but not reprocessed.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req())
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("replay: status %d body %s", rec.Code, rec.Body.String())
	}

	proc.Wait()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM github_webhook_deliveries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("delivery rows = %d, want 1", n)
	}
	var status string
	if err := db.QueryRow(`SELECT processing_status FROM github_webhook_deliveries`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ignored" { // ping is recorded and skipped
		t.Fatalf("processing_status = %q, want ignored", status)
	}
}

func TestRecordDeliveryConcurrentReplaysInsertOnce(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)

	const workers = 8
	var wg sync.WaitGroup
	inserted := make(chan bool, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ok, err := store.RecordDelivery(ctx, "same-id", "ping", "", 0, 0)
			if err != nil {
				t.Error(err)
				return
			}
			inserted <- ok
		}()
	}
	wg.Wait()
	close(inserted)

	wins := 0
	for ok := range inserted {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("winning inserts = %d, want exactly 1", wins)
	}
}

// TestWebhookIssueCommentCreatesTask drives the flagship flow through the
// real HTTP path: signed issue_comment delivery -> async processing -> one
// task (acceptance criterion "one real issue comment creates exactly one
// task").
func TestWebhookIssueCommentCreatesTask(t *testing.T) {
	db := dbtest.Open(t)
	store := NewStore(db)
	api := &fakeAPI{
		repos:      []Repository{testRepo(501, "acme/service")},
		permission: "write",
		headSHA:    "0123456789012345678901234567890123456789",
	}
	proc := NewProcessor(store, task.NewStore(db), api, testLogger(),
		observability.NewRegistry())
	h := NewWebhook(testSecret, store, proc, testLogger(),
		observability.NewRegistry())

	install := installationJSON(t, "created")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, webhookRequest(install, "d-e2e-install", "installation",
		sign(testSecret, install)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("installation delivery: status %d", rec.Code)
	}
	proc.Wait() // the repo sync must land before the command arrives

	comment := issueCommentJSON(t, commentOpts{body: "/agent-trail run"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, webhookRequest(comment, "d-e2e-comment", "issue_comment",
		sign(testSecret, comment)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("comment delivery: status %d", rec.Code)
	}
	proc.Wait()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("tasks = %d, want exactly 1", n)
	}
	var status string
	err := db.QueryRow(`
		SELECT processing_status FROM github_webhook_deliveries
		WHERE github_delivery_id = 'd-e2e-comment'`).Scan(&status)
	if err != nil || status != "processed" {
		t.Fatalf("comment delivery status = %q err = %v", status, err)
	}
}
