package biz

import (
	"context"
	"errors"
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

func TestDeriveCPAExpired(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		credential *ent.CPACredential
		want       bool
	}{
		{
			name: "successful quota refresh overrides stale subscription end",
			credential: &ent.CPACredential{
				QuotaState:   string(objects.CPAQuotaStateSuccess),
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-06-10T07:20:05+00:00"},
			},
			want: false,
		},
		{
			name:       "empty subscription end and active status",
			credential: &ent.CPACredential{Status: "active"},
			want:       false,
		},
		{
			name: "date-only subscription end passed",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-08-20"},
			},
			want: true,
		},
		{
			name: "date-only subscription end still active through the last day",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-08-21"},
			},
			want: false,
		},
		{
			name: "rfc3339 subscription end passed",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-08-01T00:00:00Z"},
			},
			want: true,
		},
		{
			name: "unix seconds subscription end future",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2900000000"},
			},
			want: false,
		},
		{
			name:       "invalid subscription end ignored",
			credential: &ent.CPACredential{QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "not-a-date"}},
			want:       false,
		},
		{
			name:       "error status with unauthorized message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "unauthorized"},
			want:       true,
		},
		{
			name:       "error status with payment required message",
			credential: &ent.CPACredential{Status: "ERROR", StatusMessage: "payment_required"},
			want:       true,
		},
		{
			name:       "error status with forbidden message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "Forbidden"},
			want:       true,
		},
		{
			name:       "error status with not found message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "not_found"},
			want:       true,
		},
		{
			name:       "error status with unrelated message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "transient upstream error"},
			want:       false,
		},
		{
			name:       "active status with unauthorized message",
			credential: &ent.CPACredential{Status: "active", StatusMessage: "unauthorized"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveCPAExpired(tt.credential, now); got != tt.want {
				t.Fatalf("deriveCPAExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCPACredentialRecovered(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		credential *ent.CPACredential
		want       bool
	}{
		{
			name: "success state without exhausted windows recovers",
			credential: &ent.CPACredential{
				QuotaState: string(objects.CPAQuotaStateSuccess),
				QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
					{ID: "w1", UsedPercent: floatPtr(50)},
				}},
			},
			want: true,
		},
		{
			name:       "unsupported state counts as usable",
			credential: &ent.CPACredential{QuotaState: string(objects.CPAQuotaStateUnsupported)},
			want:       true,
		},
		{
			name: "exhausted window blocks recovery",
			credential: &ent.CPACredential{
				QuotaState: string(objects.CPAQuotaStateSuccess),
				QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
					{ID: "w1", UsedPercent: floatPtr(100)},
				}},
			},
			want: false,
		},
		{
			// 0/0 quota means a usable credential, never an exhausted one.
			name: "zero limit quota recovers",
			credential: &ent.CPACredential{
				QuotaState: string(objects.CPAQuotaStateSuccess),
				QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
					{ID: "w1", Limit: floatPtr(0), Remaining: floatPtr(0)},
				}},
			},
			want: true,
		},
		{
			name:       "error state keeps credential disabled",
			credential: &ent.CPACredential{QuotaState: string(objects.CPAQuotaStateError)},
			want:       false,
		},
		{
			name:       "pending state keeps credential disabled",
			credential: &ent.CPACredential{QuotaState: string(objects.CPAQuotaStatePending)},
			want:       false,
		},
		{
			// A successful refresh proves the credential is alive even when the
			// cached JWT subscription date has passed.
			name: "stale subscription end with successful quota recovers",
			credential: &ent.CPACredential{
				QuotaState:   string(objects.CPAQuotaStateSuccess),
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-01-01"},
			},
			want: true,
		},
		{
			name: "failed quota with passed subscription end does not recover",
			credential: &ent.CPACredential{
				QuotaState:   string(objects.CPAQuotaStateError),
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-01-01"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpaCredentialRecovered(tt.credential, now); got != tt.want {
				t.Fatalf("cpaCredentialRecovered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCPAEnabledPatrolPatchesAndSyncsWithOneClient(t *testing.T) {
	var openCalls atomic.Int32
	var closeCalls atomic.Int32
	var listCalls atomic.Int32
	var patchCalls atomic.Int32
	managementClient := &cpaTestManagementClient{
		callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"limits":[{"name":"Weekly","detail":{"used":100,"limit":100,"remaining":0}}]}`)}, nil
		},
		patchAuthFileStatus: func(_ context.Context, name, authIndex string, disabled bool) error {
			patchCalls.Add(1)
			require.Equal(t, "kimi.json", name)
			require.Equal(t, "kimi-auth", authIndex)
			require.True(t, disabled)
			return nil
		},
		listCredentials: func(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
			listCalls.Add(1)
			return patrolAuthFiles(true), cpaclient.BuildInfo{}, nil
		},
		closeIdle: func() { closeCalls.Add(1) },
	}
	ctx, svc, instance, credential := setupCPAPatrolTest(t, false, managementClient, &openCalls)

	svc.patrolInstanceEnabled(ctx, instance)

	require.Equal(t, int32(1), openCalls.Load())
	require.Equal(t, int32(1), closeCalls.Load())
	require.Equal(t, int32(1), patchCalls.Load())
	require.Equal(t, int32(1), listCalls.Load())
	updated := svc.entFromContext(ctx).CPACredential.GetX(ctx, credential.ID)
	require.True(t, updated.Disabled)
}

func TestCPAEnabledPatrolPatchFailureKeepsLocalState(t *testing.T) {
	var openCalls atomic.Int32
	var listCalls atomic.Int32
	managementClient := &cpaTestManagementClient{
		callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"limits":[{"name":"Weekly","detail":{"used":100,"limit":100,"remaining":0}}]}`)}, nil
		},
		patchAuthFileStatus: func(context.Context, string, string, bool) error {
			return errors.New("remote patch failed")
		},
		listCredentials: func(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
			listCalls.Add(1)
			return patrolAuthFiles(true), cpaclient.BuildInfo{}, nil
		},
	}
	ctx, svc, instance, credential := setupCPAPatrolTest(t, false, managementClient, &openCalls)

	svc.patrolInstanceEnabled(ctx, instance)

	require.Equal(t, int32(1), openCalls.Load())
	require.Zero(t, listCalls.Load())
	updated := svc.entFromContext(ctx).CPACredential.GetX(ctx, credential.ID)
	require.False(t, updated.Disabled)
}

func TestCPADisabledPatrolRecoversAndSyncsWithOneClient(t *testing.T) {
	var openCalls atomic.Int32
	var closeCalls atomic.Int32
	var listCalls atomic.Int32
	var patchCalls atomic.Int32
	managementClient := &cpaTestManagementClient{
		callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"limits":[{"name":"Weekly","detail":{"used":20,"limit":100,"remaining":80}}]}`)}, nil
		},
		patchAuthFileStatus: func(_ context.Context, name, authIndex string, disabled bool) error {
			patchCalls.Add(1)
			require.Equal(t, "kimi.json", name)
			require.Equal(t, "kimi-auth", authIndex)
			require.False(t, disabled)
			return nil
		},
		listCredentials: func(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
			listCalls.Add(1)
			return patrolAuthFiles(false), cpaclient.BuildInfo{}, nil
		},
		closeIdle: func() { closeCalls.Add(1) },
	}
	ctx, svc, instance, credential := setupCPAPatrolTest(t, true, managementClient, &openCalls)

	svc.patrolInstanceDisabled(ctx, instance)

	require.Equal(t, int32(1), openCalls.Load())
	require.Equal(t, int32(1), closeCalls.Load())
	require.Equal(t, int32(1), patchCalls.Load())
	require.Equal(t, int32(1), listCalls.Load())
	updated := svc.entFromContext(ctx).CPACredential.GetX(ctx, credential.ID)
	require.False(t, updated.Disabled)
}

func setupCPAPatrolTest(
	t *testing.T,
	disabled bool,
	managementClient cpaclient.ManagementClient,
	openCalls *atomic.Int32,
) (context.Context, *CPAService, *ent.CPAInstance, *ent.CPACredential) {
	t.Helper()
	client := enttest.NewEntClient(t, "sqlite3", "file:"+t.Name()+"?mode=memory&_fk=0")
	t.Cleanup(func() { _ = client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) {
		openCalls.Add(1)
		return managementClient, nil
	}
	instance := client.CPAInstance.Create().
		SetName("patrol").
		SetBaseURL("http://127.0.0.1:8323").
		SetEncryptedSecret("encrypted").
		SetEnabled(true).
		SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("kimi:kimi.json").
		SetAuthIndex("kimi-auth").
		SetRemoteName("kimi.json").
		SetDisplayName("Kimi").
		SetProvider("kimi").
		SetStatus("active").
		SetDisabled(disabled).
		SetQuotaState(string(objects.CPAQuotaStatePending)).
		SetHealthState(string(objects.CPACredentialHealthPending)).
		SaveX(ctx)
	return ctx, svc, instance, credential
}

func patrolAuthFiles(disabled bool) *cpaclient.AuthFilesResponse {
	return &cpaclient.AuthFilesResponse{Files: []cpaclient.AuthFile{{
		AuthIndex: "kimi-auth",
		Name:      "kimi.json",
		Type:      "kimi",
		Status:    "active",
		Disabled:  disabled,
	}}}
}

func floatPtr(value float64) *float64 {
	return &value
}
