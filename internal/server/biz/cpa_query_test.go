package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPAQuotaItemExhausted(t *testing.T) {
	t.Parallel()

	used := 100.0
	remainingPercent := 0.0
	require.True(t, cpaQuotaItemExhausted(objects.CPAQuotaItem{
		UsedPercent:      &used,
		RemainingPercent: &remainingPercent,
	}))

	limit, remaining := 100.0, 0.0
	require.True(t, cpaQuotaItemExhausted(objects.CPAQuotaItem{
		Limit:     &limit,
		Remaining: &remaining,
	}))

	zero := 0.0
	require.False(t, cpaQuotaItemExhausted(objects.CPAQuotaItem{
		Used:      &zero,
		Limit:     &zero,
		Remaining: &zero,
	}))

	partial := 42.0
	require.False(t, cpaQuotaItemExhausted(objects.CPAQuotaItem{UsedPercent: &partial}))
}

func TestCPAQuotaCooldownPicksEarliestReset(t *testing.T) {
	t.Parallel()

	used := 100.0
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	earlier := time.Date(2026, 8, 21, 17, 0, 0, 0, time.UTC)
	cooling, until := cpaQuotaCooldown(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "weekly", UsedPercent: &used, ResetAt: &later},
		{ID: "hourly", UsedPercent: &used, ResetAt: &earlier},
	}}, now)
	require.True(t, cooling)
	require.NotNil(t, until)
	require.True(t, until.Equal(earlier))

	cooling, until = cpaQuotaCooldown(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "monthly-balance", Used: new(float64), Limit: new(float64), Remaining: new(float64)},
	}}, now)
	require.False(t, cooling)
	require.Nil(t, until)
}

func TestCPAQuotaCooldownIgnoresPassedReset(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	used := 100.0
	passed := now.Add(-time.Hour)
	upcoming := now.Add(time.Hour)

	// An exhausted window whose reset already passed is no longer cooling;
	// the stale snapshot only awaits the next successful refresh.
	cooling, until := cpaQuotaCooldown(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "hourly", UsedPercent: &used, ResetAt: &passed},
	}}, now)
	require.False(t, cooling)
	require.Nil(t, until)

	// Mixed snapshots stay cooling because of the still-upcoming window and
	// report its reset time as the cooldown end.
	cooling, until = cpaQuotaCooldown(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "hourly", UsedPercent: &used, ResetAt: &passed},
		{ID: "weekly", UsedPercent: &used, ResetAt: &upcoming},
	}}, now)
	require.True(t, cooling)
	require.NotNil(t, until)
	require.True(t, until.Equal(upcoming))
}

func TestParseCPAStatusFilterOR(t *testing.T) {
	t.Parallel()

	filter := parseCPAStatusFilter([]string{"abnormal", "cooldown"})
	require.True(t, filter.matches(&CPACredentialView{Abnormal: true}))
	require.True(t, filter.matches(&CPACredentialView{Cooling: true}))
	require.False(t, filter.matches(&CPACredentialView{Available: true}))

	enabled := parseCPAStatusFilter([]string{"enabled"})
	require.True(t, enabled.matches(&CPACredentialView{Disabled: false}))
	require.False(t, enabled.matches(&CPACredentialView{Disabled: true}))

	empty := parseCPAStatusFilter([]string{})
	require.True(t, empty.matches(&CPACredentialView{Disabled: true, Abnormal: false}))
}

func TestQueryCredentialsStatusFilterAndCooldown(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_query_status?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := &CPAService{
		AbstractService: &AbstractService{db: client},
		quotaRegistry:   cpaclient.NewQuotaRegistry(),
		now:             func() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) },
	}
	instance, err := client.CPAInstance.Create().
		SetName("Primary CPA").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	used := 100.0
	remaining := 0.0
	_, err = client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex-ok").
		SetAuthIndex("codex-ok").
		SetRemoteName("ok.json").
		SetDisplayName("Available Codex").
		SetProvider("codex").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex-disabled").
		SetAuthIndex("codex-disabled").
		SetRemoteName("disabled.json").
		SetDisplayName("Disabled Codex").
		SetProvider("codex").
		SetStatus("disabled").
		SetDisabled(true).
		SetQuotaState(string(objects.CPAQuotaStatePending)).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex-error").
		SetAuthIndex("codex-error").
		SetRemoteName("error.json").
		SetDisplayName("Error Codex").
		SetProvider("codex").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateError)).
		SetQuotaLastError("quota request failed").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex-exhausted").
		SetAuthIndex("codex-exhausted").
		SetRemoteName("exhausted.json").
		SetDisplayName("Exhausted Codex").
		SetProvider("codex").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaData(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
			ID:               "code-primary",
			Group:            "Code",
			Label:            "7 day",
			UsedPercent:      &used,
			RemainingPercent: &remaining,
			ResetAt:          &resetAt,
		}}}).
		Save(ctx)
	require.NoError(t, err)

	abnormal, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []string{"abnormal"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, abnormal.TotalCount)
	require.Equal(t, "Error Codex", abnormal.Edges[0].Node.DisplayName)
	require.True(t, abnormal.Edges[0].Node.Abnormal)

	cooldown, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []string{"cooldown"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, cooldown.TotalCount)
	require.Equal(t, "Exhausted Codex", cooldown.Edges[0].Node.DisplayName)
	require.True(t, cooldown.Edges[0].Node.Cooling)
	require.False(t, cooldown.Edges[0].Node.Available)
	require.NotNil(t, cooldown.Edges[0].Node.CooldownUntil)
	require.True(t, cooldown.Edges[0].Node.CooldownUntil.Equal(resetAt))

	union, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []string{"abnormal", "enabled"},
	})
	require.NoError(t, err)
	require.Equal(t, 3, union.TotalCount)

	disabled, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []string{"disabled"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, disabled.TotalCount)
	require.Equal(t, "Disabled Codex", disabled.Edges[0].Node.DisplayName)
}

func TestSupportsStoredQuotaRetriesUnsupportedXAI(t *testing.T) {
	t.Parallel()

	svc := &CPAService{quotaRegistry: cpaclient.NewQuotaRegistry()}
	require.True(t, svc.supportsStoredQuota(&ent.CPACredential{
		Provider:   "xai",
		QuotaState: string(objects.CPAQuotaStateUnsupported),
	}))
	require.False(t, svc.supportsStoredQuota(&ent.CPACredential{
		Provider:     "xai",
		QuotaState:   string(objects.CPAQuotaStateUnsupported),
		QuotaContext: objects.CPAQuotaContext{Paid: true},
	}))
	require.False(t, svc.supportsStoredQuota(&ent.CPACredential{
		Provider:   "custom-provider",
		QuotaState: string(objects.CPAQuotaStatePending),
	}))
}
