package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/objects"
)

type stubManagementClient struct {
	call func(context.Context, ProviderCall) (*ProviderCallResult, error)
}

func (s stubManagementClient) CloseIdleConnections() {}

func (s stubManagementClient) ListCredentials(context.Context) (*AuthFilesResponse, BuildInfo, error) {
	return nil, BuildInfo{}, fmt.Errorf("unexpected ListCredentials call")
}

func (s stubManagementClient) CallProvider(ctx context.Context, call ProviderCall) (*ProviderCallResult, error) {
	return s.call(ctx, call)
}

func (s stubManagementClient) ListUsageQueue(context.Context, int) ([]*UsageEvent, error) {
	return nil, fmt.Errorf("unexpected ListUsageQueue call")
}

func (s stubManagementClient) PatchAuthFileStatus(context.Context, string, string, bool) error {
	return fmt.Errorf("unexpected PatchAuthFileStatus call")
}

func newStubProviderClient(usage, credits string, creditsErr error) stubManagementClient {
	return stubManagementClient{call: func(_ context.Context, call ProviderCall) (*ProviderCallResult, error) {
		switch call.URL {
		case codexUsageURL:
			return &ProviderCallResult{StatusCode: http.StatusOK, Body: []byte(usage)}, nil
		case codexResetCreditsURL:
			if creditsErr != nil {
				return nil, creditsErr
			}
			return &ProviderCallResult{StatusCode: http.StatusOK, Body: []byte(credits)}, nil
		default:
			return nil, fmt.Errorf("unexpected provider URL %s", call.URL)
		}
	}}
}

const codexUsageBody = `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":600}}}`

func TestCodexResetCreditsProviderEnvelope(t *testing.T) {
	var calls []ProviderCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v0/management/api-call", r.URL.Path)
		var payload struct {
			AuthIndex string            `json:"auth_index"`
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Header    map[string]string `json:"header"`
			Data      string            `json:"data"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		calls = append(calls, ProviderCall{AuthIndex: payload.AuthIndex, Method: payload.Method, URL: payload.URL, Headers: payload.Header, Body: payload.Data})
		body := `{"credits":[{"id":"credit-a","status":"available","expires_at":"2030-01-01T00:00:00Z"}]}`
		if payload.Method == http.MethodPost {
			var data map[string]string
			require.NoError(t, json.Unmarshal([]byte(payload.Data), &data))
			require.Equal(t, "credit-a", data["credit_id"])
			require.Equal(t, "request-uuid", data["redeem_request_id"])
			body = `{"code":"reset","credit":{"id":"credit-a","status":"redeemed"}}`
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body}))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, ManagementSecret: "secret"})
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	credits, err := ListCodexResetCredits(context.Background(), client, "auth-1", "account-1")
	require.NoError(t, err)
	require.Equal(t, "credit-a", credits[0].ID)
	require.NoError(t, ConsumeCodexResetCredit(context.Background(), client, "auth-1", "account-1", "credit-a", "request-uuid"))
	require.Len(t, calls, 2)
	require.Equal(t, http.MethodGet, calls[0].Method)
	require.Equal(t, http.MethodPost, calls[1].Method)
	for _, call := range calls {
		require.Equal(t, "auth-1", call.AuthIndex)
		require.Equal(t, "Bearer $TOKEN$", call.Headers["Authorization"])
		require.Equal(t, "account-1", call.Headers["Chatgpt-Account-Id"])
	}
}

func TestCodexResetCreditsRejectsProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": 404, "body": `{"error":"secret provider response"}`}))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, ManagementSecret: "secret"})
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	credits, err := ListCodexResetCredits(context.Background(), client, "auth", "account")
	require.Nil(t, credits)
	require.ErrorContains(t, err, "failed")
	require.NotContains(t, err.Error(), "secret provider response")
}

func TestUsableCodexResetCreditsFiltersAndOrders(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	raw := []CodexResetCredit{
		{ID: "late", Status: "available", Title: "Late", ExpiresAt: now.Add(40 * time.Minute).Format(time.RFC3339)},
		{ID: "first", Status: "available", Title: "First", ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339), GrantedAt: now.Add(time.Hour).Format(time.RFC3339)},
		{ID: "manual", Status: "available", Title: "Manual"},
		{ID: "broken-expiry", Status: "available", Title: "Broken", ExpiresAt: "not-a-date"},
		{ID: "past", Status: "available", ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339)},
		{ID: "used", Status: "redeemed", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
		{ID: "  ", Status: "available", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
	}
	credits := UsableCodexResetCredits(raw, now)
	require.Equal(t, []string{"first", "late", "broken-expiry", "manual"}, []string{credits[0].ID, credits[1].ID, credits[2].ID, credits[3].ID})
	require.Nil(t, credits[2].ExpiresAt)
	require.Nil(t, credits[3].ExpiresAt)
	require.NotNil(t, credits[0].ExpiresAt)
	require.NotNil(t, credits[0].GrantedAt)
}

func TestCodexQuotaFetchIncludesResetCredits(t *testing.T) {
	now := time.Now().UTC()
	credits := fmt.Sprintf(`{"credits":[{"id":"second","status":"available","expires_at":%q},{"id":"first","status":"available","expires_at":%q}]}`,
		now.Add(20*time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339))
	result, err := codexQuotaAdapter{}.Fetch(context.Background(), newStubProviderClient(codexUsageBody, credits, nil), CredentialInput{
		AuthIndex: "auth", Provider: "codex", AccountID: "account",
	})
	require.NoError(t, err)
	require.Equal(t, objects.CPAQuotaStateSuccess, result.State)
	require.Len(t, result.Snapshot.Items, 1)
	require.False(t, result.Snapshot.ResetCreditsFailed)
	require.Len(t, result.Snapshot.ResetCredits, 2)
	require.Equal(t, "first", result.Snapshot.ResetCredits[0].ID)
	require.Equal(t, "second", result.Snapshot.ResetCredits[1].ID)
}

func TestCodexQuotaFetchKeepsQuotaWhenCardReadFails(t *testing.T) {
	result, err := codexQuotaAdapter{}.Fetch(context.Background(), newStubProviderClient(codexUsageBody, "", fmt.Errorf("tunnel down")), CredentialInput{
		AuthIndex: "auth", Provider: "codex", AccountID: "account",
	})
	require.NoError(t, err)
	require.Equal(t, objects.CPAQuotaStateSuccess, result.State)
	require.NotEmpty(t, result.Snapshot.Items)
	require.Empty(t, result.Snapshot.ResetCredits)
	require.True(t, result.Snapshot.ResetCreditsFailed)
}
