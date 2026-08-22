package biz

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPASyncQueryAndSnapshotDeletion(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_sync?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := &CPAService{
		AbstractService: &AbstractService{db: client},
		quotaRegistry:   cpaclient.NewQuotaRegistry(),
		now:             func() time.Time { return time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC) },
	}
	instance, err := client.CPAInstance.Create().
		SetName("Primary CPA").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	files := []cpaclient.AuthFile{
		{
			AuthIndex: "codex-1",
			Name:      "z-codex.json",
			Type:      "codex",
			Label:     "Codex Work",
			Email:     "work@example.com",
			Status:    "active",
			Priority:  20,
			IDToken:   json.RawMessage(`{"chatgpt_account_id":"account-1","plan_type":"plus"}`),
		},
		{
			AuthIndex: "custom-1",
			Name:      "a-custom.json",
			Type:      "custom-provider",
			Status:    "active",
			Priority:  5,
		},
		{
			Name:        "unstable-runtime",
			Type:        "codex",
			RuntimeOnly: true,
			Status:      "active",
		},
	}
	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, files, svc.now()))

	connection, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []string{},
		PlanTypes:  []string{},
	})
	require.NoError(t, err)
	require.Equal(t, 2, connection.TotalCount)
	require.Equal(t, "Codex Work", connection.Edges[0].Node.DisplayName)
	require.Equal(t, "work@example.com", connection.Edges[0].Node.Email)
	require.Equal(t, "plus", connection.Edges[0].Node.PlanType)

	stats, err := svc.CredentialStats(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, &CPACredentialStats{Available: 1, Total: 2, Abnormal: 0}, stats)

	search := "WORK@EXAMPLE"
	connection, err = svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Search:     &search,
		Statuses:   []string{},
		PlanTypes:  []string{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, connection.TotalCount)
	require.Equal(t, "codex", connection.Edges[0].Node.Provider)

	codexCredential, err := client.CPACredential.Get(ctx, connection.Edges[0].Node.ID)
	require.NoError(t, err)
	require.NoError(t, client.CPACredential.UpdateOne(codexCredential).
		SetQuotaState(string(objects.CPAQuotaStateError)).
		SetQuotaLastError("quota request failed").
		Exec(ctx))
	stats, err = svc.CredentialStats(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Abnormal)
	require.Equal(t, 1, stats.Available)

	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, files[:1], svc.now()))
	count, err := client.CPACredential.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestCPASyncClearsLegacyOAuthPlanPlaceholder(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_sync_oauth?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := &CPAService{
		AbstractService: &AbstractService{db: client},
		quotaRegistry:   cpaclient.NewQuotaRegistry(),
		now:             func() time.Time { return time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC) },
	}
	instance, err := client.CPAInstance.Create().
		SetName("Legacy CPA").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	// Legacy row polluted by the old account_type fallback.
	credential, err := client.CPACredential.Create().
		SetExternalKey("xai:legacy").
		SetRemoteName("legacy.json").
		SetDisplayName("legacy").
		SetProvider("xai").
		SetStatus("active").
		SetPlanType("oauth").
		SetCpaInstanceID(instance.ID).
		Save(ctx)
	require.NoError(t, err)

	// Sync with an auth file that carries no plan claim must clear the
	// placeholder instead of keeping "oauth" forever.
	files := []cpaclient.AuthFile{
		{
			AuthIndex: "xai:legacy",
			Name:      "legacy.json",
			Type:      "xai",
			Status:    "active",
		},
	}
	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, files, svc.now()))

	updated, err := client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Empty(t, updated.PlanType)
}

func TestDeleteInstanceRemovesCredentials(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_delete?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := &CPAService{
		AbstractService: &AbstractService{db: client},
		quotaRegistry:   cpaclient.NewQuotaRegistry(),
		now:             func() time.Time { return time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC) },
	}
	instance, err := client.CPAInstance.Create().
		SetName("Delete me").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	files := []cpaclient.AuthFile{
		{AuthIndex: "codex-1", Name: "a.json", Type: "codex", Label: "Codex", Email: "codex@example.com", Status: "active"},
		{AuthIndex: "claude-1", Name: "b.json", Type: "claude", Label: "Claude", Email: "claude@example.com", Status: "active"},
	}
	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, files, svc.now()))

	require.NoError(t, svc.DeleteInstance(ctx, instance.ID))

	_, err = client.CPAInstance.Get(ctx, instance.ID)
	require.Error(t, err, "instance should be gone after delete")
	count, err := client.CPACredential.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "credentials must be deleted together with the instance")
}
