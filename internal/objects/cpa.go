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
	// exposed by GraphQL. Weekly and monthly windows use it to carry a stable
	// local interval baseline without racing the collector-owned observation.
	// The historical SessionID JSON name is retained for compatibility; the
	// value is the stable local collector identity for the CPA instance.
	EstimateCollectorSessionID  string   `json:"estimate_collector_session_id,omitempty"`
	EstimateBaselineUsedPercent *float64 `json:"estimate_baseline_used_percent,omitempty"`
	EstimateBaselineEventID     *int     `json:"estimate_baseline_event_id,omitempty"`
	EstimateLatestEventID       *int     `json:"estimate_latest_event_id,omitempty"`
}

// CPAQuotaObserved captures a continuous local observation interval for the
// precise Codex 7d quota percentage, normalized by reported window duration.
// The Secondary* JSON fields retain their historical names for compatibility;
// they may contain a weekly signal originating from either wire slot. A
// collector restart, reset change, or percentage regression starts a new
// interval at whatever percentage is first observed; a reset never needs to be
// seen at exactly zero usage.
type CPAQuotaObserved struct {
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

// CPAQuotaContext contains the minimum non-token metadata needed by quota adapters.
type CPAQuotaContext struct {
	ProjectID       string `json:"project_id,omitempty"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	SubscriptionEnd string `json:"subscription_end,omitempty"`
	AccountType     string `json:"account_type,omitempty"`
	Paid            bool   `json:"paid,omitempty"`
}
