package biz

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
)

type codexSecondaryObservation struct {
	collectorSessionID string
	eventID            int
	usedPercent        float64
	resetAt            time.Time
	observedAt         time.Time
}

// observeCodexUsageIntervals advances the precise 7d observation interval from
// persisted usage events. The first observation of a collector session or
// upstream quota window is only a baseline; it does not need to be zero usage.
func (svc *CPAService) observeCodexUsageIntervals(ctx context.Context, events []persistedUsageEvent) {
	byCredential := make(map[int][]codexSecondaryObservation)
	for _, persisted := range events {
		envelope := persisted.envelope
		event := envelope.event
		if event == nil || event.Failed || !strings.EqualFold(strings.TrimSpace(event.Provider), "codex") {
			continue
		}
		observation, ok := parseCodexSecondaryObservation(envelope.sessionID(), persisted.eventID, event)
		if !ok {
			continue
		}
		credentialID, found := svc.lookupCredentialID(ctx, envelope.instanceID, event.AuthIndex)
		if !found {
			continue
		}
		byCredential[credentialID] = append(byCredential[credentialID], observation)
	}
	if len(byCredential) == 0 {
		return
	}

	ids := make([]int, 0, len(byCredential))
	for credentialID := range byCredential {
		ids = append(ids, credentialID)
	}
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.IDIn(ids...)).
		All(ctx)
	if err != nil {
		log.Warn(ctx, "load CPA credentials for quota observations failed", log.Cause(err))
		return
	}
	for _, credential := range credentials {
		observed := credential.QuotaObserved
		reanchored := false
		for _, observation := range byCredential[credential.ID] {
			var changed bool
			observed, changed = advanceCodexSecondaryObservation(observed, observation)
			reanchored = reanchored || changed
		}
		if observed.SecondaryUsedPercent == nil {
			continue
		}

		svc.usageCacheMu.Lock()
		state := svc.usageObservedAt[credential.ID]
		skip := !reanchored && state.hasLastWrite &&
			time.Since(state.lastWrite) < percentObservationMinGap &&
			absFloat64(*observed.SecondaryUsedPercent-state.lastPercent) < 0.01
		svc.usageCacheMu.Unlock()
		if skip {
			continue
		}
		if err := svc.withCPAInstanceWriteRetry(ctx, credential.CpaInstanceID, func() error {
			return svc.entFromContext(ctx).CPACredential.UpdateOneID(credential.ID).
				SetQuotaObserved(observed).
				Exec(ctx)
		}); err != nil {
			log.Warn(ctx, "persist CPA observed quota interval failed",
				log.Int("credential_id", credential.ID),
				log.Cause(err),
			)
			continue
		}
		svc.usageCacheMu.Lock()
		if svc.usageObservedAt == nil {
			svc.usageObservedAt = make(map[int]usageObservedState)
		}
		svc.usageObservedAt[credential.ID] = usageObservedState{
			lastWrite:    time.Now().UTC(),
			lastPercent:  *observed.SecondaryUsedPercent,
			hasLastWrite: true,
		}
		svc.usageCacheMu.Unlock()
	}
}

func (envelope usageEventEnvelope) sessionID() string {
	if envelope.session == nil {
		return ""
	}
	return envelope.session.id
}

func parseCodexSecondaryObservation(sessionID string, eventID int, event interface {
	HeaderValue(string) string
}) (codexSecondaryObservation, bool) {
	rawPercent := strings.TrimSuffix(event.HeaderValue("x-codex-secondary-used-percent"), "%")
	percent, err := strconv.ParseFloat(strings.TrimSpace(rawPercent), 64)
	if err != nil || percent < 0 || percent > 100 {
		return codexSecondaryObservation{}, false
	}
	resetAt := parseHeaderUnixTime(event.HeaderValue("x-codex-secondary-reset-at"))
	if sessionID == "" || eventID <= 0 || resetAt == nil {
		return codexSecondaryObservation{}, false
	}
	return codexSecondaryObservation{
		collectorSessionID: sessionID,
		eventID:            eventID,
		usedPercent:        percent,
		resetAt:            *resetAt,
		observedAt:         time.Now().UTC(),
	}, true
}

func advanceCodexSecondaryObservation(
	observed objects.CPAQuotaObserved,
	next codexSecondaryObservation,
) (objects.CPAQuotaObserved, bool) {
	reanchor := observed.SecondaryCollectorSessionID != next.collectorSessionID ||
		observed.SecondaryBaselineUsedPercent == nil ||
		observed.SecondaryBaselineEventID == nil ||
		observed.SecondaryUsedPercent == nil ||
		observed.SecondaryLatestEventID == nil ||
		observed.SecondaryResetAt == nil ||
		!sameQuotaReset(*observed.SecondaryResetAt, next.resetAt) ||
		next.usedPercent < *observed.SecondaryUsedPercent ||
		next.eventID <= *observed.SecondaryLatestEventID
	if reanchor {
		baselinePercent := next.usedPercent
		baselineEventID := next.eventID
		latestPercent := next.usedPercent
		latestEventID := next.eventID
		resetAt := next.resetAt
		observed.SecondaryCollectorSessionID = next.collectorSessionID
		observed.SecondaryBaselineUsedPercent = &baselinePercent
		observed.SecondaryBaselineEventID = &baselineEventID
		observed.SecondaryUsedPercent = &latestPercent
		observed.SecondaryLatestEventID = &latestEventID
		observed.SecondaryResetAt = &resetAt
		observed.ObservedAt = &next.observedAt
		return observed, true
	}

	latestPercent := next.usedPercent
	latestEventID := next.eventID
	observed.SecondaryUsedPercent = &latestPercent
	observed.SecondaryLatestEventID = &latestEventID
	observed.ObservedAt = &next.observedAt
	return observed, false
}

type codexEstimateInterval struct {
	usedPercent float64
	fromEventID int
	toEventID   int
	source      string
}

func codexEstimateIntervalForItem(
	item *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
) *codexEstimateInterval {
	if item == nil || item.PeriodSeconds == nil || item.ResetAt == nil {
		return nil
	}
	period := *item.PeriodSeconds
	switch {
	case period == cpaWeeklyPeriodSeconds:
		return codexSecondaryEstimateInterval(item, observed)
	case isMonthlyQuotaPeriod(period):
		return codexRefreshEstimateInterval(item)
	default:
		return nil
	}
}

func codexSecondaryEstimateInterval(
	item *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
) *codexEstimateInterval {
	if observed.SecondaryCollectorSessionID == "" ||
		observed.SecondaryBaselineUsedPercent == nil ||
		observed.SecondaryBaselineEventID == nil ||
		observed.SecondaryUsedPercent == nil ||
		observed.SecondaryLatestEventID == nil ||
		observed.SecondaryResetAt == nil ||
		!sameQuotaReset(*observed.SecondaryResetAt, *item.ResetAt) ||
		*observed.SecondaryLatestEventID <= *observed.SecondaryBaselineEventID {
		return nil
	}
	// WHAM is commonly integer-rounded. A drop exceeding one percentage point
	// while reset_at remains unchanged is still treated as an activity reset or
	// quota expansion, but sub-point rounding differences do not invalidate a
	// precise header interval.
	if item.UsedPercent != nil && *item.UsedPercent+1 < *observed.SecondaryUsedPercent {
		return nil
	}
	delta := *observed.SecondaryUsedPercent - *observed.SecondaryBaselineUsedPercent
	if delta < cpaEstimateMinPercentDelta {
		return nil
	}
	return &codexEstimateInterval{
		usedPercent: delta,
		fromEventID: *observed.SecondaryBaselineEventID,
		toEventID:   *observed.SecondaryLatestEventID,
		source:      "precise-header-delta",
	}
}

func codexRefreshEstimateInterval(item *objects.CPAQuotaItem) *codexEstimateInterval {
	if item.EstimateCollectorSessionID == "" ||
		item.EstimateBaselineUsedPercent == nil ||
		item.EstimateBaselineEventID == nil ||
		item.EstimateLatestEventID == nil ||
		item.UsedPercent == nil ||
		*item.EstimateLatestEventID <= *item.EstimateBaselineEventID {
		return nil
	}
	delta := *item.UsedPercent - *item.EstimateBaselineUsedPercent
	if delta < cpaEstimateMinPercentDelta {
		return nil
	}
	return &codexEstimateInterval{
		usedPercent: delta,
		fromEventID: *item.EstimateBaselineEventID,
		toEventID:   *item.EstimateLatestEventID,
		source:      "refresh-delta",
	}
}

func prepareCodexMonthlyInterval(
	item *objects.CPAQuotaItem,
	previous *objects.CPAQuotaItem,
	collectorSessionID string,
	latestEventID int,
) {
	if item == nil || item.UsedPercent == nil || item.ResetAt == nil || collectorSessionID == "" {
		clearCodexEstimateInterval(item)
		return
	}
	reanchor := previous == nil || previous.UsedPercent == nil || previous.ResetAt == nil ||
		previous.EstimateCollectorSessionID != collectorSessionID ||
		previous.EstimateBaselineUsedPercent == nil || previous.EstimateBaselineEventID == nil ||
		!sameQuotaReset(*previous.ResetAt, *item.ResetAt) ||
		*item.UsedPercent < *previous.UsedPercent ||
		latestEventID < *previous.EstimateBaselineEventID
	if reanchor {
		baselinePercent := *item.UsedPercent
		baselineEventID := latestEventID
		item.EstimateCollectorSessionID = collectorSessionID
		item.EstimateBaselineUsedPercent = &baselinePercent
		item.EstimateBaselineEventID = &baselineEventID
		item.EstimateLatestEventID = &baselineEventID
		return
	}
	baselinePercent := *previous.EstimateBaselineUsedPercent
	baselineEventID := *previous.EstimateBaselineEventID
	item.EstimateCollectorSessionID = collectorSessionID
	item.EstimateBaselineUsedPercent = &baselinePercent
	item.EstimateBaselineEventID = &baselineEventID
	item.EstimateLatestEventID = &latestEventID
}

func clearCodexEstimateInterval(item *objects.CPAQuotaItem) {
	if item == nil {
		return
	}
	item.EstimateCollectorSessionID = ""
	item.EstimateBaselineUsedPercent = nil
	item.EstimateBaselineEventID = nil
	item.EstimateLatestEventID = nil
}

func previousQuotaItem(snapshot objects.CPAQuotaSnapshot, current *objects.CPAQuotaItem) *objects.CPAQuotaItem {
	if current == nil {
		return nil
	}
	for index := range snapshot.Items {
		candidate := &snapshot.Items[index]
		if candidate.ID == current.ID {
			return candidate
		}
	}
	return nil
}

func isMonthlyQuotaPeriod(period int) bool {
	return period >= 28*24*60*60 && period <= 31*24*60*60
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

func sameQuotaReset(left, right time.Time) bool {
	return left.Unix() == right.Unix()
}

func absFloat64(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
