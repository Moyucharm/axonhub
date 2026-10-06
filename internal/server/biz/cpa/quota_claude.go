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
	// cedar_ember=1 adds the reset-grant block to the same usage read, so grants
	// ride along with quota without a second call to this rate-limited endpoint.
	claudeUsageURL   = claudeAPIOrigin + "/api/oauth/usage?cedar_ember=1"
	claudeProfileURL = claudeAPIOrigin + "/api/oauth/profile"
)

type claudeQuotaAdapter struct{}

func (claudeQuotaAdapter) Provider() string { return "claude" }

func (claudeQuotaAdapter) Fetch(ctx context.Context, client ManagementClient, input CredentialInput) (QuotaResult, error) {
	headers := claudeRequestHeaders()
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
	// period marks a window whose utilization covers the credential's whole
	// usage. Only those windows gate availability and feed the local amount
	// estimate, which divides the cost aggregated over every model by the
	// window's percentage delta. Model- or app-scoped sub-limits keep period
	// unset and stay display-only: exhausting one only blocks its own scope,
	// and a subset percentage cannot be applied to the full credential cost.
	windows := []struct {
		key    string
		label  string
		period int
	}{
		{key: "five_hour", label: "5 hour", period: objects.CPAFiveHourPeriodSeconds},
		{key: "seven_day", label: "7 day", period: objects.CPAWeeklyPeriodSeconds},
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
		// Claude's oauth/usage reports utilization as a 0-100 percentage, so
		// utilization=1 means 1%; the fraction heuristic would read it as 100%.
		used, remaining := percentPointersFromScaledUsed(firstValue(window, "utilization"))
		item := objects.CPAQuotaItem{
			ID:               strings.ReplaceAll(windowInfo.key, "_", "-"),
			Label:            windowInfo.label,
			UsedPercent:      used,
			RemainingPercent: remaining,
			ResetAt:          resetFromRecord(window, now),
		}
		if windowInfo.period > 0 {
			period := windowInfo.period
			item.PeriodSeconds = &period
			item.EstimateEligible = true
		} else {
			item.DisplayOnly = true
		}
		items = append(items, item)
	}

	if extra := asMap(firstValue(usage, "extra_usage", "extraUsage")); len(extra) > 0 {
		used := numberValue(firstValue(extra, "used_credits", "usedCredits"))
		limit := numberValue(firstValue(extra, "monthly_limit", "monthlyLimit"))
		usedPercent, remainingPercent := percentPointersFromScaledUsed(firstValue(extra, "utilization"))
		var remaining *float64
		if used != nil && limit != nil {
			value := *limit - *used
			remaining = &value
		}
		// Extra usage is pay-as-you-go spending on top of the plan windows;
		// running out of it never blocks requests the plan still covers.
		items = append(items, objects.CPAQuotaItem{
			ID:               "extra-usage",
			Label:            "Extra usage",
			UsedPercent:      usedPercent,
			RemainingPercent: remainingPercent,
			Used:             used,
			Limit:            limit,
			Remaining:        remaining,
			Unit:             "credits",
			DisplayOnly:      true,
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

	snapshot := objects.CPAQuotaSnapshot{Items: items}
	// A malformed or missing grant block must not fail quota, but it stays
	// visible as a failure instead of looking like "no grants".
	if status, ok := ParseClaudeReset(firstValue(usage, "cedar_ember")); ok {
		snapshot.ClaudeReset = status
	} else {
		snapshot.ClaudeResetFailed = true
	}
	return QuotaResult{
		State:    objects.CPAQuotaStateSuccess,
		PlanType: planType,
		Snapshot: snapshot,
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
