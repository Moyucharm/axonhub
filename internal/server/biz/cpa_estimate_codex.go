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

// cpaQuotaResetTolerance absorbs small drift between provider snapshots and usage headers.
const cpaQuotaResetTolerance = 5 * time.Minute

type codexWindowObservation struct {
	collectorSessionID string
	eventID            int
	periodSeconds      int
	usedPercent        float64
	resetAt            time.Time
	observedAt         time.Time
	durationVerified   bool
}

// observeCodexUsageIntervals advances each reported window independently.
func (repository *cpaUsageRepository) observeCodexUsageIntervals(ctx context.Context, events []persistedUsageEvent) {
	byCredential := make(map[int][]codexWindowObservation)
	for _, persisted := range events {
		envelope := persisted.envelope
		event := envelope.event
		if event == nil || event.Failed || !strings.EqualFold(strings.TrimSpace(event.Provider), "codex") {
			continue
		}
		observations := parseCodexWindowObservations(envelope.sessionID(), persisted.eventID, event)
		if len(observations) == 0 {
			continue
		}
		credentialID, found := repository.lookupCredentialID(ctx, envelope.instanceID, event.AuthIndex)
		if !found {
			continue
		}
		byCredential[credentialID] = append(byCredential[credentialID], observations...)
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
			if !observation.durationVerified && !codexWindowResetConfirmed(credential.QuotaData, observation.periodSeconds, observation.resetAt) {
				continue
			}
			var changed bool
			observed, changed = advanceCodexWindowObservation(observed, observation)
			reanchored = reanchored || changed
		}
		windows := observed.ObservedWindows()
		if len(windows) == 0 {
			continue
		}
		state := repository.observedState(credential.ID)
		skip := !reanchored && state.hasLastWrite &&
			repository.now().Sub(state.lastWrite) < percentObservationMinGap
		for _, window := range windows {
			previous, ok := state.lastPercentByPeriod[window.PeriodSeconds]
			if window.UsedPercent == nil || !ok || absFloat64(*window.UsedPercent-previous) >= 0.01 {
				skip = false
				break
			}
		}
		if len(state.lastPercentByPeriod) != len(windows) {
			skip = false
		}
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
		percentByPeriod := make(map[int]float64, len(windows))
		for _, window := range windows {
			if window.UsedPercent != nil {
				percentByPeriod[window.PeriodSeconds] = *window.UsedPercent
			}
		}
		repository.setObservedState(credential.ID, usageObservedState{
			lastWrite:           repository.now(),
			lastPercentByPeriod: percentByPeriod,
			hasLastWrite:        true,
		})
	}
}

func (envelope usageEventEnvelope) sessionID() string {
	if envelope.session == nil {
		return ""
	}
	return envelope.session.id
}

func parseCodexWindowObservations(sessionID string, eventID int, event interface {
	HeaderValue(string) string
}) []codexWindowObservation {
	if sessionID == "" || eventID <= 0 {
		return nil
	}
	var observations []codexWindowObservation
	for _, slot := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + slot + "-"
		minutes, err := strconv.Atoi(strings.TrimSpace(event.HeaderValue(prefix + "window-minutes")))
		if err != nil || (minutes*60 != cpaFiveHourPeriodSeconds && minutes*60 != cpaWeeklyPeriodSeconds) {
			continue
		}
		if observation, ok := parseCodexWeeklyWindowObservation(sessionID, eventID, prefix, event); ok {
			observation.periodSeconds = minutes * 60
			observation.durationVerified = true
			observations = append(observations, observation)
		}
	}
	// Duration-less secondary records require a matching weekly snapshot reset.
	if strings.TrimSpace(event.HeaderValue("x-codex-secondary-window-minutes")) == "" {
		if observation, ok := parseCodexWeeklyWindowObservation(sessionID, eventID, "x-codex-secondary-", event); ok {
			observation.periodSeconds = cpaWeeklyPeriodSeconds
			observations = append(observations, observation)
		}
	}
	return observations
}

func parseCodexWeeklyWindowObservation(sessionID string, eventID int, prefix string, event interface {
	HeaderValue(string) string
}) (codexWindowObservation, bool) {
	rawPercent := strings.TrimSuffix(event.HeaderValue(prefix+"used-percent"), "%")
	percent, err := strconv.ParseFloat(strings.TrimSpace(rawPercent), 64)
	if err != nil || percent < 0 || percent > 100 {
		return codexWindowObservation{}, false
	}
	resetAt := parseHeaderUnixTime(event.HeaderValue(prefix + "reset-at"))
	if resetAt == nil {
		return codexWindowObservation{}, false
	}
	return codexWindowObservation{
		collectorSessionID: sessionID,
		eventID:            eventID,
		usedPercent:        percent,
		resetAt:            *resetAt,
		observedAt:         time.Now().UTC(),
	}, true
}

// codexWindowResetConfirmed verifies an ambiguous legacy sample against its snapshot.
func codexWindowResetConfirmed(snapshot objects.CPAQuotaSnapshot, periodSeconds int, resetAt time.Time) bool {
	for index := range snapshot.Items {
		item := &snapshot.Items[index]
		if item.PeriodSeconds != nil && *item.PeriodSeconds == periodSeconds &&
			item.ResetAt != nil && sameQuotaReset(*item.ResetAt, resetAt) {
			return true
		}
	}
	return false
}

func advanceCodexWindowObservation(
	observed objects.CPAQuotaObserved,
	next codexWindowObservation,
) (objects.CPAQuotaObserved, bool) {
	window, _ := observed.Window(next.periodSeconds)
	reanchor := window.CollectorSessionID != next.collectorSessionID ||
		window.BaselineUsedPercent == nil || window.BaselineEventID == nil ||
		window.UsedPercent == nil || window.LatestEventID == nil ||
		window.ResetAt == nil || !sameQuotaReset(*window.ResetAt, next.resetAt)
	if reanchor {
		baselinePercent, eventID, resetAt := next.usedPercent, next.eventID, next.resetAt
		window = objects.CPAQuotaWindowObservation{
			PeriodSeconds: next.periodSeconds, CollectorSessionID: next.collectorSessionID,
			BaselineUsedPercent: &baselinePercent, BaselineEventID: &eventID,
			UsedPercent: &baselinePercent, LatestEventID: &eventID,
			CheckpointEventID: &eventID, ResetAt: &resetAt, ObservedAt: &next.observedAt,
		}
		return observed.WithWindow(window), true
	}
	checkpointEventID := window.CheckpointEventID
	if checkpointEventID == nil {
		checkpointEventID = window.LatestEventID
	}
	if next.eventID <= *checkpointEventID {
		return observed, false
	}
	checkpoint := next.eventID
	window.CheckpointEventID = &checkpoint
	window.ObservedAt = &next.observedAt
	if next.usedPercent >= *window.UsedPercent {
		percent, eventID := next.usedPercent, next.eventID
		window.UsedPercent = &percent
		window.LatestEventID = &eventID
	}
	return observed.WithWindow(window), false
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

func codexEstimateIntervalDecisionForItem(item *objects.CPAQuotaItem, observed objects.CPAQuotaObserved, provider string) codexEstimateIntervalDecision {
	if item == nil || item.PeriodSeconds == nil || item.ResetAt == nil {
		return codexEstimateIntervalDecision{skipReason: "missing-window-metadata"}
	}
	period := *item.PeriodSeconds
	if !cpaEstimableQuotaPeriod(period) {
		return codexEstimateIntervalDecision{skipReason: "unsupported-window-period"}
	}
	if cpaPreciseWindowPeriod(provider, period) {
		return codexPreciseEstimateIntervalDecision(item, observed)
	}
	return codexRefreshEstimateIntervalDecision(item)
}

func codexPreciseEstimateIntervalDecision(item *objects.CPAQuotaItem, observed objects.CPAQuotaObserved) codexEstimateIntervalDecision {
	window, ok := observed.Window(*item.PeriodSeconds)
	if !ok || window.CollectorSessionID == "" || window.BaselineUsedPercent == nil ||
		window.BaselineEventID == nil || window.UsedPercent == nil ||
		window.LatestEventID == nil || window.ResetAt == nil {
		return codexEstimateIntervalDecision{skipReason: "missing-precise-observation"}
	}
	if !sameQuotaReset(*window.ResetAt, *item.ResetAt) {
		return codexEstimateIntervalDecision{skipReason: "reset-mismatch"}
	}
	if *window.LatestEventID <= *window.BaselineEventID {
		return codexEstimateIntervalDecision{skipReason: "invalid-event-range"}
	}
	// One point of slack absorbs integer-rounded quota snapshot percentages.
	if item.UsedPercent != nil && *item.UsedPercent+1 < *window.UsedPercent {
		return codexEstimateIntervalDecision{skipReason: "quota-percent-regression"}
	}
	delta := *window.UsedPercent - *window.BaselineUsedPercent
	if delta < cpaEstimateMinPercentDeltaFor(*item.PeriodSeconds) {
		return codexEstimateIntervalDecision{skipReason: "insufficient-percent-delta"}
	}
	return codexEstimateIntervalDecision{interval: &codexEstimateInterval{
		usedPercent: delta, fromEventID: *window.BaselineEventID,
		toEventID: *window.LatestEventID, source: "precise-header-delta",
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
	if delta < cpaEstimateMinPercentDeltaFor(*item.PeriodSeconds) {
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

func prepareCodexPreciseInterval(item *objects.CPAQuotaItem, previous *objects.CPAQuotaItem, observed objects.CPAQuotaObserved, collectorSessionID string) bool {
	if item == nil || item.ResetAt == nil || item.PeriodSeconds == nil ||
		(*item.PeriodSeconds != cpaFiveHourPeriodSeconds && *item.PeriodSeconds != cpaWeeklyPeriodSeconds) {
		clearCodexEstimateInterval(item)
		return true
	}
	if collectorSessionID == "" {
		return adoptCodexQuotaInterval(item, previous)
	}
	window, ok := observed.Window(*item.PeriodSeconds)
	if !ok || window.CollectorSessionID != collectorSessionID ||
		window.BaselineUsedPercent == nil || window.BaselineEventID == nil ||
		window.UsedPercent == nil || window.LatestEventID == nil ||
		window.ResetAt == nil || !sameQuotaReset(*item.ResetAt, *window.ResetAt) {
		clearCodexEstimateInterval(item)
		return true
	}
	reanchored := !sameCodexEstimateWindow(item, previous) ||
		previous.EstimateCollectorSessionID != collectorSessionID ||
		previous.EstimateBaselineUsedPercent == nil || previous.EstimateBaselineEventID == nil ||
		previous.EstimateLatestEventID == nil ||
		*previous.EstimateBaselineUsedPercent != *window.BaselineUsedPercent ||
		*previous.EstimateBaselineEventID != *window.BaselineEventID ||
		*window.LatestEventID < *previous.EstimateLatestEventID
	baselinePercent, baselineEventID, latestEventID := *window.BaselineUsedPercent, *window.BaselineEventID, *window.LatestEventID
	item.EstimateCollectorSessionID = collectorSessionID
	item.EstimateBaselineUsedPercent = &baselinePercent
	item.EstimateBaselineEventID = &baselineEventID
	item.EstimateLatestEventID = &latestEventID
	return reanchored
}

func prepareCodexRefreshInterval(
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
		*item.UsedPercent+cpaEstimateMinPercentDeltaFor(*item.PeriodSeconds) <= *previous.EstimateBaselineUsedPercent ||
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

// carryForwardCodexQuotaEstimate keeps the last estimate of a window that
// cannot be recomputed. The 5h and 7d Codex windows share the precise contract.
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
