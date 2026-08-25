package cpa

import (
	"encoding/json"
	"testing"
)

func TestUsageEventJSONAndHeaderValue(t *testing.T) {
	var event UsageEvent
	err := json.Unmarshal([]byte(`{
		"timestamp":"2026-08-22T03:00:00Z",
		"auth_index":"abc123",
		"provider":"codex",
		"model":"gpt-5.2",
		"tokens":{"input_tokens":100,"output_tokens":50,"cache_read_tokens":30},
		"response_headers":{"X-Codex-Secondary-Used-Percent":["", "3.42"]}
	}`), &event)
	if err != nil {
		t.Fatalf("decode usage event: %v", err)
	}
	if event.AuthIndex != "abc123" || event.Tokens.InputTokens != 100 || event.Tokens.CacheReadTokens != 30 {
		t.Fatalf("unexpected event: %#v", event)
	}
	if got := event.HeaderValue("x-codex-secondary-used-percent"); got != "3.42" {
		t.Fatalf("HeaderValue = %q, want 3.42", got)
	}
}
