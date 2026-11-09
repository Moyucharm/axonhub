package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPACodexAutoResetConsumesOnlyExpiringCredits(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var posted []string
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		var call struct {
			URL  string `json:"url"`
			Data string `json:"data"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&call))
		body := `{"credits":[{"id":"29","status":"available","expires_at":"2026-09-01T12:29:00Z"},{"id":"30","status":"available","expires_at":"2026-09-01T12:30:00Z"},{"id":"31","status":"available","expires_at":"2026-09-01T12:31:00Z"},{"id":"manual","status":"available"}]}`
		switch call.URL {
		case "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume":
			var data map[string]string
			require.NoError(t, json.Unmarshal([]byte(call.Data), &data))
			mu.Lock()
			posted = append(posted, data["credit_id"])
			mu.Unlock()
			body = `{"code":"reset","credit":{"id":"` + data["credit_id"] + `","status":"redeemed"}}`
		case "https://chatgpt.com/backend-api/wham/usage":
			body = `{"rate_limit":{"primary_window":{"used_percent":10}}}`
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body}))
	}))
	defer server.Close()
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_auto_reset?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	instance := client.CPAInstance.Create().SetName("auto").SetBaseURL(server.URL).SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex:1").SetRemoteName("codex.json").SetDisplayName("Codex").
		SetProvider("codex").SetAuthIndex("auth").
		SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "account-a"}).
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaData(objects.CPAQuotaSnapshot{ResetCredits: []objects.CPAQuotaResetCredit{
			{ID: "29", ExpiresAt: lo.ToPtr(now.Add(29 * time.Minute))},
			{ID: "30", ExpiresAt: lo.ToPtr(now.Add(30 * time.Minute))},
			{ID: "31", ExpiresAt: lo.ToPtr(now.Add(31 * time.Minute))},
			{ID: "manual"},
		}}).
		SaveX(ctx)
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) {
		return cpaclient.NewClient(cpaclient.Config{BaseURL: server.URL, ManagementSecret: "secret"})
	}

	// Disabled switch: the phase must not touch the provider at all.
	svc.autoResetCodexInstance(ctx, instance, []*ent.CPACredential{client.CPACredential.GetX(ctx, credential.ID)})
	require.Empty(t, posted)
	require.Zero(t, providerCalls.Load())

	// Automatic use requires refresh, but not automatic status management.
	instance = client.CPAInstance.UpdateOneID(instance.ID).SetAutoRefreshEnabled(false).SetAutoResetEnabled(true).SaveX(ctx)
	svc.autoResetCodexInstance(ctx, instance, []*ent.CPACredential{client.CPACredential.GetX(ctx, credential.ID)})
	require.Empty(t, posted)
	require.Zero(t, providerCalls.Load())

	// Only cards inside the 30 minute window are used, in expiry order.
	instance = client.CPAInstance.UpdateOneID(instance.ID).SetAutoRefreshEnabled(true).SaveX(ctx)
	failedCards := *credential
	failedCards.QuotaData.ResetCreditsFailed = true
	svc.autoResetCodexInstance(ctx, instance, []*ent.CPACredential{&failedCards})
	require.Zero(t, providerCalls.Load(), "failed card collection cannot authorize consumption")
	svc.autoResetCodexInstance(ctx, instance, []*ent.CPACredential{client.CPACredential.GetX(ctx, credential.ID)})
	require.Equal(t, []string{"29", "30"}, posted)

	// A repeated cycle cannot spend the same card twice.
	svc.autoResetCodexInstance(ctx, instance, []*ent.CPACredential{client.CPACredential.GetX(ctx, credential.ID)})
	require.Equal(t, []string{"29", "30"}, posted)
	require.Len(t, client.CPAResetAttempt.Query().AllX(ctx), 2)
	require.Equal(t, string(objects.CPAQuotaStateSuccess), client.CPACredential.GetX(ctx, credential.ID).QuotaState)
}

func TestCPACodexResetAndStatusManagementAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		manage, disabled, refreshFails, consumeFails bool
		wantPatches                                  int
	}{
		{name: "only reset permission", disabled: true},
		{name: "reset before disable", manage: true},
		{name: "enable after reset", manage: true, disabled: true, wantPatches: 1},
		{name: "failed refresh never toggles", manage: true, refreshFails: true},
		{name: "uncertain consume never toggles", manage: true, consumeFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:"+t.Name()+"?mode=memory&_fk=0")
			defer client.Close()
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			svc := newCPAServiceForTest(client, func() time.Time { return now })
			instance := client.CPAInstance.Create().SetName("auto").SetBaseURL("http://127.0.0.1:8317").
				SetEncryptedSecret("encrypted").SetAutoRefreshEnabled(true).
				SetAutoManageEnabled(tc.manage).SetAutoResetEnabled(true).SaveX(ctx)
			credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).
				SetExternalKey("auth").SetRemoteName("codex.json").SetDisplayName("Codex").
				SetProvider("codex").SetAuthIndex("auth").SetDisabled(tc.disabled).
				SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "account"}).
				SetQuotaState(string(objects.CPAQuotaStateSuccess)).
				SetQuotaData(objects.CPAQuotaSnapshot{
					Items:        []objects.CPAQuotaItem{{ID: "weekly", UsedPercent: lo.ToPtr(100.0), PeriodSeconds: lo.ToPtr(604800)}},
					ResetCredits: []objects.CPAQuotaResetCredit{{ID: "soon", ExpiresAt: lo.ToPtr(now.Add(20 * time.Minute))}},
				}).SaveX(ctx)
			var operations []string
			patches := 0
			management := &cpaTestManagementClient{
				callProvider: func(_ context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
					body := `{"credits":[{"id":"soon","status":"available","expires_at":"2026-09-01T12:20:00Z"}]}`
					switch call.URL {
					case "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume":
						operations = append(operations, "consume")
						if tc.consumeFails {
							return nil, errors.New("tunnel dropped")
						}
						body = `{"code":"reset","credit":{"id":"soon","status":"redeemed"}}`
					case "https://chatgpt.com/backend-api/wham/usage":
						operations = append(operations, "refresh")
						if tc.refreshFails {
							return nil, errors.New("quota unavailable")
						}
						body = `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000},"secondary_window":{"used_percent":10,"limit_window_seconds":604800}}}`
					}
					return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(body)}, nil
				},
				patchAuthFileStatus: func(_ context.Context, _, _ string, disabled bool) error {
					operations = append(operations, "patch")
					patches++
					require.False(t, disabled, "a successful reset must not disable the recovered credential")
					return nil
				},
				listCredentials: func(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
					return &cpaclient.AuthFilesResponse{Files: []cpaclient.AuthFile{{AuthIndex: "auth", Name: "codex.json", Type: "codex"}}}, cpaclient.BuildInfo{}, nil
				},
			}
			svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) { return management, nil }
			svc.automateRefreshedCredentials(ctx, instance, []cpaQuotaExecutionOutcome{refreshOutcomeFromCredential(credential, now)})
			require.Equal(t, tc.wantPatches, patches)
			require.NotEmpty(t, operations)
			require.Equal(t, "consume", operations[0])
			if !tc.consumeFails {
				require.Equal(t, "refresh", operations[1])
			}
			if tc.wantPatches > 0 {
				require.Equal(t, "patch", operations[len(operations)-1])
			}
		})
	}
}

func TestCPARuntimeRefreshOnlyCollectsQuotaAndResetCredits(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:"+t.Name()+"?mode=memory&_fk=0")
			defer client.Close()
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			now := time.Now().UTC()
			svc := newCPAServiceForTest(client, func() time.Time { return now })
			instance := client.CPAInstance.Create().SetName("read-only").SetBaseURL("http://127.0.0.1:8317").
				SetEncryptedSecret("encrypted").SetAutoRefreshEnabled(true).
				SetAutoManageEnabled(false).SetAutoResetEnabled(false).SaveX(ctx)
			var quotaCalls, cardCalls, writes atomic.Int32
			management := &cpaTestManagementClient{
				listCredentials: func(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
					return &cpaclient.AuthFilesResponse{Files: []cpaclient.AuthFile{{
						AuthIndex: "auth", Name: "codex.json", Type: "codex", Status: "active", Disabled: disabled,
						IDToken: json.RawMessage(`{"chatgpt_account_id":"account"}`),
					}}}, cpaclient.BuildInfo{}, nil
				},
				callProvider: func(_ context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
					if call.Method == http.MethodPost {
						writes.Add(1)
					}
					body := fmt.Sprintf(`{"credits":[{"id":"soon","status":"available","expires_at":%q}]}`, now.Add(20*time.Minute).Format(time.RFC3339))
					if strings.HasSuffix(call.URL, "/usage") {
						quotaCalls.Add(1)
						body = `{"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800}}}`
					} else {
						cardCalls.Add(1)
					}
					return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(body)}, nil
				},
				patchAuthFileStatus: func(context.Context, string, string, bool) error { writes.Add(1); return nil },
			}
			svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) { return management, nil }
			svc.runCPARuntime(ctx)
			credential := client.CPACredential.Query().OnlyX(ctx)
			require.Equal(t, string(objects.CPAQuotaStateSuccess), credential.QuotaState)
			require.Len(t, credential.QuotaData.ResetCredits, 1)
			require.Equal(t, disabled, credential.Disabled)
			require.Equal(t, int32(1), quotaCalls.Load())
			require.Equal(t, int32(1), cardCalls.Load())
			require.Zero(t, writes.Load())
			updated := client.CPAInstance.GetX(ctx, instance.ID)
			require.NotNil(t, updated.NextRefreshAt)
			require.NotNil(t, updated.NextEnabledPatrolAt)
			require.NotNil(t, updated.NextDisabledPatrolAt)
		})
	}
}
