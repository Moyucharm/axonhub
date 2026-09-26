package biz

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
	"github.com/stretchr/testify/require"
)

func TestCPACodexNoExpiryCreditOnlyRedeemedManually(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_reset_manual?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) })
	instance := client.CPAInstance.Create().SetName("manual").SetBaseURL("http://127.0.0.1:8317").SetEncryptedSecret("encrypted").SetAutoResetEnabled(true).SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("auth").SetRemoteName("codex.json").SetDisplayName("Codex").
		SetProvider("codex").SetAuthIndex("auth").
		SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "account-a"}).
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaData(objects.CPAQuotaSnapshot{ResetCredits: []objects.CPAQuotaResetCredit{{ID: "manual", Title: "Manual"}}}).
		SaveX(ctx)
	posts := 0
	management := &cpaTestManagementClient{callProvider: func(_ context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		if call.Method == http.MethodPost {
			posts++
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"code":"reset","credit":{"id":"manual","status":"redeemed"}}`)}, nil
		}
		if call.URL == "https://chatgpt.com/backend-api/wham/usage" {
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"rate_limit":{"primary_window":{"used_percent":10}}}`)}, nil
		}
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"credits":[{"id":"manual","status":"available","expires_at":"not-a-date"}]}`)}, nil
	}}
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) { return management, nil }
	svc.autoResetCodexInstance(ctx, instance)
	require.Zero(t, posts)
	live, err := svc.liveCodexResetCredits(ctx, management, credential)
	require.NoError(t, err)
	require.Len(t, live, 1)
	require.Equal(t, "manual", live[0].ID)
	require.Nil(t, live[0].ExpiresAt)
	ok, err := svc.ResetCodexCredential(ctx, credential.ID, "manual")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, posts)
}
