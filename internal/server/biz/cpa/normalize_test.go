package cpa

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/objects"
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
	if normalized.PlanType != "" || !normalized.QuotaContext.Paid {
		t.Fatalf("paid xAI tier must not invent a plan name: plan=%q paid=%v", normalized.PlanType, normalized.QuotaContext.Paid)
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

func TestNormalizeAuthFileRoutingCooldownState(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, time.October, 23, 10, 0, 0, 0, time.UTC)
	for name, test := range map[string]struct {
		raw   string
		known bool
		count int
	}{
		"missing":              {},
		"whitespace":           {raw: " \n\t "},
		"null":                 {raw: "null"},
		"padded null":          {raw: " \n null \t"},
		"empty array":          {raw: "[]", known: true},
		"object":               {raw: "{}"},
		"string":               {raw: `"[]"`},
		"number":               {raw: "1"},
		"boolean":              {raw: "true"},
		"malformed JSON":       {raw: "["},
		"null item":            {raw: "[null]"},
		"empty item":           {raw: "[{}]"},
		"array item":           {raw: "[[]]"},
		"string item":          {raw: `["credential"]`},
		"unknown scope":        {raw: `[{"scope":"all","retry_at":"2026-10-24T10:00:00Z"}]`},
		"missing scope":        {raw: `[{"retry_at":"2026-10-24T10:00:00Z"}]`},
		"malformed scope":      {raw: `[{"scope":1,"retry_at":"2026-10-24T10:00:00Z"}]`},
		"missing model key":    {raw: `[{"scope":"model","retry_at":"2026-10-24T10:00:00Z"}]`},
		"empty model key":      {raw: `[{"scope":"model","model_key":"","retry_at":"2026-10-24T10:00:00Z"}]`},
		"whitespace model key": {raw: `[{"scope":"model","model_key":" \t ","retry_at":"2026-10-24T10:00:00Z"}]`},
		"malformed model key":  {raw: `[{"scope":"model","model_key":[],"retry_at":"2026-10-24T10:00:00Z"}]`},
		"missing retry time":   {raw: `[{"scope":"credential"}]`},
		"null retry time":      {raw: `[{"scope":"credential","retry_at":null}]`},
		"zero retry time":      {raw: `[{"scope":"credential","retry_at":"0001-01-01T00:00:00Z"}]`},
		"invalid retry time":   {raw: `[{"scope":"credential","retry_at":"tomorrow"}]`},
		"numeric retry time":   {raw: `[{"scope":"credential","retry_at":1792836000}]`},
		"invalid reason type":  {raw: `[{"scope":"credential","reason":{},"retry_at":"2026-10-24T10:00:00Z"}]`},
		"invalid backoff type": {raw: `[{"scope":"credential","retry_at":"2026-10-24T10:00:00Z","backoff_level":"1"}]`},
		"fractional status":    {raw: `[{"scope":"credential","retry_at":"2026-10-24T10:00:00Z","http_status":429.5}]`},
		"valid credential":     {raw: `[{"scope":"credential","reason":"quota","retry_at":"2026-10-24T10:00:00Z"}]`, known: true, count: 1},
		"valid model":          {raw: `[{"scope":"model","model_key":"model-1","reason":"quota","retry_at":"2026-10-24T10:00:00Z"}]`, known: true, count: 1},
		"mixed invalid block":  {raw: `[{"scope":"credential","reason":"quota","retry_at":"2026-10-24T10:00:00Z"},null]`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			normalized, err := NormalizeAuthFile(AuthFile{
				Name:               "account.json",
				Provider:           "codex",
				Unavailable:        true,
				StatusMessage:      "quota exhausted; retry tomorrow",
				Cooldowns:          json.RawMessage(test.raw),
				CooldownObservedAt: &observedAt,
			})
			if err != nil {
				t.Fatalf("cooldown data must not fail credential normalization: %v", err)
			}
			if normalized.RemoteName != "account.json" || !normalized.Unavailable {
				t.Fatalf("credential metadata was lost: %+v", normalized)
			}
			snapshot := normalized.RoutingCooldown
			if snapshot.Known != test.known || len(snapshot.Items) != test.count {
				t.Fatalf("unexpected cooldown snapshot: %+v", snapshot)
			}
			if test.known {
				if snapshot.Items == nil {
					t.Fatal("a known empty snapshot must retain a non-nil array")
				}
				if snapshot.ObservedAt == nil || !snapshot.ObservedAt.Equal(observedAt) {
					t.Fatalf("CPA observation time was not retained: %v", snapshot.ObservedAt)
				}
			} else if snapshot.Items != nil || snapshot.ObservedAt != nil {
				t.Fatalf("an unreliable block must produce the unknown zero value: %+v", snapshot)
			}
		})
	}
}

func TestNormalizeRoutingCooldownReasons(t *testing.T) {
	t.Parallel()

	for _, reason := range []objects.CPARoutingCooldownReason{
		objects.CPARoutingCooldownReasonCredentialQuota,
		objects.CPARoutingCooldownReasonQuota,
		objects.CPARoutingCooldownReasonCloudflareChallenge,
		objects.CPARoutingCooldownReasonModelNotSupported,
		objects.CPARoutingCooldownReasonInvalidGrant,
		objects.CPARoutingCooldownReasonUnauthorized,
		objects.CPARoutingCooldownReasonPaymentRequired,
		objects.CPARoutingCooldownReasonNotFound,
		objects.CPARoutingCooldownReasonTransientError,
		objects.CPARoutingCooldownReasonUnknown,
		objects.CPARoutingCooldownReason("new-upstream-reason"),
		objects.CPARoutingCooldownReason(""),
	} {
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()

			raw := json.RawMessage(`[{"scope":"credential","reason":"` + string(reason) + `","retry_at":"2026-10-24T10:00:00Z"}]`)
			snapshot := normalizeRoutingCooldown(raw, nil)
			if !snapshot.Known || len(snapshot.Items) != 1 {
				t.Fatalf("expected a known cooldown: %+v", snapshot)
			}
			want := reason
			if want == "new-upstream-reason" || want == "" {
				want = objects.CPARoutingCooldownReasonUnknown
			}
			if snapshot.Items[0].Reason != want {
				t.Fatalf("reason = %q, want %q", snapshot.Items[0].Reason, want)
			}
			if snapshot.ObservedAt != nil {
				t.Fatalf("missing CPA observation time must remain nil: %v", snapshot.ObservedAt)
			}
		})
	}
}

func TestNormalizeRoutingCooldownRetainsAbsoluteTimeAndOptionalFields(t *testing.T) {
	t.Parallel()

	snapshot := normalizeRoutingCooldown(json.RawMessage(`[
		{"scope":"model","model_key":" model-1 ","reason":"quota","retry_at":"2000-01-01T01:02:03+02:00","backoff_level":0,"http_status":429},
		{"scope":"credential","reason":"unauthorized","retry_at":"2000-01-01T00:00:00Z","backoff_level":null,"http_status":null},
		{"scope":"credential","reason":"transient_error","retry_at":"2000-01-01T00:00:00Z","backoff_level":3}
	]`), nil)
	if !snapshot.Known || len(snapshot.Items) != 3 {
		t.Fatalf("expired deadlines must be retained until queried: %+v", snapshot)
	}
	model := snapshot.Items[0]
	if model.ModelKey != "model-1" || model.RetryAt.Format(time.RFC3339) != "2000-01-01T01:02:03+02:00" {
		t.Fatalf("model identity or absolute deadline changed: %+v", model)
	}
	if model.BackoffLevel == nil || *model.BackoffLevel != 0 || model.HTTPStatus == nil || *model.HTTPStatus != 429 {
		t.Fatalf("optional values, including zero, must be retained: %+v", model)
	}
	if snapshot.Items[1].BackoffLevel != nil || snapshot.Items[1].HTTPStatus != nil {
		t.Fatalf("null optional fields must remain nil: %+v", snapshot.Items[1])
	}
	if snapshot.Items[2].BackoffLevel == nil || *snapshot.Items[2].BackoffLevel != 3 || snapshot.Items[2].HTTPStatus != nil {
		t.Fatalf("present and absent optional fields must remain distinct: %+v", snapshot.Items[2])
	}
}

func TestNormalizeRoutingCooldownPersistsOnlyWhitelistedFields(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, time.October, 23, 10, 0, 0, 0, time.UTC)
	snapshot := normalizeRoutingCooldown(json.RawMessage(`[{
		"scope":"model","model_key":"model-1","reason":"secret-reason-value",
		"retry_at":"2026-10-24T10:00:00Z","backoff_level":2,"http_status":429,
		"remaining_seconds":86400,"token":"secret-token-value",
		"management_secret":"secret-management-value","status_message":"secret-status-value",
		"raw_error":{"body":"secret-upstream-value"}
	}]`), &observedAt)
	if !snapshot.Known || len(snapshot.Items) != 1 {
		t.Fatalf("unknown fields must not invalidate a valid block: %+v", snapshot)
	}
	persisted, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal cooldown snapshot: %v", err)
	}
	for _, forbidden := range []string{"remaining_seconds", "token", "management_secret", "status_message", "raw_error", "secret-"} {
		if strings.Contains(string(persisted), forbidden) {
			t.Fatalf("unsafe or relative upstream data %q was persisted: %s", forbidden, persisted)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(persisted, &fields); err != nil {
		t.Fatalf("decode persisted snapshot: %v", err)
	}
	if len(fields) != 3 || string(fields["known"]) != "true" || string(fields["observed_at"]) != `"2026-10-23T10:00:00Z"` {
		t.Fatalf("unexpected snapshot JSON shape: %s", persisted)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(fields["items"], &items); err != nil || len(items) != 1 {
		t.Fatalf("decode persisted items: %v; snapshot=%s", err, persisted)
	}
	wantFields := map[string]string{
		"scope":         `"model"`,
		"model_key":     `"model-1"`,
		"reason":        `"unknown"`,
		"retry_at":      `"2026-10-24T10:00:00Z"`,
		"backoff_level": "2",
		"http_status":   "429",
	}
	if len(items[0]) != len(wantFields) {
		t.Fatalf("unexpected item JSON keys: %s", persisted)
	}
	for key, want := range wantFields {
		if string(items[0][key]) != want {
			t.Fatalf("persisted %s = %s, want %s", key, items[0][key], want)
		}
	}
}
