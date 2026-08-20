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
}

// CPAQuotaContext contains the minimum non-token metadata needed by quota adapters.
type CPAQuotaContext struct {
	ProjectID       string `json:"project_id,omitempty"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	SubscriptionEnd string `json:"subscription_end,omitempty"`
	AccountType     string `json:"account_type,omitempty"`
	Paid            bool   `json:"paid,omitempty"`
}
