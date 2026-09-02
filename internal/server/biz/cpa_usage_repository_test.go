package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
)

func TestCPAUsageRepositoryInvalidatesCredentialLookupCache(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_repository_cache?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("usage repository cache").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("credential.json").
		SetDisplayName("credential.json").
		SetProvider("codex").
		SaveX(ctx)

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := newCPAServiceForTest(client, func() time.Time { return now }).usageRepository
	got, found := repository.lookupCredentialID(ctx, instance.ID, " auth-1 ")
	require.True(t, found)
	require.Equal(t, credential.ID, got)

	require.NoError(t, client.CPACredential.DeleteOneID(credential.ID).Exec(ctx))
	got, found = repository.lookupCredentialID(ctx, instance.ID, "auth-1")
	require.True(t, found, "the warm cache should serve the current TTL")
	require.Equal(t, credential.ID, got)

	repository.invalidateCredentialCache(instance.ID)
	_, found = repository.lookupCredentialID(ctx, instance.ID, "auth-1")
	require.False(t, found, "invalidated auth-index cache must query the current database state")
}

func TestCPAUsageRepositoryLoadsLatestPersistedEventIDs(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_repository_checkpoints?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("usage repository checkpoints").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)

	for _, event := range []struct {
		authIndex string
		model     string
	}{
		{authIndex: "auth-a", model: "first"},
		{authIndex: "auth-b", model: "other"},
		{authIndex: "auth-a", model: "latest"},
	} {
		client.CpaUsageEvent.Create().
			SetCpaInstanceID(instance.ID).
			SetAuthIndex(event.authIndex).
			SetProvider("codex").
			SetModel(event.model).
			SetRequestedAt(time.Now().UTC()).
			SaveX(ctx)
	}

	repository := newCPAServiceForTest(client, time.Now).usageRepository
	checkpoints, err := repository.latestPersistedEventIDs(ctx, instance.ID)
	require.NoError(t, err)
	require.Len(t, checkpoints, 2)
	require.Greater(t, checkpoints["auth-a"], checkpoints["auth-b"])
	require.Equal(t, checkpoints["auth-a"], mustLatestUsageEventID(t, repository, ctx, instance.ID, " auth-a "))
	require.Equal(t, 0, mustLatestUsageEventID(t, repository, ctx, instance.ID, "missing"))
}

func mustLatestUsageEventID(t *testing.T, repository *cpaUsageRepository, ctx context.Context, instanceID int, authIndex string) int {
	t.Helper()
	latest, err := repository.latestPersistedEventID(ctx, instanceID, authIndex)
	require.NoError(t, err)
	return latest
}

func TestCPAUsageRepositoryExpiresCredentialLookupCache(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_repository_expiry?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("usage repository expiry").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("credential.json").
		SetDisplayName("credential.json").
		SetProvider("codex").
		SaveX(ctx)

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := newCPAServiceForTest(client, func() time.Time { return now }).usageRepository
	_, found := repository.lookupCredentialID(ctx, instance.ID, "auth-1")
	require.True(t, found)
	now = now.Add(credentialLookupTTL + time.Second)
	require.NoError(t, client.CPACredential.DeleteOneID(credential.ID).Exec(ctx))
	_, found = repository.lookupCredentialID(ctx, instance.ID, "auth-1")
	require.False(t, found, "expired auth-index cache must query the current database state")
}
