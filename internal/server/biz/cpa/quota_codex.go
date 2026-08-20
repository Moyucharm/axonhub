package cpa

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

const codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

type codexQuotaAdapter struct{}

func (codexQuotaAdapter) Provider() string { return "codex" }

func (codexQuotaAdapter) Fetch(ctx context.Context, client *Client, input CredentialInput) (QuotaResult, error) {
	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
	}
	if input.AccountID != "" {
		headers["Chatgpt-Account-Id"] = input.AccountID
	}

	var payload map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodGet,
		URL:       codexUsageURL,
		Headers:   headers,
	}, &payload); err != nil {
		return QuotaResult{}, err
	}

	planType := strings.ToLower(stringValue(firstValue(payload, "plan_type", "planType")))
	if planType == "" {
		planType = input.PlanType
	}
	now := time.Now().UTC()
	items := make([]objects.CPAQuotaItem, 0, 8)
	items = append(items, codexLimitItems("code", "Code", asMap(firstValue(payload, "rate_limit", "rateLimit")), now)...)
	items = append(items, codexLimitItems("review", "Code review", asMap(firstValue(payload, "code_review_rate_limit", "codeReviewRateLimit")), now)...)

	for index, additionalRaw := range asSlice(firstValue(payload, "additional_rate_limits", "additionalRateLimits")) {
		additional := asMap(additionalRaw)
		name := stringValue(firstValue(additional, "limit_name", "limitName", "metered_feature", "meteredFeature"))
		if name == "" {
			name = fmt.Sprintf("Additional limit %d", index+1)
		}
		prefix := fmt.Sprintf("additional-%d", index+1)
		items = append(items, codexLimitItems(prefix, name, asMap(firstValue(additional, "rate_limit", "rateLimit")), now)...)
	}

	if len(items) == 0 {
		return QuotaResult{}, fmt.Errorf("Codex quota response contained no windows")
	}
	return QuotaResult{
		State:    objects.CPAQuotaStateSuccess,
		PlanType: planType,
		Snapshot: objects.CPAQuotaSnapshot{Items: items},
	}, nil
}

func codexLimitItems(prefix, label string, rateLimit map[string]any, now time.Time) []objects.CPAQuotaItem {
	if len(rateLimit) == 0 {
		return nil
	}
	limitReached, _ := firstValue(rateLimit, "limit_reached", "limitReached").(bool)
	allowed, hasAllowed := firstValue(rateLimit, "allowed").(bool)

	windows := []struct {
		key      string
		fallback string
	}{
		{key: "primary_window", fallback: "Primary"},
		{key: "secondary_window", fallback: "Secondary"},
	}
	items := make([]objects.CPAQuotaItem, 0, len(windows))
	for index, windowInfo := range windows {
		window := asMap(firstValue(rateLimit, windowInfo.key, strings.ReplaceAll(windowInfo.key, "_", "")))
		if len(window) == 0 {
			camelKey := "primaryWindow"
			if index == 1 {
				camelKey = "secondaryWindow"
			}
			window = asMap(firstValue(rateLimit, camelKey))
		}
		if len(window) == 0 {
			continue
		}

		used, remaining := percentPointersFromUsed(firstValue(window, "used_percent", "usedPercent"))
		if used == nil && (limitReached || (hasAllowed && !allowed)) {
			full, empty := 100.0, 0.0
			used, remaining = &full, &empty
		}
		period := periodSeconds(window)
		windowLabel := codexWindowLabel(windowInfo.fallback, period)
		items = append(items, objects.CPAQuotaItem{
			ID:               prefix + "-" + strings.ToLower(windowInfo.fallback),
			Group:            label,
			Label:            windowLabel,
			UsedPercent:      used,
			RemainingPercent: remaining,
			ResetAt:          resetFromRecord(window, now),
			PeriodSeconds:    period,
		})
	}
	return items
}

func codexWindowLabel(fallback string, period *int) string {
	if period == nil {
		return fallback
	}
	switch *period {
	case 18_000:
		return "5 hour"
	case 604_800:
		return "7 day"
	default:
		if *period >= 28*24*60*60 && *period <= 31*24*60*60 {
			return "Monthly"
		}
		return fallback
	}
}
