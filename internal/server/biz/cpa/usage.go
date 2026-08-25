package cpa

import (
	"strings"
	"time"
)

// UsageEventTokens mirrors the token stats block of CPA's usage payload.
type UsageEventTokens struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
}

// UsageEvent mirrors CLIProxyAPI's queued usage detail payload.
type UsageEvent struct {
	Timestamp       time.Time        `json:"timestamp"`
	AuthIndex       string           `json:"auth_index"`
	Provider        string           `json:"provider"`
	ExecutorType    string           `json:"executor_type"`
	Model           string           `json:"model"`
	Alias           string           `json:"alias"`
	AuthType        string           `json:"auth_type"`
	APIKey          string           `json:"api_key"`
	Source          string           `json:"source"`
	Failed          bool             `json:"failed"`
	Tokens          UsageEventTokens `json:"tokens"`
	ResponseHeaders map[string]any   `json:"response_headers,omitempty"`
}

// HeaderValue returns the first value of the named response header
// (case-insensitive), or an empty string when absent.
func (e *UsageEvent) HeaderValue(name string) string {
	if len(e.ResponseHeaders) == 0 {
		return ""
	}
	for key, value := range e.ResponseHeaders {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		return firstHeaderString(value)
	}
	return ""
}

func firstHeaderString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}
