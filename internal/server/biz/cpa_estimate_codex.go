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

// cpaQuotaResetTolerance absorbs the small drift of a provider-reported window
// boundary. Upstream reports a floating reset until usage anchors a window, and
// AxonHub reads the same boundary from two independent sources (usage response
// headers and the quota snapshot), so exact-second equality re-anchored healthy
// intervals. The value stays far below the shortest estimable window (7 days).
const cpaQuotaResetTolerance = 5 * time.Minute

type codexWeeklyObservation struct {
	collectorSessionID string
	eventID            int
	usedPercent        float64
	resetAt            time.Time
	observedAt         time.Time
	// durationVerified records whether the sample carried an explicit 7d window
	// duration, which is what distinguishes the weekly window from a 5h window
	// in the same wire slot. It is never persisted.
	durationVerified bool
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
			if !observation.durationVerified && !codexWeeklyResetConfirmed(credential.QuotaData, observation.resetAt) {
				// A duration-less legacy sample cannot tell the 5h window from
				// the 7d window, so only a snapshot that reports the same reset
				// as its 7d window makes it trustworthy.
				continue
			}
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
			observation.durationVerified = true
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

// codexWeeklyResetConfirmed reports whether the credential's current quota
// snapshot already identifies this reset as its 7d window. Duration-less legacy
// records cannot tell the 5h window from the 7d window, so an ambiguous sample
// is only trusted when the snapshot agrees.
func codexWeeklyResetConfirmed(snapshot objects.CPAQuotaSnapshot, resetAt time.Time) bool {
	for index := range snapshot.Items {
		item := &snapshot.Items[index]
		if item.PeriodSeconds == nil || *item.PeriodSeconds != cpaWeeklyPeriodSeconds || item.ResetAt == nil {
			continue
		}
		if sameQuotaReset(*item.ResetAt, resetAt) {
			return true
		}
	}
	return false
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
		!sameQuotaReset(*observed.SecondaryResetAt, next.resetAt)
	if reanchor {
		baselinePercent := next.usedPercent
		baselineEventID := next.eventID
		latestPercent := next.usedPercent
		latestEventID := next.eventID
		checkpointEventID := next.eventID
		resetAt := next.resetAt
		observed.SecondaryCollectorSessionID = next.collectorSessionID
		observed.SecondaryBaselineUsedPercent = &baselinePercent
		observed.SecondaryBaselineEventID = &baselineEventID
		observed.SecondaryUsedPercent = &latestPercent
		observed.SecondaryLatestEventID = &latestEventID
		observed.SecondaryCheckpointEventID = &checkpointEventID
		observed.SecondaryResetAt = &resetAt
		observed.ObservedAt = &next.observedAt
		return observed, true
	}

	// Event IDs are the durable interval boundary. A replayed or out-of-order
	// usage event must not look like a new quota window and clear a valid
	// estimate.
	checkpointEventID := observed.SecondaryCheckpointEventID
	if checkpointEventID == nil {
		// Rows written before this field existed stored the newest event in
		// SecondaryLatestEventID, even when that sample had a lower percentage.
		// Until a sample at or above the high-water arrives, such a row's
		// interval end can therefore sit on a regressed sample and read a little
		// low; the pairing is not recoverable and heals on the next high-water
		// sample.
		checkpointEventID = observed.SecondaryLatestEventID
	}
	if next.eventID <= *checkpointEventID {
		return observed, false
	}
	newCheckpointEventID := next.eventID
	observed.SecondaryCheckpointEventID = &newCheckpointEventID
	observed.ObservedAt = &next.observedAt
	// The 7d reset timestamp is the authoritative window boundary. A lower
	// percentage with the same reset is a non-monotonic provider sample (for
	// example a 5h refresh side effect or rolling-window correction), not a new
	// local interval. Retain the high-water percentage so the existing estimate
	// remains usable until the 7d window actually changes.
	if next.usedPercent < *observed.SecondaryUsedPercent {
		return observed, false
	}

	latestPercent := next.usedPercent
	latestEventID := next.eventID
	observed.SecondaryUsedPercent = &latestPercent
	observed.SecondaryLatestEventID = &latestEventID
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

// sameQuotaPeriod reports whether two quota items describe the same window period.
func sameQuotaPeriod(left, right *objects.CPAQuotaItem) bool {
	return left != nil && right != nil &&
		left.PeriodSeconds != nil && right.PeriodSeconds != nil &&
		*left.PeriodSeconds == *right.PeriodSeconds
}

// sameCodexEstimateWindow reports whether two quota items describe the same
// estimation window: same wire slot or same resource group, plus the same period
// and reset boundary. The id encodes the upstream wire slot, and a dynamic
// window can move between slots; the group label can be renamed by the provider
// (additional limits take it from `limit_name`), so either identity anchor is
// accepted. Cross-group pairs still cannot match, because that would require two
// different slot ids to be equal.
func sameCodexEstimateWindow(left, right *objects.CPAQuotaItem) bool {
	return left != nil && right != nil &&
		(left.ID == right.ID || left.Group == right.Group) &&
		sameQuotaPeriod(left, right) &&
		left.ResetAt != nil && right.ResetAt != nil && sameQuotaReset(*left.ResetAt, *right.ResetAt)
}

// adoptCodexQuotaInterval carries a still-current window's interval metadata and
// last-known estimate when this refresh has no fresh observation to advance it
// (no live collector checkpoint, or a snapshot without a usable percentage). It
// reports whether the interval must be treated as re-anchored.
func adoptCodexQuotaInterval(item, previous *objects.CPAQuotaItem) bool {
	if !sameCodexEstimateWindow(item, previous) {
		clearCodexEstimateInterval(item)
		return true
	}
	item.EstimateCollectorSessionID = previous.EstimateCollectorSessionID
	item.EstimateBaselineUsedPercent = previous.EstimateBaselineUsedPercent
	item.EstimateBaselineEventID = previous.EstimateBaselineEventID
	item.EstimateLatestEventID = previous.EstimateLatestEventID
	return false
}

func prepareCodexWeeklyInterval(
	item *objects.CPAQuotaItem,
	previous *objects.CPAQuotaItem,
	observed objects.CPAQuotaObserved,
	collectorSessionID string,
) bool {
	if item == nil || item.ResetAt == nil || item.PeriodSeconds == nil ||
		*item.PeriodSeconds != cpaWeeklyPeriodSeconds {
		clearCodexEstimateInterval(item)
		return true
	}
	if collectorSessionID == "" {
		// No live collector checkpoint: the observation cannot advance, so the
		// durable metadata in quota_data is the only interval state. Keep it
		// instead of discarding a window that is still current.
		return adoptCodexQuotaInterval(item, previous)
	}
	if observed.SecondaryCollectorSessionID != collectorSessionID ||
		observed.SecondaryBaselineUsedPercent == nil || observed.SecondaryBaselineEventID == nil ||
		observed.SecondaryUsedPercent == nil || observed.SecondaryLatestEventID == nil ||
		observed.SecondaryResetAt == nil || !sameQuotaReset(*item.ResetAt, *observed.SecondaryResetAt) {
		clearCodexEstimateInterval(item)
		return true
	}

	reanchored := !sameCodexEstimateWindow(item, previous) ||
		previous.EstimateCollectorSessionID != collectorSessionID ||
		previous.EstimateBaselineUsedPercent == nil || previous.EstimateBaselineEventID == nil ||
		previous.EstimateLatestEventID == nil ||
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
	if item == nil || item.UsedPercent == nil || item.ResetAt == nil || collectorSessionID == "" || latestEventID <= 0 {
		// No usable sample for this refresh: keep the previous window state
		// instead of clearing a window that has not moved. A non-positive
		// checkpoint event id cannot anchor an interval: it would turn the
		// interval into "all events of the cycle" while the baseline percentage
		// still measures only usage after this refresh.
		return adoptCodexQuotaInterval(item, previous)
	}
	// A percentage that fell a full threshold below the anchor is a real reset
	// or grant, not provider rounding; sub-threshold movement must not restart a
	// long local interval. A non-positive recorded baseline event id is a legacy
	// anchor written before this distinction existed and must be re-anchored.
	reanchor := previous == nil || !sameCodexEstimateWindow(item, previous) ||
		previous.EstimateCollectorSessionID != collectorSessionID ||
		previous.EstimateBaselineUsedPercent == nil ||
		previous.EstimateBaselineEventID == nil || *previous.EstimateBaselineEventID <= 0 ||
		*item.UsedPercent+cpaEstimateMinPercentDelta <= *previous.EstimateBaselineUsedPercent ||
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

// carryForwardCodexQuotaEstimate keeps the last known estimate of a window that
// this refresh cannot recompute. Callers must pass items filtered to 7d or
// 28-31d windows (see estimateWindowItems); the period is otherwise only checked
// for equality, and a 5h window has no estimate contract.
func carryForwardCodexQuotaEstimate(
	item *objects.CPAQuotaItem,
	previous *objects.CPAQuotaItem,
	intervalReanchored bool,
) {
	if item == nil || previous == nil || intervalReanchored ||
		previous.EstimatedLimitUSD == nil || previous.EstimatedCostUSD == nil ||
		!sameCodexEstimateWindow(item, previous) ||
		item.EstimateCollectorSessionID == "" ||
		item.EstimateCollectorSessionID != previous.EstimateCollectorSessionID ||
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

func clearCodexEstimateInterval(item *objects.CPAQuotaItem) {
	if item == nil {
		return
	}
	item.EstimateCollectorSessionID = ""
	item.EstimateBaselineUsedPercent = nil
	item.EstimateBaselineEventID = nil
	item.EstimateLatestEventID = nil
}

// previousQuotaItem returns the previous snapshot's counterpart of the same
// window. The window identity decides, and an id match only wins among
// candidates that describe the same window, so a slot that changed window does
// not shadow the window that migrated into another slot.
func previousQuotaItem(snapshot objects.CPAQuotaSnapshot, current *objects.CPAQuotaItem) *objects.CPAQuotaItem {
	if current == nil {
		return nil
	}
	var sameWindow *objects.CPAQuotaItem
	for index := range snapshot.Items {
		candidate := &snapshot.Items[index]
		if !sameCodexEstimateWindow(candidate, current) {
			continue
		}
		if candidate.ID == current.ID {
			return candidate
		}
		if sameWindow == nil {
			sameWindow = candidate
		}
	}
	return sameWindow
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

// sameQuotaReset reports whether two observations describe the same window
// boundary. Provider reset timestamps drift by seconds between the usage
// response headers and the quota snapshot, and an unanchored window reports a
// floating reset, so a small tolerance keeps one window from looking like two.
func sameQuotaReset(left, right time.Time) bool {
	return left.Sub(right).Abs() <= cpaQuotaResetTolerance
}

func absFloat64(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
