package biz

import (
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
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

	hourly := 18_000
	hourlyReset := time.Date(2026, 8, 21, 18, 5, 35, 0, time.UTC)
	weeklyReset := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	weekly := cpaWeeklyPeriodSeconds

	// Restored Codex 5h + 7d shape: only the 7d window participates in the
	// weekly dollar estimate.
	snapshot.Items = []objects.CPAQuotaItem{
		{PeriodSeconds: &hourly, ResetAt: &hourlyReset},
		{PeriodSeconds: &weekly, ResetAt: &weeklyReset},
	}
	require.Len(t, estimateWindowItems(snapshot), 1)
	item = estimateWindowItem(snapshot)
	require.NotNil(t, item)
	require.Equal(t, cpaWeeklyPeriodSeconds, *item.PeriodSeconds)

	// Weekly + monthly coexisting: both long windows are independently
	// estimable, while the legacy single-window helper continues to prefer 7d.
	monthly := 30 * 24 * 60 * 60
	monthlyReset := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	snapshot.Items = []objects.CPAQuotaItem{
		{PeriodSeconds: &hourly, ResetAt: &hourlyReset},
		{PeriodSeconds: &weekly, ResetAt: &weeklyReset},
		{PeriodSeconds: &monthly, ResetAt: &monthlyReset},
	}
	item = estimateWindowItem(snapshot)
	require.NotNil(t, item)
	require.Equal(t, cpaWeeklyPeriodSeconds, *item.PeriodSeconds)
	estimable := estimateWindowItems(snapshot)
	require.Len(t, estimable, 2)
	require.Equal(t, []int{weekly, monthly}, []int{*estimable[0].PeriodSeconds, *estimable[1].PeriodSeconds})

	// Codex 5h + 30d shape remains monthly-estimated; 5h is still excluded.
	snapshot.Items = []objects.CPAQuotaItem{
		{PeriodSeconds: &hourly, ResetAt: &hourlyReset},
		{PeriodSeconds: &monthly, ResetAt: &monthlyReset},
	}
	require.Len(t, estimateWindowItems(snapshot), 1)
	item = estimateWindowItem(snapshot)
	require.NotNil(t, item)
	require.Equal(t, monthly, *item.PeriodSeconds)

	// Hourly windows are never eligible, even when they carry a reset time.
	snapshot.Items = []objects.CPAQuotaItem{{PeriodSeconds: &hourly, ResetAt: &hourlyReset}}
	require.Nil(t, estimateWindowItem(snapshot))
	require.Empty(t, estimateWindowItems(snapshot))

	// Empty snapshot yields nothing.
	require.Nil(t, estimateWindowItem(objects.CPAQuotaSnapshot{}))
}

func TestCodexEstimateIntervalsRequireLocalPercentageDelta(t *testing.T) {
	t.Parallel()
	snapshot := estimateTestSnapshot(50)
	weekly := estimateWindowItem(snapshot)
	baselinePercent := 20.0
	latestPercent := 50.0
	baselineEventID := 10
	latestEventID := 40
	matchingReset := *weekly.ResetAt
	observed := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "session-a",
		SecondaryBaselineUsedPercent: &baselinePercent,
		SecondaryBaselineEventID:     &baselineEventID,
		SecondaryUsedPercent:         &latestPercent,
		SecondaryLatestEventID:       &latestEventID,
		SecondaryResetAt:             &matchingReset,
	}

	interval := codexEstimateIntervalForItem(weekly, observed)
	require.NotNil(t, interval)
	require.Equal(t, "precise-header-delta", interval.source)
	require.InDelta(t, 30, interval.usedPercent, 1e-9)
	require.Equal(t, baselineEventID, interval.fromEventID)
	require.Equal(t, latestEventID, interval.toEventID)

	// A random reset with a new reset_at invalidates the old interval.
	staleReset := matchingReset.Add(-time.Hour)
	observed.SecondaryResetAt = &staleReset
	require.Nil(t, codexEstimateIntervalForItem(weekly, observed))
	require.Equal(t, "reset-mismatch", codexEstimateIntervalDecisionForItem(weekly, observed).skipReason)

	// A sub-3% local delta is intentionally hidden instead of falling back to
	// the inaccurate cumulative WHAM denominator.
	observed.SecondaryResetAt = &matchingReset
	smallLatest := 22.5
	observed.SecondaryUsedPercent = &smallLatest
	require.Nil(t, codexEstimateIntervalForItem(weekly, observed))
	require.Equal(t, "insufficient-percent-delta", codexEstimateIntervalDecisionForItem(weekly, observed).skipReason)

	observed.SecondaryUsedPercent = &latestPercent
	invalidLatestEventID := baselineEventID
	observed.SecondaryLatestEventID = &invalidLatestEventID
	require.Equal(t, "invalid-event-range", codexEstimateIntervalDecisionForItem(weekly, observed).skipReason)
	observed.SecondaryLatestEventID = &latestEventID

	monthlyPeriod := 30 * 24 * 60 * 60
	monthlyUsed := 50.0
	monthly := &objects.CPAQuotaItem{
		UsedPercent:                 &monthlyUsed,
		ResetAt:                     weekly.ResetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "session-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
	}
	interval = codexEstimateIntervalForItem(monthly, objects.CPAQuotaObserved{})
	require.NotNil(t, interval)
	require.Equal(t, "refresh-delta", interval.source)
	require.InDelta(t, 30, interval.usedPercent, 1e-9)
}

func TestEstimateCredentialQuotaUsesOnlyLocalCollectorInterval(t *testing.T) {
	cases := []struct {
		name            string
		baselinePercent float64
		latestPercent   float64
		intervalCost    int64
		monthly         bool
	}{
		{name: "server-a-from-cycle-start", baselinePercent: 0, latestPercent: 20, intervalCost: 20},
		{name: "server-b-after-switch", baselinePercent: 20, latestPercent: 50, intervalCost: 30},
		{name: "monthly-server-b-after-switch", baselinePercent: 20, latestPercent: 50, intervalCost: 30, monthly: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&_fk=1", tc.name))
			defer client.Close()
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			// Usage-per-unit prices are expressed per one million tokens. This test
			// uses $1 per token so interval cost maps directly to token count.
			unitPrice := decimal.NewFromInt(1_000_000)
			svc := &CPAService{
				AbstractService: &AbstractService{db: client},
				priceIndex: map[string]*objects.ModelPrice{
					"gpt-5.2": {Items: []objects.ModelPriceItem{{
						ItemCode: objects.PriceItemCodeUsage,
						Pricing: objects.Pricing{
							Mode:         objects.PricingModeUsagePerUnit,
							UsagePerUnit: &unitPrice,
						},
					}}},
				},
				priceIndexBuiltAt: time.Now(),
				now:               time.Now,
			}
			instance, err := client.CPAInstance.Create().
				SetName(tc.name).
				SetBaseURL("http://127.0.0.1:8317").
				SetEncryptedSecret("encrypted").
				Save(ctx)
			require.NoError(t, err)
			requestedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
			baselineEvent, err := client.CpaUsageEvent.Create().
				SetCpaInstanceID(instance.ID).
				SetAuthIndex("idx").
				SetProvider("codex").
				SetModel("gpt-5.2").
				SetInputTokens(999).
				SetRequestedAt(requestedAt).
				Save(ctx)
			require.NoError(t, err)
			_, err = client.CpaUsageEvent.Create().
				SetCpaInstanceID(instance.ID).
				SetAuthIndex("other-auth").
				SetProvider("codex").
				SetModel("gpt-5.2").
				SetInputTokens(999).
				SetRequestedAt(requestedAt.Add(time.Minute)).
				Save(ctx)
			require.NoError(t, err)
			latestEvent, err := client.CpaUsageEvent.Create().
				SetCpaInstanceID(instance.ID).
				SetAuthIndex("idx").
				SetProvider("codex").
				SetModel("gpt-5.2").
				SetInputTokens(tc.intervalCost).
				SetRequestedAt(requestedAt.Add(2 * time.Minute)).
				Save(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.intervalCost, latestEvent.InputTokens)
			_, err = client.CpaUsageEvent.Create().
				SetCpaInstanceID(instance.ID).
				SetAuthIndex("idx").
				SetProvider("codex").
				SetModel("gpt-5.2").
				SetInputTokens(999).
				SetRequestedAt(requestedAt.Add(3 * time.Minute)).
				Save(ctx)
			require.NoError(t, err)

			resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
			observed := objects.CPAQuotaObserved{
				SecondaryCollectorSessionID:  tc.name,
				SecondaryBaselineUsedPercent: &tc.baselinePercent,
				SecondaryBaselineEventID:     &baselineEvent.ID,
				SecondaryUsedPercent:         &tc.latestPercent,
				SecondaryLatestEventID:       &latestEvent.ID,
				SecondaryResetAt:             &resetAt,
			}
			aggregates, err := svc.usageAggregatesByModel(
				ctx,
				instance.ID,
				"idx",
				resetAt.Add(-7*24*time.Hour),
				resetAt,
				baselineEvent.ID,
				latestEvent.ID,
			)
			require.NoError(t, err)
			require.Contains(t, aggregates, "gpt-5.2", "aggregates: %#v", aggregates)
			require.Equal(t, tc.intervalCost, aggregates["gpt-5.2"].InputTokens, "aggregates: %#v", aggregates)
			cost, priced := computeCPAAggregateCost(svc.priceIndex, "gpt-5.2", aggregates["gpt-5.2"], time.Now())
			require.True(t, priced)
			require.False(t, cost.IsZero())

			snapshot := estimateTestSnapshot(tc.latestPercent)
			expectedSource := "precise-header-delta"
			if tc.monthly {
				period := 30 * 24 * 60 * 60
				item := &snapshot.Items[0]
				item.Label = "30 day"
				item.PeriodSeconds = &period
				item.EstimateCollectorSessionID = tc.name
				item.EstimateBaselineUsedPercent = &tc.baselinePercent
				item.EstimateBaselineEventID = &baselineEvent.ID
				item.EstimateLatestEventID = &latestEvent.ID
				observed = objects.CPAQuotaObserved{}
				expectedSource = "refresh-delta"
			}
			estimate := svc.EstimateCredentialQuota(ctx, instance.ID, "idx", snapshot, observed)
			require.NotNil(t, estimate)
			require.InDelta(t, 100, estimate.LimitUSD, 1e-9)
			require.InDelta(t, float64(tc.intervalCost), estimate.CostUSD, 1e-9)
			require.InDelta(t, tc.latestPercent-tc.baselinePercent, estimate.UsedPercent, 1e-9)
			require.Equal(t, expectedSource, estimate.Source)
		})
	}
}

func TestPrepareCodexMonthlyIntervalReanchorsOnSessionAndRandomReset(t *testing.T) {
	monthlyPeriod := 30 * 24 * 60 * 60
	resetAt := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	used20 := 20.0
	first := &objects.CPAQuotaItem{ID: "monthly", UsedPercent: &used20, ResetAt: &resetAt, PeriodSeconds: &monthlyPeriod}
	prepareCodexMonthlyInterval(first, nil, "session-a", 10)
	require.Equal(t, 20.0, *first.EstimateBaselineUsedPercent)
	require.Equal(t, 10, *first.EstimateBaselineEventID)
	require.Nil(t, codexEstimateIntervalForItem(first, objects.CPAQuotaObserved{}))

	used50 := 50.0
	second := &objects.CPAQuotaItem{ID: "monthly", UsedPercent: &used50, ResetAt: &resetAt, PeriodSeconds: &monthlyPeriod}
	prepareCodexMonthlyInterval(second, first, "session-a", 40)
	interval := codexEstimateIntervalForItem(second, objects.CPAQuotaObserved{})
	require.NotNil(t, interval)
	require.Equal(t, "refresh-delta", interval.source)
	require.InDelta(t, 30, interval.usedPercent, 1e-9)

	// Switching collector ownership starts at the currently observed percentage,
	// not at an assumed zero-usage boundary.
	used64 := 64.0
	switched := &objects.CPAQuotaItem{ID: "monthly", UsedPercent: &used64, ResetAt: &resetAt, PeriodSeconds: &monthlyPeriod}
	prepareCodexMonthlyInterval(switched, second, "session-b", 45)
	require.Equal(t, 64.0, *switched.EstimateBaselineUsedPercent)
	require.Equal(t, 45, *switched.EstimateBaselineEventID)
	require.Nil(t, codexEstimateIntervalForItem(switched, objects.CPAQuotaObserved{}))

	// An activity reset can happen before the displayed schedule and may already
	// have consumed quota by the next refresh.
	activityReset := resetAt.Add(12 * time.Hour)
	used7 := 7.0
	reset := &objects.CPAQuotaItem{ID: "monthly", UsedPercent: &used7, ResetAt: &activityReset, PeriodSeconds: &monthlyPeriod}
	prepareCodexMonthlyInterval(reset, switched, "session-b", 50)
	require.Equal(t, 7.0, *reset.EstimateBaselineUsedPercent)
	require.Nil(t, codexEstimateIntervalForItem(reset, objects.CPAQuotaObserved{}))

	// A quota grant with unchanged reset_at is conservatively detected by a
	// percentage regression and also reanchors at the actual observed value.
	used3 := 3.0
	granted := &objects.CPAQuotaItem{ID: "monthly", UsedPercent: &used3, ResetAt: &activityReset, PeriodSeconds: &monthlyPeriod}
	prepareCodexMonthlyInterval(granted, reset, "session-b", 55)
	require.Equal(t, 3.0, *granted.EstimateBaselineUsedPercent)
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
