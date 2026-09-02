package biz

import (
	"context"
	"net/http"
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

func TestCPARefreshLeaseAllowsOneOwnerAcrossServices(t *testing.T) {
	clientA := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease?mode=memory&cache=shared&_fk=1")
	defer clientA.Close()
	clientB := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease?mode=memory&cache=shared&_fk=1")
	defer clientB.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), clientA))
	instance := clientA.CPAInstance.Create().
		SetName("refresh lease").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	credential := clientA.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("credential.json").
		SetDisplayName("credential.json").
		SetProvider("codex").
		SaveX(ctx)

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	serviceA := newCPAServiceForTest(clientA, func() time.Time { return now })
	serviceB := newCPAServiceForTest(clientB, func() time.Time { return now })
	claimA, err := serviceA.repository.claimRefreshLease(ctx, credential.ID, 0, now)
	require.NoError(t, err)
	require.True(t, claimA.claimed)
	ctxB := authz.WithTestBypass(ent.NewContext(t.Context(), clientB))
	claimB, err := serviceB.repository.claimRefreshLease(ctxB, credential.ID, 0, now)
	require.NoError(t, err)
	require.True(t, claimB.waiting)
	require.False(t, claimB.claimed)
	require.Equal(t, claimA.revision, claimB.credential.RefreshRevision)

	outcome := cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionSuccess,
		instanceID:   instance.ID,
		credentialID: credential.ID,
		quotaState:   objects.CPAQuotaStateSuccess,
		planType:     "plus",
		attemptedAt:  now,
		snapshot: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
			ID:    "weekly",
			Label: "7 day",
		}}},
	}
	_, err = serviceA.repository.applyQuotaOutcomeWithLease(ctx, outcome, claimA.token, claimA.revision)
	require.NoError(t, err)
	fresh, err := clientB.CPACredential.Get(ctxB, credential.ID)
	require.NoError(t, err)
	require.Equal(t, claimA.revision+1, fresh.RefreshRevision)
	require.Empty(t, fresh.RefreshLeaseToken)
	require.Nil(t, fresh.RefreshLeaseUntil)

	completed, err := serviceB.repository.claimRefreshLease(ctxB, credential.ID, 0, now)
	require.NoError(t, err)
	require.True(t, completed.completed)
	require.Equal(t, objects.CPAQuotaStateSuccess, objects.CPAQuotaState(completed.credential.QuotaState))
}

func TestCPARefreshLeaseDeduplicatesProviderFetchAcrossServices(t *testing.T) {
	clientA := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_fetch?mode=memory&cache=shared&_fk=1")
	defer clientA.Close()
	clientB := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_fetch?mode=memory&cache=shared&_fk=1")
	defer clientB.Close()
	ctxA := authz.WithTestBypass(ent.NewContext(t.Context(), clientA))
	ctxB := authz.WithTestBypass(ent.NewContext(t.Context(), clientB))
	instance := clientA.CPAInstance.Create().
		SetName("refresh lease fetch").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctxA)
	credential := clientA.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("credential.json").
		SetDisplayName("credential.json").
		SetProvider("codex").
		SaveX(ctxA)

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	serviceA := newCPAServiceForTest(clientA, func() time.Time { return now })
	serviceB := newCPAServiceForTest(clientB, func() time.Time { return now })
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	newClient := func() *cpaTestManagementClient {
		return &cpaTestManagementClient{callProvider: func(ctx context.Context, _ cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
				return &cpaclient.ProviderCallResult{
					StatusCode: 200,
					Headers:    make(http.Header),
					Body:       []byte(`{"rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":60}}}`),
				}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
	}
	clientForA := newClient()
	clientForB := newClient()
	results := make(chan cpaQuotaExecutionOutcome, 2)
	go func() { results <- serviceA.refreshCredentialOutcome(ctxA, clientForA, credential) }()
	go func() { results <- serviceB.refreshCredentialOutcome(ctxB, clientForB, credential) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider fetch did not start")
	}
	close(release)
	first := <-results
	second := <-results
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, cpaQuotaExecutionSuccess, first.status)
	require.Equal(t, cpaQuotaExecutionSuccess, second.status)
	fresh := clientA.CPACredential.GetX(ctxA, credential.ID)
	require.Equal(t, 1, fresh.RefreshRevision)
	require.Empty(t, fresh.RefreshLeaseToken)
}

func TestCPARefreshLeaseExpiredOwnerCanBeTakenOver(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_expiry?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("refresh lease expiry").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("credential.json").
		SetDisplayName("credential.json").
		SetProvider("codex").
		SetRefreshLeaseToken("dead-owner").
		SetRefreshLeaseUntil(now.Add(-time.Minute)).
		SaveX(ctx)

	svc := newCPAServiceForTest(client, func() time.Time { return now })
	claim, err := svc.repository.claimRefreshLease(ctx, credential.ID, 0, now)
	require.NoError(t, err)
	require.True(t, claim.claimed)
	require.NotEqual(t, "dead-owner", claim.token)
	require.Equal(t, 0, claim.revision)
}

func TestCPARefreshLeaseCanceledOwnerReleasesLease(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_cancel?mode=memory&_fk=1")
	defer client.Close()
	baseCtx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("refresh lease cancel").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(baseCtx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("credential.json").
		SetDisplayName("credential.json").
		SetProvider("codex").
		SaveX(baseCtx)
	svc := newCPAServiceForTest(client, time.Now)
	started := make(chan struct{})
	managementClient := &cpaTestManagementClient{callProvider: func(ctx context.Context, _ cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	refreshCtx, cancel := context.WithCancel(baseCtx)
	resultCh := make(chan cpaQuotaExecutionOutcome, 1)
	go func() { resultCh <- svc.refreshCredentialOutcome(refreshCtx, managementClient, credential) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider fetch did not start")
	}
	cancel()
	result := <-resultCh
	require.Equal(t, cpaQuotaExecutionFailure, result.status)
	fresh := client.CPACredential.GetX(baseCtx, credential.ID)
	require.Empty(t, fresh.RefreshLeaseToken)
	require.Nil(t, fresh.RefreshLeaseUntil)
}

func TestCPARefreshLeaseReleaseDoesNotClearNewOwner(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_release?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("refresh lease release").
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
	svc := newCPAServiceForTest(client, time.Now)
	first, err := svc.repository.claimRefreshLease(ctx, credential.ID, 0, svc.now())
	require.NoError(t, err)
	require.True(t, first.claimed)
	secondToken := "new-owner"
	secondUntil := svc.now().Add(time.Minute)
	require.NoError(t, client.CPACredential.UpdateOneID(credential.ID).
		SetRefreshLeaseToken(secondToken).
		SetRefreshLeaseUntil(secondUntil).
		Exec(ctx))
	require.NoError(t, svc.repository.releaseRefreshLease(ctx, credential.ID, first.token))
	fresh := client.CPACredential.GetX(ctx, credential.ID)
	require.Equal(t, secondToken, fresh.RefreshLeaseToken)
	require.NotNil(t, fresh.RefreshLeaseUntil)
}

func TestCPARefreshLeaseLateOwnerCannotOverwriteNewOwner(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_late?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().SetName("refresh lease late").SetBaseURL("http://127.0.0.1:8317").SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).SetExternalKey("credential-1").SetAuthIndex("auth-1").SetRemoteName("credential.json").SetDisplayName("credential.json").SetProvider("codex").SaveX(ctx)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	first, err := svc.repository.claimRefreshLease(ctx, credential.ID, 0, now)
	require.NoError(t, err)
	require.True(t, first.claimed)
	require.NoError(t, client.CPACredential.UpdateOneID(credential.ID).SetRefreshLeaseUntil(now.Add(-time.Second)).Exec(ctx))
	second, err := svc.repository.claimRefreshLease(ctx, credential.ID, 0, now)
	require.NoError(t, err)
	require.True(t, second.claimed)
	require.NotEqual(t, first.token, second.token)

	outcome := cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionSuccess,
		instanceID:   instance.ID,
		credentialID: credential.ID,
		quotaState:   objects.CPAQuotaStateSuccess,
		attemptedAt:  now,
		snapshot:     objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{ID: "weekly"}}},
	}
	_, err = svc.repository.applyQuotaOutcomeWithLease(ctx, outcome, second.token, second.revision)
	require.NoError(t, err)
	_, err = svc.repository.applyQuotaOutcomeWithLease(ctx, outcome, first.token, first.revision)
	require.Error(t, err)
	fresh := client.CPACredential.GetX(ctx, credential.ID)
	require.Equal(t, 1, fresh.RefreshRevision)
	require.Equal(t, objects.CPAQuotaStateSuccess, objects.CPAQuotaState(fresh.QuotaState))
	require.Empty(t, fresh.RefreshLeaseToken)
}

func TestCPARefreshLeaseWaitHonorsCallerCancellation(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_wait?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("refresh lease wait").
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
		SetRefreshLeaseToken("owner").
		SetRefreshLeaseUntil(time.Now().UTC().Add(time.Hour)).
		SaveX(ctx)
	svc := newCPAServiceForTest(client, time.Now)
	waitCtx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	_, err := svc.repository.waitForRefreshRevision(waitCtx, credential.ID, 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCPARefreshLeaseConcurrentClaimsUseSingleOwner(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_refresh_lease_concurrent?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("refresh lease concurrent").
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
	services := []*CPAService{
		newCPAServiceForTest(client, time.Now),
		newCPAServiceForTest(client, time.Now),
	}
	var owners atomic.Int32
	type claimResult struct {
		claim cpaRefreshLeaseClaim
		err   error
	}
	results := make(chan claimResult, len(services))
	for _, svc := range services {
		go func(svc *CPAService) {
			claim, err := svc.repository.claimRefreshLease(ctx, credential.ID, 0, svc.now())
			results <- claimResult{claim: claim, err: err}
		}(svc)
	}
	for range services {
		result := <-results
		require.NoError(t, result.err)
		if result.claim.claimed {
			owners.Add(1)
		}
	}
	require.Equal(t, int32(1), owners.Load())
}
