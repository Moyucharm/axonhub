package cpa

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

const kimiUsageURL = "https://api.kimi.com/coding/v1/usages"

type kimiQuotaAdapter struct{}

func (kimiQuotaAdapter) Provider() string { return "kimi" }

func (kimiQuotaAdapter) Fetch(ctx context.Context, client ManagementClient, input CredentialInput) (QuotaResult, error) {
	var payload map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodGet,
		URL:       kimiUsageURL,
		Headers: map[string]string{
			"Authorization": "Bearer $TOKEN$",
		},
	}, &payload); err != nil {
		return QuotaResult{}, err
	}

	now := time.Now().UTC()
	items := make([]objects.CPAQuotaItem, 0, 6)
	for index, limitRaw := range asSlice(firstValue(payload, "limits")) {
		limitItem := asMap(limitRaw)
		detail := asMap(firstValue(limitItem, "detail"))
		if len(detail) == 0 {
			detail = limitItem
		}
		label := stringValue(firstValue(limitItem, "name", "title", "scope"))
		if label == "" {
			label = fmt.Sprintf("Limit %d", index+1)
		}
		window := asMap(firstValue(limitItem, "window"))
		items = appendKimiItem(items, fmt.Sprintf("limit-%d", index+1), label, detail, window, now)
	}
	if usage := asMap(firstValue(payload, "usage")); len(usage) > 0 {
		items = appendKimiItem(items, "usage", "Usage", usage, nil, now)
	}
	if len(items) == 0 {
		return QuotaResult{}, fmt.Errorf("Kimi quota response contained no limits")
	}

	return QuotaResult{
		State:    objects.CPAQuotaStateSuccess,
		PlanType: input.PlanType,
		Snapshot: objects.CPAQuotaSnapshot{Items: items},
	}, nil
}

func appendKimiItem(items []objects.CPAQuotaItem, id, label string, detail, window map[string]any, now time.Time) []objects.CPAQuotaItem {
	used := numberValue(firstValue(detail, "used"))
	limit := numberValue(firstValue(detail, "limit"))
	remaining := numberValue(firstValue(detail, "remaining"))
	if used == nil && remaining != nil && limit != nil {
		value := *limit - *remaining
		used = &value
	}
	if remaining == nil && used != nil && limit != nil {
		value := *limit - *used
		remaining = &value
	}
	if used == nil && limit == nil && remaining == nil {
		return items
	}

	var usedPercent, remainingPercent *float64
	if used != nil && limit != nil && *limit > 0 {
		usedValue := clampPercent((*used / *limit) * 100)
		remainingValue := 100 - usedValue
		usedPercent, remainingPercent = &usedValue, &remainingValue
	}
	period := kimiPeriod(window, detail)
	resetAt := resetFromRecord(detail, now)
	if resetAt == nil {
		resetAt = resetFromRecord(window, now)
	}
	return append(items, objects.CPAQuotaItem{
		ID:               id,
		Label:            label,
		UsedPercent:      usedPercent,
		RemainingPercent: remainingPercent,
		Used:             used,
		Limit:            limit,
		Remaining:        remaining,
		Unit:             "requests",
		ResetAt:          resetAt,
		PeriodSeconds:    period,
	})
}

func kimiPeriod(window, detail map[string]any) *int {
	duration := numberValue(firstValue(window, "duration"))
	if duration == nil {
		duration = numberValue(firstValue(detail, "duration"))
	}
	if duration == nil || *duration <= 0 {
		return nil
	}
	unit := strings.ToUpper(stringValue(firstValue(window, "timeUnit", "time_unit")))
	unit = strings.TrimPrefix(unit, "TIME_UNIT_")
	multiplier := float64(time.Minute / time.Second)
	switch unit {
	case "SECOND", "SECONDS":
		multiplier = 1
	case "HOUR", "HOURS":
		multiplier = float64(time.Hour / time.Second)
	case "DAY", "DAYS":
		multiplier = float64(24 * time.Hour / time.Second)
	case "WEEK", "WEEKS":
		multiplier = float64(7 * 24 * time.Hour / time.Second)
	}
	seconds := int(*duration * multiplier)
	return &seconds
}
