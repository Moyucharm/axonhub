package biz

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

func TestCPAInstanceWriteRetrySerializesSameInstance(t *testing.T) {
	svc := &CPAService{}
	ctx := context.Background()
	var active atomic.Int32
	var maxActive atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.withCPAInstanceWriteRetry(ctx, 1, func() error {
				current := active.Add(1)
				for {
					maximum := maxActive.Load()
					if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				active.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), maxActive.Load())
	require.Empty(t, svc.instanceWrite)
}

func TestCPAInstanceWriteRetryAllowsDifferentInstancesInParallel(t *testing.T) {
	svc := &CPAService{}
	ctx := context.Background()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for instanceID := 1; instanceID <= 2; instanceID++ {
		wg.Add(1)
		go func(instanceID int) {
			defer wg.Done()
			errs <- svc.withCPAInstanceWriteRetry(ctx, instanceID, func() error {
				started <- struct{}{}
				<-release
				return nil
			})
		}(instanceID)
	}

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("different CPA instances did not enter their write sections concurrently")
		}
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Empty(t, svc.instanceWrite)
}

func TestCPAInstanceWriteEntryStaysRegisteredForWaiters(t *testing.T) {
	svc := &CPAService{}
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- svc.withCPAInstanceWriteLock(ctx, 7, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	go func() {
		secondDone <- svc.withCPAInstanceWriteLock(ctx, 7, func() error { return nil })
	}()

	require.Eventually(t, func() bool {
		svc.instanceWriteMu.Lock()
		defer svc.instanceWriteMu.Unlock()
		entry := svc.instanceWrite[7]
		return entry != nil && entry.refs == 2
	}, time.Second, time.Millisecond)

	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.Empty(t, svc.instanceWrite)
}

func TestCPAWriteRetryRetriesRetryableOperation(t *testing.T) {
	expected := errors.New("locked")
	var attempts atomic.Int32

	err := withCPAWriteRetry(
		context.Background(),
		semaphore.NewWeighted(1),
		func(err error) bool { return errors.Is(err, expected) },
		func() error {
			if attempts.Add(1) == 1 {
				return expected
			}
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, int32(2), attempts.Load())
}

func TestCPAUsageWriteRetrySkipsGlobalGateOutsideSQLite(t *testing.T) {
	limiter := semaphore.NewWeighted(1)
	require.NoError(t, limiter.Acquire(context.Background(), 1))
	defer limiter.Release(1)

	called := false
	err := withCPAUsageWriteRetryForDialect(context.Background(), dialect.Postgres, limiter, func() error {
		called = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, called)
}

func TestSQLiteWriteLockErrorIgnoresNonSQLiteErrors(t *testing.T) {
	require.False(t, isSQLiteWriteLockError(errors.New("database is deadlocked (6)")))
}
