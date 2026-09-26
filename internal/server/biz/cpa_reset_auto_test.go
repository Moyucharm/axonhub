package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	svc.autoResetCodexInstance(ctx, instance)
	require.Empty(t, posted)
	require.Zero(t, providerCalls.Load())

	// Auto use depends on the patrol that refreshes quota and reset credits.
	instance = client.CPAInstance.UpdateOneID(instance.ID).SetAutoResetEnabled(true).SaveX(ctx)
	svc.autoResetCodexInstance(ctx, instance)
	require.Empty(t, posted)
	require.Zero(t, providerCalls.Load())

	// Only cards inside the 30 minute window are used, in expiry order.
	instance = client.CPAInstance.UpdateOneID(instance.ID).SetAutoManageEnabled(true).SaveX(ctx)
	svc.autoResetCodexInstance(ctx, instance)
	require.Equal(t, []string{"29", "30"}, posted)

	// A repeated cycle cannot spend the same card twice.
	svc.autoResetCodexInstance(ctx, instance)
	require.Equal(t, []string{"29", "30"}, posted)
	require.Len(t, client.CPACodexResetAttempt.Query().AllX(ctx), 2)
	require.Equal(t, string(objects.CPAQuotaStateSuccess), client.CPACredential.GetX(ctx, credential.ID).QuotaState)
}
