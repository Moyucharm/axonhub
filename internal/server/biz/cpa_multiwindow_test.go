package biz

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

func TestCodexWindowObservationsAdvanceIndependently(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	observed := objects.CPAQuotaObserved{}
	for _, period := range []int{cpaWeeklyPeriodSeconds, cpaFiveHourPeriodSeconds} {
		var changed bool
		observed, changed = advanceCodexWindowObservation(observed, codexWindowObservation{
			collectorSessionID: "collector-a", eventID: 10, periodSeconds: period,
			usedPercent: 12, resetAt: reset,
		})
		require.True(t, changed)
		observed, changed = advanceCodexWindowObservation(observed, codexWindowObservation{
			collectorSessionID: "collector-a", eventID: 11, periodSeconds: period,
			usedPercent: 15, resetAt: reset,
		})
		require.False(t, changed)
	}
	require.Len(t, observed.ObservedWindows(), 2)
	for _, period := range []int{cpaWeeklyPeriodSeconds, cpaFiveHourPeriodSeconds} {
		window, ok := observed.Window(period)
		require.True(t, ok)
		require.Equal(t, 10, *window.BaselineEventID)
		require.Equal(t, 11, *window.LatestEventID)
	}
	observed, changed := advanceCodexWindowObservation(observed, codexWindowObservation{
		collectorSessionID: "collector-a", eventID: 12, periodSeconds: cpaFiveHourPeriodSeconds,
		usedPercent: 1, resetAt: reset.Add(5 * time.Hour),
	})
	require.True(t, changed)
	fiveHour, _ := observed.Window(cpaFiveHourPeriodSeconds)
	weekly, _ := observed.Window(cpaWeeklyPeriodSeconds)
	require.Equal(t, 12, *fiveHour.BaselineEventID)
	require.Equal(t, 10, *weekly.BaselineEventID)
}

func TestCPAQuotaWindowContractsAndThresholds(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 9, 7, 3, 42, 28, 0, time.UTC)
	for _, tc := range []struct {
		provider string
		period   int
		precise  bool
	}{
		{"codex", cpaFiveHourPeriodSeconds, true},
		{"codex", cpaWeeklyPeriodSeconds, true},
		{"claude", cpaFiveHourPeriodSeconds, false},
		{"codex", 30 * 24 * 60 * 60, false},
	} {
		require.Equal(t, tc.precise, cpaPreciseWindowPeriod(tc.provider, tc.period))
	}
	for _, tc := range []struct {
		period int
		delta  float64
		pass   bool
	}{
		{cpaFiveHourPeriodSeconds, 2.0, true},
		{cpaFiveHourPeriodSeconds, 1.9, false},
		{cpaWeeklyPeriodSeconds, 2.9, false},
	} {
		baseline, latest := 10.0, 10.0+tc.delta
		from, to := 10, 11
		item := &objects.CPAQuotaItem{PeriodSeconds: &tc.period, ResetAt: &reset, UsedPercent: &latest}
		observed := objects.CPAQuotaObserved{Windows: []objects.CPAQuotaWindowObservation{{
			PeriodSeconds: tc.period, CollectorSessionID: "collector-a",
			BaselineUsedPercent: &baseline, UsedPercent: &latest,
			BaselineEventID: &from, LatestEventID: &to, ResetAt: &reset,
		}}}
		decision := codexEstimateIntervalDecisionForItem(item, observed, "codex")
		if tc.pass {
			require.NotNil(t, decision.interval)
			require.Equal(t, "precise-header-delta", decision.interval.source)
		} else {
			require.Equal(t, "insufficient-percent-delta", decision.skipReason)
		}
	}
}

func TestApplyQuotaEstimateEstimatesFiveHourAndWeeklyWindows(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_multiwindow_estimate?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	resetAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	unitPrice := decimal.NewFromInt(1_000_000)
	svc := newCPAServiceForTest(client, time.Now)
	svc.pricingRepository.setForTest(map[string]*objects.ModelPrice{
		"gpt-5.2": {Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &unitPrice},
		}}},
	}, time.Now())
	instance := client.CPAInstance.Create().SetName("multiwindow").SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").SetEnabled(true).SetUsageStreamEnabled(true).
		SetUsageCollectorID("collector-a").SaveX(ctx)
	session := newUsageCollectorSession("collector-a", nil)
	svc.usageCollectors = map[int]*usageCollectorWorker{instance.ID: {
		target:  usageCollectorTarget{instanceID: instance.ID, collectorID: "collector-a"},
		session: session, done: make(chan struct{}),
	}}
	credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").SetAuthIndex("auth-1").SetRemoteName("codex.json").
		SetDisplayName("codex.json").SetProvider("codex").SaveX(ctx)
	for index, percents := range [][2]string{{"10", "20"}, {"15", "24"}} {
		fmtReset := strconv.FormatInt(resetAt.Unix(), 10)
		event := &cpaclient.UsageEvent{
			Timestamp: resetAt.Add(-time.Hour).Add(time.Duration(index) * time.Minute),
			AuthIndex: "auth-1", Provider: "codex", Model: "gpt-5.2",
			Tokens: cpaclient.UsageEventTokens{InputTokens: 5},
			ResponseHeaders: map[string]any{
				"x-codex-primary-used-percent":   percents[0],
				"x-codex-primary-window-minutes": "300", "x-codex-primary-reset-at": fmtReset,
				"x-codex-secondary-used-percent":   percents[1],
				"x-codex-secondary-window-minutes": "10080", "x-codex-secondary-reset-at": fmtReset,
			},
		}
		require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{{instanceID: instance.ID, session: session, event: event}}))
	}
	loaded := client.CPACredential.GetX(ctx, credential.ID)
	require.Len(t, loaded.QuotaObserved.ObservedWindows(), 2)
	fiveHours, weekly := cpaFiveHourPeriodSeconds, cpaWeeklyPeriodSeconds
	primaryUsed, secondaryUsed := 15.0, 24.0
	snapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "codex-primary", Group: "Code", Label: "5 hour", UsedPercent: &primaryUsed, ResetAt: &resetAt, PeriodSeconds: &fiveHours},
		{ID: "codex-secondary", Group: "Code", Label: "7 day", UsedPercent: &secondaryUsed, ResetAt: &resetAt, PeriodSeconds: &weekly},
	}}
	svc.applyQuotaEstimate(ctx, loaded, &snapshot)
	for index, want := range []float64{100, 125} {
		item := snapshot.Items[index]
		require.NotNil(t, item.EstimatedLimitUSD, "item %s", item.ID)
		require.InDelta(t, want, *item.EstimatedLimitUSD, 1e-9)
		require.Equal(t, "precise-header-delta", item.EstimateSource)
	}
	currentObserved := loaded.QuotaObserved
	weeklyWindow, ok := currentObserved.Window(weekly)
	require.True(t, ok)
	legacyObserved := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  weeklyWindow.CollectorSessionID,
		SecondaryBaselineUsedPercent: weeklyWindow.BaselineUsedPercent,
		SecondaryBaselineEventID:     weeklyWindow.BaselineEventID,
		SecondaryUsedPercent:         weeklyWindow.UsedPercent,
		SecondaryLatestEventID:       weeklyWindow.LatestEventID,
		SecondaryResetAt:             weeklyWindow.ResetAt,
	}
	client.CPACredential.UpdateOneID(credential.ID).SetQuotaObserved(legacyObserved).ExecX(ctx)
	legacyRow := client.CPACredential.GetX(ctx, credential.ID)
	require.Empty(t, legacyRow.QuotaObserved.Windows)
	legacySnapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
		ID: "codex-secondary", Group: "Code", Label: "7 day",
		UsedPercent: &secondaryUsed, ResetAt: &resetAt, PeriodSeconds: &weekly,
	}}}
	svc.applyQuotaEstimate(ctx, legacyRow, &legacySnapshot)
	require.NotNil(t, legacySnapshot.Items[0].EstimatedLimitUSD)
	require.InDelta(t, *snapshot.Items[1].EstimatedLimitUSD, *legacySnapshot.Items[0].EstimatedLimitUSD, 1e-9)
	client.CPACredential.UpdateOneID(credential.ID).SetQuotaObserved(currentObserved).ExecX(ctx)
	client.CPACredential.UpdateOneID(credential.ID).SetQuotaData(snapshot).ExecX(ctx)
	nextFiveHourReset := resetAt.Add(5 * time.Hour)
	resetEvent := &cpaclient.UsageEvent{
		Timestamp: resetAt.Add(-30 * time.Minute), AuthIndex: "auth-1",
		Provider: "codex", Model: "gpt-5.2",
		Tokens: cpaclient.UsageEventTokens{InputTokens: 5},
		ResponseHeaders: map[string]any{
			"x-codex-primary-used-percent": "1", "x-codex-primary-window-minutes": "300",
			"x-codex-primary-reset-at":       strconv.FormatInt(nextFiveHourReset.Unix(), 10),
			"x-codex-secondary-used-percent": "24", "x-codex-secondary-window-minutes": "10080",
			"x-codex-secondary-reset-at": strconv.FormatInt(resetAt.Unix(), 10),
		},
	}
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{{instanceID: instance.ID, session: session, event: resetEvent}}))
	loaded = client.CPACredential.GetX(ctx, credential.ID)
	newFiveHourUsed := 1.0
	resetSnapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
		{ID: "codex-primary", Group: "Code", Label: "5 hour", UsedPercent: &newFiveHourUsed, ResetAt: &nextFiveHourReset, PeriodSeconds: &fiveHours},
		{ID: "codex-secondary", Group: "Code", Label: "7 day", UsedPercent: &secondaryUsed, ResetAt: &resetAt, PeriodSeconds: &weekly},
	}}
	svc.applyQuotaEstimate(ctx, loaded, &resetSnapshot)
	require.Nil(t, resetSnapshot.Items[0].EstimatedLimitUSD)
	require.NotNil(t, resetSnapshot.Items[1].EstimatedLimitUSD)
}

func TestApplyQuotaEstimateEstimatesClaudeWindows(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_claude_refresh_estimate?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	now := time.Now().UTC().Truncate(time.Second)
	unitPrice := decimal.NewFromInt(1_000_000)
	svc := newCPAServiceForTest(client, time.Now)
	svc.pricingRepository.setForTest(map[string]*objects.ModelPrice{
		"claude-sonnet": {Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &unitPrice},
		}}},
	}, time.Now())
	instance := client.CPAInstance.Create().SetName("claude-refresh").SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").SetEnabled(true).SetUsageStreamEnabled(true).
		SetUsageCollectorID("collector-a").SaveX(ctx)
	session := newUsageCollectorSession("collector-a", nil)
	svc.usageCollectors = map[int]*usageCollectorWorker{instance.ID: {
		target:  usageCollectorTarget{instanceID: instance.ID, collectorID: "collector-a"},
		session: session, done: make(chan struct{}),
	}}
	credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").SetAuthIndex("auth-1").SetRemoteName("claude.json").
		SetDisplayName("claude.json").SetProvider("claude").SaveX(ctx)
	fiveHours, weekly := cpaFiveHourPeriodSeconds, cpaWeeklyPeriodSeconds
	fiveHourReset, weeklyReset := now.Add(2*time.Hour), now.Add(6*24*time.Hour)
	for index, percents := range [][2]float64{{10, 20}, {14, 24}} {
		event := &cpaclient.UsageEvent{
			Timestamp: now.Add(-time.Hour).Add(time.Duration(index) * time.Minute),
			AuthIndex: "auth-1", Provider: "claude", Model: "claude-sonnet",
			Tokens: cpaclient.UsageEventTokens{InputTokens: 5},
		}
		require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{{instanceID: instance.ID, session: session, event: event}}))
		loaded := client.CPACredential.GetX(ctx, credential.ID)
		require.Empty(t, loaded.QuotaObserved.ObservedWindows())
		fiveHourUsed, weeklyUsed := percents[0], percents[1]
		snapshot := objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
			{ID: "five-hour", Label: "5 hour", UsedPercent: &fiveHourUsed, ResetAt: &fiveHourReset, PeriodSeconds: &fiveHours},
			{ID: "seven-day", Label: "7 day", UsedPercent: &weeklyUsed, ResetAt: &weeklyReset, PeriodSeconds: &weekly},
		}}
		svc.applyQuotaEstimate(ctx, loaded, &snapshot)
		if index == 0 {
			for _, item := range snapshot.Items {
				require.Nil(t, item.EstimatedLimitUSD)
			}
		} else {
			for _, item := range snapshot.Items {
				require.NotNil(t, item.EstimatedLimitUSD)
				require.InDelta(t, 125, *item.EstimatedLimitUSD, 1e-9)
				require.Equal(t, "refresh-delta", item.EstimateSource)
			}
		}
		client.CPACredential.UpdateOneID(credential.ID).SetQuotaData(snapshot).ExecX(ctx)
	}
	proxy := &cpaTestManagementClient{callProvider: func(_ context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		if strings.HasSuffix(call.URL, "/oauth/usage") {
			body := fmt.Sprintf(`{"five_hour":{"utilization":14,"resets_at":%q},"seven_day":{"utilization":24,"resets_at":%q}}`,
				fiveHourReset.Format(time.RFC3339), weeklyReset.Format(time.RFC3339))
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(body)}, nil
		}
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{}`)}, nil
	}}
	executor := newCPAQuotaExecutor(cpaclient.NewQuotaRegistry(), svc.applyQuotaEstimate, time.Now)
	outcome := executor.execute(ctx, proxy, client.CPACredential.GetX(ctx, credential.ID))
	require.Equal(t, cpaQuotaExecutionSuccess, outcome.status)
	require.Len(t, outcome.snapshot.Items, 2)
	for _, item := range outcome.snapshot.Items {
		require.NotNil(t, item.EstimatedLimitUSD)
		require.Equal(t, "refresh-delta", item.EstimateSource)
	}
}
