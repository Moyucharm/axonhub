package biz

import (
	"context"
	"errors"
	"time"

	"entgo.io/ent/dialect"
	"golang.org/x/sync/semaphore"
	moderncsqlite "modernc.org/sqlite"
)

const (
	cpaSQLiteWriteRetryAttempts = 3
	cpaSQLiteWriteRetryDelay    = 50 * time.Millisecond
)

type cpaInstanceWriteEntry struct {
	limiter *semaphore.Weighted
	refs    int
}

func (svc *CPAService) retainInstanceWriteEntry(instanceID int) *cpaInstanceWriteEntry {
	svc.instanceWriteMu.Lock()
	defer svc.instanceWriteMu.Unlock()
	if svc.instanceWrite == nil {
		svc.instanceWrite = make(map[int]*cpaInstanceWriteEntry)
	}
	entry := svc.instanceWrite[instanceID]
	if entry == nil {
		entry = &cpaInstanceWriteEntry{limiter: semaphore.NewWeighted(1)}
		svc.instanceWrite[instanceID] = entry
	}
	entry.refs++
	return entry
}

func (svc *CPAService) releaseInstanceWriteEntry(instanceID int, entry *cpaInstanceWriteEntry) {
	svc.instanceWriteMu.Lock()
	defer svc.instanceWriteMu.Unlock()
	entry.refs--
	if entry.refs == 0 && svc.instanceWrite[instanceID] == entry {
		delete(svc.instanceWrite, instanceID)
	}
}

func (svc *CPAService) usageWriteLimiter() *semaphore.Weighted {
	svc.usageWriteMu.Lock()
	defer svc.usageWriteMu.Unlock()
	if svc.usageWrite == nil {
		svc.usageWrite = semaphore.NewWeighted(1)
	}
	return svc.usageWrite
}

// withCPAInstanceWriteLock serializes local CPA writes for one instance.
func (svc *CPAService) withCPAInstanceWriteLock(ctx context.Context, instanceID int, fn func() error) error {
	entry := svc.retainInstanceWriteEntry(instanceID)
	defer svc.releaseInstanceWriteEntry(instanceID, entry)
	return withCPAWriteLock(ctx, entry.limiter, fn)
}

// withCPAInstanceWriteRetry serializes local CPA writes for one instance and
// retries only SQLite lock errors from a complete, repeatable operation.
func (svc *CPAService) withCPAInstanceWriteRetry(ctx context.Context, instanceID int, fn func() error) error {
	entry := svc.retainInstanceWriteEntry(instanceID)
	defer svc.releaseInstanceWriteEntry(instanceID, entry)
	return withCPAWriteRetry(ctx, entry.limiter, isSQLiteWriteLockError, fn)
}

func (svc *CPAService) withCPAUsageWriteRetry(ctx context.Context, fn func() error) error {
	return withCPAUsageWriteRetryForDialect(
		ctx,
		svc.entFromContext(ctx).Driver().Dialect(),
		svc.usageWriteLimiter(),
		fn,
	)
}

func withCPAUsageWriteRetryForDialect(ctx context.Context, dialectName string, limiter *semaphore.Weighted, fn func() error) error {
	if dialectName != dialect.SQLite {
		return fn()
	}
	return withCPAWriteRetry(ctx, limiter, isSQLiteWriteLockError, fn)
}

func withCPAWriteLock(ctx context.Context, limiter *semaphore.Weighted, fn func() error) error {
	if err := limiter.Acquire(ctx, 1); err != nil {
		return err
	}
	defer limiter.Release(1)
	return fn()
}

func withCPAWriteRetry(ctx context.Context, limiter *semaphore.Weighted, retryable func(error) bool, fn func() error) error {
	var err error
	for attempt := 0; attempt < cpaSQLiteWriteRetryAttempts; attempt++ {
		err = withCPAWriteLock(ctx, limiter, fn)
		if err == nil || !retryable(err) || attempt == cpaSQLiteWriteRetryAttempts-1 {
			return err
		}
		if !sleepWithContext(ctx, cpaSQLiteWriteRetryDelay*time.Duration(1<<attempt)) {
			return ctx.Err()
		}
	}
	return err
}

func isSQLiteWriteLockError(err error) bool {
	var sqliteErr *moderncsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	// SQLITE_BUSY and SQLITE_LOCKED, including their extended result codes,
	// share these low eight bits. busy_timeout handles ordinary BUSY waits;
	// this retry also covers LOCKED/deadlocked transactions.
	code := sqliteErr.Code() & 0xff
	return code == 5 || code == 6
}
