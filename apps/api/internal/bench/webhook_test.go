package bench

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const (
	webhookDeliveries = 10000
	uniqueDeliveries  = 5000
	webhookClients    = 64

	benchInstallationID = 999
	benchRepositoryID   = 501
)

type memoryAPI struct {
	repos []github.Repository
}

func benchRepo() github.Repository {
	var r github.Repository
	r.ID = benchRepositoryID
	r.Name = "service"
	r.FullName = "acme/service"
	r.DefaultBranch = "main"
	r.CloneURL = "https://github.example/acme/service.git"
	r.Owner.Login = "acme"
	return r
}

func (m *memoryAPI) ListInstallationRepositories(context.Context, int64) ([]github.Repository, error) {
	return append([]github.Repository(nil), m.repos...), nil
}

func (m *memoryAPI) CollaboratorPermission(context.Context, int64, string, string, string) (string, error) {
	return "write", nil
}

func (m *memoryAPI) BranchHeadSHA(context.Context, int64, string, string, string) (string, error) {
	return "0123456789012345678901234567890123456789", nil
}

func (m *memoryAPI) CreateIssueComment(context.Context, int64, string, string, int64, string) error {
	return nil
}

func (m *memoryAPI) CreateCheckRun(context.Context, int64, string, string, github.CheckRunParams) (int64, error) {
	return 777, nil
}

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, client *http.Client, url string, secret []byte, deliveryID, eventType string, payload []byte) (status int, ack string, latency time.Duration) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", sign(secret, payload))

	start := time.Now()
	resp, err := client.Do(req)
	latency = time.Since(start)
	if err != nil {
		t.Fatalf("POST %s: %v", deliveryID, err)
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Error != "" {
		ack = body.Error
	} else {
		ack = body.Status
	}
	return resp.StatusCode, ack, latency
}

func installationEvent(t *testing.T, installationID int) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"action": "created",
		"installation": map[string]any{
			"id": installationID,
			"account": map[string]any{
				"id": 61, "login": "acme", "type": "Organization",
			},
			"permissions": map[string]string{"issues": "write"},
			"events":      []string{"issues", "issue_comment"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func runCommandEvent(t *testing.T, installationID, repositoryID, issue int) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"action": "created",
		"comment": map[string]any{
			"id":   issue,
			"body": "/agent-trail run",
			"user": map[string]any{"id": 7, "login": "alice", "type": "User"},
		},
		"issue": map[string]any{
			"number": issue,
			"title":  fmt.Sprintf("bench issue %d", issue),
			"body":   "benchmark load-test issue",
		},
		"repository": map[string]any{
			"id": repositoryID,
			"owner": map[string]any{
				"id": 61, "login": "acme", "type": "Organization",
			},
		},
		"installation": map[string]any{"id": installationID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestWebhookIdempotency10k(t *testing.T) {
	db := openDB(t)
	secret := []byte("bench-webhook-secret")
	logger := discardLogger()
	metrics := observability.NewRegistry()
	store := github.NewStore(db)
	tasks := task.NewStore(db)
	processor := github.NewProcessor(store, tasks, &memoryAPI{repos: []github.Repository{benchRepo()}}, logger, metrics)
	webhook := github.NewWebhook(secret, store, processor, logger, metrics)

	srv := httptest.NewServer(webhook)
	defer srv.Close()
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        webhookClients,
			MaxIdleConnsPerHost: webhookClients,
		},
	}

	status, ack, _ := postWebhook(t, client, srv.URL, secret,
		"bench-install-1", "installation", installationEvent(t, benchInstallationID))
	if status != http.StatusAccepted || ack != "accepted" {
		t.Fatalf("installation seed: status=%d ack=%q", status, ack)
	}
	waitInt(t, db, fmt.Sprintf(
		`SELECT count(*) FROM repositories WHERE github_repository_id = %d AND is_enabled`,
		benchRepositoryID), 1, 10*time.Second, "repository sync")

	type shot struct {
		deliveryID string
		payload    []byte
	}
	shots := make([]shot, 0, webhookDeliveries)
	for i := 0; i < uniqueDeliveries; i++ {
		shots = append(shots, shot{
			deliveryID: fmt.Sprintf("bench-delivery-%05d", i),
			payload:    runCommandEvent(t, benchInstallationID, benchRepositoryID, i+1),
		})
	}
	for i := 0; i < webhookDeliveries-uniqueDeliveries; i++ {
		shots = append(shots, shots[i%uniqueDeliveries])
	}
	rng := rand.New(rand.NewSource(13))
	rng.Shuffle(len(shots), func(i, j int) { shots[i], shots[j] = shots[j], shots[i] })

	var (
		mu         sync.Mutex
		latencies  []time.Duration
		accepted   int
		duplicates int
		unexpected []string
	)
	work := make(chan shot)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < webhookClients; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sh := range work {
				status, ack, latency := postWebhook(t, client, srv.URL,
					secret, sh.deliveryID, "issue_comment", sh.payload)
				mu.Lock()
				latencies = append(latencies, latency)
				switch {
				case status == http.StatusAccepted && ack == "accepted":
					accepted++
				case status == http.StatusAccepted && ack == "duplicate":
					duplicates++
				default:
					unexpected = append(unexpected,
						fmt.Sprintf("%s: status=%d ack=%q", sh.deliveryID, status, ack))
				}
				mu.Unlock()
			}
		}()
	}
	for _, sh := range shots {
		work <- sh
	}
	close(work)
	wg.Wait()
	sendWall := time.Since(start)

	// ack only says "recorded"; task creation is async
	processor.Wait()
	processWall := time.Since(start)

	if len(unexpected) > 0 {
		t.Fatalf("%d deliveries not acked 202: first %q",
			len(unexpected), unexpected[0])
	}
	if accepted != uniqueDeliveries || duplicates != webhookDeliveries-uniqueDeliveries {
		t.Errorf("acks: accepted=%d duplicate=%d, want %d and %d",
			accepted, duplicates, uniqueDeliveries, webhookDeliveries-uniqueDeliveries)
	}

	if got := queryInt(t, db,
		`SELECT count(*) FROM github_webhook_deliveries WHERE github_delivery_id LIKE 'bench-delivery-%'`); got != uniqueDeliveries {
		t.Errorf("delivery ledger rows = %d, want %d", got, uniqueDeliveries)
	}
	if got := queryInt(t, db, `SELECT count(*) FROM tasks`); got != uniqueDeliveries {
		t.Errorf("tasks created = %d, want %d", got, uniqueDeliveries)
	}
	if dup := queryInt(t, db, `
		SELECT count(*) FROM (
			SELECT source_issue_number FROM tasks
			GROUP BY repository_id, source_issue_number
			HAVING count(*) > 1) d`); dup != 0 {
		t.Errorf("issues with duplicate tasks = %d, want 0", dup)
	}

	t.Logf("bench webhook: deliveries=%d unique=%d clients=%d pool=%d",
		webhookDeliveries, uniqueDeliveries, webhookClients, benchPoolSize)
	t.Logf("bench webhook: send_wall=%s (%.0f req/s) process_wall=%s",
		sendWall.Round(time.Millisecond),
		float64(webhookDeliveries)/sendWall.Seconds(),
		processWall.Round(time.Millisecond))
	reportDurations(t, "webhook ack latency", latencies)
}
