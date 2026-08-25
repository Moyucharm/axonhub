package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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
		target: usageCollectorTarget{instanceID: 7, baseURL: server.URL, managementKey: "secret"},
		cancel: cancel,
		done:   make(chan struct{}),
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
