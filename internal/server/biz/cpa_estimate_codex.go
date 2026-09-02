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

type codexWeeklyObservation struct {
	collectorSessionID string
	eventID            int
	usedPercent        float64
	resetAt            time.Time
	observedAt         time.Time
}

// observeCodexUsageIntervals advances the precise 7d observation interval from
// persisted usage events. Primary and secondary are wire slots, so the weekly
// window is normalized by its reported duration before advancing the interval.
// The first observation of a collector identity or upstream quota window is only
// a baseline; it does not need to be zero usage.
func (repository *cpaUsageRepository) observeCodexUsageIntervals(ctx context.Context, events []persistedUsageEvent) {
	byCredential := make(map[int][]codexWeeklyObservation)
	for _, persisted := range events {
		envelope := persisted.envelope
		event := envelope.event
		if event == nil || event.Failed || !strings.EqualFold(strings.TrimSpace(event.Provider), "codex") {
			continue
		}
		observation, ok := parseCodexWeeklyObservation(envelope.sessionID(), persisted.eventID, event)
		if !ok {
			continue
		}
		credentialID, found := repository.lookupCredentialID(ctx, envelope.instanceID, event.AuthIndex)
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
	credentials, err := repository.entFromContext(ctx).CPACredential.Query().
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
			observed, changed = advanceCodexWeeklyObservation(observed, observation)
			reanchored = reanchored || changed
		}
		if observed.SecondaryUsedPercent == nil {
			continue
		}

		state := repository.observedState(credential.ID)
		skip := !reanchored && state.hasLastWrite &&
			repository.now().Sub(state.lastWrite) < percentObservationMinGap &&
			absFloat64(*observed.SecondaryUsedPercent-state.lastPercent) < 0.01
		if skip {
			continue
		}
		if err := repository.withInstanceWriteRetry(ctx, credential.CpaInstanceID, func() error {
			return repository.entFromContext(ctx).CPACredential.UpdateOneID(credential.ID).
				SetQuotaObserved(observed).
				Exec(ctx)
		}); err != nil {
			log.Warn(ctx, "persist CPA observed quota interval failed",
				log.Int("credential_id", credential.ID),
				log.Cause(err),
			)
			continue
		}
		repository.setObservedState(credential.ID, usageObservedState{
			lastWrite:    repository.now(),
			lastPercent:  *observed.SecondaryUsedPercent,
			hasLastWrite: true,
		})
	}
}

func (envelope usageEventEnvelope) sessionID() string {
	if envelope.session == nil {
		return ""
	}
	return envelope.session.id
}

func parseCodexWeeklyObservation(sessionID string, eventID int, event interface {
	HeaderValue(string) string
}) (codexWeeklyObservation, bool) {
	if sessionID == "" || eventID <= 0 {
		return codexWeeklyObservation{}, false
	}
	for _, slot := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + slot + "-"
		minutes, err := strconv.Atoi(strings.TrimSpace(event.HeaderValue(prefix + "window-minutes")))
		if err != nil || minutes*60 != cpaWeeklyPeriodSeconds {
			continue
		}
		if observation, ok := parseCodexWeeklyWindowObservation(sessionID, eventID, prefix, event); ok {
			return observation, true
		}
	}

	// Older CPA HTTP usage records can carry the legacy secondary percent/reset
	// pair without window-minutes. Keep accepting that established 7d shape, but
	// never reinterpret an explicitly non-weekly secondary duration.
	if strings.TrimSpace(event.HeaderValue("x-codex-secondary-window-minutes")) == "" {
		return parseCodexWeeklyWindowObservation(sessionID, eventID, "x-codex-secondary-", event)
	}
	return codexWeeklyObservation{}, false
}

func parseCodexWeeklyWindowObservation(sessionID string, eventID int, prefix string, event interface {
	HeaderValue(string) string
}) (codexWeeklyObservation, bool) {
	rawPercent := strings.TrimSuffix(event.HeaderValue(prefix+"used-percent"), "%")
	percent, err := strconv.ParseFloat(strings.TrimSpace(rawPercent), 64)
	if err != nil || percent < 0 || percent > 100 {
		return codexWeeklyObservation{}, false
	}
	resetAt := parseHeaderUnixTime(event.HeaderValue(prefix + "reset-at"))
	if resetAt == nil {
		return codexWeeklyObservation{}, false
	}
	return codexWeeklyObservation{
		collectorSessionID: sessionID,
		eventID:            eventID,
		usedPercent:        percent,
		resetAt:            *resetAt,
		observedAt:         time.Now().UTC(),
	}, true
}

func advanceCodexWeeklyObservation(
	observed objects.CPAQuotaObserved,
	next codexWeeklyObservation,
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

type codexEstimateIntervalDecision struct {
	interval   *codexEstimateInterval
	skipReason string
}

func codexEstimateIntervalForItem(
	item *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
) *codexEstimateInterval {
	return codexEstimateIntervalDecisionForItem(item, observed).interval
}

func codexEstimateIntervalDecisionForItem(
	item *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
) codexEstimateIntervalDecision {
	if item == nil || item.PeriodSeconds == nil || item.ResetAt == nil {
		return codexEstimateIntervalDecision{skipReason: "missing-window-metadata"}
	}
	period := *item.PeriodSeconds
	switch {
	case period == cpaWeeklyPeriodSeconds:
		return codexWeeklyEstimateIntervalDecision(item, observed)
	case isMonthlyQuotaPeriod(period):
		return codexRefreshEstimateIntervalDecision(item)
	default:
		return codexEstimateIntervalDecision{skipReason: "unsupported-window-period"}
	}
}

func codexWeeklyEstimateIntervalDecision(
	item *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
) codexEstimateIntervalDecision {
	if observed.SecondaryCollectorSessionID == "" ||
		observed.SecondaryBaselineUsedPercent == nil ||
		observed.SecondaryBaselineEventID == nil ||
		observed.SecondaryUsedPercent == nil ||
		observed.SecondaryLatestEventID == nil ||
		observed.SecondaryResetAt == nil {
		return codexEstimateIntervalDecision{skipReason: "missing-weekly-observation"}
	}
	if !sameQuotaReset(*observed.SecondaryResetAt, *item.ResetAt) {
		return codexEstimateIntervalDecision{skipReason: "reset-mismatch"}
	}
	if *observed.SecondaryLatestEventID <= *observed.SecondaryBaselineEventID {
		return codexEstimateIntervalDecision{skipReason: "invalid-event-range"}
	}
	// WHAM is commonly integer-rounded. A drop exceeding one percentage point
	// while reset_at remains unchanged is still treated as an activity reset or
	// quota expansion, but sub-point rounding differences do not invalidate a
	// precise header interval.
	if item.UsedPercent != nil && *item.UsedPercent+1 < *observed.SecondaryUsedPercent {
		return codexEstimateIntervalDecision{skipReason: "quota-percent-regression"}
	}
	delta := *observed.SecondaryUsedPercent - *observed.SecondaryBaselineUsedPercent
	if delta < cpaEstimateMinPercentDelta {
		return codexEstimateIntervalDecision{skipReason: "insufficient-percent-delta"}
	}
	return codexEstimateIntervalDecision{interval: &codexEstimateInterval{
		usedPercent: delta,
		fromEventID: *observed.SecondaryBaselineEventID,
		toEventID:   *observed.SecondaryLatestEventID,
		source:      "precise-header-delta",
	}}
}

func codexRefreshEstimateIntervalDecision(item *objects.CPAQuotaItem) codexEstimateIntervalDecision {
	if item.EstimateCollectorSessionID == "" ||
		item.EstimateBaselineUsedPercent == nil ||
		item.EstimateBaselineEventID == nil ||
		item.EstimateLatestEventID == nil ||
		item.UsedPercent == nil {
		return codexEstimateIntervalDecision{skipReason: "missing-refresh-observation"}
	}
	if *item.EstimateLatestEventID <= *item.EstimateBaselineEventID {
		return codexEstimateIntervalDecision{skipReason: "invalid-event-range"}
	}
	delta := *item.UsedPercent - *item.EstimateBaselineUsedPercent
	if delta < cpaEstimateMinPercentDelta {
		return codexEstimateIntervalDecision{skipReason: "insufficient-percent-delta"}
	}
	return codexEstimateIntervalDecision{interval: &codexEstimateInterval{
		usedPercent: delta,
		fromEventID: *item.EstimateBaselineEventID,
		toEventID:   *item.EstimateLatestEventID,
		source:      "refresh-delta",
	}}
}

func prepareCodexWeeklyInterval(
	item *objects.CPAQuotaItem,
	previous *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
	collectorSessionID string,
) bool {
	if item == nil || item.ResetAt == nil || item.PeriodSeconds == nil ||
		*item.PeriodSeconds != cpaWeeklyPeriodSeconds || collectorSessionID == "" ||
		observed.SecondaryCollectorSessionID != collectorSessionID ||
		observed.SecondaryBaselineUsedPercent == nil || observed.SecondaryBaselineEventID == nil ||
		observed.SecondaryUsedPercent == nil || observed.SecondaryLatestEventID == nil ||
		observed.SecondaryResetAt == nil || !sameQuotaReset(*item.ResetAt, *observed.SecondaryResetAt) {
		clearCodexEstimateInterval(item)
		return true
	}

	reanchored := previous == nil || previous.EstimateCollectorSessionID != collectorSessionID ||
		previous.EstimateBaselineUsedPercent == nil || previous.EstimateBaselineEventID == nil ||
		previous.EstimateLatestEventID == nil || previous.ResetAt == nil ||
		!sameQuotaReset(*previous.ResetAt, *item.ResetAt) ||
		*previous.EstimateBaselineUsedPercent != *observed.SecondaryBaselineUsedPercent ||
		*previous.EstimateBaselineEventID != *observed.SecondaryBaselineEventID ||
		*observed.SecondaryLatestEventID < *previous.EstimateLatestEventID

	baselinePercent := *observed.SecondaryBaselineUsedPercent
	baselineEventID := *observed.SecondaryBaselineEventID
	latestEventID := *observed.SecondaryLatestEventID
	item.EstimateCollectorSessionID = collectorSessionID
	item.EstimateBaselineUsedPercent = &baselinePercent
	item.EstimateBaselineEventID = &baselineEventID
	item.EstimateLatestEventID = &latestEventID
	return reanchored
}

func prepareCodexMonthlyInterval(
	item *objects.CPAQuotaItem,
	previous *objects.CPAQuotaItem,
	collectorSessionID string,
	latestEventID int,
) bool {
	if item == nil || item.UsedPercent == nil || item.ResetAt == nil || collectorSessionID == "" {
		clearCodexEstimateInterval(item)
		return true
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
		return true
	}
	baselinePercent := *previous.EstimateBaselineUsedPercent
	baselineEventID := *previous.EstimateBaselineEventID
	item.EstimateCollectorSessionID = collectorSessionID
	item.EstimateBaselineUsedPercent = &baselinePercent
	item.EstimateBaselineEventID = &baselineEventID
	item.EstimateLatestEventID = &latestEventID
	return false
}

func carryForwardCodexQuotaEstimate(
	item *objects.CPAQuotaItem,
	previous *objects.CPAQuotaItem,
	collectorSessionID string,
	intervalReanchored bool,
) {
	if item == nil || previous == nil || intervalReanchored || collectorSessionID == "" ||
		previous.EstimatedLimitUSD == nil || previous.EstimatedCostUSD == nil ||
		item.PeriodSeconds == nil || previous.PeriodSeconds == nil ||
		*item.PeriodSeconds != *previous.PeriodSeconds || item.ResetAt == nil || previous.ResetAt == nil ||
		!sameQuotaReset(*item.ResetAt, *previous.ResetAt) ||
		!isEstimableCodexQuotaPeriod(*item.PeriodSeconds) ||
		item.EstimateCollectorSessionID != collectorSessionID ||
		previous.EstimateCollectorSessionID != collectorSessionID ||
		item.EstimateBaselineUsedPercent == nil || previous.EstimateBaselineUsedPercent == nil ||
		*item.EstimateBaselineUsedPercent != *previous.EstimateBaselineUsedPercent ||
		item.EstimateBaselineEventID == nil || previous.EstimateBaselineEventID == nil ||
		*item.EstimateBaselineEventID != *previous.EstimateBaselineEventID ||
		item.EstimateLatestEventID == nil || previous.EstimateLatestEventID == nil ||
		*item.EstimateLatestEventID < *previous.EstimateLatestEventID {
		return
	}

	limit := *previous.EstimatedLimitUSD
	cost := *previous.EstimatedCostUSD
	item.EstimatedLimitUSD = &limit
	item.EstimatedCostUSD = &cost
	item.EstimateSource = previous.EstimateSource
}

func isEstimableCodexQuotaPeriod(period int) bool {
	return period == cpaWeeklyPeriodSeconds || isMonthlyQuotaPeriod(period)
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
