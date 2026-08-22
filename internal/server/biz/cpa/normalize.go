package cpa

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/looplj/axonhub/internal/objects"
)

// NormalizedCredential is the database-safe auth-files representation.
type NormalizedCredential struct {
	ExternalKey   string
	AuthIndex     string
	RemoteName    string
	Label         string
	DisplayName   string
	Provider      string
	Email         string
	Status        string
	StatusMessage string
	Disabled      bool
	Unavailable   bool
	RuntimeOnly   bool
	Priority      int
	PlanType      string
	QuotaContext  objects.CPAQuotaContext
}

// NormalizeAuthFile converts a CPA auth-files entry without retaining token material.
func NormalizeAuthFile(file AuthFile) (NormalizedCredential, error) {
	provider := NormalizeProvider(firstNonEmpty(file.Provider, file.Type))
	remoteName := firstNonEmpty(file.Name, file.ID)
	if remoteName == "" {
		return NormalizedCredential{}, fmt.Errorf("CPA credential is missing name and id")
	}
	displayName := firstNonEmpty(file.Label, remoteName)
	externalKey := strings.TrimSpace(file.AuthIndex)
	if externalKey == "" {
		externalKey = provider + ":" + remoteName
	}

	claims := parseIDTokenClaims(file.IDToken)
	accountType := strings.TrimSpace(file.AccountType)
	// Auth method values (api_key/oauth) describe how the credential
	// authenticates, never which plan it is on; keep them out of PlanType.
	planFallback := accountType
	if strings.EqualFold(planFallback, "api_key") || strings.EqualFold(planFallback, "oauth") {
		planFallback = ""
	}
	planType := strings.ToLower(firstNonEmpty(
		stringValue(firstValue(claims, "plan_type", "planType")),
		planFallback,
	))
	accountID := stringValue(firstValue(claims, "chatgpt_account_id", "chatgptAccountId"))
	subscriptionEnd := stringValue(firstValue(claims,
		"chatgpt_subscription_active_until", "chatgptSubscriptionActiveUntil"))
	tier := numberValue(firstValue(claims, "tier"))
	paid := (file.UsingAPI && strings.EqualFold(file.Prefix, "paid")) ||
		strings.Contains(planType, "paid") || strings.Contains(strings.ToLower(accountType), "paid") ||
		(tier != nil && *tier >= 1)
	if paid && planType == "" {
		planType = "paid"
	}

	email := strings.TrimSpace(file.Email)
	if strings.EqualFold(accountType, "api_key") {
		email = ""
	}

	return NormalizedCredential{
		ExternalKey:   externalKey,
		AuthIndex:     strings.TrimSpace(file.AuthIndex),
		RemoteName:    remoteName,
		Label:         strings.TrimSpace(file.Label),
		DisplayName:   displayName,
		Provider:      provider,
		Email:         email,
		Status:        strings.ToLower(firstNonEmpty(file.Status, "unknown")),
		StatusMessage: strings.TrimSpace(file.StatusMessage),
		Disabled:      file.Disabled,
		Unavailable:   file.Unavailable,
		RuntimeOnly:   file.RuntimeOnly,
		Priority:      file.Priority,
		PlanType:      planType,
		QuotaContext: objects.CPAQuotaContext{
			ProjectID:       strings.TrimSpace(file.ProjectID),
			CodexAccountID:  accountID,
			SubscriptionEnd: subscriptionEnd,
			AccountType:     accountType,
			Paid:            paid,
		},
	}, nil
}

func parseObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	return result
}

// maxIDTokenClaimsBytes caps the decoded JWT payload size accepted from a CPA
// auth-files id_token so a hostile management API response cannot force an
// unbounded allocation.
const maxIDTokenClaimsBytes = 64 << 10

// parseIDTokenClaims extracts account metadata from a CPA auth-files id_token.
// CPA may report the token either as a JSON object (legacy shape) or as a quoted
// JWT string; both forms are accepted. Only the whitelisted claims read by
// NormalizeAuthFile are extracted, the raw token is never persisted, and the
// signature is not verified (the value already arrived over an authenticated
// management API call).
func parseIDTokenClaims(raw json.RawMessage) map[string]any {
	if claims := parseObject(raw); claims != nil {
		return claims
	}
	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil
	}
	payload, err := decodeBase64URL(parts[1])
	if err != nil || len(payload) > maxIDTokenClaimsBytes {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func decodeBase64URL(value string) ([]byte, error) {
	if pad := len(value) % 4; pad != 0 {
		value += strings.Repeat("=", 4-pad)
	}
	return base64.URLEncoding.DecodeString(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
