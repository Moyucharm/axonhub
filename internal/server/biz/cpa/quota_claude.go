package cpa

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

const (
	claudeUsageURL   = "https://api.anthropic.com/api/oauth/usage"
	claudeProfileURL = "https://api.anthropic.com/api/oauth/profile"
)

type claudeQuotaAdapter struct{}

func (claudeQuotaAdapter) Provider() string { return "claude" }

func (claudeQuotaAdapter) Fetch(ctx context.Context, client *Client, input CredentialInput) (QuotaResult, error) {
	headers := map[string]string{
		"Authorization":  "Bearer $TOKEN$",
		"Content-Type":   "application/json",
		"anthropic-beta": "oauth-2025-04-20",
	}
	var usage map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodGet,
		URL:       claudeUsageURL,
		Headers:   headers,
	}, &usage); err != nil {
		return QuotaResult{}, err
	}

	now := time.Now().UTC()
	items := make([]objects.CPAQuotaItem, 0, 8)
	windows := []struct {
		key   string
		label string
	}{
		{key: "five_hour", label: "5 hour"},
		{key: "seven_day", label: "7 day"},
		{key: "seven_day_oauth_apps", label: "7 day OAuth apps"},
		{key: "seven_day_opus", label: "7 day Opus"},
		{key: "seven_day_sonnet", label: "7 day Sonnet"},
		{key: "seven_day_cowork", label: "7 day Cowork"},
		{key: "iguana_necktie", label: "7 day Fable"},
	}
	for _, windowInfo := range windows {
		window := asMap(firstValue(usage, windowInfo.key))
		if len(window) == 0 {
			continue
		}
		used, remaining := percentPointersFromUsed(firstValue(window, "utilization"))
		items = append(items, objects.CPAQuotaItem{
			ID:               strings.ReplaceAll(windowInfo.key, "_", "-"),
			Label:            windowInfo.label,
			UsedPercent:      used,
			RemainingPercent: remaining,
			ResetAt:          resetFromRecord(window, now),
		})
	}

	if extra := asMap(firstValue(usage, "extra_usage", "extraUsage")); len(extra) > 0 {
		used := numberValue(firstValue(extra, "used_credits", "usedCredits"))
		limit := numberValue(firstValue(extra, "monthly_limit", "monthlyLimit"))
		usedPercent, remainingPercent := percentPointersFromUsed(firstValue(extra, "utilization"))
		var remaining *float64
		if used != nil && limit != nil {
			value := *limit - *used
			remaining = &value
		}
		items = append(items, objects.CPAQuotaItem{
			ID:               "extra-usage",
			Label:            "Extra usage",
			UsedPercent:      usedPercent,
			RemainingPercent: remainingPercent,
			Used:             used,
			Limit:            limit,
			Remaining:        remaining,
			Unit:             "credits",
		})
	}
	if len(items) == 0 {
		return QuotaResult{}, fmt.Errorf("Claude quota response contained no windows")
	}

	planType := input.PlanType
	var profile map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodGet,
		URL:       claudeProfileURL,
		Headers:   headers,
	}, &profile); err == nil {
		planType = resolveClaudePlan(profile, planType)
	}

	return QuotaResult{
		State:    objects.CPAQuotaStateSuccess,
		PlanType: planType,
		Snapshot: objects.CPAQuotaSnapshot{Items: items},
	}, nil
}

func resolveClaudePlan(profile map[string]any, fallback string) string {
	account := asMap(firstValue(profile, "account"))
	if value, ok := firstValue(account, "has_claude_max").(bool); ok && value {
		return "max"
	}
	if value, ok := firstValue(account, "has_claude_pro").(bool); ok && value {
		return "pro"
	}
	organization := asMap(firstValue(profile, "organization"))
	if strings.EqualFold(stringValue(firstValue(organization, "organization_type")), "claude_team") &&
		strings.EqualFold(stringValue(firstValue(organization, "subscription_status")), "active") {
		return "team"
	}
	return fallback
}
