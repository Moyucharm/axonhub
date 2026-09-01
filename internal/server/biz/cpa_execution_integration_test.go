package biz

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPAExecutionIntegrationSyncsAndRefreshesThroughManagementHTTP(t *testing.T) {
	var providerCalls atomic.Int32
	authorizationHeaders := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizationHeaders <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`[{"auth_index":"claude-auth","name":"claude.json","type":"claude","status":"active","email":"user@example.com"}]`))
		case "/v0/management/api-call":
			if providerCalls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"five_hour\":{\"utilization\":25}}"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"account\":{\"has_claude_pro\":true}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_execution_integration?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	instance := client.CPAInstance.Create().
		SetName("integration").
		SetBaseURL(server.URL).
		SetEncryptedSecret("not-used-with-explicit-client").
		SaveX(ctx)
	managementClient, err := cpaclient.NewClient(cpaclient.Config{BaseURL: server.URL, ManagementSecret: "management-key"})
	require.NoError(t, err)
	defer managementClient.CloseIdleConnections()

	credentials, err := svc.syncInstanceCredentialsWithClient(ctx, instance, managementClient)
	require.NoError(t, err)
	require.Len(t, credentials, 1)
	require.Equal(t, "claude", credentials[0].Provider)
	require.Equal(t, "user@example.com", credentials[0].Email)

	outcome := svc.refreshCredentialOutcome(ctx, managementClient, credentials[0])
	require.NoError(t, outcome.err)
	require.Equal(t, cpaQuotaExecutionSuccess, outcome.status)
	require.NotNil(t, outcome.credential)
	require.Equal(t, string(objects.CPAQuotaStateSuccess), outcome.credential.QuotaState)
	require.Equal(t, string(objects.CPACredentialHealthHealthy), outcome.credential.HealthState)
	require.Equal(t, int32(2), providerCalls.Load())

	stored, err := client.CPACredential.Get(ctx, credentials[0].ID)
	require.NoError(t, err)
	require.Equal(t, outcome.credential.QuotaData, stored.QuotaData)
	require.Equal(t, now, *stored.QuotaLastSuccessAt)
	require.Equal(t, currentCPAProjectionVersion, stored.ProjectionVersion)
	for range 3 {
		require.Equal(t, "Bearer management-key", <-authorizationHeaders)
	}
}
