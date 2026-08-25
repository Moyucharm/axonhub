package biz

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpainstance"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
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

type usageCollectorWorker struct {
	target usageCollectorTarget
	cancel context.CancelFunc
	done   chan struct{}
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
	svc.usageCollectorMu.Lock()
	svc.usageCollectorStarted = false
	workers := make([]*usageCollectorWorker, 0, len(svc.usageCollectors))
	for id, worker := range svc.usageCollectors {
		worker.cancel()
		workers = append(workers, worker)
		delete(svc.usageCollectors, id)
	}
	svc.usageCollectorMu.Unlock()
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// RefreshUsageStreams reconciles HTTP usage queue collectors with enabled instances.
func (svc *CPAService) RefreshUsageStreams(ctx context.Context) error {
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
	defer svc.usageCollectorMu.Unlock()
	if !svc.usageCollectorStarted {
		return nil
	}
	if svc.usageCollectors == nil {
		svc.usageCollectors = make(map[int]*usageCollectorWorker)
	}
	for id, worker := range svc.usageCollectors {
		target, keep := desired[id]
		if keep && usageCollectorWorkerRunning(worker, target) {
			delete(desired, id)
			continue
		}
		worker.cancel()
		<-worker.done
		delete(svc.usageCollectors, id)
	}
	for id, target := range desired {
		collectorCtx, cancel := context.WithCancel(context.Background())
		worker := &usageCollectorWorker{target: target, cancel: cancel, done: make(chan struct{})}
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
				pending = append(pending, usageEventEnvelope{instanceID: target.instanceID, event: event})
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
	event      *cpaclient.UsageEvent
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
	for _, envelope := range batch {
		event := envelope.event
		if event == nil {
			continue
		}
		requestedAt := event.Timestamp
		if requestedAt.IsZero() {
			requestedAt = time.Now().UTC()
		}
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
	if err := db.CpaUsageEvent.CreateBulk(creates...).Exec(ctx); err != nil {
		log.Warn(ctx, "persist CPA usage events failed", log.Int("count", len(creates)), log.Cause(err))
		return false
	}
	svc.observePercents(ctx, batch)
	return true
}

// observePercents captures precise codex quota percentages from response headers.
func (svc *CPAService) observePercents(ctx context.Context, batch []usageEventEnvelope) {
	now := time.Now().UTC()
	type pendingUpdate struct {
		credentialID int
		observed     objects.CPAQuotaObserved
	}
	var updates []pendingUpdate
	for _, envelope := range batch {
		event := envelope.event
		if event == nil || !strings.EqualFold(strings.TrimSpace(event.Provider), "codex") || event.Failed {
			continue
		}
		rawPercent := strings.TrimSuffix(event.HeaderValue("x-codex-secondary-used-percent"), "%")
		if rawPercent == "" {
			continue
		}
		percent, errParse := strconv.ParseFloat(rawPercent, 64)
		if errParse != nil || percent < 0 || percent > 100 {
			continue
		}
		credentialID, ok := svc.lookupCredentialID(ctx, envelope.instanceID, event.AuthIndex)
		if !ok {
			continue
		}
		svc.usageCacheMu.Lock()
		state := svc.usageObservedAt[credentialID]
		if state.hasLastWrite && now.Sub(state.lastWrite) < percentObservationMinGap && absFloat64(percent-state.lastPercent) < 0.01 {
			svc.usageCacheMu.Unlock()
			continue
		}
		observed := objects.CPAQuotaObserved{SecondaryUsedPercent: &percent}
		if resetAt := parseHeaderUnixTime(event.HeaderValue("x-codex-secondary-reset-at")); resetAt != nil {
			observed.SecondaryResetAt = resetAt
		}
		observed.ObservedAt = &now
		updates = append(updates, pendingUpdate{credentialID: credentialID, observed: observed})
		state.lastWrite = now
		state.lastPercent = percent
		state.hasLastWrite = true
		svc.usageObservedAt[credentialID] = state
		svc.usageCacheMu.Unlock()
	}
	for _, update := range updates {
		if err := svc.entFromContext(ctx).CPACredential.UpdateOneID(update.credentialID).
			SetQuotaObserved(update.observed).
			Exec(ctx); err != nil {
			log.Warn(ctx, "persist CPA observed quota percent failed",
				log.Int("credential_id", update.credentialID),
				log.Cause(err),
			)
		}
	}
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

func parseHeaderUnixTime(raw string) *time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return nil
	}
	parsed := time.Unix(seconds, 0).UTC()
	return &parsed
}

func absFloat64(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
