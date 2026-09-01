package biz

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPAQuotaExecutionOutcomeValidation(t *testing.T) {
	tests := []struct {
		name    string
		outcome cpaQuotaExecutionOutcome
	}{
		{name: "failure requires error", outcome: cpaQuotaExecutionOutcome{status: cpaQuotaExecutionFailure}},
		{name: "skipped requires reason", outcome: cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSkipped}},
		{name: "success requires provider state", outcome: cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSuccess}},
		{name: "unknown status is rejected", outcome: cpaQuotaExecutionOutcome{status: "unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, tt.outcome.validate())
		})
	}
}

func TestCPAQuotaExecutionSkipsUnsupportedCredential(t *testing.T) {
	executor := newCPAQuotaExecutor(cpaclient.NewQuotaRegistry(), nil, time.Now)
	client := &cpaTestManagementClient{callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		t.Fatal("unsupported credential must not call provider")
		return nil, nil
	}}
	outcome := executor.execute(t.Context(), client, &ent.CPACredential{
		ID:            7,
		CpaInstanceID: 3,
		Provider:      "xai",
		QuotaContext:  objects.CPAQuotaContext{Paid: true},
	})
	require.Equal(t, cpaQuotaExecutionSkipped, outcome.status)
	require.Equal(t, "quota unsupported", outcome.skipReason)
}

func TestCPAQuotaExecutionReturnsTypedFailure(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	executor := newCPAQuotaExecutor(cpaclient.NewQuotaRegistry(), nil, func() time.Time { return now })
	client := &cpaTestManagementClient{callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		return nil, errors.New("provider unavailable")
	}}
	outcome := executor.execute(t.Context(), client, &ent.CPACredential{
		ID: 9, CpaInstanceID: 4, AuthIndex: "claude", Provider: "claude",
	})
	require.Equal(t, cpaQuotaExecutionFailure, outcome.status)
	require.Equal(t, objects.CPAQuotaStateError, outcome.quotaState)
	require.Equal(t, now, outcome.attemptedAt)
	require.ErrorContains(t, outcome.err, "provider unavailable")
}

func TestCPAQuotaExecutionRespectsInstanceConcurrencyLimit(t *testing.T) {
	executor := newCPAQuotaExecutor(cpaclient.NewQuotaRegistry(), nil, time.Now)
	var active atomic.Int32
	var maximum atomic.Int32
	client := &cpaTestManagementClient{callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"five_hour":{"utilization":25},"account":{"has_claude_pro":true}}`)}, nil
	}}

	outcomes := make(chan cpaQuotaExecutionOutcome, 12)
	var wg sync.WaitGroup
	for index := range 12 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			outcomes <- executor.execute(t.Context(), client, &ent.CPACredential{
				ID: index + 100, CpaInstanceID: 8, AuthIndex: "claude", Provider: "claude",
			})
		}(index)
	}
	wg.Wait()
	close(outcomes)
	for outcome := range outcomes {
		require.Equal(t, cpaQuotaExecutionSuccess, outcome.status)
	}
	require.LessOrEqual(t, maximum.Load(), int32(maxCPAInstanceConcurrency))
	require.Equal(t, int32(maxCPAInstanceConcurrency), maximum.Load())
}

func TestCPAQuotaExecutionRespectsGlobalConcurrencyLimit(t *testing.T) {
	executor := newCPAQuotaExecutor(cpaclient.NewQuotaRegistry(), nil, time.Now)
	var active atomic.Int32
	var maximum atomic.Int32
	client := &cpaTestManagementClient{callProvider: func(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"five_hour":{"utilization":25},"account":{"has_claude_pro":true}}`)}, nil
	}}

	outcomes := make(chan cpaQuotaExecutionOutcome, 24)
	var wg sync.WaitGroup
	for index := range 24 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			outcomes <- executor.execute(t.Context(), client, &ent.CPACredential{
				ID:            index + 200,
				CpaInstanceID: index%4 + 1,
				AuthIndex:     "claude",
				Provider:      "claude",
			})
		}(index)
	}
	wg.Wait()
	close(outcomes)
	for outcome := range outcomes {
		require.Equal(t, cpaQuotaExecutionSuccess, outcome.status)
	}
	require.LessOrEqual(t, maximum.Load(), int32(maxCPAGlobalConcurrency))
	require.Equal(t, int32(maxCPAGlobalConcurrency), maximum.Load())
}

func TestCPAQuotaExecutionSingleflightSharesProviderFetch(t *testing.T) {
	executor := newCPAQuotaExecutor(cpaclient.NewQuotaRegistry(), nil, time.Now)
	started := make(chan struct{})
	joined := make(chan struct{})
	var joinedCount atomic.Int32
	executor.afterSingleflightJoin = func() {
		if joinedCount.Add(1) == 2 {
			close(joined)
		}
	}
	release := make(chan struct{})
	var calls atomic.Int32
	client := &cpaTestManagementClient{callProvider: func(ctx context.Context, _ cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
		call := calls.Add(1)
		if call == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"five_hour":{"utilization":25}}`)}, nil
		}
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"account":{"has_claude_pro":true}}`)}, nil
	}}
	credential := &ent.CPACredential{ID: 11, CpaInstanceID: 5, AuthIndex: "claude", Provider: "claude"}

	outcomes := make(chan cpaQuotaExecutionOutcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes <- executor.execute(t.Context(), client, credential)
		}()
	}
	<-started
	<-joined
	close(release)
	wg.Wait()
	close(outcomes)
	for outcome := range outcomes {
		require.Equal(t, cpaQuotaExecutionSuccess, outcome.status)
		require.Equal(t, objects.CPAQuotaStateSuccess, outcome.quotaState)
	}
	require.Equal(t, int32(2), calls.Load(), "one shared Claude refresh performs its two protocol calls once")
}
