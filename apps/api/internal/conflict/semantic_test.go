package conflict

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFakeSemanticDetectsContradictoryContract(t *testing.T) {
	provider := FakeSemantic{}
	got, err := provider.Assess(context.Background(), SemanticRequest{
		TaskDiff:      "+// semantic-contract: auth.failure=deny",
		OtherTaskDiff: "+// semantic-contract: auth.failure=allow",
	})
	if err != nil || !got.Conflicts || got.Severity != SeverityHigh || len(got.Evidence) != 2 {
		t.Fatalf("verdict = %+v, err = %v", got, err)
	}

	got, err = provider.Assess(context.Background(), SemanticRequest{
		TaskDiff: "+func login() {}", OtherTaskDiff: "+func report() {}",
	})
	if err != nil || got.Conflicts {
		t.Fatalf("benign verdict = %+v, err = %v", got, err)
	}
}

func TestNewSemanticGating(t *testing.T) {
	for _, opts := range []SemanticOptions{
		{Enabled: false, Provider: FakeProvider},
		{Enabled: true, Provider: AnthropicProvider},
	} {
		got, err := NewSemantic(opts)
		if err != nil || got != nil {
			t.Fatalf("assessor = %T, err = %v, want disabled", got, err)
		}
	}
	got, err := NewSemantic(SemanticOptions{Enabled: true, Provider: FakeProvider})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(FakeSemantic); !ok {
		t.Fatalf("assessor = %T, want FakeSemantic", got)
	}
	if _, err := NewSemantic(SemanticOptions{Enabled: true, Provider: "unknown"}); err == nil {
		t.Fatal("unknown semantic provider accepted")
	}
}

func TestAnthropicSemanticUsesPinnedMessagesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" ||
			r.Header.Get("anthropic-version") != AnthropicAPIVersion {
			t.Errorf("headers = %+v", r.Header)
		}
		var body anthropicMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != DefaultSemanticModel || body.MaxTokens != 400 {
			t.Errorf("request = %+v", body)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"conflicts\":true,\"severity\":\"medium\",\"explanation\":\"Both changes redefine auth.Session.\",\"evidence\":[\"auth.Session\"]}"}]}`))
	}))
	defer server.Close()

	provider, err := NewAnthropicSemantic(AnthropicOptions{
		APIKey: "test-key", Endpoint: server.URL, Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := provider.Assess(context.Background(), SemanticRequest{
		TaskID: "a", TaskTitle: "one", TaskDiff: "+change",
		OtherTaskID: "b", OtherTaskTitle: "two", OtherTaskDiff: "+other",
	})
	if err != nil || !got.Conflicts || got.Severity != SeverityMedium {
		t.Fatalf("verdict = %+v, err = %v", got, err)
	}
}

func TestAnthropicSemanticRejectsConflictWithoutEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"conflicts\":true,\"severity\":\"high\",\"explanation\":\"Maybe incompatible.\",\"evidence\":[]}"}]}`))
	}))
	defer server.Close()
	provider, err := NewAnthropicSemantic(AnthropicOptions{
		APIKey: "test-key", Endpoint: server.URL, Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Assess(context.Background(), SemanticRequest{}); err == nil {
		t.Fatal("unsupported conflict verdict accepted")
	}
}
