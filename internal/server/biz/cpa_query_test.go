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

func TestCPACredentialViewDistinguishesInstanceFailureFromProviderExpiry(t *testing.T) {
	t.Parallel()

	instanceError := "CPA management request failed: connection refused"
	instance := &ent.CPAInstance{Enabled: true, LastError: &instanceError}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	connectionFailure := buildCPACredentialView(instance, &ent.CPACredential{
		Status:         "active",
		QuotaState:     string(objects.CPAQuotaStateError),
		QuotaLastError: `CPA management request failed: Post "http://unauthorized.internal/v0/management/api-call": connection refused`,
	}, now)
	require.True(t, connectionFailure.Stale)
	require.False(t, connectionFailure.Expired)

	providerFailure := buildCPACredentialView(instance, &ent.CPACredential{
		Status:         "active",
		QuotaState:     string(objects.CPAQuotaStateError),
		QuotaLastError: "provider quota request returned HTTP 401",
	}, now)
	require.True(t, providerFailure.Stale)
	require.True(t, providerFailure.Expired)
}

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

	// An exhausted window without a reset is indefinite, even when another
	// exhausted window advertises a finite reset time.
	cooling, until = cpaQuotaCooldown(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "weekly", UsedPercent: &used, ResetAt: &upcoming},
		{ID: "monthly-balance", UsedPercent: &used},
	}}, now)
	require.True(t, cooling)
	require.Nil(t, until)
}

func TestCPAQuotaCooldownCombinations(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	used := 100.0
	passed := now.Add(-time.Hour)
	soon := now.Add(time.Hour)
	later := now.Add(24 * time.Hour)
	fiveHours := 5 * 60 * 60
	weekly := 7 * 24 * 60 * 60

	tests := []struct {
		name      string
		items     []objects.CPAQuotaItem
		cooling   bool
		wantUntil *time.Time
	}{
		{
			name:      "single future reset",
			items:     []objects.CPAQuotaItem{{ID: "weekly", UsedPercent: &used, ResetAt: &later}},
			cooling:   true,
			wantUntil: &later,
		},
		{
			name:    "single resetless window",
			items:   []objects.CPAQuotaItem{{ID: "balance", UsedPercent: &used}},
			cooling: true,
		},
		{
			name: "multiple future resets choose earliest",
			items: []objects.CPAQuotaItem{
				{ID: "weekly", UsedPercent: &used, ResetAt: &later},
				{ID: "hourly", UsedPercent: &used, ResetAt: &soon},
			},
			cooling:   true,
			wantUntil: &soon,
		},
		{
			name: "resetless window overrides finite reset",
			items: []objects.CPAQuotaItem{
				{ID: "weekly", UsedPercent: &used, ResetAt: &later},
				{ID: "balance", UsedPercent: &used},
			},
			cooling: true,
		},
		{
			name:    "expired resets are ignored",
			items:   []objects.CPAQuotaItem{{ID: "hourly", UsedPercent: &used, ResetAt: &passed}},
			cooling: false,
		},
		{
			name: "UI cooldown includes five hour window",
			items: []objects.CPAQuotaItem{
				{ID: "code-primary", PeriodSeconds: &fiveHours, UsedPercent: &used, ResetAt: &soon},
				{ID: "code-secondary", PeriodSeconds: &weekly, UsedPercent: &used, ResetAt: &later},
			},
			cooling:   true,
			wantUntil: &soon,
		},
		{
			// Model sub-limits and spending balances are shown but never gate:
			// an exhausted one must not cool down a credential with plan room.
			name: "display-only items never cool down",
			items: []objects.CPAQuotaItem{
				{ID: "seven-day-opus", UsedPercent: &used, ResetAt: &later, DisplayOnly: true},
				{ID: "extra-usage", UsedPercent: &used, DisplayOnly: true},
			},
			cooling: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cooling, until := cpaQuotaCooldown(objects.CPAQuotaSnapshot{Items: tt.items}, now)
			require.Equal(t, tt.cooling, cooling)
			if tt.wantUntil == nil {
				require.Nil(t, until)
				return
			}
			require.NotNil(t, until)
			require.True(t, until.Equal(*tt.wantUntil))
		})
	}
}

func TestCPAAutoManageQuotaCooldownIgnoresFiveHour(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	used := 100.0
	remaining := 0.0
	hourly := 5 * 60 * 60
	weekly := 7 * 24 * 60 * 60
	hourlyReset := now.Add(time.Hour)
	weeklyReset := now.Add(24 * time.Hour)

	cooling, until, exhausted := cpaAutoManageQuotaCooldownDetail(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "code-primary", Label: "5 hour", PeriodSeconds: &hourly, UsedPercent: &used, RemainingPercent: &remaining, ResetAt: &hourlyReset},
		{ID: "code-secondary", Label: "7 day", PeriodSeconds: &weekly, UsedPercent: &used, RemainingPercent: &remaining, ResetAt: &weeklyReset},
	}}, now)
	require.True(t, cooling)
	require.NotNil(t, until)
	require.True(t, until.Equal(weeklyReset))
	require.NotNil(t, exhausted)
	require.Equal(t, "code-secondary", exhausted.ID)

	cooling, until, exhausted = cpaAutoManageQuotaCooldownDetail(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "code-primary", Label: "5 hour", PeriodSeconds: &hourly, UsedPercent: &used, RemainingPercent: &remaining, ResetAt: &hourlyReset},
	}}, now)
	require.False(t, cooling)
	require.Nil(t, until)
	require.Nil(t, exhausted)
}

func TestCPAQuotaItemIsFiveHour(t *testing.T) {
	t.Parallel()

	periodFiveHours := 5 * 60 * 60
	periodWeekly := 7 * 24 * 60 * 60
	tests := []struct {
		name string
		item objects.CPAQuotaItem
		want bool
	}{
		{name: "period has priority", item: objects.CPAQuotaItem{Label: "7 day", PeriodSeconds: &periodFiveHours}, want: true},
		{name: "non five hour period has priority", item: objects.CPAQuotaItem{Label: "5h", PeriodSeconds: &periodWeekly}, want: false},
		{name: "five hour label", item: objects.CPAQuotaItem{Label: "5 hour"}, want: true},
		{name: "five hour id", item: objects.CPAQuotaItem{ID: "five-hour"}, want: true},
		{name: "other window", item: objects.CPAQuotaItem{Label: "daily"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cpaQuotaItemIsFiveHour(tt.item))
		})
	}
}

func TestParseCPAStatusFilterOR(t *testing.T) {
	t.Parallel()

	filter := parseCPAStatusFilter([]CPACredentialFilterStatus{"abnormal", "cooldown"})
	require.True(t, filter.matches(&CPACredentialView{Abnormal: true}))
	require.True(t, filter.matches(&CPACredentialView{Cooling: true}))
	require.False(t, filter.matches(&CPACredentialView{Available: true}))

	enabled := parseCPAStatusFilter([]CPACredentialFilterStatus{"enabled"})
	require.True(t, enabled.matches(&CPACredentialView{Disabled: false}))
	require.False(t, enabled.matches(&CPACredentialView{Disabled: true}))

	empty := parseCPAStatusFilter([]CPACredentialFilterStatus{})
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
	require.NoError(t, svc.backfillCPACredentialProjections(ctx))

	abnormal, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []CPACredentialFilterStatus{"abnormal"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, abnormal.TotalCount)
	require.Equal(t, "Error Codex", abnormal.Edges[0].Node.DisplayName)
	require.True(t, abnormal.Edges[0].Node.Abnormal)

	cooldown, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []CPACredentialFilterStatus{"cooldown"},
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
		Statuses:   []CPACredentialFilterStatus{"abnormal", "enabled"},
	})
	require.NoError(t, err)
	require.Equal(t, 3, union.TotalCount)

	disabled, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
		InstanceID: instance.ID,
		First:      20,
		Statuses:   []CPACredentialFilterStatus{"disabled"},
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

func TestActiveCPARoutingCooldown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	past := objects.CPARoutingCooldown{Scope: objects.CPARoutingCooldownScopeCredential, RetryAt: now.Add(-time.Second)}
	boundary := objects.CPARoutingCooldown{Scope: objects.CPARoutingCooldownScopeCredential, RetryAt: now}
	future := objects.CPARoutingCooldown{Scope: objects.CPARoutingCooldownScopeModel, ModelKey: "model", RetryAt: now.Add(time.Hour)}
	for _, tt := range []struct {
		name  string
		known bool
		items []objects.CPARoutingCooldown
		want  []objects.CPARoutingCooldown
	}{
		{name: "unknown"},
		{name: "unknown ignores entries", items: []objects.CPARoutingCooldown{future}},
		{name: "known nil", known: true},
		{name: "known empty", known: true, items: []objects.CPARoutingCooldown{}},
		{name: "future", known: true, items: []objects.CPARoutingCooldown{future}, want: []objects.CPARoutingCooldown{future}},
		{name: "exact deadline expires", known: true, items: []objects.CPARoutingCooldown{boundary}},
		{name: "all expired", known: true, items: []objects.CPARoutingCooldown{past, boundary}},
		{name: "mixed", known: true, items: []objects.CPARoutingCooldown{past, future, boundary}, want: []objects.CPARoutingCooldown{future}},
		{name: "expired suffix", known: true, items: []objects.CPARoutingCooldown{future, boundary}, want: []objects.CPARoutingCooldown{future}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := objects.CPARoutingCooldownSnapshot{Known: tt.known, ObservedAt: &observedAt, Items: tt.items}
			original := append([]objects.CPARoutingCooldown{}, tt.items...)
			active := activeCPARoutingCooldown(snapshot, now)
			require.Equal(t, tt.known, active.Known)
			require.Equal(t, &observedAt, active.ObservedAt)
			require.NotNil(t, active.Items)
			require.Equal(t, append([]objects.CPARoutingCooldown{}, tt.want...), active.Items)
			require.Equal(t, original, append([]objects.CPARoutingCooldown{}, snapshot.Items...))
		})
	}
}

func TestCPACredentialRoutingCooldownView(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)
	modelCooldown := objects.CPARoutingCooldownSnapshot{Known: true, Items: []objects.CPARoutingCooldown{{
		Scope: objects.CPARoutingCooldownScopeModel, ModelKey: "model", Reason: objects.CPARoutingCooldownReasonQuota, RetryAt: until,
	}}}
	for _, tt := range []struct {
		name             string
		instanceDisabled bool
		authIndex        string
		disabled         bool
		expired          bool
		quotaExhausted   bool
		snapshot         objects.CPARoutingCooldownSnapshot
		canReset         bool
		available        bool
	}{
		{name: "model cooldown remains available", authIndex: "auth", snapshot: modelCooldown, canReset: true, available: true},
		{name: "disabled credential can clear cooldown", authIndex: "auth", disabled: true, snapshot: modelCooldown, canReset: true},
		{name: "expired credential can clear cooldown", authIndex: "auth", expired: true, snapshot: modelCooldown, canReset: true},
		{name: "disabled instance cannot clear cooldown", instanceDisabled: true, authIndex: "auth", snapshot: modelCooldown, available: true},
		{name: "missing auth index", snapshot: modelCooldown, available: true},
		{name: "blank auth index", authIndex: " \t", snapshot: modelCooldown, available: true},
		{name: "quota exhausted with known empty native state", authIndex: "auth", quotaExhausted: true, snapshot: objects.CPARoutingCooldownSnapshot{Known: true}},
		{name: "quota exhaustion and native cooldown coexist", authIndex: "auth", quotaExhausted: true, snapshot: modelCooldown, canReset: true},
		{name: "quota exhaustion does not imply native cooldown", authIndex: "auth", quotaExhausted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			credential := &ent.CPACredential{
				AuthIndex: tt.authIndex, Status: "active", Disabled: tt.disabled,
				HealthState: string(objects.CPACredentialHealthHealthy), RoutingCooldown: tt.snapshot,
				QuotaCooling: tt.quotaExhausted,
			}
			if tt.quotaExhausted {
				credential.QuotaCooldownUntil = &until
			}
			if tt.disabled {
				credential.HealthState = string(objects.CPACredentialHealthDisabled)
			}
			if tt.expired {
				credential.HealthState = string(objects.CPACredentialHealthAbnormal)
				credential.QuotaState = string(objects.CPAQuotaStateError)
				credential.QuotaLastError = "provider quota request returned HTTP 401"
			}
			view := buildCPACredentialView(&ent.CPAInstance{Enabled: !tt.instanceDisabled}, credential, now)
			require.Equal(t, tt.canReset, view.CanResetCooldown)
			require.Equal(t, tt.available, view.Available)
			require.Equal(t, tt.quotaExhausted, view.Cooling)
			require.Equal(t, tt.expired, view.Expired)
			require.Equal(t, tt.disabled, view.Disabled)
			require.Equal(t, tt.snapshot.Known, view.RoutingCooldown.Known)
			require.NotNil(t, view.RoutingCooldown.Items)
		})
	}
}

func TestParseCPARoutingCooldownFilterOR(t *testing.T) {
	t.Parallel()
	active := &CPACredentialView{RoutingCooldown: objects.CPARoutingCooldownSnapshot{Known: true, Items: []objects.CPARoutingCooldown{{Scope: objects.CPARoutingCooldownScopeModel}}}}
	filter := parseCPAStatusFilter([]CPACredentialFilterStatus{" CPA_COOLDOWN "})
	require.False(t, filter.any)
	require.True(t, filter.matches(active))
	require.False(t, filter.matches(&CPACredentialView{Cooling: true}))
	require.False(t, filter.matches(&CPACredentialView{RoutingCooldown: objects.CPARoutingCooldownSnapshot{Known: true}}))
	union := parseCPAStatusFilter([]CPACredentialFilterStatus{CPACredentialFilterCPACooldown, CPACredentialFilterCooldown, CPACredentialFilterDisabled})
	require.True(t, union.matches(active))
	require.True(t, union.matches(&CPACredentialView{Cooling: true}))
	require.True(t, union.matches(&CPACredentialView{Disabled: true}))
	require.False(t, union.matches(&CPACredentialView{Available: true}))
}

func TestQueryCPARoutingCooldownPaginationAndFilters(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_query_routing_cooldown?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return now })
	instance := client.CPAInstance.Create().
		SetName("routing filters").SetBaseURL("http://127.0.0.1:8317").SetEncryptedSecret("encrypted").SaveX(ctx)
	otherInstance := client.CPAInstance.Create().
		SetName("other routing filters").SetBaseURL("http://127.0.0.1:8318").SetEncryptedSecret("encrypted").SaveX(ctx)
	until := now.Add(time.Hour)
	future := objects.CPARoutingCooldownSnapshot{Known: true, Items: []objects.CPARoutingCooldown{{
		Scope: objects.CPARoutingCooldownScopeModel, ModelKey: "model", RetryAt: until,
	}}}
	boundary := objects.CPARoutingCooldownSnapshot{Known: true, Items: []objects.CPARoutingCooldown{{
		Scope: objects.CPARoutingCooldownScopeCredential, RetryAt: now,
	}}}
	empty := objects.CPARoutingCooldownSnapshot{Known: true, Items: []objects.CPARoutingCooldown{}}
	create := func(instanceID int, name, provider, plan string, disabled bool, snapshot objects.CPARoutingCooldownSnapshot) *ent.CPACredential {
		projection := projectCPACredential(name, "active", disabled, false, objects.CPAQuotaStateSuccess, objects.CPAQuotaSnapshot{}, now)
		builder := client.CPACredential.Create().
			SetCpaInstanceID(instanceID).SetExternalKey(name).SetAuthIndex(name).SetRemoteName(name + ".json").SetDisplayName(name).
			SetProvider(provider).SetPlanType(plan).SetStatus("active").SetDisabled(disabled).
			SetHealthState(string(projection.healthState)).SetQuotaState(string(objects.CPAQuotaStateSuccess)).
			SetDisplayNameSortKey(projection.displayNameSortKey).SetDisplayNameSortLength(projection.displayNameSortLength).
			SetProjectionVersion(currentCPAProjectionVersion).SetRoutingCooldown(snapshot)
		if until := cpaRoutingCooldownUntil(snapshot); until != nil {
			builder.SetRoutingCooldownUntil(*until)
		}
		return builder.SaveX(ctx)
	}
	first := create(instance.ID, "Target 1", "codex", "plus", false, future)
	second := create(instance.ID, "Target 2", "codex", "plus", false, future)
	create(instance.ID, "Target 3", "codex", "plus", true, empty)
	exhausted := create(instance.ID, "Target 4", "codex", "plus", false, empty)
	client.CPACredential.UpdateOne(exhausted).SetQuotaCooling(true).SetQuotaCooldownUntil(until).ExecX(ctx)
	create(instance.ID, "Target 5", "codex", "plus", false, boundary)
	create(instance.ID, "Target 6", "xai", "plus", false, future)
	create(instance.ID, "Target 7", "codex", "free", false, future)
	create(instance.ID, "Excluded search", "codex", "plus", false, future)
	create(otherInstance.ID, "Target 1", "codex", "plus", false, future)
	provider, search := "codex", "Target"
	input := QueryCPACredentialsInput{
		InstanceID: instance.ID, First: 1, Provider: &provider, Search: &search, PlanTypes: []string{"plus"},
		Statuses: []CPACredentialFilterStatus{CPACredentialFilterCPACooldown},
	}
	page, err := svc.QueryCredentials(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 2, page.TotalCount)
	require.Len(t, page.Edges, 1)
	require.Equal(t, first.ID, page.Edges[0].Node.ID)
	require.True(t, page.Edges[0].Node.Available)
	require.True(t, page.PageInfo.HasNextPage)
	input.After = page.PageInfo.EndCursor
	page, err = svc.QueryCredentials(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 2, page.TotalCount)
	require.Len(t, page.Edges, 1)
	require.Equal(t, second.ID, page.Edges[0].Node.ID)
	require.True(t, page.PageInfo.HasPreviousPage)
	require.False(t, page.PageInfo.HasNextPage)

	input.After, input.First = nil, 20
	for _, tt := range []struct {
		name     string
		statuses []CPACredentialFilterStatus
		count    int
	}{
		{name: "native only", statuses: []CPACredentialFilterStatus{CPACredentialFilterCPACooldown}, count: 2},
		{name: "quota only", statuses: []CPACredentialFilterStatus{CPACredentialFilterCooldown}, count: 1},
		{name: "native or disabled", statuses: []CPACredentialFilterStatus{CPACredentialFilterCPACooldown, CPACredentialFilterDisabled}, count: 3},
		{name: "native or quota", statuses: []CPACredentialFilterStatus{CPACredentialFilterCPACooldown, CPACredentialFilterCooldown}, count: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input.Statuses = tt.statuses
			page, err := svc.QueryCredentials(ctx, input)
			require.NoError(t, err)
			require.Equal(t, tt.count, page.TotalCount)
			require.Len(t, page.Edges, tt.count)
			filter := parseCPAStatusFilter(tt.statuses)
			for _, edge := range page.Edges {
				require.Equal(t, instance.ID, edge.Node.InstanceID)
				require.True(t, filter.matches(edge.Node))
				if edge.Node.ID == exhausted.ID {
					require.True(t, edge.Node.Cooling)
					require.Empty(t, edge.Node.RoutingCooldown.Items)
					require.False(t, edge.Node.CanResetCooldown)
				}
			}
		})
	}
	input.Statuses = []CPACredentialFilterStatus{CPACredentialFilterCPACooldown}
	input.Search = nil
	input.Provider = nil
	input.PlanTypes = nil
	page, err = svc.QueryCredentials(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 5, page.TotalCount)
	input.InstanceID = otherInstance.ID
	page, err = svc.QueryCredentials(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 1, page.TotalCount)

	overview, err := svc.Overview(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, &CPACredentialStats{Available: 6, Total: 8, Abnormal: 0}, overview.Stats)
}
