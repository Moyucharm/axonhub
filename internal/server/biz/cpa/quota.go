package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

// CredentialInput is the safe credential metadata available to quota adapters.
type CredentialInput struct {
	AuthIndex   string
	Provider    string
	PlanType    string
	ProjectID   string
	AccountID   string
	AccountType string
	Paid        bool
}

// QuotaResult is a normalized provider quota response.
type QuotaResult struct {
	State    objects.CPAQuotaState
	PlanType string
	Snapshot objects.CPAQuotaSnapshot
}

// QuotaAdapter loads quota through CPA for one provider.
type QuotaAdapter interface {
	Provider() string
	Fetch(context.Context, ManagementClient, CredentialInput) (QuotaResult, error)
}

// QuotaRegistry contains the first-phase CPA quota adapters.
type QuotaRegistry struct {
	adapters map[string]QuotaAdapter
}

// NewQuotaRegistry builds the default provider registry.
func NewQuotaRegistry() *QuotaRegistry {
	registry := &QuotaRegistry{adapters: make(map[string]QuotaAdapter)}
	for _, adapter := range []QuotaAdapter{
		codexQuotaAdapter{},
		claudeQuotaAdapter{},
		antigravityQuotaAdapter{},
		kimiQuotaAdapter{},
		xaiQuotaAdapter{},
	} {
		registry.adapters[adapter.Provider()] = adapter
	}
	return registry
}

// Supports reports whether AxonHub can read quota for this provider via CPA.
func (r *QuotaRegistry) Supports(provider string) bool {
	if r == nil {
		return false
	}
	_, ok := r.adapters[NormalizeProvider(provider)]
	return ok
}

// Fetch executes one registered adapter.
func (r *QuotaRegistry) Fetch(ctx context.Context, client ManagementClient, input CredentialInput) (QuotaResult, error) {
	adapter, ok := r.adapters[NormalizeProvider(input.Provider)]
	if !ok {
		return QuotaResult{State: objects.CPAQuotaStateUnsupported, PlanType: input.PlanType}, nil
	}
	if strings.TrimSpace(input.AuthIndex) == "" {
		return QuotaResult{State: objects.CPAQuotaStateInsufficientData, PlanType: input.PlanType}, nil
	}
	result, err := adapter.Fetch(ctx, client, input)
	if err != nil {
		var managementErr *ManagementHTTPError
		if errors.As(err, &managementErr) &&
			(managementErr.StatusCode == http.StatusNotFound ||
				managementErr.StatusCode == http.StatusMethodNotAllowed ||
				managementErr.StatusCode == http.StatusNotImplemented) {
			return QuotaResult{State: objects.CPAQuotaStateUnsupported, PlanType: input.PlanType}, nil
		}
	}
	return result, err
}

// NormalizeProvider normalizes aliases returned by CPA.
func NormalizeProvider(raw string) string {
	provider := strings.ToLower(strings.TrimSpace(raw))
	provider = strings.ReplaceAll(provider, "_", "-")
	switch provider {
	case "", "unknown":
		return "unknown"
	case "x-ai", "grok":
		return "xai"
	case "anthropic":
		return "claude"
	case "moonshot", "moonshot-coding":
		return "kimi"
	default:
		return provider
	}
}

func callJSON(ctx context.Context, client ManagementClient, call ProviderCall, output any) (*ProviderCallResult, error) {
	result, err := client.CallProvider(ctx, call)
	if err != nil {
		return nil, err
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return nil, &ProviderHTTPError{
			StatusCode: result.StatusCode,
			Body:       append([]byte(nil), result.Body...),
		}
	}
	if output != nil {
		if err := json.Unmarshal(result.Body, output); err != nil {
			return nil, fmt.Errorf("decode provider quota response: %w", err)
		}
	}
	return result, nil
}

func asMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func asSlice(value any) []any {
	result, _ := value.([]any)
	return result
}

func firstValue(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func numberValue(value any) *float64 {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		number = parsed
	default:
		return nil
	}
	return &number
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

// shareValue reads a usage share expressed on the provider's declared scale:
// 100 for 0-100 percentages, 1 for 0-1 fractions. A "45%" string is always a
// 0-100 figure and is converted into the declared scale. The scale is never
// inferred from the magnitude: 1 on the percent scale is 1%, not 100%.
func shareValue(value any, scale float64) *float64 {
	if text, ok := value.(string); ok {
		if number, found := strings.CutSuffix(strings.TrimSpace(text), "%"); found {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
			if err != nil {
				return nil
			}
			scaled := parsed * scale / 100
			return &scaled
		}
	}
	return numberValue(value)
}

// percentPointersFromScaledUsed reads a used share on the 0-100 scale, e.g.
// Claude utilization, Codex used_percent and xAI usagePercent.
func percentPointersFromScaledUsed(value any) (*float64, *float64) {
	used := shareValue(value, 100)
	if used == nil {
		return nil, nil
	}
	usedValue := clampPercent(*used)
	remainingValue := 100 - usedValue
	return &usedValue, &remainingValue
}

// percentPointersFromRemainingFraction reads a remaining share on the 0-1
// scale, e.g. Antigravity remainingFraction.
func percentPointersFromRemainingFraction(value any) (*float64, *float64) {
	remaining := shareValue(value, 1)
	if remaining == nil {
		return nil, nil
	}
	remainingValue := clampPercent(*remaining * 100)
	usedValue := 100 - remainingValue
	return &usedValue, &remainingValue
}

func parseTimeValue(value any, now time.Time) *time.Time {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				parsed = parsed.UTC()
				return &parsed
			}
		}
		if numeric, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return unixTime(numeric)
		}
	case float64:
		return unixTime(int64(typed))
	case int64:
		return unixTime(typed)
	case int:
		return unixTime(int64(typed))
	}
	return nil
}

func unixTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	if value > 1_000_000_000_000 {
		parsed := time.UnixMilli(value).UTC()
		return &parsed
	}
	parsed := time.Unix(value, 0).UTC()
	return &parsed
}

func resetFromRecord(record map[string]any, now time.Time) *time.Time {
	if reset := parseTimeValue(firstValue(record, "reset_at", "resetAt", "reset_time", "resetTime", "resets_at"), now); reset != nil {
		return reset
	}
	seconds := numberValue(firstValue(record, "reset_after_seconds", "resetAfterSeconds", "reset_in", "resetIn", "ttl"))
	if seconds == nil || *seconds <= 0 {
		return nil
	}
	reset := now.Add(time.Duration(*seconds * float64(time.Second))).UTC()
	return &reset
}

func periodSeconds(record map[string]any) *int {
	value := numberValue(firstValue(record, "limit_window_seconds", "limitWindowSeconds"))
	if value == nil || *value <= 0 {
		return nil
	}
	period := int(*value)
	return &period
}
