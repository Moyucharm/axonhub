package biz

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpainstance"
	"github.com/looplj/axonhub/internal/log"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

const (
	usageQueueBatchSize      = 200
	usageQueuePollInterval   = time.Second
	usageQueueInitialBackoff = time.Second
	usageQueueMaxBackoff     = time.Minute
	usageQueuePersistTimeout = 30 * time.Second
	credentialLookupTTL      = 10 * time.Minute
	percentObservationMinGap = time.Minute
)

type usageCollectorTarget struct {
	instanceID      int
	baseURL         string
	managementKey   string
	insecureSkipTLS bool
}

func (target usageCollectorTarget) sameConfig(other usageCollectorTarget) bool {
	return target.baseURL == other.baseURL &&
		target.managementKey == other.managementKey &&
		target.insecureSkipTLS == other.insecureSkipTLS
}

type usageCollectorSession struct {
	id string

	mu             sync.Mutex
	latestEventIDs map[string]int
}

func newUsageCollectorSession() *usageCollectorSession {
	return &usageCollectorSession{
		id:             uuid.NewString(),
		latestEventIDs: make(map[string]int),
	}
}

func (session *usageCollectorSession) recordPersisted(authIndex string, eventID int) {
	if session == nil || eventID <= 0 {
		return
	}
	key := strings.TrimSpace(authIndex)
	if key == "" {
		return
	}
	session.mu.Lock()
	if session.latestEventIDs == nil {
		session.latestEventIDs = make(map[string]int)
	}
	if eventID > session.latestEventIDs[key] {
		session.latestEventIDs[key] = eventID
	}
	session.mu.Unlock()
}

func (session *usageCollectorSession) checkpoint(authIndex string) (string, int) {
	if session == nil {
		return "", 0
	}
	key := strings.TrimSpace(authIndex)
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.id, session.latestEventIDs[key]
}

type usageCollectorWorker struct {
	target  usageCollectorTarget
	session *usageCollectorSession
	cancel  context.CancelFunc
	done    chan struct{}
}

func usageCollectorWorkerRunning(worker *usageCollectorWorker, target usageCollectorTarget) bool {
	if worker == nil || !worker.target.sameConfig(target) {
		return false
	}
	select {
	case <-worker.done:
		return false
	default:
		return true
	}
}

func (svc *CPAService) usageCollectorCheckpoint(instanceID int, authIndex string) (string, int) {
	svc.usageCollectorMu.Lock()
	worker := svc.usageCollectors[instanceID]
	if worker == nil || !usageCollectorWorkerRunning(worker, worker.target) {
		svc.usageCollectorMu.Unlock()
		return "", 0
	}
	session := worker.session
	svc.usageCollectorMu.Unlock()
	return session.checkpoint(authIndex)
}

// StartUsageStream retains the public lifecycle name for compatibility, but the
// implementation now collects CPA's HTTP usage queue instead of using RESP.
func (svc *CPAService) StartUsageStream(ctx context.Context) error {
	svc.usageCollectorMu.Lock()
	if svc.usageCollectorStarted {
		svc.usageCollectorMu.Unlock()
		return nil
	}
	svc.usageCollectorStarted = true
	if svc.usageCollectors == nil {
		svc.usageCollectors = make(map[int]*usageCollectorWorker)
	}
	if svc.usageCredentialCache == nil {
		svc.usageCredentialCache = make(map[int]map[string]credentialCacheEntry)
	}
	if svc.usageObservedAt == nil {
		svc.usageObservedAt = make(map[int]usageObservedState)
	}
	svc.usageCollectorMu.Unlock()
	return svc.RefreshUsageStreams(ctx)
}

// StopUsageStream stops all HTTP usage queue collectors.
func (svc *CPAService) StopUsageStream(ctx context.Context) error {
	svc.usageCollectorReconcileMu.Lock()
	defer svc.usageCollectorReconcileMu.Unlock()

	svc.usageCollectorMu.Lock()
	svc.usageCollectorStarted = false
	workers := make([]*usageCollectorWorker, 0, len(svc.usageCollectors))
	for id, worker := range svc.usageCollectors {
		if worker.cancel != nil {
			worker.cancel()
		}
		workers = append(workers, worker)
		delete(svc.usageCollectors, id)
	}
	svc.usageCollectorMu.Unlock()
	return waitUsageCollectorWorkers(ctx, workers)
}

func waitUsageCollectorWorkers(ctx context.Context, workers []*usageCollectorWorker) error {
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// stopUsageCollectorForInstance must be called while usageCollectorReconcileMu
// is held. The worker is detached before waiting so checkpoint readers cannot
// observe a collector that is already draining its final batch.
func (svc *CPAService) stopUsageCollectorForInstance(ctx context.Context, instanceID int) error {
	svc.usageCollectorMu.Lock()
	worker := svc.usageCollectors[instanceID]
	if worker != nil {
		delete(svc.usageCollectors, instanceID)
		if worker.cancel != nil {
			worker.cancel()
		}
	}
	svc.usageCollectorMu.Unlock()
	if worker == nil {
		return nil
	}
	return waitUsageCollectorWorkers(ctx, []*usageCollectorWorker{worker})
}

// RefreshUsageStreams reconciles HTTP usage queue collectors with enabled instances.
func (svc *CPAService) RefreshUsageStreams(ctx context.Context) error {
	svc.usageCollectorReconcileMu.Lock()
	defer svc.usageCollectorReconcileMu.Unlock()

	ctx = authz.WithSystemBypass(ctx, "cpa-usage-collector")
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(cpainstance.EnabledEQ(true), cpainstance.UsageStreamEnabledEQ(true)).
		All(ctx)
	if err != nil {
		return err
	}
	desired := make(map[int]usageCollectorTarget, len(instances))
	for _, instance := range instances {
		secret, errDecrypt := svc.decryptSecret(ctx, instance.EncryptedSecret)
		if errDecrypt != nil {
			log.Warn(ctx, "skip CPA usage collector for instance with undecryptable secret",
				log.Int("instance_id", instance.ID),
				log.Cause(errDecrypt),
			)
			continue
		}
		desired[instance.ID] = usageCollectorTarget{
			instanceID:      instance.ID,
			baseURL:         instance.BaseURL,
			managementKey:   secret,
			insecureSkipTLS: instance.InsecureSkipTLS,
		}
	}

	svc.usageCollectorMu.Lock()
	if !svc.usageCollectorStarted {
		svc.usageCollectorMu.Unlock()
		return nil
	}
	if svc.usageCollectors == nil {
		svc.usageCollectors = make(map[int]*usageCollectorWorker)
	}
	workersToStop := make([]*usageCollectorWorker, 0)
	for id, worker := range svc.usageCollectors {
		target, keep := desired[id]
		if keep && usageCollectorWorkerRunning(worker, target) {
			delete(desired, id)
			continue
		}
		if worker.cancel != nil {
			worker.cancel()
		}
		workersToStop = append(workersToStop, worker)
		delete(svc.usageCollectors, id)
	}
	svc.usageCollectorMu.Unlock()
	if err := waitUsageCollectorWorkers(ctx, workersToStop); err != nil {
		return err
	}

	svc.usageCollectorMu.Lock()
	defer svc.usageCollectorMu.Unlock()
	if !svc.usageCollectorStarted {
		return nil
	}
	for id, target := range desired {
		collectorCtx, cancel := context.WithCancel(context.Background())
		worker := &usageCollectorWorker{
			target:  target,
			session: newUsageCollectorSession(),
			cancel:  cancel,
			done:    make(chan struct{}),
		}
		svc.usageCollectors[id] = worker
		go svc.runUsageQueueCollector(collectorCtx, worker)
	}
	return nil
}

func (svc *CPAService) runUsageQueueCollector(ctx context.Context, worker *usageCollectorWorker) {
	target := worker.target
	defer close(worker.done)
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error(ctx, "CPA usage collector panicked",
				log.Int("instance_id", target.instanceID),
				log.Any("panic", recovered),
			)
		}
	}()

	client, err := cpaclient.NewClient(cpaclient.Config{
		BaseURL:          target.baseURL,
		ManagementSecret: target.managementKey,
		InsecureSkipTLS:  target.insecureSkipTLS,
	})
	if err != nil {
		log.Warn(ctx, "CPA usage collector failed to build client",
			log.Int("instance_id", target.instanceID),
			log.Cause(err),
		)
		return
	}
	defer client.CloseIdleConnections()

	writeCtx := authz.WithSystemBypass(context.Background(), "cpa-usage-collector-writer")
	backoff := usageQueueInitialBackoff
	var pending []usageEventEnvelope
	for {
		if len(pending) > 0 {
			persistCtx, cancelPersist := context.WithTimeout(writeCtx, usageQueuePersistTimeout)
			persisted := svc.persistUsageBatch(persistCtx, pending)
			cancelPersist()
			if persisted {
				pending = nil
				backoff = usageQueueInitialBackoff
				continue
			}
			if ctx.Err() != nil {
				drainCtx, cancelDrain := context.WithTimeout(writeCtx, usageQueuePersistTimeout)
				svc.drainUsageEvents(drainCtx, target.instanceID, pending)
				cancelDrain()
				return
			}
			if !sleepWithContext(ctx, backoff) {
				continue
			}
			backoff = min(backoff*2, usageQueueMaxBackoff)
			continue
		}

		events, errFetch := client.ListUsageQueue(ctx, usageQueueBatchSize)
		if errFetch != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn(ctx, "CPA usage queue fetch failed",
				log.Int("instance_id", target.instanceID),
				log.Cause(errFetch),
			)
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, usageQueueMaxBackoff)
			continue
		}
		backoff = usageQueueInitialBackoff
		if len(events) == 0 {
			if !sleepWithContext(ctx, usageQueuePollInterval) {
				return
			}
			continue
		}
		pending = make([]usageEventEnvelope, 0, len(events))
		for _, event := range events {
			if event != nil {
				pending = append(pending, usageEventEnvelope{
					instanceID: target.instanceID,
					session:    worker.session,
					event:      event,
				})
			}
		}
	}
}

func (svc *CPAService) drainUsageEvents(ctx context.Context, instanceID int, pending []usageEventEnvelope) {
	backoff := usageQueueInitialBackoff
	for {
		if svc.persistUsageBatch(ctx, pending) {
			return
		}
		if !sleepWithContext(ctx, backoff) {
			log.Error(ctx, "CPA usage collector stopped with unpersisted events",
				log.Int("instance_id", instanceID),
				log.Int("pending_count", len(pending)),
			)
			return
		}
		backoff = min(backoff*2, usageQueueMaxBackoff)
	}
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type usageEventEnvelope struct {
	instanceID int
	session    *usageCollectorSession
	event      *cpaclient.UsageEvent
}

type persistedUsageEvent struct {
	envelope usageEventEnvelope
	eventID  int
}

type credentialCacheEntry struct {
	credentialID int
	loadedAt     time.Time
}

type usageObservedState struct {
	lastWrite    time.Time
	lastPercent  float64
	hasLastWrite bool
}

func (svc *CPAService) persistUsageBatch(ctx context.Context, batch []usageEventEnvelope) bool {
	if svc.usagePersistHook != nil {
		return svc.usagePersistHook(ctx, batch)
	}
	return svc.persistUsageEvents(ctx, batch)
}

func (svc *CPAService) persistUsageEvents(ctx context.Context, batch []usageEventEnvelope) bool {
	if len(batch) == 0 {
		return true
	}
	db := svc.entFromContext(ctx)
	creates := make([]*ent.CpaUsageEventCreate, 0, len(batch))
	validEnvelopes := make([]usageEventEnvelope, 0, len(batch))
	for _, envelope := range batch {
		event := envelope.event
		if event == nil {
			continue
		}
		requestedAt := event.Timestamp
		if requestedAt.IsZero() {
			requestedAt = time.Now().UTC()
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
	err := svc.withCPAInstanceWriteLock(ctx, instanceID, func() error {
		return svc.withCPAUsageWriteRetry(ctx, func() error {
			var saveErr error
			saved, saveErr = db.CpaUsageEvent.CreateBulk(creates...).Save(ctx)
			return saveErr
		})
	})
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
	svc.observeCodexUsageIntervals(ctx, persisted)
	return true
}

func (svc *CPAService) lookupCredentialID(ctx context.Context, instanceID int, authIndex string) (int, bool) {
	key := strings.TrimSpace(authIndex)
	if key == "" {
		return 0, false
	}
	svc.usageCacheMu.Lock()
	index := svc.usageCredentialCache[instanceID]
	if index != nil {
		if entry, ok := index[key]; ok && time.Since(entry.loadedAt) < credentialLookupTTL {
			svc.usageCacheMu.Unlock()
			return entry.credentialID, true
		}
	}
	svc.usageCacheMu.Unlock()

	ctx = authz.WithSystemBypass(ctx, "cpa-usage-lookup")
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instanceID),
			cpacredential.AuthIndexEQ(key),
		).
		IDs(ctx)
	if err != nil || len(credentials) == 0 {
		return 0, false
	}

	svc.usageCacheMu.Lock()
	if svc.usageCredentialCache == nil {
		svc.usageCredentialCache = make(map[int]map[string]credentialCacheEntry)
	}
	if svc.usageCredentialCache[instanceID] == nil {
		svc.usageCredentialCache[instanceID] = make(map[string]credentialCacheEntry)
	}
	svc.usageCredentialCache[instanceID][key] = credentialCacheEntry{
		credentialID: credentials[0],
		loadedAt:     time.Now(),
	}
	svc.usageCacheMu.Unlock()
	return credentials[0], true
}

func (svc *CPAService) invalidateUsageCredentialCache(instanceID int) {
	svc.usageCacheMu.Lock()
	delete(svc.usageCredentialCache, instanceID)
	svc.usageCacheMu.Unlock()
}
