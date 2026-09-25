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
			svc := newCPAServiceForTest(client, time.Now)
			svc.pricingRepository.setForTest(map[string]*objects.ModelPrice{
				"gpt-5.2": {Items: []objects.ModelPriceItem{{
					ItemCode: objects.PriceItemCodeUsage,
					Pricing: objects.Pricing{
						Mode:         objects.PricingModeUsagePerUnit,
						UsagePerUnit: &unitPrice,
					},
				}}},
			}, time.Now())
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
			aggregates, err := svc.usageRepository.usageAggregatesByModel(
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
			cost, priced := computeCPAAggregateCost(svc.pricingRepository.snapshot(ctx), "gpt-5.2", aggregates["gpt-5.2"], time.Now())
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

func TestPrepareCodexWeeklyIntervalKeepsStableBaselineAcrossRefreshes(t *testing.T) {
	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	baselinePercent := 20.0
	baselineEventID := 10
	latestEventID := 20
	previous := &objects.CPAQuotaItem{
		ID:                          "weekly",
		ResetAt:                     &resetAt,
		PeriodSeconds:               &weeklyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
	}
	latestPercent := 35.0
	current := &objects.CPAQuotaItem{ID: "weekly", ResetAt: &resetAt, PeriodSeconds: &weeklyPeriod}
	observed := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "collector-a",
		SecondaryBaselineUsedPercent: &baselinePercent,
		SecondaryBaselineEventID:     &baselineEventID,
		SecondaryUsedPercent:         &latestPercent,
		SecondaryLatestEventID:       &latestEventID,
		SecondaryResetAt:             &resetAt,
	}
	require.False(t, prepareCodexWeeklyInterval(current, previous, observed, "collector-a"))
	require.Equal(t, "collector-a", current.EstimateCollectorSessionID)
	require.Equal(t, baselineEventID, *current.EstimateBaselineEventID)
	require.Equal(t, latestEventID, *current.EstimateLatestEventID)

	newBaselinePercent := 5.0
	newBaselineEventID := 30
	observed.SecondaryBaselineUsedPercent = &newBaselinePercent
	observed.SecondaryBaselineEventID = &newBaselineEventID
	observed.SecondaryUsedPercent = &newBaselinePercent
	observed.SecondaryLatestEventID = &newBaselineEventID
	require.True(t, prepareCodexWeeklyInterval(current, previous, observed, "collector-a"))
	require.Equal(t, newBaselineEventID, *current.EstimateBaselineEventID)
}

func TestPrepareCodexMonthlyIntervalReanchorsOnIdentityAndRandomReset(t *testing.T) {
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

func TestCarryForwardCodexQuotaEstimateKeepsCompatibleLastValue(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	previousUsed := 40.0
	previousLimit := 100.0
	previousCost := 40.0
	baselinePercent := 20.0
	baselineEventID := 10
	latestEventID := 20
	previous := &objects.CPAQuotaItem{
		ID:                          "weekly",
		Group:                       "Code",
		UsedPercent:                 &previousUsed,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &weeklyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
		EstimatedLimitUSD:           &previousLimit,
		EstimatedCostUSD:            &previousCost,
		EstimateSource:              "precise-header-delta",
	}
	currentUsed := 50.0
	newCurrent := func() *objects.CPAQuotaItem {
		return &objects.CPAQuotaItem{
			ID:                          "weekly",
			Group:                       "Code",
			UsedPercent:                 &currentUsed,
			ResetAt:                     &resetAt,
			PeriodSeconds:               &weeklyPeriod,
			EstimateCollectorSessionID:  "collector-a",
			EstimateBaselineUsedPercent: &baselinePercent,
			EstimateBaselineEventID:     &baselineEventID,
			EstimateLatestEventID:       &latestEventID,
		}
	}

	// Same window with the same recorded interval identity: keep the value.
	current := newCurrent()
	carryForwardCodexQuotaEstimate(current, previous, false)
	require.NotNil(t, current.EstimatedLimitUSD)
	require.NotNil(t, current.EstimatedCostUSD)
	require.Equal(t, previousLimit, *current.EstimatedLimitUSD)
	require.Equal(t, previousCost, *current.EstimatedCostUSD)
	require.Equal(t, previous.EstimateSource, current.EstimateSource)

	// A collector identity rotation re-anchors the interval, so the previous
	// identity's amount must not be inherited.
	rotated := newCurrent()
	rotated.EstimateCollectorSessionID = "collector-b"
	carryForwardCodexQuotaEstimate(rotated, previous, false)
	require.Nil(t, rotated.EstimatedLimitUSD)

	// An interval reported as re-anchored never inherits.
	reanchoredItem := newCurrent()
	carryForwardCodexQuotaEstimate(reanchoredItem, previous, true)
	require.Nil(t, reanchoredItem.EstimatedLimitUSD)

	// The interval end never moves backwards.
	rewound := newCurrent()
	shorterLatest := latestEventID - 1
	rewound.EstimateLatestEventID = &shorterLatest
	carryForwardCodexQuotaEstimate(rewound, previous, false)
	require.Nil(t, rewound.EstimatedLimitUSD)

	// A missing recorded interval end (legacy row) never inherits.
	withoutIntervalEnd := newCurrent()
	withoutIntervalEnd.EstimateLatestEventID = nil
	carryForwardCodexQuotaEstimate(withoutIntervalEnd, previous, false)
	require.Nil(t, withoutIntervalEnd.EstimatedLimitUSD)

	// A different window never inherits, even with identical interval metadata.
	movedReset := resetAt.Add(7 * 24 * time.Hour)
	moved := newCurrent()
	moved.ResetAt = &movedReset
	carryForwardCodexQuotaEstimate(moved, previous, false)
	require.Nil(t, moved.EstimatedLimitUSD)

	monthlyPeriod := 30 * 24 * 60 * 60
	monthlyBaseline := 20.0
	monthlyLatestEventID := 20
	monthlyPrevious := &objects.CPAQuotaItem{
		ID:                          "monthly",
		UsedPercent:                 &previousUsed,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &monthlyBaseline,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &monthlyLatestEventID,
		EstimatedLimitUSD:           &previousLimit,
		EstimatedCostUSD:            &previousCost,
	}
	monthlyCurrentUsed := 50.0
	monthlyCurrent := &objects.CPAQuotaItem{
		ID:                          "monthly",
		UsedPercent:                 &monthlyCurrentUsed,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &monthlyBaseline,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &monthlyLatestEventID,
	}
	carryForwardCodexQuotaEstimate(monthlyCurrent, monthlyPrevious, false)
	require.Equal(t, previousLimit, *monthlyCurrent.EstimatedLimitUSD)

	monthlyReanchored := &objects.CPAQuotaItem{
		ID:                          "monthly",
		UsedPercent:                 &monthlyCurrentUsed,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &monthlyBaseline,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &monthlyLatestEventID,
	}
	carryForwardCodexQuotaEstimate(monthlyReanchored, monthlyPrevious, true)
	require.Nil(t, monthlyReanchored.EstimatedLimitUSD)
}

func TestApplyQuotaEstimateKeepsWeeklyEstimateWhenFiveHourResets(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_estimate_preserve?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	period := cpaWeeklyPeriodSeconds
	baselinePercent := 20.0
	latestPercent := 50.0
	baselineEventID := 10
	latestEventID := 20
	observed := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "collector-a",
		SecondaryBaselineUsedPercent: &baselinePercent,
		SecondaryBaselineEventID:     &baselineEventID,
		SecondaryUsedPercent:         &latestPercent,
		SecondaryLatestEventID:       &latestEventID,
		SecondaryResetAt:             &resetAt,
	}
	previousLimit := 100.0
	previousCost := 40.0
	fiveHourPeriod := 5 * 60 * 60
	previousFiveHourReset := resetAt.Add(-time.Hour)
	previousFiveHourUsed := 80.0
	instance := client.CPAInstance.Create().
		SetName("preserve estimate").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SetEnabled(true).
		SetUsageStreamEnabled(true).
		SetUsageCollectorID("collector-a").
		SaveX(ctx)
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("codex.json").
		SetDisplayName("codex.json").
		SetProvider("codex").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaObserved(observed).
		SetQuotaData(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
			{
				ID:            "code-primary",
				UsedPercent:   &previousFiveHourUsed,
				ResetAt:       &previousFiveHourReset,
				PeriodSeconds: &fiveHourPeriod,
			},
			{
				ID:                          "code-secondary",
				ResetAt:                     &resetAt,
				PeriodSeconds:               &period,
				EstimateCollectorSessionID:  "collector-a",
				EstimateBaselineUsedPercent: &baselinePercent,
				EstimateBaselineEventID:     &baselineEventID,
				EstimateLatestEventID:       &latestEventID,
				EstimatedLimitUSD:           &previousLimit,
				EstimatedCostUSD:            &previousCost,
				EstimateSource:              "precise-header-delta",
			},
		}}).
		SaveX(ctx)

	svc := newCPAServiceForTest(client, time.Now)
	svc.usageCollectors = map[int]*usageCollectorWorker{
		instance.ID: {
			target:  usageCollectorTarget{instanceID: instance.ID, collectorID: "collector-a"},
			session: newUsageCollectorSession("collector-a", map[string]int{"auth-1": latestEventID}),
			done:    make(chan struct{}),
		},
	}
	loaded := client.CPACredential.GetX(ctx, credential.ID)
	currentFiveHourReset := previousFiveHourReset.Add(5 * time.Hour)
	currentFiveHourUsed := 0.0
	snapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{
			ID:            "code-primary",
			UsedPercent:   &currentFiveHourUsed,
			ResetAt:       &currentFiveHourReset,
			PeriodSeconds: &fiveHourPeriod,
		},
		{
			ID:            "code-secondary",
			UsedPercent:   &latestPercent,
			ResetAt:       &resetAt,
			PeriodSeconds: &period,
		},
	}}
	svc.applyQuotaEstimate(ctx, loaded, &snapshot)
	weekly := &snapshot.Items[1]
	require.Equal(t, previousLimit, *weekly.EstimatedLimitUSD)
	require.Equal(t, previousCost, *weekly.EstimatedCostUSD)
	require.Equal(t, "precise-header-delta", weekly.EstimateSource)
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
	svc := newCPAServiceForTest(client, func() time.Time { return fixedNow })

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

func TestEstimateCredentialQuotaUsesBuiltinModelPrice(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_builtin_estimate?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	fixedNow := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return fixedNow })
	instance, err := client.CPAInstance.Create().
		SetName("builtin-price-estimate").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	requestedAt := fixedNow.Add(-24 * time.Hour)
	baselineEvent, err := client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).
		SetAuthIndex("idx").
		SetProvider("codex").
		SetModel("gpt-5.6-sol").
		SetRequestedAt(requestedAt).
		Save(ctx)
	require.NoError(t, err)
	latestEvent, err := client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).
		SetAuthIndex("idx").
		SetProvider("codex").
		SetModel("gpt-5.6-sol").
		SetInputTokens(1_000_000).
		SetRequestedAt(requestedAt.Add(time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	snapshot := estimateTestSnapshot(6)
	baselinePercent := 1.0
	latestPercent := 6.0
	resetAt := *snapshot.Items[0].ResetAt
	observed := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "builtin-price-session",
		SecondaryBaselineUsedPercent: &baselinePercent,
		SecondaryBaselineEventID:     &baselineEvent.ID,
		SecondaryUsedPercent:         &latestPercent,
		SecondaryLatestEventID:       &latestEvent.ID,
		SecondaryResetAt:             &resetAt,
	}

	estimate := svc.EstimateCredentialQuota(ctx, instance.ID, "idx", snapshot, observed)
	require.NotNil(t, estimate)
	require.InDelta(t, 5, estimate.CostUSD, 1e-9)
	require.InDelta(t, 100, estimate.LimitUSD, 1e-9)
	require.InDelta(t, 5, estimate.UsedPercent, 1e-9)
}

func TestCodexEstimateSurvivesResetJitter(t *testing.T) {
	t.Parallel()
	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	observed, reanchored := advanceCodexWeeklyObservation(objects.CPAQuotaObserved{}, codexWeeklyObservation{
		collectorSessionID: "session-a",
		eventID:            100,
		usedPercent:        6.4,
		resetAt:            resetAt,
		observedAt:         resetAt.Add(-time.Hour),
	})
	require.True(t, reanchored)

	// The usage response headers and the quota snapshot report the same window
	// boundary with a few seconds of drift; none of it may restart the interval.
	eventID := 100
	for _, jitter := range []time.Duration{-4 * time.Minute, -30 * time.Second, 0, 30 * time.Second, 4 * time.Minute} {
		eventID++
		observed, reanchored = advanceCodexWeeklyObservation(observed, codexWeeklyObservation{
			collectorSessionID: "session-a",
			eventID:            eventID,
			usedPercent:        14.2,
			resetAt:            resetAt.Add(jitter),
			observedAt:         resetAt.Add(-30 * time.Minute),
		})
		require.False(t, reanchored, "reset drift %s must not re-anchor the interval", jitter)
		require.Equal(t, 100, *observed.SecondaryBaselineEventID)
		require.InDelta(t, 6.4, *observed.SecondaryBaselineUsedPercent, 1e-9)
		require.Equal(t, resetAt, *observed.SecondaryResetAt, "the window anchor must stay stable")
		require.Equal(t, eventID, *observed.SecondaryLatestEventID)
	}

	// The drifted snapshot reset still matches the anchored observation, so the
	// interval stays estimable.
	snapshotReset := resetAt.Add(-4 * time.Minute)
	used := 14.2
	item := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       &snapshotReset,
		PeriodSeconds: &weeklyPeriod,
	}
	interval := codexEstimateIntervalForItem(item, observed)
	require.NotNil(t, interval)
	require.Equal(t, "precise-header-delta", interval.source)
	require.Equal(t, 100, interval.fromEventID)
	require.Equal(t, eventID, interval.toEventID)
	require.InDelta(t, 7.8, interval.usedPercent, 1e-9)

	// The tolerance is inclusive: drift of exactly cpaQuotaResetTolerance is
	// still the same window, both for the observation and for the estimate.
	atBoundary := resetAt.Add(cpaQuotaResetTolerance)
	boundaryObserved, boundaryReanchored := advanceCodexWeeklyObservation(observed, codexWeeklyObservation{
		collectorSessionID: "session-a",
		eventID:            eventID + 1,
		usedPercent:        15.0,
		resetAt:            atBoundary,
		observedAt:         resetAt.Add(-time.Second),
	})
	require.False(t, boundaryReanchored, "drift of exactly the tolerance must not re-anchor")
	require.Equal(t, resetAt, *boundaryObserved.SecondaryResetAt, "the window anchor keeps the original boundary")
	boundaryItem := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       &atBoundary,
		PeriodSeconds: &weeklyPeriod,
	}
	require.NotNil(t, codexEstimateIntervalForItem(boundaryItem, boundaryObserved),
		"a snapshot reset at the tolerance boundary is still the same window")

	// One second past the tolerance is a new window: the interval re-anchors and
	// the estimate reports the reset mismatch instead of a delta from the old one.
	beyondBoundary := atBoundary.Add(time.Second)
	reanchoredObserved, beyondReanchored := advanceCodexWeeklyObservation(boundaryObserved, codexWeeklyObservation{
		collectorSessionID: "session-a",
		eventID:            eventID + 2,
		usedPercent:        1.2,
		resetAt:            beyondBoundary,
		observedAt:         resetAt.Add(-time.Second),
	})
	require.True(t, beyondReanchored)
	require.Equal(t, beyondBoundary, *reanchoredObserved.SecondaryResetAt, "beyond the tolerance the anchor moves")
	require.InDelta(t, 1.2, *reanchoredObserved.SecondaryBaselineUsedPercent, 1e-9)
	farItem := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       func() *time.Time { value := beyondBoundary.Add(cpaQuotaResetTolerance + time.Second); return &value }(),
		PeriodSeconds: &weeklyPeriod,
	}
	require.Equal(t, "reset-mismatch",
		codexEstimateIntervalDecisionForItem(farItem, reanchoredObserved).skipReason)
}

func TestPrepareCodexWeeklyIntervalReanchorsOnForeignObservation(t *testing.T) {
	t.Parallel()
	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	storedBaselinePercent := 20.0
	storedUsedPercent := 30.0
	storedBaselineEventID := 10
	storedLatestEventID := 20
	storedLimit := 100.0
	storedCost := 40.0
	previous := &objects.CPAQuotaItem{
		ID:                          "code-secondary",
		Group:                       "Code",
		UsedPercent:                 &storedUsedPercent,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &weeklyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &storedBaselinePercent,
		EstimateBaselineEventID:     &storedBaselineEventID,
		EstimateLatestEventID:       &storedLatestEventID,
		EstimatedLimitUSD:           &storedLimit,
		EstimatedCostUSD:            &storedCost,
		EstimateSource:              "precise-header-delta",
	}
	observedPercent := 34.0
	foreign := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "collector-a",
		SecondaryBaselineUsedPercent: &storedBaselinePercent,
		SecondaryBaselineEventID:     &storedBaselineEventID,
		SecondaryUsedPercent:         &observedPercent,
		SecondaryLatestEventID:       &storedLatestEventID,
		SecondaryResetAt:             &resetAt,
	}

	// The live collector does not own this observation, so it must not become the
	// window's interval.
	item := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &observedPercent,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	}
	require.True(t, prepareCodexWeeklyInterval(item, previous, foreign, "collector-b"))
	require.Empty(t, item.EstimateCollectorSessionID)
	require.Nil(t, item.EstimateBaselineEventID)
	require.Nil(t, item.EstimateLatestEventID)
	carryForwardCodexQuotaEstimate(item, previous, true)
	require.Nil(t, item.EstimatedLimitUSD)

	// A live collector whose observation belongs to another window boundary is
	// cleared the same way.
	shiftedReset := resetAt.Add(time.Hour)
	item = &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &observedPercent,
		ResetAt:       &shiftedReset,
		PeriodSeconds: &weeklyPeriod,
	}
	require.True(t, prepareCodexWeeklyInterval(item, previous, foreign, "collector-a"))
	require.Empty(t, item.EstimateCollectorSessionID)
	require.Nil(t, item.EstimateBaselineEventID)

	// The owning collector keeps the interval it recorded.
	item = &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &observedPercent,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	}
	require.False(t, prepareCodexWeeklyInterval(item, previous, foreign, "collector-a"))
	require.Equal(t, "collector-a", item.EstimateCollectorSessionID)
	require.Equal(t, storedBaselineEventID, *item.EstimateBaselineEventID)
	require.Equal(t, storedLatestEventID, *item.EstimateLatestEventID)
}

func TestCodexMonthlyIntervalAdoptsWhenCheckpointHasNoEvents(t *testing.T) {
	t.Parallel()
	monthlyPeriod := 30 * 24 * 60 * 60
	resetAt := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	storedBaselinePercent := 20.0
	storedUsedPercent := 25.0
	storedBaselineEventID := 10
	storedLatestEventID := 20
	storedLimit := 100.0
	storedCost := 40.0
	previous := &objects.CPAQuotaItem{
		ID:                          "code-primary",
		Group:                       "Code",
		UsedPercent:                 &storedUsedPercent,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &storedBaselinePercent,
		EstimateBaselineEventID:     &storedBaselineEventID,
		EstimateLatestEventID:       &storedLatestEventID,
		EstimatedLimitUSD:           &storedLimit,
		EstimatedCostUSD:            &storedCost,
		EstimateSource:              "refresh-delta",
	}
	currentUsed := 26.0
	newItem := func() *objects.CPAQuotaItem {
		return &objects.CPAQuotaItem{
			ID:            "code-primary",
			Group:         "Code",
			UsedPercent:   &currentUsed,
			ResetAt:       &resetAt,
			PeriodSeconds: &monthlyPeriod,
		}
	}

	// A live collector that has not persisted an event yet cannot anchor an
	// interval: event id 0 would count the whole cycle as the numerator while the
	// baseline percentage still starts at this refresh. The durable interval of
	// the same window is adopted instead, so the displayed value survives.
	item := newItem()
	require.False(t, prepareCodexMonthlyInterval(item, previous, "collector-a", 0))
	require.Equal(t, "collector-a", item.EstimateCollectorSessionID)
	require.Equal(t, storedBaselinePercent, *item.EstimateBaselineUsedPercent)
	require.Equal(t, storedBaselineEventID, *item.EstimateBaselineEventID)
	require.Equal(t, storedLatestEventID, *item.EstimateLatestEventID)
	carryForwardCodexQuotaEstimate(item, previous, false)
	require.Equal(t, storedLimit, *item.EstimatedLimitUSD)

	// A window that moved is still surrendered: retention must not outlive it.
	shiftedReset := resetAt.Add(30 * 24 * time.Hour)
	moved := &objects.CPAQuotaItem{
		ID:            "code-primary",
		Group:         "Code",
		UsedPercent:   &currentUsed,
		ResetAt:       &shiftedReset,
		PeriodSeconds: &monthlyPeriod,
	}
	require.True(t, prepareCodexMonthlyInterval(moved, previous, "collector-a", 0))
	require.Empty(t, moved.EstimateCollectorSessionID)
	require.Nil(t, moved.EstimateBaselineEventID)
	carryForwardCodexQuotaEstimate(moved, previous, true)
	require.Nil(t, moved.EstimatedLimitUSD)

	// A legacy anchor recorded at event id 0 is not a usable interval either: the
	// next refresh re-anchors at the current percentage and a real event.
	zeroEventID := 0
	legacyPrevious := &objects.CPAQuotaItem{
		ID:                          "code-primary",
		Group:                       "Code",
		UsedPercent:                 &storedUsedPercent,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &storedBaselinePercent,
		EstimateBaselineEventID:     &zeroEventID,
		EstimateLatestEventID:       &storedLatestEventID,
		EstimatedLimitUSD:           &storedLimit,
		EstimatedCostUSD:            &storedCost,
		EstimateSource:              "refresh-delta",
	}
	item = newItem()
	require.True(t, prepareCodexMonthlyInterval(item, legacyPrevious, "collector-a", 30))
	require.Equal(t, currentUsed, *item.EstimateBaselineUsedPercent)
	require.Equal(t, 30, *item.EstimateBaselineEventID)
	require.Equal(t, 30, *item.EstimateLatestEventID)
	carryForwardCodexQuotaEstimate(item, legacyPrevious, true)
	require.Nil(t, item.EstimatedLimitUSD)
}

func TestCodexEstimateClearsOnRealWindowReset(t *testing.T) {
	t.Parallel()
	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	monthlyPeriod := 30 * 24 * 60 * 60
	baselinePercent := 20.0
	latestPercent := 50.0
	baselineEventID := 10
	latestEventID := 20
	observed := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "session-a",
		SecondaryBaselineUsedPercent: &baselinePercent,
		SecondaryBaselineEventID:     &baselineEventID,
		SecondaryUsedPercent:         &latestPercent,
		SecondaryLatestEventID:       &latestEventID,
		SecondaryResetAt:             &resetAt,
	}
	limit := 100.0
	cost := 40.0
	previous := &objects.CPAQuotaItem{
		ID:                          "code-secondary",
		Group:                       "Code",
		UsedPercent:                 &latestPercent,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &weeklyPeriod,
		EstimateCollectorSessionID:  "session-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
		EstimatedLimitUSD:           &limit,
		EstimatedCostUSD:            &cost,
		EstimateSource:              "precise-header-delta",
	}

	// Drift wider than the tolerance is a real window change, and a fresh window
	// has no estimable interval yet.
	shiftedReset := resetAt.Add(7 * 24 * time.Hour)
	shiftedPercent := 1.5
	observed, reanchored := advanceCodexWeeklyObservation(observed, codexWeeklyObservation{
		collectorSessionID: "session-a",
		eventID:            21,
		usedPercent:        shiftedPercent,
		resetAt:            shiftedReset,
		observedAt:         resetAt.Add(-time.Minute),
	})
	require.True(t, reanchored)
	require.InDelta(t, shiftedPercent, *observed.SecondaryBaselineUsedPercent, 1e-9)
	require.Equal(t, shiftedReset, *observed.SecondaryResetAt)

	item := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &shiftedPercent,
		ResetAt:       &shiftedReset,
		PeriodSeconds: &weeklyPeriod,
	}
	require.True(t, prepareCodexWeeklyInterval(item, previous, observed, "session-a"))
	carryForwardCodexQuotaEstimate(item, previous, true)
	require.Nil(t, item.EstimatedLimitUSD)
	require.Nil(t, item.EstimatedCostUSD)
	require.Nil(t, codexEstimateIntervalForItem(item, observed),
		"a freshly reset window needs a three point delta before an estimate exists")

	// The monthly window drops its interval on a real reset in the same way.
	monthlyItem := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &shiftedPercent,
		ResetAt:       &shiftedReset,
		PeriodSeconds: &monthlyPeriod,
	}
	monthlyPrevious := &objects.CPAQuotaItem{
		ID:                          "code-secondary",
		Group:                       "Code",
		UsedPercent:                 &latestPercent,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "session-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
		EstimatedLimitUSD:           &limit,
		EstimatedCostUSD:            &cost,
	}
	require.True(t, prepareCodexMonthlyInterval(monthlyItem, monthlyPrevious, "session-a", 40))
	require.InDelta(t, shiftedPercent, *monthlyItem.EstimateBaselineUsedPercent, 1e-9)
	carryForwardCodexQuotaEstimate(monthlyItem, monthlyPrevious, true)
	require.Nil(t, monthlyItem.EstimatedLimitUSD)
	require.Nil(t, codexEstimateIntervalForItem(monthlyItem, objects.CPAQuotaObserved{}))
}

func TestCodexMonthlyIntervalSurvivesPercentRegression(t *testing.T) {
	t.Parallel()
	monthlyPeriod := 30 * 24 * 60 * 60
	resetAt := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	used20 := 20.0
	baselinePercent := 20.0
	baselineEventID := 10
	previousLatestEventID := 10
	previous := &objects.CPAQuotaItem{
		ID:                          "code-secondary",
		Group:                       "Code",
		UsedPercent:                 &used20,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "session-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &previousLatestEventID,
	}

	// One point of provider rounding must not restart a 30 day interval.
	rounded := 19.0
	item := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &rounded,
		ResetAt:       &resetAt,
		PeriodSeconds: &monthlyPeriod,
	}
	require.False(t, prepareCodexMonthlyInterval(item, previous, "session-a", 15))
	require.Equal(t, 20.0, *item.EstimateBaselineUsedPercent)
	require.Equal(t, 10, *item.EstimateBaselineEventID)
	require.Equal(t, 15, *item.EstimateLatestEventID)
	require.Equal(t, "insufficient-percent-delta",
		codexEstimateIntervalDecisionForItem(item, objects.CPAQuotaObserved{}).skipReason,
		"keeping the interval must not invent a smaller delta")

	// Once usage grows past the threshold, the preserved interval is used and it
	// still starts at the original baseline sample.
	recovered := 24.0
	recoveredItem := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &recovered,
		ResetAt:       &resetAt,
		PeriodSeconds: &monthlyPeriod,
	}
	require.False(t, prepareCodexMonthlyInterval(recoveredItem, item, "session-a", 30))
	interval := codexEstimateIntervalForItem(recoveredItem, objects.CPAQuotaObserved{})
	require.NotNil(t, interval)
	require.Equal(t, 10, interval.fromEventID)
	require.Equal(t, 30, interval.toEventID)
	require.InDelta(t, 4, interval.usedPercent, 1e-9)

	// A regression of a full threshold is a real grant and re-anchors.
	granted := 16.0
	grantedItem := &objects.CPAQuotaItem{
		ID:            "code-secondary",
		Group:         "Code",
		UsedPercent:   &granted,
		ResetAt:       &resetAt,
		PeriodSeconds: &monthlyPeriod,
	}
	require.True(t, prepareCodexMonthlyInterval(grantedItem, recoveredItem, "session-a", 35))
	require.Equal(t, granted, *grantedItem.EstimateBaselineUsedPercent)
	require.Equal(t, 35, *grantedItem.EstimateBaselineEventID)
}

func TestCodexEstimateKeepsIntervalWhenCollectorIsUnavailable(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_estimate_no_collector?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	monthlyPeriod := 30 * 24 * 60 * 60
	previousUsed := 50.0
	baselinePercent := 20.0
	baselineEventID := 10
	latestEventID := 20
	limit := 100.0
	cost := 40.0
	instance := client.CPAInstance.Create().
		SetName("no collector").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SetEnabled(true).
		SetUsageStreamEnabled(false).
		SaveX(ctx)
	weeklyItem := objects.CPAQuotaItem{
		ID:                          "code-secondary",
		Group:                       "Code",
		Label:                       "7 day",
		UsedPercent:                 &previousUsed,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &weeklyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
		EstimatedLimitUSD:           &limit,
		EstimatedCostUSD:            &cost,
		EstimateSource:              "precise-header-delta",
	}
	monthlyItem := objects.CPAQuotaItem{
		ID:                          "code-primary",
		Group:                       "Code",
		Label:                       "Monthly",
		UsedPercent:                 &previousUsed,
		ResetAt:                     &resetAt,
		PeriodSeconds:               &monthlyPeriod,
		EstimateCollectorSessionID:  "collector-a",
		EstimateBaselineUsedPercent: &baselinePercent,
		EstimateBaselineEventID:     &baselineEventID,
		EstimateLatestEventID:       &latestEventID,
		EstimatedLimitUSD:           &limit,
		EstimatedCostUSD:            &cost,
		EstimateSource:              "refresh-delta",
	}
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("codex.json").
		SetDisplayName("codex.json").
		SetProvider("codex").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaData(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{weeklyItem, monthlyItem}}).
		SaveX(ctx)

	svc := newCPAServiceForTest(client, time.Now)
	loaded := client.CPACredential.GetX(ctx, credential.ID)
	// The monthly window lost its percentage and no live collector checkpoint
	// exists, so quota_data is the only interval state for either window.
	monthlyWithoutPercent := monthlyItem
	monthlyWithoutPercent.UsedPercent = nil
	snapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{monthlyWithoutPercent, weeklyItem}}
	svc.applyQuotaEstimate(ctx, loaded, &snapshot)

	weekly := &snapshot.Items[1]
	require.Equal(t, "collector-a", weekly.EstimateCollectorSessionID)
	require.Equal(t, 20.0, *weekly.EstimateBaselineUsedPercent)
	require.Equal(t, 10, *weekly.EstimateBaselineEventID)
	require.Equal(t, 20, *weekly.EstimateLatestEventID)
	require.NotNil(t, weekly.EstimatedLimitUSD)
	require.Equal(t, limit, *weekly.EstimatedLimitUSD)
	require.Equal(t, cost, *weekly.EstimatedCostUSD)
	require.Equal(t, "precise-header-delta", weekly.EstimateSource)

	monthly := &snapshot.Items[0]
	require.Equal(t, "collector-a", monthly.EstimateCollectorSessionID)
	require.Equal(t, 20.0, *monthly.EstimateBaselineUsedPercent)
	require.Equal(t, 10, *monthly.EstimateBaselineEventID)
	require.Equal(t, 20, *monthly.EstimateLatestEventID)
	require.NotNil(t, monthly.EstimatedLimitUSD)
	require.Equal(t, limit, *monthly.EstimatedLimitUSD)
	require.Equal(t, cost, *monthly.EstimatedCostUSD)
	require.Equal(t, "refresh-delta", monthly.EstimateSource)

	// A window change while the collector is away still resets the interval and
	// drops the last value: retention must not outlive the window.
	movedReset := resetAt.Add(7 * 24 * time.Hour)
	movedUsed := 12.0
	moved := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{monthlyItem, weeklyItem}}
	for index := range moved.Items {
		// A refresh always persists a snapshot built by the adapter, which never
		// carries the previous estimate fields.
		moved.Items[index].ResetAt = &movedReset
		moved.Items[index].UsedPercent = &movedUsed
		moved.Items[index].EstimatedLimitUSD = nil
		moved.Items[index].EstimatedCostUSD = nil
		moved.Items[index].EstimateSource = ""
	}
	svc.applyQuotaEstimate(ctx, loaded, &moved)
	for _, item := range moved.Items {
		require.Empty(t, item.EstimateCollectorSessionID, "item %s", item.ID)
		require.Nil(t, item.EstimateBaselineUsedPercent, "item %s", item.ID)
		require.Nil(t, item.EstimateBaselineEventID, "item %s", item.ID)
		require.Nil(t, item.EstimateLatestEventID, "item %s", item.ID)
		require.Nil(t, item.EstimatedLimitUSD, "item %s", item.ID)
		require.Nil(t, item.EstimatedCostUSD, "item %s", item.ID)
	}
}

func TestCodexEstimatePersistsUntilWindowReset(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_estimate_persistence?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	period := cpaWeeklyPeriodSeconds
	svc := newCPAServiceForTest(client, time.Now)
	// Usage-per-unit prices are expressed per one million tokens; $1 per token
	// makes the interval cost equal the token count.
	unitPrice := decimal.NewFromInt(1_000_000)
	svc.pricingRepository.setForTest(map[string]*objects.ModelPrice{
		"gpt-5.2": {Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing: objects.Pricing{
				Mode:         objects.PricingModeUsagePerUnit,
				UsagePerUnit: &unitPrice,
			},
		}}},
	}, time.Now())

	instance := client.CPAInstance.Create().
		SetName("estimate-persistence").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SetEnabled(true).
		SetUsageStreamEnabled(true).
		SetUsageCollectorID("collector-a").
		SaveX(ctx)
	session := newUsageCollectorSession("collector-a", nil)
	svc.usageCollectors = map[int]*usageCollectorWorker{
		instance.ID: {
			target:  usageCollectorTarget{instanceID: instance.ID, collectorID: "collector-a"},
			session: session,
			done:    make(chan struct{}),
		},
	}

	observed := objects.CPAQuotaObserved{}
	credential := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("codex.json").
		SetDisplayName("codex.json").
		SetProvider("codex").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetQuotaObserved(observed).
		SaveX(ctx)

	eventIndex := 0
	usageEvent := func(tokens int64, cycleReset time.Time) int {
		eventIndex++
		requestedAt := cycleReset.Add(-24*time.Hour + time.Duration(eventIndex)*time.Minute)
		return client.CpaUsageEvent.Create().
			SetCpaInstanceID(instance.ID).
			SetAuthIndex("auth-1").
			SetProvider("codex").
			SetModel("gpt-5.2").
			SetInputTokens(tokens).
			SetRequestedAt(requestedAt).
			SaveX(ctx).ID
	}
	// The usage collector persists an event and advances the durable interval.
	collect := func(tokens int64, percent float64, sampleReset time.Time) int {
		eventID := usageEvent(tokens, sampleReset)
		advanced, _ := advanceCodexWeeklyObservation(observed, codexWeeklyObservation{
			collectorSessionID: "collector-a",
			eventID:            eventID,
			usedPercent:        percent,
			resetAt:            sampleReset,
			observedAt:         time.Now().UTC(),
		})
		observed = advanced
		client.CPACredential.UpdateOneID(credential.ID).SetQuotaObserved(observed).ExecX(ctx)
		session.recordPersisted("auth-1", eventID)
		return eventID
	}
	// A quota refresh writes the estimate back into quota_data.
	refresh := func(usedPercent *float64, windowReset time.Time) *objects.CPAQuotaItem {
		loaded := client.CPACredential.GetX(ctx, credential.ID)
		snapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
			ID:            "code-secondary",
			Group:         "Code",
			Label:         "7 day",
			UsedPercent:   usedPercent,
			ResetAt:       &windowReset,
			PeriodSeconds: &period,
		}}}
		svc.applyQuotaEstimate(ctx, loaded, &snapshot)
		client.CPACredential.UpdateOneID(credential.ID).SetQuotaData(snapshot).ExecX(ctx)
		return &snapshot.Items[0]
	}
	percent := func(value float64) *float64 { return &value }

	// (a) A window that has consumed four points of quota produces the first
	// estimate over the interval from the baseline sample to the latest one.
	usageEvent(500, resetAt)                       // recorded before the baseline, outside the interval
	baselineEventID := collect(500, 10.0, resetAt) // baseline sample
	collect(200, 14.0, resetAt)                    // interval end
	item := refresh(percent(14.0), resetAt)
	require.NotNil(t, item.EstimatedLimitUSD)
	require.InDelta(t, 5000, *item.EstimatedLimitUSD, 1e-9)
	require.InDelta(t, 200, *item.EstimatedCostUSD, 1e-9)
	require.Equal(t, "precise-header-delta", item.EstimateSource)

	// (a2) The interval advances with usage: both the denominator and the cost
	// grow, so the amount stays in the same range while the interval widens.
	intervalEndEventID := collect(400, 18.0, resetAt)
	item = refresh(percent(18.0), resetAt)
	require.NotNil(t, item.EstimatedLimitUSD)
	require.InDelta(t, 600, *item.EstimatedCostUSD, 1e-9)
	require.InDelta(t, 7500, *item.EstimatedLimitUSD, 1e-9)

	// (b1) A provider sample that regresses below the high-water percentage is
	// not a new window: the interval end stays on the high-water sample, so the
	// denominator does not stay frozen while new cost piles up.
	regressedEventID := collect(50, 15.7, resetAt)
	item = refresh(percent(18.0), resetAt)
	require.NotNil(t, item.EstimatedLimitUSD, "a regressed sample must not clear the estimate")
	require.InDelta(t, 7500, *item.EstimatedLimitUSD, 1e-9)
	require.InDelta(t, 600, *item.EstimatedCostUSD, 1e-9)
	require.Equal(t, baselineEventID, *item.EstimateBaselineEventID)
	require.Equal(t, intervalEndEventID, *item.EstimateLatestEventID,
		"the interval end must stay on the high-water sample")
	require.Equal(t, regressedEventID, *observed.SecondaryCheckpointEventID,
		"the regressed sample only advances the checkpoint")

	// The observation must survive the round trip through quota_observed: the
	// checkpoint carries the newest event while the interval end keeps the
	// high-water sample.
	persisted := client.CPACredential.GetX(ctx, credential.ID)
	require.NotNil(t, persisted.QuotaObserved.SecondaryCheckpointEventID)
	require.Equal(t, regressedEventID, *persisted.QuotaObserved.SecondaryCheckpointEventID)
	require.Equal(t, intervalEndEventID, *persisted.QuotaObserved.SecondaryLatestEventID)

	// (b2) Reset drift between the snapshot and the observation is the same
	// window, so nothing is re-anchored.
	item = refresh(percent(18.0), resetAt.Add(-4*time.Minute))
	require.NotNil(t, item.EstimatedLimitUSD, "reset drift must not clear the estimate")
	require.InDelta(t, 7500, *item.EstimatedLimitUSD, 1e-9)

	// (b3) A refresh without a live collector keeps the durable interval that
	// quota_data already holds instead of discarding it.
	client.CPAInstance.UpdateOneID(instance.ID).SetUsageStreamEnabled(false).ExecX(ctx)
	svc.usageCollectors = map[int]*usageCollectorWorker{}
	item = refresh(percent(18.0), resetAt)
	require.NotNil(t, item.EstimatedLimitUSD, "a missing collector checkpoint must not clear the estimate")
	require.InDelta(t, 7500, *item.EstimatedLimitUSD, 1e-9)
	require.Equal(t, "collector-a", item.EstimateCollectorSessionID)
	require.Equal(t, baselineEventID, *item.EstimateBaselineEventID)
	require.Equal(t, intervalEndEventID, *item.EstimateLatestEventID)

	// The live collector is back for the real window change below, so the reset is
	// handled by the live observation branch instead of by adoption.
	svc.usageCollectors = map[int]*usageCollectorWorker{
		instance.ID: {
			target:  usageCollectorTarget{instanceID: instance.ID, collectorID: "collector-a"},
			session: session,
			done:    make(chan struct{}),
		},
	}
	client.CPAInstance.UpdateOneID(instance.ID).SetUsageStreamEnabled(true).ExecX(ctx)

	// (c) A real window change clears the estimate until the new window has
	// consumed three points of quota.
	nextReset := resetAt.Add(7 * 24 * time.Hour)
	nextBaselineEventID := collect(900, 1.5, nextReset)
	item = refresh(percent(1.5), nextReset)
	require.Nil(t, item.EstimatedLimitUSD, "a freshly reset window has no estimable interval")
	require.Equal(t, "collector-a", item.EstimateCollectorSessionID)
	require.Equal(t, 1.5, *item.EstimateBaselineUsedPercent)
	require.Equal(t, nextBaselineEventID, *item.EstimateBaselineEventID)
	require.Equal(t, nextReset, *observed.SecondaryResetAt)

	// (d) The next window reaches the threshold and the estimate returns.
	collect(300, 4.5, nextReset)
	item = refresh(percent(4.5), nextReset)
	require.NotNil(t, item.EstimatedLimitUSD, "the next window estimate must reappear")
	require.InDelta(t, 10000, *item.EstimatedLimitUSD, 1e-9)
	require.InDelta(t, 300, *item.EstimatedCostUSD, 1e-9)
	require.Equal(t, nextBaselineEventID, *item.EstimateBaselineEventID)
}

func TestPreviousQuotaItemFollowsWindowAcrossSlots(t *testing.T) {
	t.Parallel()
	resetAt := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	weeklyPeriod := cpaWeeklyPeriodSeconds
	monthlyPeriod := 30 * 24 * 60 * 60
	used := 50.0
	weeklyLimit := 100.0
	displacedLimit := 111.0
	shiftedReset := resetAt.Add(time.Hour)
	snapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{
			ID:                "code-secondary",
			Group:             "Code",
			UsedPercent:       &used,
			ResetAt:           &resetAt,
			PeriodSeconds:     &weeklyPeriod,
			EstimatedLimitUSD: &weeklyLimit,
		},
		{
			ID:            "review-secondary",
			Group:         "Code review",
			UsedPercent:   &used,
			ResetAt:       &resetAt,
			PeriodSeconds: &weeklyPeriod,
		},
	}}

	// A window that moved to the other wire slot is still the same window.
	moved := &objects.CPAQuotaItem{
		ID:            "code-primary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	}
	previous := previousQuotaItem(snapshot, moved)
	require.NotNil(t, previous)
	require.Equal(t, "code-secondary", previous.ID)
	require.Equal(t, weeklyLimit, *previous.EstimatedLimitUSD)

	// The id wins among candidates that describe the same window.
	snapshot.Items = append(snapshot.Items, objects.CPAQuotaItem{
		ID:                "code-primary",
		Group:             "Code",
		UsedPercent:       &used,
		ResetAt:           &resetAt,
		PeriodSeconds:     &weeklyPeriod,
		EstimatedLimitUSD: &displacedLimit,
	})
	previous = previousQuotaItem(snapshot, moved)
	require.NotNil(t, previous)
	require.Equal(t, "code-primary", previous.ID)
	require.Equal(t, displacedLimit, *previous.EstimatedLimitUSD)

	// A different period, group or reset is a different window.
	require.Nil(t, previousQuotaItem(snapshot, &objects.CPAQuotaItem{
		ID:            "code-primary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       &resetAt,
		PeriodSeconds: &monthlyPeriod,
	}))
	require.Nil(t, previousQuotaItem(snapshot, &objects.CPAQuotaItem{
		ID:            "code-primary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       &shiftedReset,
		PeriodSeconds: &weeklyPeriod,
	}))
	review := previousQuotaItem(snapshot, &objects.CPAQuotaItem{
		ID:            "review-primary",
		Group:         "Code review",
		UsedPercent:   &used,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	})
	require.NotNil(t, review)
	require.Equal(t, "review-secondary", review.ID)

	// A renamed group label keeps the window for the same wire slot: additional
	// limits derive the label from a provider-supplied name while the slot id
	// stays positional.
	renamedSnapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
		ID:                "additional-1-primary",
		Group:             "Old label",
		UsedPercent:       &used,
		ResetAt:           &resetAt,
		PeriodSeconds:     &weeklyPeriod,
		EstimatedLimitUSD: &weeklyLimit,
	}}}
	renamed := previousQuotaItem(renamedSnapshot, &objects.CPAQuotaItem{
		ID:            "additional-1-primary",
		Group:         "New label",
		UsedPercent:   &used,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	})
	require.NotNil(t, renamed)
	require.Equal(t, weeklyLimit, *renamed.EstimatedLimitUSD)

	// A different slot id combined with a different group is not the same window.
	require.Nil(t, previousQuotaItem(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
		ID:            "code-primary",
		Group:         "Code",
		UsedPercent:   &used,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	}}}, &objects.CPAQuotaItem{
		ID:            "review-primary",
		Group:         "Code review",
		UsedPercent:   &used,
		ResetAt:       &resetAt,
		PeriodSeconds: &weeklyPeriod,
	}))
	require.Nil(t, previousQuotaItem(snapshot, nil))
}
