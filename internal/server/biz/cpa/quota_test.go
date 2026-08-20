package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/looplj/axonhub/internal/objects"
)

func TestQuotaAdaptersRouteEveryRequestThroughCPA(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	providerURLs := make([]string, 0, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/api-call" {
			t.Errorf("unexpected direct path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var payload struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode provider call: %v", err)
			return
		}
		mu.Lock()
		providerURLs = append(providerURLs, payload.URL)
		mu.Unlock()

		body := `{}`
		switch {
		case payload.URL == codexUsageURL:
			body = `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":60}}}`
		case payload.URL == claudeUsageURL:
			body = `{"five_hour":{"utilization":0.25,"resets_at":"2030-01-01T00:00:00Z"}}`
		case payload.URL == claudeProfileURL:
			body = `{"account":{"has_claude_pro":true}}`
		case payload.URL == kimiUsageURL:
			body = `{"limits":[{"name":"Weekly","detail":{"used":20,"limit":100,"remaining":80}}]}`
		case strings.HasPrefix(payload.URL, "https://cli-chat-proxy.grok.com/v1/billing"):
			body = `{"config":{"creditUsagePercent":20,"currentPeriod":{"end":"2030-01-01T00:00:00Z"}}}`
		case strings.Contains(payload.URL, "retrieveUserQuotaSummary"):
			body = `{"groups":[{"displayName":"Models","buckets":[{"bucketId":"gemini","displayName":"Gemini","remainingFraction":0.8,"window":"5h","resetTime":"2030-01-01T00:00:00Z"}]}]}`
		case payload.URL == antigravitySubscriptionURL:
			body = `{"currentTier":{"id":"g1-pro-tier"}}`
		default:
			t.Errorf("unexpected provider URL: %s", payload.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 200,
			"header":      map[string][]string{"Content-Type": {"application/json"}},
			"body":        body,
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, ManagementSecret: "management-key"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.CloseIdleConnections()
	registry := NewQuotaRegistry()

	tests := []CredentialInput{
		{AuthIndex: "codex-1", Provider: "codex", AccountID: "account-1"},
		{AuthIndex: "claude-1", Provider: "claude"},
		{AuthIndex: "kimi-1", Provider: "kimi"},
		{AuthIndex: "xai-1", Provider: "xai"},
		{AuthIndex: "ag-1", Provider: "antigravity", ProjectID: "project-1"},
	}
	for _, input := range tests {
		input := input
		t.Run(input.Provider, func(t *testing.T) {
			result, err := registry.Fetch(context.Background(), client, input)
			if err != nil {
				t.Fatalf("fetch %s quota: %v", input.Provider, err)
			}
			if result.State != objects.CPAQuotaStateSuccess {
				t.Fatalf("unexpected %s state: %s", input.Provider, result.State)
			}
			if len(result.Snapshot.Items) == 0 {
				t.Fatalf("%s returned no normalized quota items", input.Provider)
			}
		})
	}

	mu.Lock()
	requestsBeforePaidCheck := len(providerURLs)
	mu.Unlock()
	paid, err := registry.Fetch(context.Background(), client, CredentialInput{
		AuthIndex: "xai-paid",
		Provider:  "xai",
		Paid:      true,
	})
	if err != nil {
		t.Fatalf("classify paid xAI credential: %v", err)
	}
	if paid.State != objects.CPAQuotaStateUnsupported {
		t.Fatalf("unexpected paid xAI state: %s", paid.State)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(providerURLs) != requestsBeforePaidCheck {
		t.Fatal("paid xAI classification unexpectedly issued a health or quota request")
	}
}

func TestPercentNormalizationAcceptsFractionsAndPercentStrings(t *testing.T) {
	t.Parallel()

	used, remaining := percentPointersFromUsed("45%")
	if used == nil || remaining == nil || *used != 45 || *remaining != 55 {
		t.Fatalf("unexpected percent string normalization: used=%v remaining=%v", used, remaining)
	}
	used, remaining = percentPointersFromUsed(0.25)
	if used == nil || remaining == nil || *used != 25 || *remaining != 75 {
		t.Fatalf("unexpected fraction normalization: used=%v remaining=%v", used, remaining)
	}
}

func TestQuotaRegistryDegradesMissingManagementCapabilityToUnsupported(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, ManagementSecret: "management-key"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.CloseIdleConnections()

	result, err := NewQuotaRegistry().Fetch(context.Background(), client, CredentialInput{
		AuthIndex: "codex-1",
		Provider:  "codex",
	})
	if err != nil {
		t.Fatalf("missing CPA api-call capability should degrade without error: %v", err)
	}
	if result.State != objects.CPAQuotaStateUnsupported {
		t.Fatalf("unexpected degraded state: %s", result.State)
	}
}
