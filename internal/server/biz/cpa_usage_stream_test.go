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
		usagePersistHook: func(_ context.Context, batch []usageEventEnvelope) bool {
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
		},
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
	event := func(percent string, offset time.Duration) *cpaclient.UsageEvent {
		return &cpaclient.UsageEvent{
			Timestamp: resetAt.Add(-time.Hour + offset),
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
	svc := &CPAService{
		AbstractService:      &AbstractService{db: client},
		usageCredentialCache: make(map[int]map[string]credentialCacheEntry),
		usageObservedAt:      make(map[int]usageObservedState),
	}
	sessionA := &usageCollectorSession{id: "session-a"}
	sessionB := &usageCollectorSession{id: "session-b"}
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionA, event: event("6.4", 0)},
		{instanceID: instance.ID, session: sessionA, event: event("10.2", time.Minute)},
	}))
	loaded, err := client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.Equal(t, "session-a", loaded.QuotaObserved.SecondaryCollectorSessionID)
	require.InDelta(t, 6.4, *loaded.QuotaObserved.SecondaryBaselineUsedPercent, 1e-9)
	require.InDelta(t, 10.2, *loaded.QuotaObserved.SecondaryUsedPercent, 1e-9)
	require.Less(t, *loaded.QuotaObserved.SecondaryBaselineEventID, *loaded.QuotaObserved.SecondaryLatestEventID)
	_, sessionALatest := sessionA.checkpoint("auth-1")
	require.Equal(t, *loaded.QuotaObserved.SecondaryLatestEventID, sessionALatest)

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
	require.Equal(t, "session-b", loaded.QuotaObserved.SecondaryCollectorSessionID)
	require.InDelta(t, 61.7, *loaded.QuotaObserved.SecondaryBaselineUsedPercent, 1e-9)
	require.Equal(t, *loaded.QuotaObserved.SecondaryBaselineEventID, *loaded.QuotaObserved.SecondaryLatestEventID)

	// A reset card can preserve reset_at but lower the percentage; do not wait
	// for an exact 0% observation before reanchoring.
	require.True(t, svc.persistUsageEvents(ctx, []usageEventEnvelope{
		{instanceID: instance.ID, session: sessionB, event: event("4.8", 3*time.Minute)},
	}))
	loaded, err = client.CPACredential.Get(ctx, credential.ID)
	require.NoError(t, err)
	require.InDelta(t, 4.8, *loaded.QuotaObserved.SecondaryBaselineUsedPercent, 1e-9)
}

func TestUsageCollectorCheckpointIsSessionAndCredentialScoped(t *testing.T) {
	sessionA := &usageCollectorSession{id: "session-a"}
	sessionA.recordPersisted("auth-a", 10)
	sessionA.recordPersisted("auth-b", 20)
	workerA := &usageCollectorWorker{
		target:  usageCollectorTarget{instanceID: 1},
		session: sessionA,
		done:    make(chan struct{}),
	}
	svc := &CPAService{usageCollectors: map[int]*usageCollectorWorker{1: workerA}}

	sessionID, eventID := svc.usageCollectorCheckpoint(1, " auth-a ")
	require.Equal(t, "session-a", sessionID)
	require.Equal(t, 10, eventID)
	_, eventID = svc.usageCollectorCheckpoint(1, "auth-b")
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

	sessionID, eventID = svc.usageCollectorCheckpoint(1, "auth-a")
	require.Equal(t, "session-b", sessionID)
	require.Equal(t, 30, eventID)
}

func TestAdvanceCodexSecondaryObservationReanchorsWithoutZeroUsage(t *testing.T) {
	resetAt := time.Date(2026, 8, 24, 13, 5, 35, 0, time.UTC)
	observed, reanchored := advanceCodexSecondaryObservation(objects.CPAQuotaObserved{}, codexSecondaryObservation{
		collectorSessionID: "session-a",
		eventID:            10,
		usedPercent:        6.4,
		resetAt:            resetAt,
		observedAt:         resetAt.Add(-time.Hour),
	})
	require.True(t, reanchored)
	require.InDelta(t, 6.4, *observed.SecondaryBaselineUsedPercent, 1e-9)
	require.Equal(t, 10, *observed.SecondaryBaselineEventID)

	observed, reanchored = advanceCodexSecondaryObservation(observed, codexSecondaryObservation{
		collectorSessionID: "session-a",
		eventID:            11,
		usedPercent:        9.5,
		resetAt:            resetAt,
		observedAt:         resetAt.Add(-30 * time.Minute),
	})
	require.False(t, reanchored)
	require.InDelta(t, 6.4, *observed.SecondaryBaselineUsedPercent, 1e-9)
	require.InDelta(t, 9.5, *observed.SecondaryUsedPercent, 1e-9)

	// A different collector (development/production handoff) always starts a
	// fresh local interval at its first actual observation.
	observed, reanchored = advanceCodexSecondaryObservation(observed, codexSecondaryObservation{
		collectorSessionID: "session-b",
		eventID:            20,
		usedPercent:        61.0,
		resetAt:            resetAt,
		observedAt:         resetAt.Add(-20 * time.Minute),
	})
	require.True(t, reanchored)
	require.InDelta(t, 61, *observed.SecondaryBaselineUsedPercent, 1e-9)

	// Codex can reset early. A changed reset_at reanchors even though the first
	// observed usage in the new window is already non-zero.
	activityReset := resetAt.Add(24 * time.Hour)
	observed, reanchored = advanceCodexSecondaryObservation(observed, codexSecondaryObservation{
		collectorSessionID: "session-b",
		eventID:            21,
		usedPercent:        7.2,
		resetAt:            activityReset,
		observedAt:         resetAt.Add(-10 * time.Minute),
	})
	require.True(t, reanchored)
	require.InDelta(t, 7.2, *observed.SecondaryBaselineUsedPercent, 1e-9)

	// A grant/reset card may preserve reset_at but lower used percentage.
	observed, reanchored = advanceCodexSecondaryObservation(observed, codexSecondaryObservation{
		collectorSessionID: "session-b",
		eventID:            22,
		usedPercent:        3.1,
		resetAt:            activityReset,
		observedAt:         resetAt.Add(-5 * time.Minute),
	})
	require.True(t, reanchored)
	require.InDelta(t, 3.1, *observed.SecondaryBaselineUsedPercent, 1e-9)
}

func TestUsageCollectorTargetSameConfig(t *testing.T) {
	base := usageCollectorTarget{instanceID: 1, baseURL: "http://cpa", managementKey: "secret"}
	if !base.sameConfig(usageCollectorTarget{instanceID: 2, baseURL: "http://cpa", managementKey: "secret"}) {
		t.Fatal("instance identity must not affect connection config equality")
	}
	if base.sameConfig(usageCollectorTarget{baseURL: "http://other", managementKey: "secret"}) {
		t.Fatal("different base URL must restart the collector")
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
