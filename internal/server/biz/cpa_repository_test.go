package biz

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPARepositoryFailurePreservesSnapshotAndCurrentProjection(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_repository_failure?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	instance := client.CPAInstance.Create().
		SetName("repository").
		SetBaseURL("http://127.0.0.1:8321").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	oldSnapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{ID: "weekly", Label: "old"}}}
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("claude:repo").
		SetRemoteName("repo.json").
		SetDisplayName("Stale 10").
		SetProvider("claude").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaData(oldSnapshot).
		SetHealthState(string(objects.CPACredentialHealthHealthy)).
		SaveX(ctx)

	require.NoError(t, client.CPACredential.UpdateOneID(credential.ID).
		SetDisplayName("Current 2").
		SetDisabled(true).
		Exec(ctx))
	outcome, err := svc.repository.applyQuotaOutcome(ctx, cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionFailure,
		instanceID:   instance.ID,
		credentialID: credential.ID,
		attemptedAt:  now,
		err:          errors.New("Authorization: Bearer secret-token"),
	})
	require.NoError(t, err)
	require.NotNil(t, outcome.credential)
	require.Equal(t, oldSnapshot, outcome.credential.QuotaData)
	require.Equal(t, "Current 2", outcome.credential.DisplayName)
	require.True(t, outcome.credential.Disabled)
	require.Equal(t, string(objects.CPACredentialHealthDisabled), outcome.credential.HealthState)
	require.Equal(t, cpaDisplayNameSortKey("Current 2"), outcome.credential.DisplayNameSortKey)
	require.NotContains(t, outcome.credential.QuotaLastError, "secret-token")
}

func TestCPARepositoryPersistsProviderErrorClassification(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_repository_error_classification?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	instance := client.CPAInstance.Create().
		SetName("repository error classification").
		SetBaseURL("http://127.0.0.1:8323").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex:error-classification").
		SetRemoteName("codex.json").
		SetDisplayName("Codex").
		SetProvider("codex").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStatePending)).
		SaveX(ctx)

	tests := []struct {
		name       string
		err        error
		wantExpiry bool
	}{
		{
			name:       "provider unauthorized",
			err:        &cpaclient.ProviderHTTPError{StatusCode: 401},
			wantExpiry: true,
		},
		{
			name: "provider quota exhausted",
			err: &cpaclient.ProviderHTTPError{
				StatusCode: 429,
				Body:       []byte(`{"error":{"code":"usage_limit_reached"}}`),
			},
			wantExpiry: false,
		},
		{
			name: "provider refresh token revoked",
			err: &cpaclient.ProviderHTTPError{
				StatusCode: 400,
				Body:       []byte(`{"error":"invalid_grant","error_description":"refresh token has been revoked"}`),
			},
			wantExpiry: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.repository.applyQuotaOutcome(ctx, cpaQuotaExecutionOutcome{
				status:       cpaQuotaExecutionFailure,
				instanceID:   instance.ID,
				credentialID: credential.ID,
				attemptedAt:  now,
				err:          tt.err,
			})
			require.NoError(t, err)
			fresh := client.CPACredential.GetX(ctx, credential.ID)
			require.Equal(t, string(objects.CPAQuotaStateError), fresh.QuotaState)
			require.Equal(t, tt.wantExpiry, deriveCPAExpired(fresh))
		})
	}

	_, err := svc.repository.applyQuotaOutcome(ctx, cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionSuccess,
		instanceID:   instance.ID,
		credentialID: credential.ID,
		quotaState:   objects.CPAQuotaStateSuccess,
		attemptedAt:  now,
	})
	require.NoError(t, err)
	fresh := client.CPACredential.GetX(ctx, credential.ID)
	require.Empty(t, fresh.QuotaLastError)
	require.False(t, deriveCPAExpired(fresh))
}

func TestCPARepositoryUnsupportedClearsSnapshot(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_repository_unsupported?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	instance := client.CPAInstance.Create().SetName("repository").SetBaseURL("http://127.0.0.1:8322").SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("xai:repo").
		SetRemoteName("repo.json").
		SetDisplayName("Repo").
		SetProvider("xai").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaData(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{ID: "old"}}}).
		SaveX(ctx)

	outcome, err := svc.repository.applyQuotaOutcome(ctx, cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionSuccess,
		instanceID:   instance.ID,
		credentialID: credential.ID,
		quotaState:   objects.CPAQuotaStateUnsupported,
		snapshot:     objects.CPAQuotaSnapshot{},
		attemptedAt:  now,
	})
	require.NoError(t, err)
	require.Empty(t, outcome.credential.QuotaData.Items)
	require.Equal(t, string(objects.CPACredentialHealthHealthy), outcome.credential.HealthState)
}
