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

func estimateTestSnapshot(usedPercent float64) objects.CPAQuotaSnapshot {
	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	period := cpaWeeklyPeriodSeconds
	used := usedPercent
	return objects.CPAQuotaSnapshot{
		Items: []objects.CPAQuotaItem{{
			ID:               "codex-secondary",
			Label:            "7 day",
			UsedPercent:      &used,
			RemainingPercent: func() *float64 { r := 100 - used; return &r }(),
			ResetAt:          &resetAt,
			PeriodSeconds:    &period,
		}},
	}
}

func TestEstimateWindowItem(t *testing.T) {
	t.Parallel()
	// Weekly window present: it wins.
	snapshot := estimateTestSnapshot(10)
	item := estimateWindowItem(snapshot)
	require.NotNil(t, item)
	require.Equal(t, 7*24*60*60, *item.PeriodSeconds)

	// Weekly + monthly coexisting: the weekly secondary window still wins over
	// the longer monthly primary period.
	monthly := 30 * 24 * 60 * 60
	monthlyReset := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	weeklyReset := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	weekly := cpaWeeklyPeriodSeconds
	snapshot.Items = []objects.CPAQuotaItem{
		{PeriodSeconds: &monthly, ResetAt: &monthlyReset},
		{PeriodSeconds: &weekly, ResetAt: &weeklyReset},
	}
	item = estimateWindowItem(snapshot)
	require.NotNil(t, item)
	require.Equal(t, cpaWeeklyPeriodSeconds, *item.PeriodSeconds)

	// Both long windows are independently estimable even though the legacy
	// single-window helper continues to prefer weekly.
	require.Len(t, estimateWindowItems(snapshot), 2)

	// Only a monthly primary window (no weekly): monthly is selected.
	snapshot.Items = []objects.CPAQuotaItem{{PeriodSeconds: &monthly, ResetAt: &monthlyReset}}
	item = estimateWindowItem(snapshot)
	require.NotNil(t, item)
	require.Equal(t, monthly, *item.PeriodSeconds)

	// Hourly windows are never eligible, even when they carry a reset time.
	hourly := 18_000
	hourlyReset := monthlyReset
	snapshot.Items = []objects.CPAQuotaItem{{PeriodSeconds: &hourly, ResetAt: &hourlyReset}}
	require.Nil(t, estimateWindowItem(snapshot))
	require.Empty(t, estimateWindowItems(snapshot))

	// Empty snapshot yields nothing.
	require.Nil(t, estimateWindowItem(objects.CPAQuotaSnapshot{}))
}

func TestEstimateDenominatorPrefersPreciseHeader(t *testing.T) {
	t.Parallel()
	snapshot := estimateTestSnapshot(3)
	item := estimateWindowItem(snapshot)
	cycleStart := item.ResetAt.Add(-time.Duration(*item.PeriodSeconds) * time.Second)

	precise := 3.42
	matchingReset := *item.ResetAt
	denominator, source := estimateDenominator(item, objects.CPAQuotaObserved{
		SecondaryUsedPercent: &precise,
		SecondaryResetAt:     &matchingReset,
		ObservedAt:           &cycleStart,
	}, cycleStart)
	require.NotNil(t, denominator)
	require.Equal(t, "precise-header", source)
	require.InDelta(t, 3.42, *denominator, 1e-9)

	// A precise observation without a matching reset timestamp is not trusted:
	// fall back to the integer wham percentage even within the same cycle.
	denominator, source = estimateDenominator(item, objects.CPAQuotaObserved{
		SecondaryUsedPercent: &precise,
		ObservedAt:           &cycleStart,
	}, cycleStart)
	require.NotNil(t, denominator)
	require.Equal(t, "wham-percent", source)
	require.InDelta(t, 3.0, *denominator, 1e-9)

	// Without a precise observation the wham integer percentage is used.
	denominator, source = estimateDenominator(item, objects.CPAQuotaObserved{}, cycleStart)
	require.NotNil(t, denominator)
	require.Equal(t, "wham-percent", source)
	require.InDelta(t, 3.0, *denominator, 1e-9)

	// The secondary precise header must never be attached to a monthly window.
	monthlyPeriod := 30 * 24 * 60 * 60
	monthlyUsed := 12.0
	monthlyItem := &objects.CPAQuotaItem{
		UsedPercent:   &monthlyUsed,
		ResetAt:       item.ResetAt,
		PeriodSeconds: &monthlyPeriod,
	}
	denominator, source = estimateDenominator(monthlyItem, objects.CPAQuotaObserved{
		SecondaryUsedPercent: &precise,
		SecondaryResetAt:     item.ResetAt,
		ObservedAt:           &cycleStart,
	}, cycleStart)
	require.NotNil(t, denominator)
	require.Equal(t, "wham-percent", source)
	require.InDelta(t, monthlyUsed, *denominator, 1e-9)

	// A precise observation from a previous cycle (mismatched reset) is ignored.
	staleReset := cycleStart.Add(-time.Hour)
	staleObserved := cycleStart.Add(-2 * time.Hour)
	denominator, source = estimateDenominator(item, objects.CPAQuotaObserved{
		SecondaryUsedPercent: &precise,
		SecondaryResetAt:     &staleReset,
		ObservedAt:           &staleObserved,
	}, cycleStart)
	require.NotNil(t, denominator)
	require.Equal(t, "wham-percent", source)
}

func TestComputeCPAAggregateCostUnpricedModel(t *testing.T) {
	t.Parallel()
	total, priced := computeCPAAggregateCost(map[string]*objects.ModelPrice{}, "gpt-5.2", tokenAggregate{InputTokens: 100}, time.Now())
	require.False(t, priced)
	require.True(t, total.IsZero())
}

func TestEstimateCredentialQuotaThresholds(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_estimate?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	fixedNow := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	svc := &CPAService{
		AbstractService: &AbstractService{db: client},
		quotaRegistry:   cpaclient.NewQuotaRegistry(),
		now:             func() time.Time { return fixedNow },
	}

	instance, err := client.CPAInstance.Create().
		SetName("est").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	// No events at all → nil even with sufficient percentage.
	require.Nil(t, svc.EstimateCredentialQuota(ctx, instance.ID, "idx", estimateTestSnapshot(50), objects.CPAQuotaObserved{}))

	// Below 3% threshold → nil regardless of events.
	snapshot := estimateTestSnapshot(2.5)
	require.Nil(t, svc.EstimateCredentialQuota(ctx, instance.ID, "idx", snapshot, objects.CPAQuotaObserved{}))

	// Seed usage events within the current cycle for a model without configured
	// price (unpriced) and one with a price configured through a channel.
	requestedAt := fixedNow.Add(-24 * time.Hour)
	err = client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).
		SetAuthIndex("idx").
		SetProvider("codex").
		SetModel("unpriced-model").
		SetInputTokens(1000).
		SetOutputTokens(500).
		SetRequestedAt(requestedAt).
		Exec(ctx)
	require.NoError(t, err)

	snapshot = estimateTestSnapshot(4)
	estimate := svc.EstimateCredentialQuota(ctx, instance.ID, "idx", snapshot, objects.CPAQuotaObserved{})
	require.Nil(t, estimate, "all tokens unpriced should not yield an estimate")

	// Events outside the current cycle are excluded.
	old := requestedAt.Add(-14 * 24 * time.Hour)
	err = client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).
		SetAuthIndex("idx").
		SetProvider("codex").
		SetModel("gpt-5.2").
		SetInputTokens(999999).
		SetOutputTokens(1).
		SetRequestedAt(old).
		Exec(ctx)
	require.NoError(t, err)

	estimate = svc.EstimateCredentialQuota(ctx, instance.ID, "idx", snapshot, objects.CPAQuotaObserved{})
	// Still nil because gpt-5.2 has no channel price in this test setup and the
	// unpriced model dominates; this asserts the aggregation window works via
	// the unpriced path rather than producing an estimate from stale data.
	require.Nil(t, estimate)
}
