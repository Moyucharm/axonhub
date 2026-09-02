package biz

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpausageevent"
	"github.com/looplj/axonhub/internal/log"
)

const (
	credentialLookupTTL      = 10 * time.Minute
	percentObservationMinGap = time.Minute
)

type credentialCacheEntry struct {
	credentialID int
	loadedAt     time.Time
}

type usageObservedState struct {
	lastWrite    time.Time
	lastPercent  float64
	hasLastWrite bool
}

// cpaUsageRepository owns local usage-event persistence and the small caches
// needed to connect transient CPA auth indexes to local credentials. It does
// not create remote clients or manage collector workers.
type cpaUsageRepository struct {
	entFromContext         func(context.Context) *ent.Client
	withInstanceWriteLock  func(context.Context, int, func() error) error
	withInstanceWriteRetry func(context.Context, int, func() error) error
	withUsageWriteRetry    func(context.Context, func() error) error
	now                    func() time.Time
	persistHook            func(context.Context, []usageEventEnvelope) bool

	cacheMu         sync.Mutex
	credentialCache map[int]map[string]credentialCacheEntry
	observedAt      map[int]usageObservedState
}

func newCPAUsageRepository(
	db *ent.Client,
	withInstanceWriteLock func(context.Context, int, func() error) error,
	withInstanceWriteRetry func(context.Context, int, func() error) error,
	withUsageWriteRetry func(context.Context, func() error) error,
	now func() time.Time,
) *cpaUsageRepository {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &cpaUsageRepository{
		entFromContext: func(ctx context.Context) *ent.Client {
			if client := ent.FromContext(ctx); client != nil {
				return client
			}
			return db
		},
		withInstanceWriteLock:  withInstanceWriteLock,
		withInstanceWriteRetry: withInstanceWriteRetry,
		withUsageWriteRetry:    withUsageWriteRetry,
		now:                    now,
		credentialCache:        make(map[int]map[string]credentialCacheEntry),
		observedAt:             make(map[int]usageObservedState),
	}
}

func (svc *CPAService) persistUsageBatch(ctx context.Context, batch []usageEventEnvelope) bool {
	if svc == nil || svc.usageRepository == nil {
		return false
	}
	return svc.usageRepository.persistBatch(ctx, batch)
}

func (svc *CPAService) persistUsageEvents(ctx context.Context, batch []usageEventEnvelope) bool {
	if svc == nil || svc.usageRepository == nil {
		return false
	}
	return svc.usageRepository.persistEvents(ctx, batch)
}

func (repository *cpaUsageRepository) persistBatch(ctx context.Context, batch []usageEventEnvelope) bool {
	if repository == nil {
		return false
	}
	if repository.persistHook != nil {
		return repository.persistHook(ctx, batch)
	}
	return repository.persistEvents(ctx, batch)
}

func (repository *cpaUsageRepository) persistEvents(ctx context.Context, batch []usageEventEnvelope) bool {
	if len(batch) == 0 {
		return true
	}
	db := repository.entFromContext(ctx)
	creates := make([]*ent.CpaUsageEventCreate, 0, len(batch))
	validEnvelopes := make([]usageEventEnvelope, 0, len(batch))
	for _, envelope := range batch {
		event := envelope.event
		if event == nil {
			continue
		}
		requestedAt := event.Timestamp
		if requestedAt.IsZero() {
			requestedAt = repository.now()
		}
		validEnvelopes = append(validEnvelopes, envelope)
		creates = append(creates, db.CpaUsageEvent.Create().
			SetCpaInstanceID(envelope.instanceID).
			SetAuthIndex(strings.TrimSpace(event.AuthIndex)).
			SetProvider(event.Provider).
			SetModel(strings.TrimSpace(event.Model)).
			SetSource(event.Source).
			SetInputTokens(event.Tokens.InputTokens).
			SetOutputTokens(event.Tokens.OutputTokens).
			SetReasoningTokens(event.Tokens.ReasoningTokens).
			SetCachedTokens(event.Tokens.CachedTokens).
			SetCacheReadTokens(event.Tokens.CacheReadTokens).
			SetCacheCreationTokens(event.Tokens.CacheCreationTokens).
			SetTotalTokens(event.Tokens.TotalTokens).
			SetFailed(event.Failed).
			SetRequestedAt(requestedAt))
	}
	if len(creates) == 0 {
		return true
	}
	instanceID := validEnvelopes[0].instanceID
	var saved []*ent.CpaUsageEvent
	writeEvents := func() error {
		var saveErr error
		saved, saveErr = db.CpaUsageEvent.CreateBulk(creates...).Save(ctx)
		return saveErr
	}
	write := func() error {
		if repository.withUsageWriteRetry == nil {
			return writeEvents()
		}
		return repository.withUsageWriteRetry(ctx, writeEvents)
	}
	var err error
	if repository.withInstanceWriteLock != nil {
		err = repository.withInstanceWriteLock(ctx, instanceID, write)
	} else {
		err = write()
	}
	if err != nil {
		log.Warn(ctx, "persist CPA usage events failed", log.Int("count", len(creates)), log.Cause(err))
		return false
	}
	persisted := make([]persistedUsageEvent, 0, len(saved))
	for index, node := range saved {
		envelope := validEnvelopes[index]
		envelope.session.recordPersisted(envelope.event.AuthIndex, node.ID)
		persisted = append(persisted, persistedUsageEvent{envelope: envelope, eventID: node.ID})
	}
	repository.observeCodexUsageIntervals(ctx, persisted)
	return true
}

func (repository *cpaUsageRepository) lookupCredentialID(ctx context.Context, instanceID int, authIndex string) (int, bool) {
	key := strings.TrimSpace(authIndex)
	if key == "" {
		return 0, false
	}
	repository.cacheMu.Lock()
	index := repository.credentialCache[instanceID]
	if index != nil {
		if entry, ok := index[key]; ok && repository.now().Sub(entry.loadedAt) < credentialLookupTTL {
			repository.cacheMu.Unlock()
			return entry.credentialID, true
		}
	}
	repository.cacheMu.Unlock()

	ctx = authz.WithSystemBypass(ctx, "cpa-usage-lookup")
	credentials, err := repository.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instanceID),
			cpacredential.AuthIndexEQ(key),
		).
		IDs(ctx)
	if err != nil || len(credentials) == 0 {
		return 0, false
	}

	repository.cacheMu.Lock()
	if repository.credentialCache == nil {
		repository.credentialCache = make(map[int]map[string]credentialCacheEntry)
	}
	if repository.credentialCache[instanceID] == nil {
		repository.credentialCache[instanceID] = make(map[string]credentialCacheEntry)
	}
	repository.credentialCache[instanceID][key] = credentialCacheEntry{
		credentialID: credentials[0],
		loadedAt:     repository.now(),
	}
	repository.cacheMu.Unlock()
	return credentials[0], true
}

func (repository *cpaUsageRepository) invalidateCredentialCache(instanceID int) {
	if repository == nil {
		return
	}
	repository.cacheMu.Lock()
	delete(repository.credentialCache, instanceID)
	repository.cacheMu.Unlock()
}

func (repository *cpaUsageRepository) observedState(credentialID int) usageObservedState {
	repository.cacheMu.Lock()
	defer repository.cacheMu.Unlock()
	return repository.observedAt[credentialID]
}

func (repository *cpaUsageRepository) setObservedState(credentialID int, state usageObservedState) {
	repository.cacheMu.Lock()
	defer repository.cacheMu.Unlock()
	if repository.observedAt == nil {
		repository.observedAt = make(map[int]usageObservedState)
	}
	repository.observedAt[credentialID] = state
}

func (repository *cpaUsageRepository) usageAggregatesByModel(
	ctx context.Context,
	instanceID int,
	authIndex string,
	from, to time.Time,
	afterEventID, throughEventID int,
) (map[string]tokenAggregate, error) {
	var rows []struct {
		Model               string `json:"model"`
		InputTokens         int64  `json:"input_tokens"`
		OutputTokens        int64  `json:"output_tokens"`
		ReasoningTokens     int64  `json:"reasoning_tokens"`
		CachedTokens        int64  `json:"cached_tokens"`
		CacheReadTokens     int64  `json:"cache_read_tokens"`
		CacheCreationTokens int64  `json:"cache_creation_tokens"`
	}
	err := repository.entFromContext(ctx).CpaUsageEvent.Query().
		Where(
			cpausageevent.CpaInstanceIDEQ(instanceID),
			cpausageevent.AuthIndexEQ(authIndex),
			cpausageevent.FailedEQ(false),
			cpausageevent.RequestedAtGTE(from),
			cpausageevent.RequestedAtLTE(to),
			cpausageevent.IDGT(afterEventID),
			cpausageevent.IDLTE(throughEventID),
		).
		GroupBy(cpausageevent.FieldModel).
		Aggregate(
			ent.As(ent.Sum(cpausageevent.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(cpausageevent.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(cpausageevent.FieldReasoningTokens), "reasoning_tokens"),
			ent.As(ent.Sum(cpausageevent.FieldCachedTokens), "cached_tokens"),
			ent.As(ent.Sum(cpausageevent.FieldCacheReadTokens), "cache_read_tokens"),
			ent.As(ent.Sum(cpausageevent.FieldCacheCreationTokens), "cache_creation_tokens"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("aggregate CPA usage events: %w", err)
	}
	result := make(map[string]tokenAggregate, len(rows))
	for _, row := range rows {
		result[row.Model] = tokenAggregate{
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			ReasoningTokens:     row.ReasoningTokens,
			CachedTokens:        row.CachedTokens,
			CacheReadTokens:     row.CacheReadTokens,
			CacheCreationTokens: row.CacheCreationTokens,
		}
	}
	return result, nil
}
