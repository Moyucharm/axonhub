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

// CPAQuotaSnapshot stores the normalized, provider-independent quota view.
type CPAQuotaSnapshot struct {
	Items []CPAQuotaItem `json:"items,omitempty"`
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

	// EstimatedLimitUSD is the estimated total quota value of the current
	// cycle: locally observed cost sum divided by the used percentage.
	EstimatedLimitUSD *float64 `json:"estimated_limit_usd,omitempty"`
	// EstimatedCostUSD is the locally observed cost sum within the cycle.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd,omitempty"`
	// EstimateSource records where the denominator percentage came from:
	// "precise-header" (x-codex-*-used-percent response header) or
	// "wham-percent" (integer percentage from the usage endpoint).
	EstimateSource string `json:"estimate_source,omitempty"`
}

// CPAQuotaObserved captures the latest precise codex quota percentages seen in
// upstream response headers of proxied requests, used to refine quota value
// estimation beyond the integer percentages reported by the usage endpoint.
type CPAQuotaObserved struct {
	SecondaryUsedPercent *float64   `json:"secondary_used_percent,omitempty"`
	SecondaryResetAt     *time.Time `json:"secondary_reset_at,omitempty"`
	ObservedAt           *time.Time `json:"observed_at,omitempty"`
}

// CPAQuotaContext contains the minimum non-token metadata needed by quota adapters.
type CPAQuotaContext struct {
	ProjectID       string `json:"project_id,omitempty"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	SubscriptionEnd string `json:"subscription_end,omitempty"`
	AccountType     string `json:"account_type,omitempty"`
	Paid            bool   `json:"paid,omitempty"`
}
