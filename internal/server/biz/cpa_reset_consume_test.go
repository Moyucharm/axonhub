package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacodexresetattempt"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
	"github.com/stretchr/testify/require"
)

func TestCPACodexResetConsumesSelectedCreditOnce(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Data   string `json:"data"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&call))
		body := `{"credits":[{"id":"first","status":"available","expires_at":"2099-01-01T00:10:00Z"},{"id":"second","status":"available","expires_at":"2099-01-01T00:20:00Z"}]}`
		if call.Method == http.MethodPost && call.URL == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume" {
			var payload map[string]string
			require.NoError(t, json.Unmarshal([]byte(call.Data), &payload))
			require.Equal(t, "first", payload["credit_id"])
			require.NotEmpty(t, payload["redeem_request_id"])
			posts.Add(1)
			body = `{"code":"reset","credit":{"id":"first","status":"redeemed"}}`
		} else if call.URL == "https://chatgpt.com/backend-api/wham/usage" {
			body = `{"rate_limit":{"primary_window":{"used_percent":10}}}`
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body}))
	}))
	defer server.Close()
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_reset_consume?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) })
	instance := client.CPAInstance.Create().SetName("consume").SetBaseURL(server.URL).SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).SetExternalKey("codex:1").SetRemoteName("codex.json").SetDisplayName("Codex").SetProvider("codex").SetAuthIndex("auth").SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "account-a"}).SaveX(ctx)
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) {
		return cpaclient.NewClient(cpaclient.Config{BaseURL: server.URL, ManagementSecret: "secret"})
	}
	ok, err := svc.ResetCodexCredential(ctx, credential.ID, "second")
	require.Error(t, err)
	require.False(t, ok)
	ok, err = svc.ResetCodexCredential(ctx, credential.ID, "first")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = svc.ResetCodexCredential(ctx, credential.ID, "first")
	require.Error(t, err)
	require.False(t, ok)
	require.Equal(t, int32(1), posts.Load())
	connection, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{InstanceID: instance.ID, First: 10})
	require.NoError(t, err)
	require.Len(t, connection.Edges, 1)
	require.Equal(t, []string{"second"}, resetCreditIDs(connection.Edges[0].Node.QuotaData.ResetCredits))
}

func resetCreditIDs(credits []objects.CPAQuotaResetCredit) []string {
	ids := make([]string, 0, len(credits))
	for _, credit := range credits {
		ids = append(ids, credit.ID)
	}
	return ids
}

func TestCPACodexResetAllowsSuccessorAfterClaimedCard(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Data   string `json:"data"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&call))
		body := `{"credits":[{"id":"first","status":"available","expires_at":"2099-01-01T00:10:00Z"},{"id":"second","status":"available","expires_at":"2099-01-01T00:20:00Z"}]}`
		if call.URL == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume" {
			var payload map[string]string
			require.NoError(t, json.Unmarshal([]byte(call.Data), &payload))
			require.Equal(t, "second", payload["credit_id"])
			posts.Add(1)
			body = `{"code":"reset","credit":{"id":"second","status":"redeemed"}}`
		} else if call.URL == "https://chatgpt.com/backend-api/wham/usage" {
			body = `{"rate_limit":{"primary_window":{"used_percent":10}}}`
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body}))
	}))
	defer server.Close()
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_reset_successor?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) })
	instance := client.CPAInstance.Create().SetName("successor").SetBaseURL(server.URL).SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex:1").SetRemoteName("codex.json").SetDisplayName("Codex").
		SetProvider("codex").SetAuthIndex("auth").
		SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "account-a"}).
		SaveX(ctx)
	// A claimed earliest card must not keep its successor permanently rejected.
	client.CPACodexResetAttempt.Create().
		SetCreditKey(codexCreditKey("account-a", "first")).
		SetCredentialID(credential.ID).
		SetState(cpacodexresetattempt.StateUncertain).
		SaveX(ctx)
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) {
		return cpaclient.NewClient(cpaclient.Config{BaseURL: server.URL, ManagementSecret: "secret"})
	}
	ok, err := svc.ResetCodexCredential(ctx, credential.ID, "second")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int32(1), posts.Load())
	connection, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{InstanceID: instance.ID, First: 10})
	require.NoError(t, err)
	require.Len(t, connection.Edges, 1)
	require.Empty(t, resetCreditIDs(connection.Edges[0].Node.QuotaData.ResetCredits))
}
