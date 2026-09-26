package biz

import (
	"context"
	"errors"
	"net/http"
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

func TestCPACodexResetUncertainNeverReposts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_reset_uncertain?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) })
	instance := client.CPAInstance.Create().SetName("unknown").SetBaseURL("http://127.0.0.1:8317").SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).SetExternalKey("auth").SetRemoteName("codex.json").SetDisplayName("Codex").SetProvider("codex").SetAuthIndex("auth").SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "account-a"}).SaveX(ctx)
	posts := 0
	management := &cpaTestManagementClient{callProvider: func(_ context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		if call.Method == http.MethodPost {
			posts++
			return nil, errors.New("connection lost")
		}
		if call.URL == "https://chatgpt.com/backend-api/wham/usage" {
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"rate_limit":{"primary_window":{"used_percent":10}}}`)}, nil
		}
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"credits":[{"id":"first","status":"available","expires_at":"2099-01-01T00:10:00Z"},{"id":"second","status":"available","expires_at":"2099-01-01T00:20:00Z"}]}`)}, nil
	}}
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) { return management, nil }
	require.NoError(t, svc.refreshCredentialOutcome(ctx, management, credential).err)
	_, err := svc.ResetCodexCredential(ctx, credential.ID, "first")
	require.ErrorContains(t, err, "uncertain")
	_, err = svc.ResetCodexCredential(ctx, credential.ID, "first")
	require.Error(t, err)
	require.Equal(t, 1, posts)
	require.Equal(t, cpacodexresetattempt.StateUncertain, client.CPACodexResetAttempt.Query().OnlyX(ctx).State)
	// The display path hides the card whose consume outcome is unknown.
	connection, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{InstanceID: instance.ID, First: 10})
	require.NoError(t, err)
	require.Len(t, connection.Edges, 1)
	require.Equal(t, []string{"second"}, resetCreditIDs(connection.Edges[0].Node.QuotaData.ResetCredits))
}
