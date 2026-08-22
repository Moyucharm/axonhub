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
	usageEventBuffer         = 4096
	usageEventFlushSize      = 200
	usageEventFlushInterval  = 3 * time.Second
	credentialLookupTTL      = 10 * time.Minute
	percentObservationMinGap = time.Minute
)

// usageEventEnvelope couples an event with its source instance.
type usageEventEnvelope struct {
	instanceID int
	event      *cpaclient.UsageEvent
}

// initUsageStream prepares the usage stream pipeline. Call once after service
// construction; actual connections are opened by RefreshUsageStreams.
func (svc *CPAService) initUsageStream() {
	svc.usageEvents = make(chan usageEventEnvelope, usageEventBuffer)
	svc.usageCredentialCache = make(map[int]map[string]credentialCacheEntry)
	svc.usageObservedAt = make(map[int]usageObservedState)
	svc.usageStream = cpaclient.NewUsageStreamManager(svc.handleUsageEvent)
}

// StartUsageStream launches the event writer and opens subscriptions for all
// enabled instances. Intended for fx OnStart.
func (svc *CPAService) StartUsageStream(ctx context.Context) error {
	if svc.usageStream == nil {
		svc.initUsageStream()
	}
	writerCtx, cancel := context.WithCancel(context.Background())
	svc.usageWriterCancel = cancel
	svc.usageWriterWG.Add(1)
	go svc.runUsageEventWriter(writerCtx)
	return svc.RefreshUsageStreams(ctx)
}

// StopUsageStream closes subscriptions and drains the writer. Intended for fx OnStop.
// The event channel is intentionally never closed: subscription handlers may
// still be in flight during shutdown, and sending on a closed channel would
// panic. The writer exits via its context instead.
func (svc *CPAService) StopUsageStream() {
	if svc.usageStream != nil {
		svc.usageStream.Close()
	}
	if svc.usageWriterCancel != nil {
		svc.usageWriterCancel()
	}
	svc.usageWriterWG.Wait()
}

// RefreshUsageStreams reconciles subscription loops with enabled instances.
func (svc *CPAService) RefreshUsageStreams(ctx context.Context) error {
	if svc.usageStream == nil {
		return nil
	}
	ctx = authz.WithSystemBypass(ctx, "cpa-usage-stream")
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(cpainstance.EnabledEQ(true), cpainstance.UsageStreamEnabledEQ(true)).
		All(ctx)
	if err != nil {
		return err
	}
	targets := make([]cpaclient.UsageStreamTarget, 0, len(instances))
	for _, instance := range instances {
		secret, err := svc.decryptSecret(ctx, instance.EncryptedSecret)
		if err != nil {
			log.Warn(ctx, "skip CPA usage stream for instance with undecryptable secret",
				log.Int("instance_id", instance.ID),
				log.Cause(err),
			)
			continue
		}
		targets = append(targets, cpaclient.UsageStreamTarget{
			InstanceID:       instance.ID,
			BaseURL:          instance.BaseURL,
			ManagementSecret: secret,
			InsecureSkipTLS:  instance.InsecureSkipTLS,
		})
	}
	svc.usageStream.Update(targets)
	return nil
}

// handleUsageEvent is invoked on the subscription goroutine; it must never block.
func (svc *CPAService) handleUsageEvent(ctx context.Context, target cpaclient.UsageStreamTarget, event *cpaclient.UsageEvent) {
	select {
	case svc.usageEvents <- usageEventEnvelope{instanceID: target.InstanceID, event: event}:
	default:
		// Buffer full: drop rather than block CPA's delivery loop. Estimation is
		// best-effort and tolerates gaps. Increment drop counter with sampled warn
		// so a saturated buffer is observable without spamming logs.
		dropped := svc.usageDroppedTotal.Add(1)
		if dropped == 1 || dropped%200 == 0 {
			log.Warn(ctx, "CPA usage events dropped (buffer full)",
				log.Int("instance_id", target.InstanceID),
				log.Int64("dropped_total", dropped),
			)
		} else if dropped%50 == 0 {
			// Also emit at debug level for finer visibility without warn spam.
			log.Debug(ctx, "CPA usage events dropped", log.Int64("dropped_total", dropped))
		}
	}
}

// DroppedUsageEvents reports how many usage events were dropped due to buffer saturation.
func (svc *CPAService) DroppedUsageEvents() int64 {
	return svc.usageDroppedTotal.Load()
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

func (svc *CPAService) runUsageEventWriter(parentCtx context.Context) {
	defer svc.usageWriterWG.Done()
	// writeCtx is decoupled from parentCtx cancellation so the drain path can
	// still persist the tail batch after StopUsageStream cancels parentCtx.
	writeCtx := authz.WithSystemBypass(context.WithoutCancel(parentCtx), "cpa-usage-writer")
	batch := make([]usageEventEnvelope, 0, usageEventFlushSize)
	ticker := time.NewTicker(usageEventFlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		svc.persistUsageEvents(writeCtx, batch)
		batch = batch[:0]
	}

	for {
		select {
		case envelope, ok := <-svc.usageEvents:
			if !ok {
				flush()
				return
			}
			batch = append(batch, envelope)
			if len(batch) >= usageEventFlushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-parentCtx.Done():
			// Drain remaining buffered events before exiting.
			for {
				select {
				case envelope, ok := <-svc.usageEvents:
					if !ok {
						flush()
						return
					}
					batch = append(batch, envelope)
					if len(batch) >= usageEventFlushSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (svc *CPAService) persistUsageEvents(ctx context.Context, batch []usageEventEnvelope) {
	db := svc.entFromContext(ctx)
	creates := make([]*ent.CpaUsageEventCreate, 0, len(batch))
	for _, envelope := range batch {
		event := envelope.event
		requestedAt := event.Timestamp
		if requestedAt.IsZero() {
			requestedAt = time.Now().UTC()
		}
		creates = append(creates, db.CpaUsageEvent.Create().
			SetCpaInstanceID(envelope.instanceID).
			SetAuthIndex(event.AuthIndex).
			SetProvider(event.Provider).
			SetModel(event.Model).
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
	if err := db.CpaUsageEvent.CreateBulk(creates...).Exec(ctx); err != nil {
		log.Warn(ctx, "persist CPA usage events failed", log.Int("count", len(creates)), log.Cause(err))
		return
	}
	svc.observePercents(ctx, batch)
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
		if !strings.EqualFold(strings.TrimSpace(event.Provider), "codex") || event.Failed {
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
		state := svc.usageObservedAt[credentialID]
		if state.hasLastWrite && now.Sub(state.lastWrite) < percentObservationMinGap && absFloat64(percent-state.lastPercent) < 0.01 {
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

// lookupCredentialID resolves (instanceID, authIndex) to a credential ID with a
// short-lived per-instance cache to avoid a DB round trip per event.
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

// invalidateUsageCredentialCache drops cached credential mappings for an instance.
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
