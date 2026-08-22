package cpa

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeAuthFileKeepsOnlySafeAccountMetadata(t *testing.T) {
	t.Parallel()

	normalized, err := NormalizeAuthFile(AuthFile{
		AuthIndex:   "xai-paid",
		Name:        "xai.json",
		Type:        "xai",
		Email:       "secret-api-key-value",
		AccountType: "api_key",
		IDToken:     json.RawMessage(`{"tier":1}`),
	})
	if err != nil {
		t.Fatalf("normalize auth file: %v", err)
	}
	if normalized.Email != "" {
		t.Fatalf("api-key account value was retained as searchable email: %q", normalized.Email)
	}
	if normalized.PlanType != "paid" || !normalized.QuotaContext.Paid {
		t.Fatalf("paid xAI tier was not detected: plan=%q paid=%v", normalized.PlanType, normalized.QuotaContext.Paid)
	}
}

func TestNormalizeAuthFileDoesNotUseAuthMethodAsPlan(t *testing.T) {
	t.Parallel()

	for name, accountType := range map[string]string{
		"oauth":   "oauth",
		"api_key": "api_key",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			normalized, err := NormalizeAuthFile(AuthFile{
				AuthIndex:   "auth-method-1",
				Name:        "account.json",
				Type:        "xai",
				AccountType: accountType,
			})
			if err != nil {
				t.Fatalf("normalize auth file: %v", err)
			}
			if normalized.PlanType != "" {
				t.Fatalf("auth method %q leaked into plan type: %q", accountType, normalized.PlanType)
			}
		})
	}
}

func TestNormalizeAuthFileAcceptsJWTAStringIDToken(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"chatgpt_account_id":"account-9","plan_type":"plus","chatgpt_subscription_active_until":"2026-12-31"}`))
	token := "eyJhbGciOiJIUzI1NiJ9." + payload + ".signature"
	normalized, err := NormalizeAuthFile(AuthFile{
		AuthIndex: "codex-1",
		Name:      "codex.json",
		Type:      "codex",
		IDToken:   json.RawMessage(`"` + token + `"`),
	})
	if err != nil {
		t.Fatalf("normalize auth file: %v", err)
	}
	if normalized.QuotaContext.CodexAccountID != "account-9" {
		t.Fatalf("chatgpt_account_id not extracted from JWT string: %q", normalized.QuotaContext.CodexAccountID)
	}
	if normalized.PlanType != "plus" {
		t.Fatalf("plan_type not extracted from JWT string: %q", normalized.PlanType)
	}
	if normalized.QuotaContext.SubscriptionEnd != "2026-12-31" {
		t.Fatalf("subscription end not extracted from JWT string: %q", normalized.QuotaContext.SubscriptionEnd)
	}
}

func TestParseIDTokenClaimsRejectsMalformedTokens(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]json.RawMessage{
		"not-a-token":  json.RawMessage(`"not-a-jwt"`),
		"two-parts":    json.RawMessage(`"header.payload"`),
		"invalid-b64":  json.RawMessage(`"header.%41invalid.payload"`),
		"huge-payload": json.RawMessage(`"header.` + strings.Repeat("A", 1<<20) + `.sig"`),
		"empty":        json.RawMessage(``),
		"literal-null": json.RawMessage(`null`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if claims := parseIDTokenClaims(raw); claims != nil {
				t.Fatalf("expected no claims for %q, got %v", name, claims)
			}
		})
	}
}
