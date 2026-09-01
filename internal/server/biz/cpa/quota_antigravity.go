package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
}

const antigravitySubscriptionURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"

type antigravityQuotaAdapter struct{}

func (antigravityQuotaAdapter) Provider() string { return "antigravity" }

func (antigravityQuotaAdapter) Fetch(ctx context.Context, client ManagementClient, input CredentialInput) (QuotaResult, error) {
	if strings.TrimSpace(input.ProjectID) == "" {
		return QuotaResult{State: objects.CPAQuotaStateInsufficientData, PlanType: input.PlanType}, nil
	}
	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)",
	}
	requestBody, _ := json.Marshal(map[string]string{"project": input.ProjectID})

	var payload map[string]any
	var lastErr error
	for _, quotaURL := range antigravityQuotaURLs {
		payload = nil
		_, err := callJSON(ctx, client, ProviderCall{
			AuthIndex: input.AuthIndex,
			Method:    http.MethodPost,
			URL:       quotaURL,
			Headers:   headers,
			Body:      string(requestBody),
		}, &payload)
		if err == nil {
			break
		}
		lastErr = err
	}
	if payload == nil {
		return QuotaResult{}, lastErr
	}

	now := time.Now().UTC()
	items := make([]objects.CPAQuotaItem, 0, 8)
	for groupIndex, groupRaw := range asSlice(firstValue(payload, "groups")) {
		group := asMap(groupRaw)
		groupName := stringValue(firstValue(group, "displayName", "display_name"))
		if groupName == "" {
			groupName = fmt.Sprintf("Quota group %d", groupIndex+1)
		}
		for bucketIndex, bucketRaw := range asSlice(firstValue(group, "buckets")) {
			bucket := asMap(bucketRaw)
			used, remaining := percentPointersFromRemainingFraction(firstValue(bucket, "remainingFraction", "remaining_fraction"))
			if remaining == nil {
				continue
			}
			id := stringValue(firstValue(bucket, "bucketId", "bucket_id"))
			if id == "" {
				id = fmt.Sprintf("group-%d-bucket-%d", groupIndex+1, bucketIndex+1)
			}
			label := stringValue(firstValue(bucket, "displayName", "display_name"))
			if label == "" {
				label = id
			}
			window := strings.ToLower(stringValue(firstValue(bucket, "window")))
			items = append(items, objects.CPAQuotaItem{
				ID:               id,
				Group:            groupName,
				Label:            label,
				Description:      stringValue(firstValue(bucket, "description")),
				UsedPercent:      used,
				RemainingPercent: remaining,
				ResetAt:          resetFromRecord(bucket, now),
				PeriodSeconds:    antigravityPeriod(window),
			})
		}
	}
	if len(items) == 0 {
		return QuotaResult{}, fmt.Errorf("Antigravity quota response contained no buckets")
	}

	planType := input.PlanType
	var subscription map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: input.AuthIndex,
		Method:    http.MethodPost,
		URL:       antigravitySubscriptionURL,
		Headers:   headers,
		Body:      `{"metadata":{"ideType":"ANTIGRAVITY"}}`,
	}, &subscription); err == nil {
		planType = resolveAntigravityPlan(subscription, planType)
	}

	return QuotaResult{
		State:    objects.CPAQuotaStateSuccess,
		PlanType: planType,
		Snapshot: objects.CPAQuotaSnapshot{Items: items},
	}, nil
}

func antigravityPeriod(window string) *int {
	var seconds int
	switch window {
	case "5h", "five-hour", "five_hour":
		seconds = 5 * 60 * 60
	case "weekly", "week":
		seconds = 7 * 24 * 60 * 60
	default:
		return nil
	}
	return &seconds
}

func resolveAntigravityPlan(payload map[string]any, fallback string) string {
	current := asMap(firstValue(payload, "currentTier", "current_tier"))
	paid := asMap(firstValue(payload, "paidTier", "paid_tier"))
	tier := current
	if stringValue(firstValue(paid, "id")) != "" {
		tier = paid
	}
	tierID := strings.ToLower(stringValue(firstValue(tier, "id")))
	switch tierID {
	case "free-tier":
		return "free"
	case "g1-pro-tier":
		return "pro"
	case "g1-plus-tier":
		return "plus"
	case "g1-ultra-tier":
		return "ultra"
	case "g1-ultra-lite-tier":
		return "ultra-lite"
	case "":
		return fallback
	default:
		return tierID
	}
}
