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
	xaiBillingWeeklyURL  = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	xaiBillingMonthlyURL = "https://cli-chat-proxy.grok.com/v1/billing"
)

type xaiQuotaAdapter struct{}

func (xaiQuotaAdapter) Provider() string { return "xai" }

func (xaiQuotaAdapter) Fetch(ctx context.Context, client ManagementClient, input CredentialInput) (QuotaResult, error) {
	if input.Paid || strings.EqualFold(input.PlanType, "paid") {
		return QuotaResult{State: objects.CPAQuotaStateUnsupported, PlanType: "paid"}, nil
	}
	// Non-paid accounts are free regardless of whether billing returns usable
	// data; normalizing up front lets every return path report a plan so the
	// credential badge does not stay empty after insufficient_data results.
	planType := input.PlanType
	// "oauth" is an auth-method placeholder from legacy sync data, not a plan.
	if planType == "" || strings.EqualFold(planType, "oauth") {
		planType = "free"
	}
	headers := map[string]string{
		"Authorization":         "Bearer $TOKEN$",
		"x-xai-token-auth":      "xai-grok-cli",
		"x-grok-client-version": "0.2.91",
		"Accept":                "*/*",
		"User-Agent":            "grok-pager/0.2.91 grok-shell/0.2.91 (macos; aarch64)",
	}

	var weekly, monthly map[string]any
	_, weeklyErr := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodGet,
		URL:       xaiBillingWeeklyURL,
		Headers:   headers,
	}, &weekly)
	_, monthlyErr := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodGet,
		URL:       xaiBillingMonthlyURL,
		Headers:   headers,
	}, &monthly)

	items := make([]objects.CPAQuotaItem, 0, 6)
	items = append(items, xaiBillingItems("weekly", weekly)...)
	items = append(items, xaiBillingItems("monthly", monthly)...)
	if len(items) == 0 {
		if weeklyErr != nil && monthlyErr != nil {
			return QuotaResult{}, fmt.Errorf("xAI billing endpoints failed: %w", weeklyErr)
		}
		return QuotaResult{State: objects.CPAQuotaStateInsufficientData, PlanType: planType}, nil
	}

	return QuotaResult{
		State:    objects.CPAQuotaStateSuccess,
		PlanType: planType,
		Snapshot: objects.CPAQuotaSnapshot{Items: items},
	}, nil
}

func xaiBillingItems(prefix string, payload map[string]any) []objects.CPAQuotaItem {
	config := asMap(firstValue(payload, "config"))
	if len(config) == 0 {
		return nil
	}
	items := make([]objects.CPAQuotaItem, 0, 4)
	period := asMap(firstValue(config, "currentPeriod", "current_period"))
	used, remaining := percentPointersFromUsed(firstValue(config, "creditUsagePercent", "credit_usage_percent"))
	if used != nil {
		items = append(items, objects.CPAQuotaItem{
			ID:               prefix + "-credits",
			Label:            strings.ToUpper(prefix[:1]) + prefix[1:] + " credits",
			UsedPercent:      used,
			RemainingPercent: remaining,
			ResetAt:          parseTimeValue(firstValue(period, "end"), time.Now().UTC()),
		})
	}
	for index, productRaw := range asSlice(firstValue(config, "productUsage", "product_usage")) {
		product := asMap(productRaw)
		usedPercent, remainingPercent := percentPointersFromUsed(firstValue(product, "usagePercent", "usage_percent"))
		if usedPercent == nil {
			continue
		}
		label := stringValue(firstValue(product, "product"))
		if label == "" {
			label = fmt.Sprintf("Product %d", index+1)
		}
		items = append(items, objects.CPAQuotaItem{
			ID:               fmt.Sprintf("%s-product-%d", prefix, index+1),
			Group:            "Products",
			Label:            label,
			UsedPercent:      usedPercent,
			RemainingPercent: remainingPercent,
			ResetAt:          parseTimeValue(firstValue(period, "end"), time.Now().UTC()),
		})
	}

	monthlyLimit := centValue(firstValue(config, "monthlyLimit", "monthly_limit"))
	usedAmount := centValue(firstValue(config, "used"))
	if monthlyLimit != nil || usedAmount != nil {
		var remainingAmount *float64
		var usedPercent, remainingPercent *float64
		if monthlyLimit != nil && usedAmount != nil {
			value := *monthlyLimit - *usedAmount
			remainingAmount = &value
			if *monthlyLimit > 0 {
				usedValue := clampPercent((*usedAmount / *monthlyLimit) * 100)
				remainingValue := 100 - usedValue
				usedPercent, remainingPercent = &usedValue, &remainingValue
			}
		}
		items = append(items, objects.CPAQuotaItem{
			ID:               prefix + "-monthly-balance",
			Label:            "Monthly balance",
			UsedPercent:      usedPercent,
			RemainingPercent: remainingPercent,
			Used:             usedAmount,
			Limit:            monthlyLimit,
			Remaining:        remainingAmount,
			Unit:             "cents",
			ResetAt: parseTimeValue(firstValue(config,
				"billingPeriodEnd", "billing_period_end"), time.Now().UTC()),
		})
	}
	return items
}

func centValue(value any) *float64 {
	if record := asMap(value); len(record) > 0 {
		return numberValue(firstValue(record, "val"))
	}
	return numberValue(value)
}
