package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestUsageCollectorDrainsPendingBatchAfterCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/usage-queue" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[{"auth_index":"auth-1","provider":"codex","model":"gpt-5.2","tokens":{"input_tokens":10,"output_tokens":5}}]`))
	}))
	defer server.Close()

	firstAttempt := make(chan struct{})
	drained := make(chan struct{})
	var attempts atomic.Int32
	svc := &CPAService{
		usageRepository: &cpaUsageRepository{persistHook: func(_ context.Context, batch []usageEventEnvelope) bool {
			if len(batch) != 1 || batch[0].instanceID != 7 || batch[0].event.AuthIndex != "auth-1" {
				t.Errorf("unexpected batch: %#v", batch)
				return false
			}
			switch attempts.Add(1) {
			case 1:
				close(firstAttempt)
				return false
			case 2:
				close(drained)
				return true
			default:
				return true
			}
		}},
	}
	collectorCtx, cancel := context.WithCancel(context.Background())
	worker := &usageCollectorWorker{
		target:  usageCollectorTarget{instanceID: 7, baseURL: server.URL, managementKey: "secret"},
		session: &usageCollectorSession{id: "session-drain"},
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go svc.runUsageQueueCollector(collectorCtx, worker)

	select {
	case <-firstAttempt:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not attempt persistence")
	}
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not drain pending batch after cancellation")
	}
	select {
	case <-worker.done:
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not stop after draining")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("persist attempts = %d, want 2", got)
	}
}

func TestParseCodexWindowObservationsUsesReportedDuration(t *testing.T) {
	t.Parallel()
	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	event := func(headers map[string]any) *cpaclient.UsageEvent {
		return &cpaclient.UsageEvent{ResponseHeaders: headers}
	}
	primary := parseCodexWindowObservations("session-a", 10, event(map[string]any{
		"x-codex-primary-used-percent": "14", "x-codex-primary-window-minutes": "10080",
		"x-codex-primary-reset-at": strconv.FormatInt(resetAt.Unix(), 10),
	}))
	require.Len(t, primary, 1)
	require.Equal(t, cpaWeeklyPeriodSeconds, primary[0].periodSeconds)
	require.True(t, primary[0].durationVerified)

	both := parseCodexWindowObservations("session-a", 11, event(map[string]any{
		"x-codex-primary-used-percent": "40", "x-codex-primary-window-minutes": "300",
		"x-codex-primary-reset-at":       strconv.FormatInt(resetAt.Add(-time.Hour).Unix(), 10),
		"x-codex-secondary-used-percent": "15", "x-codex-secondary-window-minutes": "10080",
		"x-codex-secondary-reset-at": strconv.FormatInt(resetAt.Unix(), 10),
	}))
	require.Len(t, both, 2)
	require.Equal(t, cpaFiveHourPeriodSeconds, both[0].periodSeconds)
	require.Equal(t, cpaWeeklyPeriodSeconds, both[1].periodSeconds)
	require.True(t, both[0].durationVerified)
	require.True(t, both[1].durationVerified)

	legacy := parseCodexWindowObservations("session-a", 13, event(map[string]any{
		"x-codex-secondary-used-percent": "16",
		"x-codex-secondary-reset-at":     strconv.FormatInt(resetAt.Unix(), 10),
	}))
	require.Len(t, legacy, 1)
	require.Equal(t, cpaWeeklyPeriodSeconds, legacy[0].periodSeconds)
	require.False(t, legacy[0].durationVerified)
	require.Empty(t, parseCodexWindowObservations("session-a", 14, event(map[string]any{
		"x-codex-primary-used-percent": "16", "x-codex-primary-window-minutes": "60",
		"x-codex-primary-reset-at": strconv.FormatInt(resetAt.Unix(), 10),
	})))
}

func TestPersistUsageEventsTracksCodexCollectorInterval(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_interval?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance, err := client.CPAInstance.Create().
		SetName("usage-interval").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)
	credential, err := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("codex.json").
		SetDisplayName("codex.json").
		SetProvider("codex").
		Save(ctx)
	require.NoError(t, err)

	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	eventForSlot := func(slot, percent string, offset time.Duration) *cpaclient.UsageEvent {
		prefix := "x-codex-" + slot + "-"
		return &cpaclient.UsageEvent{
			Timestamp: resetAt.Add(-time.Hour + offset),
			AuthIndex: "auth-1",
			Provider:  "codex",
			Model:     "gpt-5.2",
			Tokens:    cpaclient.UsageEventTokens{InputTokens: 10},
			ResponseHeaders: map[string]any{
				prefix + "used-percent":   percent,
				prefix + "window-minutes": "10080",
				prefix + "reset-at":       strconv.FormatInt(resetAt.Unix(), 10),
			},
		}
	}
	event := func(percent string, offset time.Duration) *cpaclient.UsageEvent {
		return eventForSlot("secondary", percent, offset)
	}
	// The collector throttles redundant observation writes to one per minute, so
	// the regression sample below needs a clock that has moved on.
	currentTime := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := newCPAServiceForTest(client, func() time.Time { return currentTime })
	sessionA := &usageCollectorSession{id: "session-a"}
	sessionB := &usageCollectorSession{id: "session-b"}
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionA, event: event("6.4", 0)},
		{instanceID: instance.ID, session: sessionA, event: event("10.2", time.Minute)},
	}))
	loaded, err := client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Equal(t, "session-a", loaded.QuotaObserved.ObservedWindows()[0].CollectorSessionID)
	require.InDelta(t, 6.4, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 10.2, *loaded.QuotaObserved.ObservedWindows()[0].UsedPercent, 1e-9)
	require.Less(t, *loaded.QuotaObserved.ObservedWindows()[0].BaselineEventID, *loaded.QuotaObserved.ObservedWindows()[0].LatestEventID)

	// A weekly-only account can expose the same 7d window in the primary wire
	// slot. Duration normalization must advance the existing weekly interval.
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionA, event: eventForSlot("primary", "12.0", 90*time.Second)},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.InDelta(t, 6.4, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 12.0, *loaded.QuotaObserved.ObservedWindows()[0].UsedPercent, 1e-9)

	_, sessionALatest := sessionA.checkpoint("auth-1")
	require.Equal(t, *loaded.QuotaObserved.ObservedWindows()[0].CheckpointEventID, sessionALatest)

	// A process restart recreates the in-memory session but reuses the
	// persisted collector identity, so the local interval continues.
	sessionARestart := &usageCollectorSession{id: "session-a"}
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionARestart, event: event("15.3", 2*time.Minute)},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Equal(t, "session-a", loaded.QuotaObserved.ObservedWindows()[0].CollectorSessionID)
	require.InDelta(t, 6.4, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 15.3, *loaded.QuotaObserved.ObservedWindows()[0].UsedPercent, 1e-9)
	require.Less(t, *loaded.QuotaObserved.ObservedWindows()[0].BaselineEventID, *loaded.QuotaObserved.ObservedWindows()[0].LatestEventID)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	require.False(t, svc.persistUsageEvents(canceledCtx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionA, event: event("11.0", 90*time.Second)},
	}))
	_, checkpointAfterFailure := sessionA.checkpoint("auth-1")
	require.Equal(t, sessionALatest, checkpointAfterFailure)

	// Production resuming after a development test creates a new local interval
	// even when the upstream cumulative percentage is much higher.
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionB, event: event("61.7", 2*time.Minute)},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Equal(t, "session-b", loaded.QuotaObserved.ObservedWindows()[0].CollectorSessionID)
	require.InDelta(t, 61.7, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.Equal(t, *loaded.QuotaObserved.ObservedWindows()[0].BaselineEventID, *loaded.QuotaObserved.ObservedWindows()[0].LatestEventID)

	// A same-reset lower percentage is a non-monotonic sample, not proof that
	// the 7d window reset. Keep the local high-water baseline so a 5h reset
	// cannot clear the 7d estimate.
	highWaterEventID := *loaded.QuotaObserved.ObservedWindows()[0].LatestEventID
	currentTime = currentTime.Add(2 * time.Minute)
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionB, event: event("4.8", 3*time.Minute)},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.InDelta(t, 61.7, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 61.7, *loaded.QuotaObserved.ObservedWindows()[0].UsedPercent, 1e-9)
	_, sessionBLatest := sessionB.checkpoint("auth-1")
	require.Equal(t, sessionBLatest, *loaded.QuotaObserved.ObservedWindows()[0].CheckpointEventID)
	require.Equal(t, highWaterEventID, *loaded.QuotaObserved.ObservedWindows()[0].LatestEventID,
		"the persisted interval end must stay on the high-water sample")
	require.Less(t, *loaded.QuotaObserved.ObservedWindows()[0].LatestEventID, *loaded.QuotaObserved.ObservedWindows()[0].CheckpointEventID)
}

func TestAdvanceCodexWeeklyObservationUpgradesLegacyCheckpoint(t *testing.T) {
	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	baselinePercent := 6.4
	highWaterPercent := 9.8
	baselineEventID := 10
	highWaterEventID := 15
	// A row written before secondary_checkpoint_event_id existed stored the
	// newest event in SecondaryLatestEventID.
	observed := objects.CPAQuotaObserved{
		SecondaryCollectorSessionID:  "session-a",
		SecondaryBaselineUsedPercent: &baselinePercent,
		SecondaryBaselineEventID:     &baselineEventID,
		SecondaryUsedPercent:         &highWaterPercent,
		SecondaryLatestEventID:       &highWaterEventID,
		SecondaryResetAt:             &resetAt,
	}

	// The newest event of that legacy row is already recorded, so replaying it
	// must not change the interval at all.
	replayed, replayedReanchored := advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-a", periodSeconds: cpaWeeklyPeriodSeconds, eventID: highWaterEventID,
		usedPercent: highWaterPercent,
		resetAt:     resetAt,
		observedAt:  resetAt.Add(-time.Minute)})
	require.False(t, replayedReanchored)
	require.Nil(t, replayed.SecondaryCheckpointEventID, "a replayed legacy event must not be recorded")
	require.Equal(t, highWaterEventID, *replayed.ObservedWindows()[0].LatestEventID)

	// A newer sample with a lower percentage advances only the checkpoint.
	regressedEventID := highWaterEventID + 1
	regressed := 4.2
	advanced, advancedReanchored := advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-a", periodSeconds: cpaWeeklyPeriodSeconds, eventID: regressedEventID,
		usedPercent: regressed,
		resetAt:     resetAt,
		observedAt:  resetAt.Add(-30 * time.Second)})
	require.False(t, advancedReanchored)
	require.Equal(t, regressedEventID, *advanced.ObservedWindows()[0].CheckpointEventID)
	require.Equal(t, highWaterEventID, *advanced.ObservedWindows()[0].LatestEventID)
	require.InDelta(t, highWaterPercent, *advanced.ObservedWindows()[0].UsedPercent, 1e-9)
}

func TestUsageCollectorCheckpointIsSessionAndCredentialScoped(t *testing.T) {
	ctx := context.Background()
	sessionA := &usageCollectorSession{id: "session-a"}
	sessionA.recordPersisted("auth-a", 10)
	sessionA.recordPersisted("auth-b", 20)
	workerA := &usageCollectorWorker{
		target:  usageCollectorTarget{instanceID: 1},
		session: sessionA,
		done:    make(chan struct{}),
	}
	svc := &CPAService{usageCollectors: map[int]*usageCollectorWorker{1: workerA}}

	sessionID, eventID := svc.usageCollectorCheckpoint(ctx, 1, " auth-a ")
	require.Equal(t, "session-a", sessionID)
	require.Equal(t, 10, eventID)
	_, eventID = svc.usageCollectorCheckpoint(ctx, 1, "auth-b")
	require.Equal(t, 20, eventID)

	sessionB := &usageCollectorSession{id: "session-b"}
	sessionB.recordPersisted("auth-a", 30)
	svc.usageCollectorMu.Lock()
	svc.usageCollectors[1] = &usageCollectorWorker{
		target:  usageCollectorTarget{instanceID: 1},
		session: sessionB,
		done:    make(chan struct{}),
	}
	svc.usageCollectorMu.Unlock()

	sessionID, eventID = svc.usageCollectorCheckpoint(ctx, 1, "auth-a")
	require.Equal(t, "session-b", sessionID)
	require.Equal(t, 30, eventID)
}

func TestAdvanceCodexWeeklyObservationMaintainsWindowBoundaries(t *testing.T) {
	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	observed, reanchored := advanceCodexWindowObservation(objects.CPAQuotaObserved{}, codexWindowObservation{collectorSessionID: "session-a", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 10,
		usedPercent: 6.4,
		resetAt:     resetAt,
		observedAt:  resetAt.Add(-time.Hour)})
	require.True(t, reanchored)
	require.InDelta(t, 6.4, *observed.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.Equal(t, 10, *observed.ObservedWindows()[0].BaselineEventID)
	require.Equal(t, 10, *observed.ObservedWindows()[0].CheckpointEventID)

	observed, reanchored = advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-a", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 11,
		usedPercent: 9.5,
		resetAt:     resetAt,
		observedAt:  resetAt.Add(-30 * time.Minute)})
	require.False(t, reanchored)
	require.InDelta(t, 6.4, *observed.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 9.5, *observed.ObservedWindows()[0].UsedPercent, 1e-9)

	// A different collector (development/production handoff) always starts a
	// fresh local interval at its first actual observation.
	observed, reanchored = advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-b", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 20,
		usedPercent: 61.0,
		resetAt:     resetAt,
		observedAt:  resetAt.Add(-20 * time.Minute)})
	require.True(t, reanchored)
	require.InDelta(t, 61, *observed.ObservedWindows()[0].BaselineUsedPercent, 1e-9)

	// Codex can reset early. A changed reset_at reanchors even though the first
	// observed usage in the new window is already non-zero.
	activityReset := resetAt.Add(24 * time.Hour)
	observed, reanchored = advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-b", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 21,
		usedPercent: 7.2,
		resetAt:     activityReset,
		observedAt:  resetAt.Add(-10 * time.Minute)})
	require.True(t, reanchored)
	require.InDelta(t, 7.2, *observed.ObservedWindows()[0].BaselineUsedPercent, 1e-9)

	// A same-reset lower percentage is a non-monotonic sample, not a new
	// interval. Keep the high-water observation while advancing the checkpoint.
	observed, reanchored = advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-b", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 22,
		usedPercent: 3.1,
		resetAt:     activityReset,
		observedAt:  resetAt.Add(-5 * time.Minute)})
	require.False(t, reanchored)
	require.InDelta(t, 7.2, *observed.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 7.2, *observed.ObservedWindows()[0].UsedPercent, 1e-9)
	require.Equal(t, 21, *observed.ObservedWindows()[0].LatestEventID,
		"the interval end must stay on the sample that produced the high-water percentage")
	require.Equal(t, 22, *observed.ObservedWindows()[0].CheckpointEventID)

	// A replayed or out-of-order event is behind the checkpoint and must not
	// move either boundary.
	observed, reanchored = advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-b", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 21,
		usedPercent: 90.0,
		resetAt:     activityReset,
		observedAt:  resetAt.Add(-4 * time.Minute)})
	require.False(t, reanchored)
	require.InDelta(t, 7.2, *observed.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.Equal(t, 21, *observed.ObservedWindows()[0].LatestEventID)
	require.Equal(t, 22, *observed.ObservedWindows()[0].CheckpointEventID)

	// Provider percentages are rounded, so an equal sample still extends the
	// interval end while the checkpoint keeps moving.
	observed, reanchored = advanceCodexWindowObservation(observed, codexWindowObservation{collectorSessionID: "session-b", periodSeconds: cpaWeeklyPeriodSeconds, eventID: 23,
		usedPercent: 7.2,
		resetAt:     activityReset,
		observedAt:  resetAt.Add(-3 * time.Minute)})
	require.False(t, reanchored)
	require.Equal(t, 23, *observed.ObservedWindows()[0].LatestEventID)
	require.Equal(t, 23, *observed.ObservedWindows()[0].CheckpointEventID)
}

func TestLegacyCodexWeeklySignalRequiresSnapshotConfirmation(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_legacy_weekly?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance, err := client.CPAInstance.Create().
		SetName("legacy-weekly").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)

	weeklyReset := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	fiveHourReset := weeklyReset.Add(-time.Hour)
	fiveHourPeriod := 5 * 60 * 60
	weeklyPeriod := cpaWeeklyPeriodSeconds
	fiveHourUsed := 80.0
	weeklyUsed := 16.0
	credential, err := client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("credential-1").
		SetAuthIndex("auth-1").
		SetRemoteName("codex.json").
		SetDisplayName("codex.json").
		SetProvider("codex").
		SetQuotaData(objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
			{
				ID:            "code-primary",
				Group:         "Code",
				UsedPercent:   &fiveHourUsed,
				ResetAt:       &fiveHourReset,
				PeriodSeconds: &fiveHourPeriod,
			},
			{
				ID:            "code-secondary",
				Group:         "Code",
				UsedPercent:   &weeklyUsed,
				ResetAt:       &weeklyReset,
				PeriodSeconds: &weeklyPeriod,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)

	svc := newCPAServiceForTest(client, nil)
	session := newUsageCollectorSession("session-a", nil)
	legacy := func(percent string, resetAt time.Time, offset time.Duration) *cpaclient.UsageEvent {
		return &cpaclient.UsageEvent{
			Timestamp: weeklyReset.Add(-time.Hour + offset),
			AuthIndex: "auth-1",
			Provider:  "codex",
			Model:     "gpt-5.2",
			Tokens:    cpaclient.UsageEventTokens{InputTokens: 10},
			ResponseHeaders: map[string]any{
				"x-codex-secondary-used-percent": percent,
				"x-codex-secondary-reset-at":     strconv.FormatInt(resetAt.Unix(), 10),
			},
		}
	}

	// The duration-less shape cannot tell the 5h window from the 7d window, so
	// the snapshot decides: this sample belongs to the 5h window and must not
	// become the weekly observation.
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: session, event: legacy("80", fiveHourReset, 0)},
	}))
	loaded, err := client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.QuotaObserved.ObservedWindows())

	// The same ambiguous shape with the 7d reset is confirmed by the snapshot.
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: session, event: legacy("16", weeklyReset, time.Minute)},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Equal(t, "session-a", loaded.QuotaObserved.ObservedWindows()[0].CollectorSessionID)
	require.InDelta(t, 16, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.InDelta(t, 16, *loaded.QuotaObserved.ObservedWindows()[0].UsedPercent, 1e-9)
	require.Equal(t, weeklyReset, *loaded.QuotaObserved.ObservedWindows()[0].ResetAt)

	// An explicit duration is trustworthy on its own: a real window change is
	// still adopted without snapshot confirmation.
	shiftedReset := weeklyReset.Add(7 * 24 * time.Hour)
	shiftedUsed := 2.5
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: session, event: &cpaclient.UsageEvent{
			Timestamp: weeklyReset.Add(2 * time.Minute),
			AuthIndex: "auth-1",
			Provider:  "codex",
			Model:     "gpt-5.2",
			Tokens:    cpaclient.UsageEventTokens{InputTokens: 10},
			ResponseHeaders: map[string]any{
				"x-codex-secondary-used-percent":   "2.5",
				"x-codex-secondary-window-minutes": "10080",
				"x-codex-secondary-reset-at":       strconv.FormatInt(shiftedReset.Unix(), 10),
			},
		}},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.InDelta(t, shiftedUsed, *loaded.QuotaObserved.ObservedWindows()[0].BaselineUsedPercent, 1e-9)
	require.Equal(t, shiftedReset, *loaded.QuotaObserved.ObservedWindows()[0].ResetAt)
}

func TestUsageCollectorCheckpointRestoresPersistedEventID(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_checkpoint_fallback?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("checkpoint fallback").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		SetEnabled(true).
		SetUsageStreamEnabled(true).
		SetUsageCollectorID("collector-a").
		SaveX(ctx)
	client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).
		SetAuthIndex("auth-1").
		SetProvider("codex").
		SetModel("gpt-5.2").
		SetRequestedAt(time.Now().UTC()).
		SaveX(ctx)

	svc := newCPAServiceForTest(client, time.Now)
	session := newUsageCollectorSession("collector-a", nil)
	svc.usageCollectors = map[int]*usageCollectorWorker{
		instance.ID: {
			target:  usageCollectorTarget{instanceID: instance.ID, collectorID: "collector-a"},
			session: session,
			done:    make(chan struct{}),
		},
	}
	collectorID, eventID := svc.usageCollectorCheckpoint(ctx, instance.ID, "auth-1")
	require.Equal(t, "collector-a", collectorID)
	require.Positive(t, eventID)
	_, checkpoint := session.checkpoint("auth-1")
	require.Equal(t, eventID, checkpoint)
}

func TestUsageCollectorTargetSameConfig(t *testing.T) {
	base := usageCollectorTarget{instanceID: 1, baseURL: "http://cpa", managementKey: "secret", collectorID: "collector-a"}
	if !base.sameConfig(usageCollectorTarget{instanceID: 2, baseURL: "http://cpa", managementKey: "secret", collectorID: "collector-a"}) {
		t.Fatal("instance identity must not affect connection config equality")
	}
	if base.sameConfig(usageCollectorTarget{baseURL: "http://other", managementKey: "secret", collectorID: "collector-a"}) {
		t.Fatal("different base URL must restart the collector")
	}
	if base.sameConfig(usageCollectorTarget{baseURL: "http://cpa", managementKey: "secret", collectorID: "collector-b"}) {
		t.Fatal("different collector identity must restart the collector")
	}
}

func TestUsageCollectorWorkerRunning(t *testing.T) {
	target := usageCollectorTarget{instanceID: 1, baseURL: "http://cpa", managementKey: "secret"}
	alive := &usageCollectorWorker{target: target, done: make(chan struct{})}
	if !usageCollectorWorkerRunning(alive, target) {
		t.Fatal("matching worker with open done channel must be kept")
	}
	if usageCollectorWorkerRunning(alive, usageCollectorTarget{baseURL: "http://other", managementKey: "secret"}) {
		t.Fatal("worker with changed config must be replaced")
	}

	stopped := &usageCollectorWorker{target: target, done: make(chan struct{})}
	close(stopped.done)
	if usageCollectorWorkerRunning(stopped, target) {
		t.Fatal("matching worker with closed done channel must be replaced")
	}
}
