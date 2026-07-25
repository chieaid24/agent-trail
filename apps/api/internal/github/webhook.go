package github

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// maxWebhookBody bounds the payload we accept. The subscribed events stay
// far below this (issue bodies cap at 64 KiB); GitHub's own limit is 25 MiB.
const maxWebhookBody = 1 << 20 // 1 MiB

// Webhook is the POST /webhooks/github handler: it validates the HMAC
// signature over the raw body, enforces the size limit, records the
// delivery id under its unique constraint, acks fast, and hands processing
// to the Processor off the request goroutine
// (docs/architecture/github-app.md).
type Webhook struct {
	secret    []byte
	store     *Store
	processor *Processor
	logger    *slog.Logger

	received         *observability.Counter // agent_trail_webhook_received_total
	invalidSignature *observability.Counter // agent_trail_webhook_invalid_signature_total
}

// NewWebhook wires the webhook handler.
func NewWebhook(secret []byte, store *Store, processor *Processor, logger *slog.Logger, metrics *observability.Registry) *Webhook {
	return &Webhook{
		secret:    secret,
		store:     store,
		processor: processor,
		logger:    logger,
		received: metrics.Counter("agent_trail_webhook_received_total",
			"Webhook requests received."),
		invalidSignature: metrics.Counter(
			"agent_trail_webhook_invalid_signature_total",
			"Webhook requests rejected for a bad or missing signature."),
	}
}

// deliveryEnvelope is the minimal payload slice recorded in the ledger.
type deliveryEnvelope struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
}

// ServeHTTP handles one webhook request. Log lines carry only header
// metadata (delivery id, event type); payloads and secrets are never logged.
func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.received.Inc()
	traceID := observability.TraceIDFrom(r.Context())

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge,
				map[string]string{"error": "payload too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "unreadable body"})
		return
	}

	if !ValidSignature(h.secret, body, r.Header.Get("X-Hub-Signature-256")) {
		h.invalidSignature.Inc()
		h.logger.LogAttrs(r.Context(), slog.LevelWarn, "webhook signature invalid",
			slog.String("event", "webhook_signature_invalid"),
			slog.String("trace_id", traceID),
			slog.String("delivery_id", r.Header.Get("X-GitHub-Delivery")),
			slog.String("event_type", r.Header.Get("X-GitHub-Event")),
		)
		writeJSON(w, http.StatusUnauthorized,
			map[string]string{"error": "invalid signature"})
		return
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")
	eventType := r.Header.Get("X-GitHub-Event")
	if deliveryID == "" || eventType == "" || len(deliveryID) > 100 || len(eventType) > 100 {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "missing delivery headers"})
		return
	}

	// Envelope fields are best-effort ledger metadata; a payload without
	// them (e.g. ping) records zeros.
	var env deliveryEnvelope
	_ = json.Unmarshal(body, &env)

	inserted, err := h.store.RecordDelivery(r.Context(), deliveryID, eventType,
		env.Action, env.Installation.ID, env.Repository.ID)
	if err != nil {
		h.logger.LogAttrs(r.Context(), slog.LevelError, "webhook record failed",
			slog.String("event", "webhook_record_failed"),
			slog.String("trace_id", traceID),
			slog.String("delivery_id", deliveryID),
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusInternalServerError,
			map[string]string{"error": "delivery not recorded"})
		return
	}
	if !inserted {
		h.logger.LogAttrs(r.Context(), slog.LevelInfo, "webhook duplicate ignored",
			slog.String("event", "webhook_duplicate_ignored"),
			slog.String("trace_id", traceID),
			slog.String("delivery_id", deliveryID),
			slog.String("event_type", eventType),
		)
		writeJSON(w, http.StatusAccepted,
			map[string]string{"status": "duplicate"})
		return
	}

	h.logger.LogAttrs(r.Context(), slog.LevelInfo, "webhook accepted",
		slog.String("event", "webhook_accepted"),
		slog.String("trace_id", traceID),
		slog.String("delivery_id", deliveryID),
		slog.String("event_type", eventType),
		slog.String("action", env.Action),
	)
	h.processor.Dispatch(Delivery{
		ID: deliveryID, EventType: eventType, Action: env.Action,
		TraceID: traceID,
	}, body)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
