package biz

import (
	"context"
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

	svc := newCPAServiceForTest(client, func() time.Time {
		return time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	})
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
		Statuses:   []CPACredentialFilterStatus{},
		PlanTypes:  []string{},
	})
	require.NoError(t, err)
	require.Equal(t, 2, connection.TotalCount)
	require.Equal(t, "Codex Work", connection.Edges[0].Node.DisplayName)
	require.Equal(t, "work@example.com", connection.Edges[0].Node.Email)
	require.Equal(t, "plus", connection.Edges[0].Node.PlanType)

	overview, err := svc.Overview(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, &CPACredentialStats{Available: 1, Total: 2, Abnormal: 0}, overview.Stats)

	search := "WORK@EXAMPLE"
	connection, err = svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Search:     &search,
		Statuses:   []CPACredentialFilterStatus{},
		PlanTypes:  []string{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, connection.TotalCount)
	require.Equal(t, "codex", connection.Edges[0].Node.Provider)

	codexCredential, err := client.CPACredential.Get(ctx, connection.Edges[0].Node.ID)
	require.NoError(t, err)
	require.NoError(t, client.CPACredential.UpdateOne(codexCredential).
		SetQuotaState(string(objects.CPAQuotaStateError)).
		SetHealthState(string(objects.CPACredentialHealthAbnormal)).
		SetQuotaLastError("quota request failed").
		Exec(ctx))
	overview, err = svc.Overview(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, 1, overview.Stats.Abnormal)
	require.Equal(t, 1, overview.Stats.Available)

	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, files[:1], svc.now()))
	count, err := client.CPACredential.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestCPASyncRemapsSwappedAuthIndexesWithoutMergingHistory(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_sync_swap?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, nil)
	instance, err := client.CPAInstance.Create().
		SetName("swap").SetBaseURL("http://127.0.0.1:8317").SetEncryptedSecret("encrypted").Save(ctx)
	require.NoError(t, err)

	initial := []cpaclient.AuthFile{
		{AuthIndex: "A", Name: "a.json", Type: "codex", Status: "active"},
		{AuthIndex: "B", Name: "b.json", Type: "codex", Status: "active"},
	}
	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, initial, time.Now()))
	for _, event := range []struct{ index, model string }{{"A", "from-a"}, {"B", "from-b"}} {
		require.NoError(t, client.CpaUsageEvent.Create().
			SetCpaInstanceID(instance.ID).SetAuthIndex(event.index).SetModel(event.model).SetRequestedAt(time.Now()).Exec(ctx))
	}

	swapped := []cpaclient.AuthFile{
		{AuthIndex: "B", Name: "a.json", Type: "codex", Status: "active"},
		{AuthIndex: "A", Name: "b.json", Type: "codex", Status: "active"},
	}
	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, swapped, time.Now()))
	events, err := client.CpaUsageEvent.Query().All(ctx)
	require.NoError(t, err)
	byModel := make(map[string]string, len(events))
	for _, event := range events {
		byModel[event.Model] = event.AuthIndex
	}
	require.Equal(t, map[string]string{"from-a": "B", "from-b": "A"}, byModel)

	chained := []cpaclient.AuthFile{
		{AuthIndex: "C", Name: "a.json", Type: "codex", Status: "active"},
		{AuthIndex: "B", Name: "b.json", Type: "codex", Status: "active"},
	}
	require.NoError(t, svc.syncCredentialSnapshot(ctx, instance, chained, time.Now()))
	events, err = client.CpaUsageEvent.Query().All(ctx)
	require.NoError(t, err)
	byModel = make(map[string]string, len(events))
	for _, event := range events {
		byModel[event.Model] = event.AuthIndex
	}
	require.Equal(t, map[string]string{"from-a": "C", "from-b": "B"}, byModel)
}

func TestCPASyncRejectsDuplicateTargetAuthIndex(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_sync_duplicate_auth?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, nil)
	instance, err := client.CPAInstance.Create().
		SetName("duplicate").SetBaseURL("http://127.0.0.1:8318").SetEncryptedSecret("encrypted").Save(ctx)
	require.NoError(t, err)

	err = svc.syncCredentialSnapshot(ctx, instance, []cpaclient.AuthFile{
		{AuthIndex: "same", Name: "a.json", Type: "codex", Status: "active"},
		{AuthIndex: "same", Name: "b.json", Type: "codex", Status: "active"},
	}, time.Now())
	require.ErrorContains(t, err, "duplicate CPA auth index")
	count, countErr := client.CPACredential.Query().Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, count)
}

func TestCPASyncClearsLegacyOAuthPlanPlaceholder(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_sync_oauth?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := newCPAServiceForTest(client, func() time.Time {
		return time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	})
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

func TestDeleteInstanceWaitsForCollectorDrainAndBlocksReconcile(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_delete_collector?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := &CPAService{
		AbstractService:       &AbstractService{db: client},
		usageCollectorStarted: true,
		usageCollectors:       make(map[int]*usageCollectorWorker),
	}
	instance, err := client.CPAInstance.Create().
		SetName("Delete collector").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SetUsageStreamEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	collectorCtx, cancel := context.WithCancel(context.Background())
	worker := &usageCollectorWorker{
		target:  usageCollectorTarget{instanceID: instance.ID},
		session: &usageCollectorSession{id: "delete-session"},
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	svc.usageCollectors[instance.ID] = worker
	collectorCanceled := make(chan struct{})
	releaseDrain := make(chan struct{})
	drainErr := make(chan error, 1)
	go func() {
		defer close(worker.done)
		<-collectorCtx.Done()
		close(collectorCanceled)
		<-releaseDrain
		_, insertErr := client.CpaUsageEvent.Create().
			SetCpaInstanceID(instance.ID).
			SetAuthIndex("auth-1").
			SetProvider("codex").
			SetModel("gpt-5.2").
			SetRequestedAt(time.Now().UTC()).
			Save(ctx)
		drainErr <- insertErr
	}()

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- svc.DeleteInstance(ctx, instance.ID) }()
	select {
	case <-collectorCanceled:
	case <-time.After(time.Second):
		t.Fatal("delete did not cancel the instance collector")
	}

	reconcileStarted := make(chan struct{})
	reconcileDone := make(chan error, 1)
	go func() {
		close(reconcileStarted)
		reconcileDone <- svc.RefreshUsageStreams(ctx)
	}()
	<-reconcileStarted
	select {
	case err := <-reconcileDone:
		t.Fatalf("reconcile completed before delete released lifecycle lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseDrain)
	require.NoError(t, <-drainErr)
	require.NoError(t, <-deleteDone)
	require.NoError(t, <-reconcileDone)

	usageCount, err := client.CpaUsageEvent.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, usageCount, "drained usage rows must be deleted before DeleteInstance returns")
	svc.usageCollectorMu.Lock()
	_, collectorExists := svc.usageCollectors[instance.ID]
	svc.usageCollectorMu.Unlock()
	require.False(t, collectorExists, "stale reconcile must not recreate the deleted collector")
}

func TestDeleteInstanceRemovesCredentials(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_delete?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := newCPAServiceForTest(client, func() time.Time {
		return time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	})
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
