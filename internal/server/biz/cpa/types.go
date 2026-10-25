package cpa

import (
	"encoding/json"
	"net/http"
	"time"
)

// BuildInfo identifies the connected CLIProxyAPI build.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// ManagementAPI identifies the management API selected by a read-only probe.
type ManagementAPI string

const (
	ManagementAPIV0 ManagementAPI = "v0"
	ManagementAPIV8 ManagementAPI = "v8"
)

// AuthFile is the safe subset of a CPA auth-files entry used by AxonHub.
type AuthFile struct {
	ID                      string          `json:"id"`
	AuthIndex               string          `json:"auth_index"`
	Name                    string          `json:"name"`
	Type                    string          `json:"type"`
	Provider                string          `json:"provider"`
	Label                   string          `json:"label"`
	Status                  string          `json:"status"`
	StatusMessage           string          `json:"status_message"`
	Disabled                bool            `json:"disabled"`
	Unavailable             bool            `json:"unavailable"`
	RuntimeOnly             bool            `json:"runtime_only"`
	Source                  string          `json:"source"`
	Email                   string          `json:"email"`
	ProjectID               string          `json:"project_id"`
	AccountType             string          `json:"account_type"`
	Priority                int             `json:"priority"`
	Prefix                  string          `json:"prefix"`
	UsingAPI                bool            `json:"using_api"`
	IDToken                 json.RawMessage `json:"id_token"`
	LastRefresh             *time.Time      `json:"last_refresh"`
	UpdatedAt               *time.Time      `json:"updated_at"`
	Cooldowns               json.RawMessage `json:"cooldowns"`
	CooldownObservedAt      *time.Time      `json:"-"`
	SubscriptionActiveUntil any             `json:"chatgpt_subscription_active_until"`
}

// AuthFilesResponse contains a credential snapshot from either management API.
type AuthFilesResponse struct {
	Files      []AuthFile    `json:"files"`
	ObservedAt *time.Time    `json:"observed_at"`
	API        ManagementAPI `json:"-"`
}

// AuthFileModel is one model CPA currently routes to a credential.
type AuthFileModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	OwnedBy     string `json:"owned_by"`
}

// ProviderCall is an internal, typed request executed by CPA's api-call endpoint.
type ProviderCall struct {
	AuthIndex string
	Method    string
	URL       string
	Headers   map[string]string
	Body      string
}

// ProviderCallResult contains the upstream response relayed by CPA.
type ProviderCallResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
