package biz

import (
	"context"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func TestCPADisplayNameSortKeyMatchesNaturalOrder(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{"Account 2", "Account 10"},
		{"account 002", "ACCOUNT 2"},
		{"a2x", "a02x"},
		{"a2x1", "a02x01"},
		{"a9", "a:"},
		{"a/9", "a/10"},
		{"alpha", "alpha 1"},
		{"Zeta 100000000000000000000", "zeta 999999999999999999999"},
	}
	for _, pair := range pairs {
		want := naturalStringCompare(strings.ToLower(pair[0]), strings.ToLower(pair[1]))
		got := strings.Compare(cpaDisplayNameSortKey(pair[0]), cpaDisplayNameSortKey(pair[1]))
		if got == 0 {
			got = cpaDisplayNameSortLength(pair[0]) - cpaDisplayNameSortLength(pair[1])
		}
		require.Equal(t, signInt(want), signInt(got), "%q vs %q", pair[0], pair[1])
	}
}

func TestCPAProjectionBackfillIsIdempotent(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_projection_backfill?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	svc := &CPAService{AbstractService: &AbstractService{db: client}, now: func() time.Time { return now }}
	instance := client.CPAInstance.Create().
		SetName("projection").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	used := 100.0
	resetAt := now.Add(2 * time.Hour)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential").
		SetRemoteName("credential.json").
		SetDisplayName("Account 10").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateError)).
		SetQuotaData(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
			ID: "weekly", UsedPercent: &used, ResetAt: &resetAt,
		}}}).
		SaveX(ctx)

	require.NoError(t, svc.backfillCPACredentialProjections(ctx))
	first := client.CPACredential.GetX(ctx, credential.ID)
	require.Equal(t, currentCPAProjectionVersion, first.ProjectionVersion)
	require.Equal(t, cpaDisplayNameSortKey("Account 10"), first.DisplayNameSortKey)
	require.Equal(t, cpaDisplayNameSortLength("Account 10"), first.DisplayNameSortLength)
	require.Equal(t, string(objects.CPACredentialHealthAbnormal), first.HealthState)
	require.True(t, first.QuotaCooling)
	require.NotNil(t, first.QuotaCooldownUntil)
	require.True(t, first.QuotaCooldownUntil.Equal(resetAt))

	require.NoError(t, svc.backfillCPACredentialProjections(ctx))
	second := client.CPACredential.GetX(ctx, credential.ID)
	require.Equal(t, first.UpdatedAt, second.UpdatedAt)
}

func TestCPAQueryUsesStableKeysetAndOverviewAggregates(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_keyset_overview?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	svc := &CPAService{AbstractService: &AbstractService{db: client}, now: func() time.Time { return now }}
	instance := client.CPAInstance.Create().
		SetName("keyset").
		SetBaseURL("http://127.0.0.1:8318").
		SetEncryptedSecret("encrypted").
		SaveX(ctx)
	future := now.Add(time.Hour)
	fixtures := []struct {
		name       string
		provider   string
		plan       string
		priority   int
		health     objects.CPACredentialHealthState
		disabled   bool
		cooling    bool
		cooldownAt *time.Time
	}{
		{name: "Alpha", provider: "xai", priority: 20, health: objects.CPACredentialHealthDisabled, disabled: true},
		{name: "Account 10", provider: "claude", plan: "pro", priority: 10, health: objects.CPACredentialHealthAbnormal},
		{name: "Account 2", provider: "codex", plan: "plus", priority: 10, health: objects.CPACredentialHealthHealthy},
		{name: "Account 02", provider: "codex", plan: "team", priority: 10, health: objects.CPACredentialHealthHealthy},
		{name: "Token 2 active", provider: "codex", priority: 10, health: objects.CPACredentialHealthHealthy},
		{name: "Token 02 active", provider: "codex", priority: 10, health: objects.CPACredentialHealthHealthy},
		{name: "Beta", provider: "kimi", priority: 0, health: objects.CPACredentialHealthHealthy, cooling: true, cooldownAt: &future},
	}
	for index, fixture := range fixtures {
		create := client.CPACredential.Create().
			SetCpaInstanceID(instance.ID).
			SetExternalKey(fixture.provider + fixture.name).
			SetRemoteName(fixture.provider + ".json").
			SetDisplayName(fixture.name).
			SetDisplayNameSortKey(cpaDisplayNameSortKey(fixture.name)).
			SetDisplayNameSortLength(cpaDisplayNameSortLength(fixture.name)).
			SetProvider(fixture.provider).
			SetPlanType(fixture.plan).
			SetStatus("active").
			SetPriority(fixture.priority).
			SetDisabled(fixture.disabled).
			SetQuotaState(string(objects.CPAQuotaStateSuccess)).
			SetHealthState(string(fixture.health)).
			SetQuotaCooling(fixture.cooling).
			SetProjectionVersion(currentCPAProjectionVersion)
		if fixture.cooldownAt != nil {
			create.SetQuotaCooldownUntil(*fixture.cooldownAt)
		}
		create.SetAuthIndex(string(rune('a' + index))).SaveX(ctx)
	}

	expected := append([]struct {
		name       string
		provider   string
		plan       string
		priority   int
		health     objects.CPACredentialHealthState
		disabled   bool
		cooling    bool
		cooldownAt *time.Time
	}{}, fixtures...)
	sort.SliceStable(expected, func(i, j int) bool {
		if expected[i].priority != expected[j].priority {
			return expected[i].priority > expected[j].priority
		}
		return naturalStringCompare(strings.ToLower(expected[i].name), strings.ToLower(expected[j].name)) < 0
	})

	var after *string
	var names []string
	for {
		connection, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{
			InstanceID: instance.ID,
			First:      2,
			After:      after,
		})
		require.NoError(t, err)
		require.Equal(t, len(fixtures), connection.TotalCount)
		for _, edge := range connection.Edges {
			names = append(names, edge.Node.DisplayName)
		}
		if !connection.PageInfo.HasNextPage {
			break
		}
		require.NotNil(t, connection.PageInfo.EndCursor)
		after = connection.PageInfo.EndCursor
	}
	expectedNames := make([]string, 0, len(expected))
	for _, fixture := range expected {
		expectedNames = append(expectedNames, fixture.name)
	}
	require.Equal(t, expectedNames, names)

	overview, err := svc.Overview(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, &CPACredentialStats{Available: 4, Total: 7, Abnormal: 1}, overview.Stats)
	require.Equal(t, []*CPAProviderOverview{
		{Provider: "claude", Count: 1, PlanTypes: []string{"pro"}},
		{Provider: "codex", Count: 4, PlanTypes: []string{"plus", "team"}},
		{Provider: "kimi", Count: 1},
		{Provider: "xai", Count: 1},
	}, overview.Providers)
}

func TestCPARuntimeJobsRespectCrossInstanceConcurrencyLimit(t *testing.T) {
	jobs := make([]cpaRuntimeJob, 12)
	for index := range jobs {
		jobs[index] = cpaRuntimeJob{instance: &ent.CPAInstance{ID: index + 1}}
	}
	var active atomic.Int32
	var maximum atomic.Int32
	runCPARuntimeJobs(context.Background(), jobs, func(context.Context, cpaRuntimeJob) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
	})
	require.LessOrEqual(t, maximum.Load(), int32(maxCPAInstanceConcurrency))
	require.Equal(t, int32(maxCPAInstanceConcurrency), maximum.Load())
}

func TestCPARuntimeOperationsExecuteInOrder(t *testing.T) {
	var operations []string
	executeCPARuntimeOperations(
		cpaRuntimeJob{refresh: true, enabledPatrol: true, disabledPatrol: true},
		func() { operations = append(operations, "refresh") },
		func() { operations = append(operations, "enabled") },
		func() { operations = append(operations, "disabled") },
	)
	require.Equal(t, []string{"refresh", "enabled", "disabled"}, operations)

	operations = nil
	executeCPARuntimeOperations(
		cpaRuntimeJob{enabledPatrol: true},
		func() { operations = append(operations, "refresh") },
		func() { operations = append(operations, "enabled") },
		func() { operations = append(operations, "disabled") },
	)
	require.Equal(t, []string{"enabled"}, operations)
}

func TestCPARuntimeClaimIsAtomicAcrossDueOperations(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_runtime_claim?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	svc := &CPAService{AbstractService: &AbstractService{db: client}}
	due := now.Add(-time.Minute)
	created := client.CPAInstance.Create().
		SetName("runtime").
		SetBaseURL("http://127.0.0.1:8319").
		SetEncryptedSecret("encrypted").
		SetAutoRefreshEnabled(true).
		SetAutoManageEnabled(true).
		SetRefreshIntervalMinutes(5).
		SetEnabledPatrolIntervalMinutes(7).
		SetDisabledPatrolIntervalMinutes(120).
		SetNextRefreshAt(due).
		SetNextEnabledPatrolAt(due).
		SetNextDisabledPatrolAt(due).
		SaveX(ctx)
	first := client.CPAInstance.GetX(ctx, created.ID)
	stale := client.CPAInstance.GetX(ctx, created.ID)

	job, claimed, err := svc.claimCPARuntimeInstance(ctx, first, now)
	require.NoError(t, err)
	require.True(t, claimed)
	require.True(t, job.refresh)
	require.True(t, job.enabledPatrol)
	require.True(t, job.disabledPatrol)

	_, claimed, err = svc.claimCPARuntimeInstance(ctx, stale, now)
	require.NoError(t, err)
	require.False(t, claimed)

	updated := client.CPAInstance.GetX(ctx, created.ID)
	require.True(t, updated.NextRefreshAt.Equal(now.Add(5*time.Minute)))
	require.True(t, updated.NextEnabledPatrolAt.Equal(now.Add(7*time.Minute)))
	require.True(t, updated.NextDisabledPatrolAt.Equal(now.Add(120*time.Minute)))
}

func signInt(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}
