package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

const (
	codexResetCreditsURL      = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetStatusAvailable = "available"
)

type CodexResetCredit struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ResetType string `json:"reset_type"`
	GrantedAt string `json:"granted_at"`
	ExpiresAt string `json:"expires_at"`
	Title     string `json:"title"`
}

// UsableCodexResetCredits keeps the cards a user may act on: available, with an
// ID, and not already expired. Cards without a parseable expiry stay visible for
// manual use but never qualify for automatic use. Ordering is expiry ascending,
// then ID, with cards lacking an expiry last.
func UsableCodexResetCredits(raw []CodexResetCredit, now time.Time) []objects.CPAQuotaResetCredit {
	credits := make([]objects.CPAQuotaResetCredit, 0, len(raw))
	for _, credit := range raw {
		if credit.Status != codexResetStatusAvailable || strings.TrimSpace(credit.ID) == "" {
			continue
		}
		card := objects.CPAQuotaResetCredit{ID: credit.ID, Title: credit.Title, ResetType: credit.ResetType}
		if parsed, err := time.Parse(time.RFC3339, credit.ExpiresAt); err == nil {
			if !parsed.After(now) {
				continue
			}
			card.ExpiresAt = &parsed
		}
		if parsed, err := time.Parse(time.RFC3339, credit.GrantedAt); err == nil {
			card.GrantedAt = &parsed
		}
		credits = append(credits, card)
	}
	sort.Slice(credits, func(i, j int) bool {
		left, right := credits[i], credits[j]
		switch {
		case left.ExpiresAt == nil && right.ExpiresAt == nil:
			return left.ID < right.ID
		case left.ExpiresAt == nil:
			return false
		case right.ExpiresAt == nil:
			return true
		case left.ExpiresAt.Equal(*right.ExpiresAt):
			return left.ID < right.ID
		default:
			return left.ExpiresAt.Before(*right.ExpiresAt)
		}
	})
	return credits
}

func codexResetHeaders(accountID string) map[string]string {
	return map[string]string{
		"Authorization":      "Bearer $TOKEN$",
		"Chatgpt-Account-Id": accountID,
		"Content-Type":       "application/json",
	}
}

// ListCodexResetCredits reads live credits through CPA without persisting the response.
func ListCodexResetCredits(ctx context.Context, client ManagementClient, authIndex, accountID string) ([]CodexResetCredit, error) {
	var response struct {
		Credits *[]CodexResetCredit `json:"credits"`
	}
	_, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       codexResetCreditsURL,
		Headers:   codexResetHeaders(accountID),
	}, &response)
	if err != nil {
		return nil, fmt.Errorf("Codex reset credits provider request failed")
	}
	if response.Credits == nil {
		return nil, fmt.Errorf("Codex reset credits provider response is missing credits")
	}
	return *response.Credits, nil
}

// ConsumeCodexResetCredit never retries; callers acquire the unique DB claim first.
func ConsumeCodexResetCredit(ctx context.Context, client ManagementClient, authIndex, accountID, creditID, requestID string) error {
	data, err := json.Marshal(map[string]string{"credit_id": creditID, "redeem_request_id": requestID})
	if err != nil {
		return fmt.Errorf("encode Codex reset request: %w", err)
	}
	var response struct {
		Code   string `json:"code"`
		Credit struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"credit"`
	}
	_, err = callJSON(ctx, client, ProviderCall{
		AuthIndex: authIndex,
		Method:    http.MethodPost,
		URL:       codexResetCreditsURL + "/consume",
		Headers:   codexResetHeaders(accountID),
		Body:      string(data),
	}, &response)
	if err != nil {
		return fmt.Errorf("Codex reset consumption request failed")
	}
	if response.Code != "reset" || response.Credit.ID != creditID || response.Credit.Status != "redeemed" {
		return fmt.Errorf("Codex reset consumption response did not confirm selected credit")
	}
	return nil
}
