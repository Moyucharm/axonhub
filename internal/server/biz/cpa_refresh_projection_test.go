package biz

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPARefreshWorkerConvertsPanicToSingleFailure(t *testing.T) {
	resultCh := make(chan error, 1)
	callbackCount := 0
	runCPARefreshWorker(t.Context(), 7, 11, resultCh, func(failed bool) {
		callbackCount++
		require.True(t, failed)
	}, func() error {
		panic("boom")
	})

	select {
	case refreshErr := <-resultCh:
		require.ErrorContains(t, refreshErr, "CPA credential refresh panicked: boom")
	case <-time.After(time.Second):
		t.Fatal("refresh worker did not publish its failure")
	}
	require.Equal(t, 1, callbackCount)
	select {
	case extra := <-resultCh:
		t.Fatalf("refresh worker published an extra result: %v", extra)
	default:
	}
}

func TestCPARefreshWorkerContainsProgressCallbackPanic(t *testing.T) {
	resultCh := make(chan error, 1)
	require.NotPanics(t, func() {
		runCPARefreshWorker(t.Context(), 7, 11, resultCh, func(bool) {
			panic("progress boom")
		}, func() error { return nil })
	})
	require.NoError(t, <-resultCh)
}

func TestCPARefreshProjectionUsesCurrentCredential(t *testing.T) {
	tests := []struct {
		name           string
		fetchError     bool
		mutate         func(*ent.CPACredentialUpdateOne) *ent.CPACredentialUpdateOne
		wantHealth     objects.CPACredentialHealthState
		wantQuotaState objects.CPAQuotaState
	}{
		{
			name:       "success keeps concurrent disable",
			fetchError: false,
			mutate: func(update *ent.CPACredentialUpdateOne) *ent.CPACredentialUpdateOne {
				return update.SetDisplayName("Current 2").SetStatus("disabled").SetDisabled(true)
			},
			wantHealth:     objects.CPACredentialHealthDisabled,
			wantQuotaState: objects.CPAQuotaStateSuccess,
		},
		{
			name:       "error keeps concurrent unavailable state",
			fetchError: true,
			mutate: func(update *ent.CPACredentialUpdateOne) *ent.CPACredentialUpdateOne {
				return update.SetDisplayName("Current 02 active").SetUnavailable(true)
			},
			wantHealth:     objects.CPACredentialHealthAbnormal,
			wantQuotaState: objects.CPAQuotaStateError,
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", fmt.Sprintf("file:cpa_refresh_projection_%d?mode=memory&_fk=0", index))
			defer client.Close()
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			svc := &CPAService{
				AbstractService: &AbstractService{db: client},
				quotaRegistry:   cpaclient.NewQuotaRegistry(),
				globalQuota:     semaphore.NewWeighted(maxCPAGlobalConcurrency),
				now:             func() time.Time { return now },
			}
			instance := client.CPAInstance.Create().
				SetName("refresh projection").
				SetBaseURL("http://127.0.0.1:8320").
				SetEncryptedSecret("encrypted").
				SaveX(ctx)
			credential := client.CPACredential.Create().
				SetCpaInstanceID(instance.ID).
				SetExternalKey("claude:credential").
				SetAuthIndex("claude-auth").
				SetRemoteName("claude.json").
				SetDisplayName("Stale 10").
				SetDisplayNameSortKey(cpaDisplayNameSortKey("Stale 10")).
				SetDisplayNameSortLength(cpaDisplayNameSortLength("Stale 10")).
				SetProvider("claude").
				SetStatus("active").
				SetQuotaState(string(objects.CPAQuotaStatePending)).
				SetHealthState(string(objects.CPACredentialHealthPending)).
				SetProjectionVersion(currentCPAProjectionVersion).
				SaveX(ctx)

			started := make(chan struct{})
			release := make(chan struct{})
			callCount := 0
			managementClient := &cpaTestManagementClient{callProvider: func(callCtx context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
				callCount++
				if callCount == 1 {
					close(started)
					select {
					case <-release:
					case <-callCtx.Done():
						return nil, callCtx.Err()
					}
					if tt.fetchError {
						return nil, errors.New("quota fetch failed")
					}
					return &cpaclient.ProviderCallResult{
						StatusCode: 200,
						Body:       []byte(`{"five_hour":{"utilization":25}}`),
					}, nil
				}
				return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"account":{"has_claude_pro":true}}`)}, nil
			}}

			errCh := make(chan error, 1)
			go func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						errCh <- fmt.Errorf("refresh goroutine panicked: %v", recovered)
					}
				}()
				errCh <- svc.refreshOneCredential(ctx, managementClient, credential)
			}()

			<-started
			require.NoError(t, tt.mutate(client.CPACredential.UpdateOneID(credential.ID)).Exec(ctx))
			close(release)
			refreshErr := <-errCh
			if tt.fetchError {
				require.ErrorContains(t, refreshErr, "quota fetch failed")
			} else {
				require.NoError(t, refreshErr)
			}

			updated := client.CPACredential.GetX(ctx, credential.ID)
			require.Equal(t, tt.wantHealth, objects.CPACredentialHealthState(updated.HealthState))
			require.Equal(t, tt.wantQuotaState, objects.CPAQuotaState(updated.QuotaState))
			require.Equal(t, cpaDisplayNameSortKey(updated.DisplayName), updated.DisplayNameSortKey)
			require.Equal(t, cpaDisplayNameSortLength(updated.DisplayName), updated.DisplayNameSortLength)
			require.Equal(t, currentCPAProjectionVersion, updated.ProjectionVersion)
		})
	}
}

type cpaTestManagementClient struct {
	callProvider func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error)
}

func (c *cpaTestManagementClient) CloseIdleConnections() {}

func (c *cpaTestManagementClient) ListCredentials(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
	return nil, cpaclient.BuildInfo{}, errors.New("unexpected ListCredentials call")
}

func (c *cpaTestManagementClient) CallProvider(ctx context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
	if c.callProvider == nil {
		return nil, errors.New("unexpected CallProvider call")
	}
	return c.callProvider(ctx, call)
}

func (c *cpaTestManagementClient) ListUsageQueue(context.Context, int) ([]*cpaclient.UsageEvent, error) {
	return nil, errors.New("unexpected ListUsageQueue call")
}

func (c *cpaTestManagementClient) PatchAuthFileStatus(context.Context, string, string, bool) error {
	return errors.New("unexpected PatchAuthFileStatus call")
}
