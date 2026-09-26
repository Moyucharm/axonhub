package objects

import "time"

// CPAQuotaState describes the latest quota collection result for a CPA credential.
type CPAQuotaState string

const (
	CPAQuotaStatePending          CPAQuotaState = "pending"
	CPAQuotaStateSuccess          CPAQuotaState = "success"
	CPAQuotaStateError            CPAQuotaState = "error"
	CPAQuotaStateUnsupported      CPAQuotaState = "unsupported"
	CPAQuotaStateInsufficientData CPAQuotaState = "insufficient_data"
)

// CPACredentialHealthState is the persisted, time-independent health projection.
// Quota cooldown remains orthogonal so it can expire lazily without a database write.
type CPACredentialHealthState string

const (
	CPACredentialHealthHealthy  CPACredentialHealthState = "healthy"
	CPACredentialHealthAbnormal CPACredentialHealthState = "abnormal"
	CPACredentialHealthPending  CPACredentialHealthState = "pending"
	CPACredentialHealthDisabled CPACredentialHealthState = "disabled"
)

// CPAQuotaSnapshot stores the normalized, provider-independent quota view.
type CPAQuotaSnapshot struct {
	Items []CPAQuotaItem `json:"items,omitempty"`
	// ResetCredits mirrors the provider reset cards read together with quota.
	// They are display data only: every consume is re-validated live first.
	ResetCredits []CPAQuotaResetCredit `json:"reset_credits,omitempty"`
	// ResetCreditsFailed marks a refresh whose card read failed while quota
	// succeeded, so the UI never renders a failed read as "no cards".
	ResetCreditsFailed bool `json:"reset_credits_failed,omitempty"`
}

// CPAQuotaResetCredit is one provider reset card usable for a quota reset.
type CPAQuotaResetCredit struct {
	ID        string     `json:"id"`
	Title     string     `json:"title,omitempty"`
	ResetType string     `json:"reset_type,omitempty"`
	GrantedAt *time.Time `json:"granted_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// CPAQuotaItem represents one quota window, balance, or provider-specific limit.
type CPAQuotaItem struct {
	ID               string     `json:"id"`
	Group            string     `json:"group,omitempty"`
	Label            string     `json:"label"`
	Description      string     `json:"description,omitempty"`
	UsedPercent      *float64   `json:"used_percent,omitempty"`
	RemainingPercent *float64   `json:"remaining_percent,omitempty"`
	Used             *float64   `json:"used,omitempty"`
	Limit            *float64   `json:"limit,omitempty"`
	Remaining        *float64   `json:"remaining,omitempty"`
	Unit             string     `json:"unit,omitempty"`
	ResetAt          *time.Time `json:"reset_at,omitempty"`
	PeriodSeconds    *int       `json:"period_seconds,omitempty"`

	// EstimatedLimitUSD is the estimated total quota value derived from the
	// locally observed cost and percentage delta.
	EstimatedLimitUSD *float64 `json:"estimated_limit_usd,omitempty"`
	// EstimatedCostUSD is the locally observed cost within the estimation interval.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd,omitempty"`
	// EstimateSource records the estimation contract, such as
	// "precise-header-delta" or "refresh-delta".
	EstimateSource string `json:"estimate_source,omitempty"`

	// Estimate interval metadata is persisted inside quota_data but is not
	// exposed by GraphQL. Each estimable window carries its own local baseline
	// without racing the collector-owned observation.
	// The historical SessionID JSON name is retained for compatibility; the
	// value is the stable local collector identity for the CPA instance.
	EstimateCollectorSessionID  string   `json:"estimate_collector_session_id,omitempty"`
	EstimateBaselineUsedPercent *float64 `json:"estimate_baseline_used_percent,omitempty"`
	EstimateBaselineEventID     *int     `json:"estimate_baseline_event_id,omitempty"`
	EstimateLatestEventID       *int     `json:"estimate_latest_event_id,omitempty"`
}

const (
	CPAFiveHourPeriodSeconds = 5 * 60 * 60
	CPAWeeklyPeriodSeconds   = 7 * 24 * 60 * 60
)

// CPAQuotaWindowObservation captures one continuous observation interval for a window.
type CPAQuotaWindowObservation struct {
	PeriodSeconds       int        `json:"period_seconds"`
	CollectorSessionID  string     `json:"collector_session_id,omitempty"`
	BaselineUsedPercent *float64   `json:"baseline_used_percent,omitempty"`
	BaselineEventID     *int       `json:"baseline_event_id,omitempty"`
	UsedPercent         *float64   `json:"used_percent,omitempty"`
	LatestEventID       *int       `json:"latest_event_id,omitempty"`
	CheckpointEventID   *int       `json:"checkpoint_event_id,omitempty"`
	ResetAt             *time.Time `json:"reset_at,omitempty"`
	ObservedAt          *time.Time `json:"observed_at,omitempty"`
}

// CPAQuotaObserved stores independent observations by window period.
// Secondary* fields retain the legacy single-window shape for read compatibility only.
type CPAQuotaObserved struct {
	Windows []CPAQuotaWindowObservation `json:"windows,omitempty"`
	// The historical SessionID JSON name is retained for compatibility; the
	// value is the stable local collector identity for the CPA instance.
	SecondaryCollectorSessionID  string     `json:"secondary_collector_session_id,omitempty"`
	SecondaryBaselineUsedPercent *float64   `json:"secondary_baseline_used_percent,omitempty"`
	SecondaryBaselineEventID     *int       `json:"secondary_baseline_event_id,omitempty"`
	SecondaryUsedPercent         *float64   `json:"secondary_used_percent,omitempty"`
	SecondaryLatestEventID       *int       `json:"secondary_latest_event_id,omitempty"`
	SecondaryResetAt             *time.Time `json:"secondary_reset_at,omitempty"`
	ObservedAt                   *time.Time `json:"observed_at,omitempty"`
	// SecondaryCheckpointEventID is the newest persisted usage event seen in
	// this interval. It only guards against replayed or out-of-order events;
	// SecondaryLatestEventID is the interval end, i.e. the sample that produced
	// the current high-water percentage.
	SecondaryCheckpointEventID *int `json:"secondary_checkpoint_event_id,omitempty"`
}

// ObservedWindows reads the current shape, migrating legacy 7d observations in memory.
func (observed CPAQuotaObserved) ObservedWindows() []CPAQuotaWindowObservation {
	if len(observed.Windows) > 0 {
		return observed.Windows
	}
	if observed.SecondaryCollectorSessionID == "" && observed.SecondaryBaselineUsedPercent == nil &&
		observed.SecondaryBaselineEventID == nil && observed.SecondaryUsedPercent == nil &&
		observed.SecondaryLatestEventID == nil && observed.SecondaryCheckpointEventID == nil &&
		observed.SecondaryResetAt == nil {
		return nil
	}
	checkpoint := observed.SecondaryCheckpointEventID
	if checkpoint == nil {
		checkpoint = observed.SecondaryLatestEventID
	}
	return []CPAQuotaWindowObservation{{
		PeriodSeconds:       CPAWeeklyPeriodSeconds,
		CollectorSessionID:  observed.SecondaryCollectorSessionID,
		BaselineUsedPercent: observed.SecondaryBaselineUsedPercent,
		BaselineEventID:     observed.SecondaryBaselineEventID,
		UsedPercent:         observed.SecondaryUsedPercent,
		LatestEventID:       observed.SecondaryLatestEventID,
		CheckpointEventID:   checkpoint,
		ResetAt:             observed.SecondaryResetAt,
		ObservedAt:          observed.ObservedAt,
	}}
}

// Window reads the observation for one period.
func (observed CPAQuotaObserved) Window(periodSeconds int) (CPAQuotaWindowObservation, bool) {
	for _, window := range observed.ObservedWindows() {
		if window.PeriodSeconds == periodSeconds {
			return window, true
		}
	}
	return CPAQuotaWindowObservation{}, false
}

// WithWindow replaces one window without writing legacy Secondary* fields.
func (observed CPAQuotaObserved) WithWindow(window CPAQuotaWindowObservation) CPAQuotaObserved {
	windows := append([]CPAQuotaWindowObservation(nil), observed.ObservedWindows()...)
	for index := range windows {
		if windows[index].PeriodSeconds == window.PeriodSeconds {
			windows[index] = window
			observed.Windows = windows
			return observed.withoutLegacyWindow()
		}
	}
	observed.Windows = append(windows, window)
	return observed.withoutLegacyWindow()
}

func (observed CPAQuotaObserved) withoutLegacyWindow() CPAQuotaObserved {
	observed.SecondaryCollectorSessionID = ""
	observed.SecondaryBaselineUsedPercent = nil
	observed.SecondaryBaselineEventID = nil
	observed.SecondaryUsedPercent = nil
	observed.SecondaryLatestEventID = nil
	observed.SecondaryCheckpointEventID = nil
	observed.SecondaryResetAt = nil
	observed.ObservedAt = nil
	return observed
}

// CPAQuotaContext contains the minimum non-token metadata needed by quota adapters.
type CPAQuotaContext struct {
	ProjectID       string `json:"project_id,omitempty"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	SubscriptionEnd string `json:"subscription_end,omitempty"`
	AccountType     string `json:"account_type,omitempty"`
	Paid            bool   `json:"paid,omitempty"`
}
