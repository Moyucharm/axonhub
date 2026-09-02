package biz

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func TestEnsureCPAUsageCollectorIdentityAdoptsLegacyIdentity(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_identity_legacy?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("legacy identity").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("codex.json").
		SetDisplayName("codex.json").
		SetProvider("codex").
		SetQuotaObserved(objects.CPAQuotaObserved{SecondaryCollectorSessionID: "legacy-session"}).
		SaveX(ctx)

	svc := newCPAServiceForTest(client, nil)
	resolved, err := svc.ensureCPAUsageCollectorIdentity(ctx, instance)
	require.NoError(t, err)
	require.Equal(t, "legacy-session", resolved.UsageCollectorID)

	reloaded, err := client.CPAInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-session", reloaded.UsageCollectorID)

	resolved, err = svc.ensureCPAUsageCollectorIdentity(ctx, reloaded)
	require.NoError(t, err)
	require.Equal(t, "legacy-session", resolved.UsageCollectorID)
}

func TestEnsureCPAUsageCollectorIdentityCreatesStableIdentityWhenLegacyStateIsAmbiguous(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_identity_ambiguous?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("ambiguous identity").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	for index, sessionID := range []string{"legacy-a", "legacy-b"} {
		name := fmt.Sprintf("%d", index+1)
		client.CPACredential.Create().
			SetCpaInstanceID(instance.ID).
			SetExternalKey("credential-" + name).
			SetAuthIndex("auth-" + name).
			SetRemoteName("codex-" + name + ".json").
			SetDisplayName("codex-" + name + ".json").
			SetProvider("codex").
			SetQuotaObserved(objects.CPAQuotaObserved{SecondaryCollectorSessionID: sessionID}).
			SaveX(ctx)
	}

	svc := newCPAServiceForTest(client, nil)
	resolved, err := svc.ensureCPAUsageCollectorIdentity(ctx, instance)
	require.NoError(t, err)
	require.NotEmpty(t, resolved.UsageCollectorID)
	require.NotEqual(t, "legacy-a", resolved.UsageCollectorID)
	require.NotEqual(t, "legacy-b", resolved.UsageCollectorID)

	reloaded, err := client.CPAInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, resolved.UsageCollectorID, reloaded.UsageCollectorID)
}
