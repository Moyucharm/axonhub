package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/hook"
	"github.com/looplj/axonhub/internal/ent/intercept"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

const cpaCooldownTestSecret = "cooldown-management-secret-do-not-expose"

type cpaCooldownResetRequest struct {
	authIndex string
	api       cpaclient.ManagementAPI
}

type cpaCooldownTestFixture struct {
	t          *testing.T
	client     *ent.Client
	svc        *CPAService
	ctx        context.Context
	seedCtx    context.Context
	now        time.Time
	instance   *ent.CPAInstance
	credential *ent.CPACredential
	management *cpaTestManagementClient
	file       cpaclient.AuthFile
	api        cpaclient.ManagementAPI
	list       func(context.Context, int) (*cpaclient.AuthFilesResponse, error)
	reset      func(context.Context, string, cpaclient.ManagementAPI) error
	lists      atomic.Int32
	opens      atomic.Int32
	providers  atomic.Int32
	patches    atomic.Int32
	requestMu  sync.Mutex
	requests   []cpaCooldownResetRequest
}

func newCPACooldownTestFixture(t *testing.T) *cpaCooldownTestFixture {
	t.Helper()
	client := enttest.NewEntClient(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	seedCtx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	ctx := cpaCooldownUserContext(t.Context(), client, []string{string(scopes.ScopeReadSettings), string(scopes.ScopeWriteSettings)})
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f := &cpaCooldownTestFixture{
		t: t, client: client, ctx: ctx, seedCtx: seedCtx, now: now,
		svc: newCPAServiceForTest(client, func() time.Time { return now }),
		api: cpaclient.ManagementAPIV8,
	}
	f.instance = client.CPAInstance.Create().
		SetName("cooldown target").SetBaseURL("http://127.0.0.1:8330").
		SetEncryptedSecret("encrypted-" + cpaCooldownTestSecret).SetEnabled(true).
		SetAutoRefreshEnabled(false).SetAutoManageEnabled(false).SetAutoResetEnabled(false).SetUsageStreamEnabled(false).
		SaveX(seedCtx)
	f.file = cpaclient.AuthFile{
		Name: "shared.json", AuthIndex: "target-auth", Provider: "claude", Status: "active",
		StatusMessage:      "Authorization: Bearer " + cpaCooldownTestSecret,
		CooldownObservedAt: &f.now,
		Cooldowns: cpaCooldownTestRaw(t, objects.CPARoutingCooldown{
			Scope: objects.CPARoutingCooldownScopeCredential, Reason: objects.CPARoutingCooldownReasonQuota,
			RetryAt: now.Add(time.Hour),
		}),
	}
	require.NoError(t, f.svc.syncCredentialSnapshot(seedCtx, f.instance, []cpaclient.AuthFile{f.file}, now))
	f.credential = client.CPACredential.Query().OnlyX(seedCtx)
	quota := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
		ID: "weekly", Label: "7 day", UsedPercent: lo.ToPtr(100.0), ResetAt: lo.ToPtr(now.Add(7 * 24 * time.Hour)),
	}}}
	projection := projectCPACredential(f.credential.DisplayName, f.credential.Status, false, false, objects.CPAQuotaStateSuccess, quota, now)
	f.credential = client.CPACredential.UpdateOne(f.credential).
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).SetQuotaData(quota).
		SetHealthState(string(projection.healthState)).SetQuotaCooling(projection.quotaCooling).
		SetNillableQuotaCooldownUntil(projection.quotaCooldownUntil).SaveX(seedCtx)
	f.management = &cpaTestManagementClient{
		listCredentials: func(ctx context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
			call := int(f.lists.Add(1))
			if f.list != nil {
				response, err := f.list(ctx, call)
				return response, cpaclient.BuildInfo{}, err
			}
			return f.response(f.file), cpaclient.BuildInfo{}, nil
		},
		resetCooldown: func(ctx context.Context, authIndex string, api cpaclient.ManagementAPI) error {
			f.requestMu.Lock()
			f.requests = append(f.requests, cpaCooldownResetRequest{authIndex: authIndex, api: api})
			f.requestMu.Unlock()
			if f.reset != nil {
				return f.reset(ctx, authIndex, api)
			}
			f.file.Cooldowns = json.RawMessage(`[]`)
			return nil
		},
		callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
			f.providers.Add(1)
			return nil, errors.New("cooldown reset must not call the provider")
		},
		patchAuthFileStatus: func(context.Context, string, string, bool) error {
			f.patches.Add(1)
			return errors.New("cooldown reset must not enable or disable credentials")
		},
	}
	f.svc.connections.openInstanceClient = func(_ context.Context, instance *ent.CPAInstance) (cpaclient.ManagementClient, error) {
		f.opens.Add(1)
		if instance.ID != f.instance.ID {
			return nil, errors.New("cooldown reset selected the wrong instance")
		}
		return f.management, nil
	}
	t.Cleanup(func() {
		require.Zero(t, f.providers.Load(), "cooldown reset must not consume upstream quota or reset cards")
		require.Zero(t, f.patches.Load(), "cooldown reset must not toggle credentials")
	})
	return f
}

func cpaCooldownUserContext(ctx context.Context, client *ent.Client, granted []string) context.Context {
	user := &ent.User{ID: 42, Scopes: granted}
	return contexts.WithUser(authz.NewUserContext(ent.NewContext(ctx, client), user.ID), user)
}

func cpaCooldownTestRaw(t *testing.T, items ...objects.CPARoutingCooldown) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	return raw
}

func (f *cpaCooldownTestFixture) response(files ...cpaclient.AuthFile) *cpaclient.AuthFilesResponse {
	return &cpaclient.AuthFilesResponse{Files: files, ObservedAt: &f.now, API: f.api}
}

func (f *cpaCooldownTestFixture) resetRequests() []cpaCooldownResetRequest {
	f.requestMu.Lock()
	defer f.requestMu.Unlock()
	return append([]cpaCooldownResetRequest(nil), f.requests...)
}

func (f *cpaCooldownTestFixture) assertSafe(outcome *CPACredentialCooldownResetOutcome, err error) {
	f.t.Helper()
	if err != nil {
		require.NotContains(f.t, err.Error(), cpaCooldownTestSecret)
	}
	raw, marshalErr := json.Marshal(outcome)
	require.NoError(f.t, marshalErr)
	require.NotContains(f.t, string(raw), cpaCooldownTestSecret)
	require.NotContains(f.t, string(raw), "encrypted-")
}

func TestCPACooldownResetClearsOnlyTargetAndPreservesQuotaAndState(t *testing.T) {
	for _, tt := range []struct {
		name     string
		scope    objects.CPARoutingCooldownScope
		api      cpaclient.ManagementAPI
		disabled bool
		expired  bool
	}{
		{name: "credential v8", scope: objects.CPARoutingCooldownScopeCredential, api: cpaclient.ManagementAPIV8},
		{name: "model v8", scope: objects.CPARoutingCooldownScopeModel, api: cpaclient.ManagementAPIV8},
		{name: "disabled v0", scope: objects.CPARoutingCooldownScopeCredential, api: cpaclient.ManagementAPIV0, disabled: true},
		{name: "expired model v0", scope: objects.CPARoutingCooldownScopeModel, api: cpaclient.ManagementAPIV0, expired: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			f.api = tt.api
			f.file.Cooldowns = cpaCooldownTestRaw(t, objects.CPARoutingCooldown{
				Scope: tt.scope, ModelKey: "claude-model", Reason: objects.CPARoutingCooldownReasonQuota, RetryAt: f.now.Add(time.Hour),
			})
			f.file.Disabled = tt.disabled
			if tt.disabled {
				f.file.Status = "disabled"
			}
			if tt.expired {
				f.file.Status, f.file.StatusMessage = "error", "unauthorized"
				f.credential = f.client.CPACredential.UpdateOne(f.credential).
					SetQuotaState(string(objects.CPAQuotaStateError)).SetQuotaLastError("provider quota request returned HTTP 401 (unauthorized)").SaveX(f.seedCtx)
			}
			peer := f.file
			peer.Name, peer.AuthIndex, peer.Disabled, peer.Status = "peer.json", "same-instance-peer", false, "active"
			require.NoError(t, f.svc.syncCredentialSnapshot(f.seedCtx, f.instance, []cpaclient.AuthFile{f.file, peer}, f.now))
			otherInstance := f.client.CPAInstance.Create().SetName("other instance").
				SetBaseURL("http://127.0.0.1:8331").SetEncryptedSecret("other-encrypted").SaveX(f.seedCtx)
			otherFile := f.file
			otherFile.AuthIndex = "other-instance-same-name"
			require.NoError(t, f.svc.syncCredentialSnapshot(f.seedCtx, otherInstance, []cpaclient.AuthFile{otherFile}, f.now))
			before := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			allBefore := f.client.CPACredential.Query().AllX(f.seedCtx)
			f.list = func(context.Context, int) (*cpaclient.AuthFilesResponse, error) {
				return f.response(f.file, peer), nil
			}

			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.NoError(t, err)
			require.NotNil(t, outcome)
			require.True(t, outcome.Applied)
			require.Nil(t, outcome.RefreshError)
			require.NotNil(t, outcome.Credential)
			require.Equal(t, f.credential.ID, outcome.Credential.ID)
			require.Equal(t, f.instance.ID, outcome.Credential.InstanceID)
			require.True(t, outcome.Credential.RoutingCooldown.Known)
			require.Empty(t, outcome.Credential.RoutingCooldown.Items)
			require.False(t, outcome.Credential.CanResetCooldown)
			require.Equal(t, tt.disabled, outcome.Credential.Disabled)
			require.Equal(t, tt.expired, outcome.Credential.Expired)
			require.True(t, outcome.Credential.Cooling, "clearing CPA routing records must not erase exhausted quota")
			require.Equal(t, []cpaCooldownResetRequest{{authIndex: "target-auth", api: tt.api}}, f.resetRequests())
			after := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			require.Equal(t, before.QuotaData, after.QuotaData)
			require.Equal(t, before.QuotaState, after.QuotaState)
			require.Equal(t, before.QuotaLastError, after.QuotaLastError)
			for _, previous := range allBefore {
				if previous.ID != f.credential.ID {
					current := f.client.CPACredential.GetX(f.seedCtx, previous.ID)
					require.Equal(t, previous.RoutingCooldown, current.RoutingCooldown)
					require.Equal(t, previous.Disabled, current.Disabled)
					require.Equal(t, previous.QuotaData, current.QuotaData)
				}
			}
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetRejectsUnreliableIdentityWithoutPost(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*cpaCooldownTestFixture) []cpaclient.AuthFile
		want   string
	}{
		{name: "disappeared", change: func(*cpaCooldownTestFixture) []cpaclient.AuthFile { return nil }, want: "changed or disappeared"},
		{name: "auth index changed", change: func(f *cpaCooldownTestFixture) []cpaclient.AuthFile {
			file := f.file
			file.AuthIndex = "replacement-auth"
			return []cpaclient.AuthFile{file}
		}, want: "changed or disappeared"},
		{name: "provider changed", change: func(f *cpaCooldownTestFixture) []cpaclient.AuthFile {
			file := f.file
			file.Provider = "codex"
			return []cpaclient.AuthFile{file}
		}, want: "changed or disappeared"},
		{name: "remote name changed", change: func(f *cpaCooldownTestFixture) []cpaclient.AuthFile {
			file := f.file
			file.Name = "replacement.json"
			return []cpaclient.AuthFile{file}
		}, want: "changed or disappeared"},
		{name: "ambiguous auth index", change: func(f *cpaCooldownTestFixture) []cpaclient.AuthFile {
			other := f.file
			other.Name = "ambiguous.json"
			return []cpaclient.AuthFile{f.file, other}
		}, want: "ambiguous"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			files := tt.change(f)
			f.list = func(context.Context, int) (*cpaclient.AuthFilesResponse, error) { return f.response(files...), nil }
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.ErrorContains(t, err, tt.want)
			require.ErrorContains(t, err, "refresh credentials first")
			require.Nil(t, outcome)
			require.Empty(t, f.resetRequests())
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetRejectsUnknownSnapshotWithoutInferringFromQuota(t *testing.T) {
	for _, raw := range []string{"", "null", `{}`, `[{"scope":"model","retry_at":"2027-01-01T00:00:00Z"}]`} {
		t.Run(fmt.Sprintf("snapshot_%q", raw), func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			f.file.Cooldowns = json.RawMessage(raw)
			f.file.Unavailable = true
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.EqualError(t, err, "CPA does not provide a reliable cooldown snapshot; update CPA or use its management console")
			require.Nil(t, outcome)
			require.Empty(t, f.resetRequests())
			stored := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			require.True(t, stored.QuotaCooling)
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetFreshEmptyOrExpiredForcesSyncWithoutPost(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  func(*cpaCooldownTestFixture) json.RawMessage
	}{
		{name: "empty", raw: func(*cpaCooldownTestFixture) json.RawMessage { return json.RawMessage(`[]`) }},
		{name: "expired", raw: func(f *cpaCooldownTestFixture) json.RawMessage {
			return cpaCooldownTestRaw(t, objects.CPARoutingCooldown{Scope: objects.CPARoutingCooldownScopeCredential, RetryAt: f.now.Add(-time.Second)})
		}},
		{name: "expires exactly now", raw: func(f *cpaCooldownTestFixture) json.RawMessage {
			return cpaCooldownTestRaw(t, objects.CPARoutingCooldown{Scope: objects.CPARoutingCooldownScopeModel, ModelKey: "model", RetryAt: f.now})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			f.file.Cooldowns = tt.raw(f)
			f.list = func(_ context.Context, call int) (*cpaclient.AuthFilesResponse, error) {
				file := f.file
				if call > 1 {
					file.Label = "freshly synchronized label"
				}
				return f.response(file), nil
			}
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.NoError(t, err)
			require.NotNil(t, outcome)
			require.False(t, outcome.Applied)
			require.Nil(t, outcome.RefreshError)
			require.NotNil(t, outcome.Credential)
			require.Equal(t, "freshly synchronized label", outcome.Credential.DisplayName)
			require.True(t, outcome.Credential.RoutingCooldown.Known)
			require.Empty(t, outcome.Credential.RoutingCooldown.Items)
			require.True(t, outcome.Credential.Cooling)
			require.Empty(t, f.resetRequests())
			require.Equal(t, "freshly synchronized label", f.client.CPACredential.GetX(f.seedCtx, f.credential.ID).DisplayName)
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetLocalRejectionsNeverContactCPA(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*cpaCooldownTestFixture)
		want   string
	}{
		{name: "disabled instance", mutate: func(f *cpaCooldownTestFixture) {
			f.client.CPAInstance.UpdateOne(f.instance).SetEnabled(false).ExecX(f.seedCtx)
		}, want: "CPA instance is disabled"},
		{name: "missing auth index", mutate: func(f *cpaCooldownTestFixture) {
			f.client.CPACredential.UpdateOne(f.credential).SetAuthIndex(" ").ExecX(f.seedCtx)
		}, want: "CPA credential auth index is missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			tt.mutate(f)
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.EqualError(t, err, tt.want)
			require.Nil(t, outcome)
			require.Zero(t, f.opens.Load())
			require.Zero(t, f.lists.Load())
			require.Empty(t, f.resetRequests())
		})
	}
	for _, tt := range []struct {
		name   string
		scopes []string
	}{
		{name: "no scopes"},
		{name: "read settings only", scopes: []string{string(scopes.ScopeReadSettings)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			ctx := cpaCooldownUserContext(t.Context(), f.client, tt.scopes)
			outcome, err := f.svc.ResetCredentialCooldown(ctx, f.credential.ID)
			require.EqualError(t, err, "permission denied: requires write:settings scope")
			require.Nil(t, outcome)
			require.Zero(t, f.opens.Load())
			require.Zero(t, f.lists.Load())
			require.Empty(t, f.resetRequests())
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetWriteFailuresAreClassifiedAndNeverRetried(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{name: "unauthorized", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusUnauthorized}, want: "management authentication failed"},
		{name: "forbidden", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusForbidden}, want: "management authentication failed"},
		{name: "credential missing", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusNotFound, Reason: "auth not found"}, want: "credential not found"},
		{name: "route missing", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusNotFound}, want: "cooldown reset is unsupported"},
		{name: "method unsupported", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusMethodNotAllowed}, want: "cooldown reset is unsupported"},
		{name: "503", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusServiceUnavailable}, want: "management service is unavailable"},
		{name: "500", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusInternalServerError}, want: "service error"},
		{name: "disconnected", err: fmt.Errorf("connection interrupted: %w; Authorization: Bearer %s", io.ErrUnexpectedEOF, cpaCooldownTestSecret), want: "refresh state before retrying"},
		{name: "timeout", err: context.DeadlineExceeded, want: "refresh state before retrying"},
		{name: "invalid response", err: errors.New("CPA cooldown reset response is invalid; refresh state before retrying"), want: "CPA cooldown reset response is invalid; refresh state before retrying"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			before := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			f.reset = func(context.Context, string, cpaclient.ManagementAPI) error { return tt.err }
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.ErrorContains(t, err, tt.want)
			require.Nil(t, outcome)
			require.Len(t, f.resetRequests(), 1)
			after := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			require.Equal(t, before.RoutingCooldown, after.RoutingCooldown)
			require.Equal(t, before.QuotaData, after.QuotaData)
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetAcceptedButRefreshFailuresReturnPartialSuccess(t *testing.T) {
	for _, stage := range []string{"list", "persistence", "instance reload", "view"} {
		t.Run(stage, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			failure := fmt.Errorf("injected %s failure; Authorization: Bearer %s", stage, cpaCooldownTestSecret)
			var armed atomic.Bool
			switch stage {
			case "list":
				f.list = func(_ context.Context, call int) (*cpaclient.AuthFilesResponse, error) {
					if call > 1 {
						return nil, failure
					}
					return f.response(f.file), nil
				}
			case "persistence":
				f.client.CPACredential.Use(func(next ent.Mutator) ent.Mutator {
					return hook.CPACredentialFunc(func(ctx context.Context, mutation *ent.CPACredentialMutation) (ent.Value, error) {
						if armed.Load() && mutation.Op().Is(ent.OpUpdateOne|ent.OpUpdate) {
							return nil, failure
						}
						return next.Mutate(ctx, mutation)
					})
				})
				armed.Store(true)
			case "instance reload":
				f.client.CPAInstance.Use(func(next ent.Mutator) ent.Mutator {
					return hook.CPAInstanceFunc(func(ctx context.Context, mutation *ent.CPAInstanceMutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						if _, synced := mutation.LastSyncSuccessAt(); err == nil && synced {
							armed.Store(true)
						}
						return value, err
					})
				})
				f.client.CPAInstance.Intercept(intercept.Func(func(context.Context, intercept.Query) error {
					if armed.Load() {
						return failure
					}
					return nil
				}))
			case "view":
				quota := f.credential.QuotaData
				quota.ResetCredits = []objects.CPAQuotaResetCredit{{ID: "untouched-reset-card"}}
				f.client.CPACredential.UpdateOne(f.credential).SetQuotaData(quota).ExecX(f.seedCtx)
				f.client.CPAResetAttempt.Intercept(intercept.Func(func(context.Context, intercept.Query) error { return failure }))
			}
			before := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			armed.Store(false)
			require.NoError(t, err, "an accepted remote write must not be reported as an unapplied GraphQL error")
			require.NotNil(t, outcome)
			require.True(t, outcome.Applied)
			require.Nil(t, outcome.Credential)
			require.NotNil(t, outcome.RefreshError)
			require.Contains(t, *outcome.RefreshError, "state refresh failed")
			require.Contains(t, *outcome.RefreshError, "injected "+stage+" failure")
			require.Len(t, f.resetRequests(), 1)
			if stage == "list" || stage == "persistence" {
				after := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
				require.Equal(t, before.RoutingCooldown, after.RoutingCooldown, "failed sync must not fabricate an empty local snapshot")
				require.Equal(t, before.QuotaData, after.QuotaData)
			}
			f.assertSafe(outcome, err)
		})
	}
}

func TestCPACooldownResetNoopRefreshFailureIsAnError(t *testing.T) {
	f := newCPACooldownTestFixture(t)
	f.file.Cooldowns = json.RawMessage(`[]`)
	f.list = func(_ context.Context, call int) (*cpaclient.AuthFilesResponse, error) {
		if call > 1 {
			return nil, fmt.Errorf("no-op refresh failed; Authorization: Bearer %s", cpaCooldownTestSecret)
		}
		return f.response(f.file), nil
	}
	outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
	require.ErrorContains(t, err, "no-op refresh failed")
	require.Nil(t, outcome)
	require.Empty(t, f.resetRequests())
	require.NotEmpty(t, f.client.CPACredential.GetX(f.seedCtx, f.credential.ID).RoutingCooldown.Items)
	f.assertSafe(outcome, err)
}

func TestCPACooldownResetReportsCooldownTriggeredAgainAfterAcceptance(t *testing.T) {
	f := newCPACooldownTestFixture(t)
	retriggered := objects.CPARoutingCooldown{
		Scope: objects.CPARoutingCooldownScopeModel, ModelKey: "newly-limited-model",
		Reason: objects.CPARoutingCooldownReasonTransientError, RetryAt: f.now.Add(2 * time.Hour),
	}
	f.reset = func(context.Context, string, cpaclient.ManagementAPI) error {
		f.file.Cooldowns = cpaCooldownTestRaw(t, retriggered)
		return nil
	}
	outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
	require.NoError(t, err)
	require.True(t, outcome.Applied)
	require.Nil(t, outcome.RefreshError)
	require.NotNil(t, outcome.Credential)
	require.Equal(t, []objects.CPARoutingCooldown{retriggered}, outcome.Credential.RoutingCooldown.Items)
	require.True(t, outcome.Credential.CanResetCooldown)
	require.True(t, outcome.Credential.Cooling)
	require.Len(t, f.resetRequests(), 1)
	f.assertSafe(outcome, err)
}

func TestCPACooldownResetStartsFreshReadDespiteOlderSingleflight(t *testing.T) {
	f := newCPACooldownTestFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	stale := f.file
	started := make(chan struct{})
	release := make(chan struct{})
	oldDone := make(chan error, 1)
	type resetResult struct {
		outcome *CPACredentialCooldownResetOutcome
		err     error
	}
	resetDone := make(chan resetResult, 1)
	var releaseOnce sync.Once
	oldConsumed, resetConsumed := false, false
	resetStarted := false
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		if !oldConsumed {
			select {
			case <-oldDone:
			case <-time.After(5 * time.Second):
				t.Error("older sync goroutine did not exit after cleanup released its GET")
			}
		}
		if resetStarted && !resetConsumed {
			select {
			case <-resetDone:
			case <-time.After(5 * time.Second):
				t.Error("reset goroutine did not exit after cleanup")
			}
		}
	})
	oldClient := &cpaTestManagementClient{listCredentials: func(ctx context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
		close(started)
		select {
		case <-release:
			return f.response(stale), cpaclient.BuildInfo{}, nil
		case <-ctx.Done():
			return nil, cpaclient.BuildInfo{}, ctx.Err()
		}
	}}
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Logf("older sync goroutine panicked: %v", recovered)
				err = fmt.Errorf("older sync panic: %v", recovered)
			}
			oldDone <- err
		}()
		_, err = f.svc.syncInstanceCredentialsWithClient(ctx, f.instance, oldClient)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("older credential list did not start")
	}
	resetStarted = true
	go func() {
		result := resetResult{}
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Logf("cooldown reset goroutine panicked: %v", recovered)
				result.err = fmt.Errorf("cooldown reset panic: %v", recovered)
			}
			resetDone <- result
		}()
		result.outcome, result.err = f.svc.ResetCredentialCooldown(ctx, f.credential.ID)
	}()
	select {
	case result := <-resetDone:
		resetConsumed = true
		require.NoError(t, result.err)
		require.NotNil(t, result.outcome)
		require.True(t, result.outcome.Applied)
		require.Nil(t, result.outcome.RefreshError)
		require.NotNil(t, result.outcome.Credential)
		require.Empty(t, result.outcome.Credential.RoutingCooldown.Items)
		require.Empty(t, f.client.CPACredential.GetX(f.seedCtx, f.credential.ID).RoutingCooldown.Items)
		require.Len(t, f.resetRequests(), 1)
		f.assertSafe(result.outcome, result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("cooldown reset waited for a pre-write singleflight GET instead of reading fresh state")
	}
	select {
	case err := <-oldDone:
		oldConsumed = true
		t.Fatalf("older GET must remain blocked until explicitly released: %v", err)
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-oldDone:
		oldConsumed = true
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("older credential sync did not exit")
	}
}

func TestCPACooldownResetFailedLiveReadNeverWrites(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{name: "authentication", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusUnauthorized}, want: "management authentication failed"},
		{name: "unavailable", err: &cpaclient.ManagementHTTPError{StatusCode: http.StatusServiceUnavailable}, want: "management service is unavailable"},
		{name: "disconnected", err: fmt.Errorf("live read interrupted; Authorization: Bearer %s", cpaCooldownTestSecret), want: "refresh state before retrying"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCPACooldownTestFixture(t)
			before := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			f.list = func(context.Context, int) (*cpaclient.AuthFilesResponse, error) { return nil, tt.err }
			outcome, err := f.svc.ResetCredentialCooldown(f.ctx, f.credential.ID)
			require.ErrorContains(t, err, tt.want)
			require.Nil(t, outcome)
			require.Empty(t, f.resetRequests())
			after := f.client.CPACredential.GetX(f.seedCtx, f.credential.ID)
			require.Equal(t, before.RoutingCooldown, after.RoutingCooldown)
			require.Equal(t, before.QuotaData, after.QuotaData)
			f.assertSafe(outcome, err)
		})
	}
}
