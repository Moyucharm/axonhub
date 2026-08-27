package objects

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
)

// ChannelEndpoint represents an outbound API endpoint configuration within a Channel.
// Each endpoint specifies the upstream API format and an optional custom path override.
// Within a single channel, api_format must be unique.
type ChannelEndpoint struct {
	APIFormat string `json:"api_format"`
	Path      string `json:"path,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	Transport string `json:"transport,omitempty"`
}

const (
	ChannelEndpointTransportHTTP      = "http"
	ChannelEndpointTransportWebSocket = "websocket"
)

type (
	ProxyType   = httpclient.ProxyType
	ProxyConfig = httpclient.ProxyConfig
)

type ModelMapping struct {
	// From is the model name in the request.
	From string `json:"from"`

	// To is the model name in the provider.
	To string `json:"to"`
}

type HeaderEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Override operation types.
const (
	OverrideOpSet          = "set"
	OverrideOpSetIfAbsent  = "set_if_absent"
	OverrideOpDelete       = "delete"
	OverrideOpRename       = "rename"
	OverrideOpCopy         = "copy"
	OverrideOpArrayAppend  = "array_append"
	OverrideOpArrayPrepend = "array_prepend"
	OverrideOpArrayInsert  = "array_insert"
	OverrideOpArrayRemove  = "array_remove"
)

// OverrideMatch defines a simple equality matcher for array_remove operations.
type OverrideMatch struct {
	// Path is resolved relative to each array item.
	Path string `json:"path"`
	// Eq is the value that removes the item when it matches.
	Eq string `json:"eq"`
}

// OverrideOperation defines a structured override operation for request body/header manipulation.
type OverrideOperation struct {
	Op        string `json:"op"`
	Path      string `json:"path,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Value     string `json:"value,omitempty"`
	Condition string `json:"condition,omitempty"`
	// Match identifies array items removed by array_remove.
	Match *OverrideMatch `json:"match,omitempty"`
	// Index is the target position for array_insert. Only used by array_insert.
	// Negative values count from the end (-1 = before last). Out-of-range values are clamped to [0, len].
	Index *int `json:"index,omitempty"`
	// Splat controls whether a JSON-array value is spread into the target array
	// (true: each element inserted individually) or inserted as a single nested element (false).
	// Only meaningful for array_append, array_prepend, and array_insert. Defaults to true.
	Splat *bool `json:"splat,omitempty"`
}

func HeaderEntriesToOverrideOperations(headers []HeaderEntry) []OverrideOperation {
	if len(headers) == 0 {
		return nil
	}

	ops := make([]OverrideOperation, 0, len(headers))
	for _, header := range headers {
		if header.Value == "__AXONHUB_CLEAR__" {
			ops = append(ops, OverrideOperation{Op: OverrideOpDelete, Path: header.Key})
			continue
		}

		ops = append(ops, OverrideOperation{Op: OverrideOpSet, Path: header.Key, Value: header.Value})
	}

	return ops
}

// CodexSimulationPreset 定义渠道的 Codex 指纹模拟预设等级。
// 预设仅作为最近一次选中的等级标签；实际生效的功能由 Options 决定。
type CodexSimulationPreset string

const (
	// CodexSimulationPresetUA 仅模拟 User-Agent，不修改其它请求头与请求体。
	CodexSimulationPresetUA CodexSimulationPreset = "ua"
	// CodexSimulationPresetNormal 对齐 codex-disguise 默认模拟行为。
	CodexSimulationPresetNormal CodexSimulationPreset = "normal"
	// CodexSimulationPresetEnhanced 在普通级别基础上注入最小 wait 工具。
	CodexSimulationPresetEnhanced CodexSimulationPreset = "enhanced"

	// DefaultCodexSimulationVersion is the codex-disguise fingerprint baseline.
	DefaultCodexSimulationVersion = "0.144.2"
)

type CodexSimulationPlatform string

const (
	CodexSimulationPlatformWindows10 CodexSimulationPlatform = "windows10"
	CodexSimulationPlatformWindows11 CodexSimulationPlatform = "windows11"
	CodexSimulationPlatformDebian    CodexSimulationPlatform = "debian"
	CodexSimulationPlatformUbuntu    CodexSimulationPlatform = "ubuntu"
	CodexSimulationPlatformMacOS     CodexSimulationPlatform = "macos"
	CodexSimulationPlatformCustom    CodexSimulationPlatform = "custom"
)

var codexSimulationPlatforms = []CodexSimulationPlatform{
	CodexSimulationPlatformWindows10,
	CodexSimulationPlatformWindows11,
	CodexSimulationPlatformDebian,
	CodexSimulationPlatformUbuntu,
	CodexSimulationPlatformMacOS,
}

// RandomCodexSimulationPlatform returns one stable OS profile for a reset.
func RandomCodexSimulationPlatform() CodexSimulationPlatform {
	id := uuid.New()
	return codexSimulationPlatforms[int(id[0])%len(codexSimulationPlatforms)]
}

func isCodexSimulationPlatform(platform CodexSimulationPlatform) bool {
	if platform == CodexSimulationPlatformCustom {
		return true
	}
	for _, candidate := range codexSimulationPlatforms {
		if platform == candidate {
			return true
		}
	}

	return false
}

func IsCodexSimulationPlatform(platform CodexSimulationPlatform) bool {
	return isCodexSimulationPlatform(platform)
}

func CodexSimulationPlatformName(platform CodexSimulationPlatform) string {
	switch platform {
	case CodexSimulationPlatformWindows11:
		return "Windows 11"
	case CodexSimulationPlatformDebian:
		return "Debian 13.0.0"
	case CodexSimulationPlatformUbuntu:
		return "Ubuntu 24.04"
	case CodexSimulationPlatformMacOS:
		return "macOS 15.0"
	default:
		return "Windows 10"
	}
}

// CodexSimulationOptions 定义渠道模拟的子选项。用户可在预设基础上手动微调。
type CodexSimulationOptions struct {
	// Prompt 提示词结构：input 数组化、Lite 指令迁移与 additional_tools 结构调整。
	Prompt bool `json:"prompt"`
	// UserAgent 模拟 Codex 风格 User-Agent。
	UserAgent bool `json:"userAgent"`
	// CodexHeaders 注入 Codex 身份请求头（originator/version/x-codex-* 等）。
	CodexHeaders bool `json:"codexHeaders"`
	// ClientMetadata 注入 client_metadata 与渠道身份。
	ClientMetadata bool `json:"clientMetadata"`
	// ResponsesShape 调整 Responses 请求形态（include/stream/store/tool_choice 等）。
	ResponsesShape bool `json:"responsesShape"`
	// AdditionalTool 注入最小 wait 工具（仅加强级别，Lite 请求生效）。
	AdditionalTool bool `json:"additionalTool"`
}

// CodexSimulationStrategy 保存渠道独立的稳定指纹。
// 每个渠道独立生成并持久化，渠道之间不会共享。
type CodexSimulationStrategy struct {
	InstallationID   string `json:"installationId"`
	ThreadID         string `json:"threadId"`
	WindowGeneration int    `json:"windowGeneration"`
}

// CodexSimulationSettings 是渠道级 Codex 指纹模拟配置。
// 仅 openai_responses 渠道允许启用；其它渠道类型被后端强制关闭。
type CodexSimulationSettings struct {
	Enabled           bool                    `json:"enabled"`
	Preset            CodexSimulationPreset   `json:"preset,omitempty"`
	Options           *CodexSimulationOptions `json:"options,omitempty"`
	Version           string                  `json:"version,omitempty"`
	Platform          CodexSimulationPlatform `json:"platform,omitempty"`
	StandardUserAgent string                  `json:"standardUserAgent,omitempty"`
	LiteUserAgent     string                  `json:"liteUserAgent,omitempty"`
	Strategy          CodexSimulationStrategy `json:"strategy,omitempty"`
}

// EffectivePreset returns the configured preset or defaults to normal when unset.
func (s *CodexSimulationSettings) EffectivePreset() CodexSimulationPreset {
	if s == nil || s.Preset == "" {
		return CodexSimulationPresetNormal
	}

	return s.Preset
}

// EffectiveOptions returns the configured simulation options. A nil Options
// pointer means no options were submitted and falls back to the selected preset;
// a non-nil all-false value is an explicit request to disable every sub-option.
func (s *CodexSimulationSettings) EffectiveOptions() CodexSimulationOptions {
	if s == nil {
		return CodexSimulationOptions{}
	}

	if s.Options == nil {
		return PresetDefaultOptions(s.EffectivePreset())
	}

	return *s.Options
}

// EffectiveVersion returns the configured Codex client version or the stable default.
func (s *CodexSimulationSettings) EffectiveVersion() string {
	if s == nil || strings.TrimSpace(s.Version) == "" {
		return DefaultCodexSimulationVersion
	}

	return strings.TrimSpace(s.Version)
}

// DefaultCodexSimulationUserAgents builds the standard and Lite UA fingerprints
// for a version. They are materialized per channel and remain stable across requests.
func DefaultCodexSimulationUserAgents(version string, platforms ...CodexSimulationPlatform) (standard string, lite string) {
	if strings.TrimSpace(version) == "" {
		version = DefaultCodexSimulationVersion
	}
	platform := CodexSimulationPlatformWindows10
	if len(platforms) > 0 && isCodexSimulationPlatform(platforms[0]) && platforms[0] != CodexSimulationPlatformCustom {
		platform = platforms[0]
	}

	platformName := CodexSimulationPlatformName(platform)
	standard = fmt.Sprintf("codex_cli_rs/%s (%s; x86_64)", version, platformName)
	lite = fmt.Sprintf("codex_exec/%s (%s; x86_64) tmux/3.5a (codex_exec; %s)", version, platformName, version)

	return standard, lite
}

// EffectiveUserAgent returns the channel-persisted UA for the model profile.
// Empty values only occur for legacy settings and fall back to the baseline template.
func (s *CodexSimulationSettings) EffectiveUserAgent(lite bool) string {
	version := s.EffectiveVersion()
	platform := CodexSimulationPlatformWindows10
	if s != nil && isCodexSimulationPlatform(s.Platform) && s.Platform != CodexSimulationPlatformCustom {
		platform = s.Platform
	}
	standardDefault, liteDefault := DefaultCodexSimulationUserAgents(version, platform)
	if s == nil {
		if lite {
			return liteDefault
		}

		return standardDefault
	}

	if lite {
		if strings.TrimSpace(s.LiteUserAgent) != "" {
			return strings.TrimSpace(s.LiteUserAgent)
		}

		return liteDefault
	}

	if strings.TrimSpace(s.StandardUserAgent) != "" {
		return strings.TrimSpace(s.StandardUserAgent)
	}

	return standardDefault
}

// PresetDefaultOptions returns the option set for each simulation preset.
func PresetDefaultOptions(preset CodexSimulationPreset) CodexSimulationOptions {
	switch preset {
	case CodexSimulationPresetUA:
		return CodexSimulationOptions{UserAgent: true}
	case CodexSimulationPresetEnhanced:
		return CodexSimulationOptions{
			Prompt:         true,
			UserAgent:      true,
			CodexHeaders:   true,
			ClientMetadata: true,
			ResponsesShape: true,
			AdditionalTool: true,
		}
	default: // normal
		return CodexSimulationOptions{
			Prompt:         true,
			UserAgent:      true,
			CodexHeaders:   true,
			ClientMetadata: true,
			ResponsesShape: true,
		}
	}
}

// NewCodexSimulationStrategy generates an independent, per-channel random fingerprint.
// 与 codex-disguise 一致：installation_id 为带连字符 UUID，thread_id 为 "thread_" + hex。
func NewCodexSimulationStrategy() CodexSimulationStrategy {
	return CodexSimulationStrategy{
		InstallationID:   uuid.NewString(),
		ThreadID:         "thread_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		WindowGeneration: 0,
	}
}

type TransformOptions struct {
	// ForceArrayInstructions forces the channel to accept array format for instructions.
	ForceArrayInstructions bool `json:"forceArrayInstructions"`

	// ForceArrayInputs forces the channel to accept array format for inputs.
	ForceArrayInputs bool `json:"forceArrayInputs"`

	// ReplaceDeveloperRoleWithSystem replaces developer role with system in messages for Bailian compatibility.
	ReplaceDeveloperRoleWithSystem bool `json:"replaceDeveloperRoleWithSystem"`

	// ReasoningEffortMapping maps inbound reasoning_effort values to outbound ones for
	// non-standard OpenAI-compatible providers. The first entry whose From matches the
	// effort value wins; values not in the list pass through unchanged.
	// e.g. [{"from":"xhigh","to":"max"}] converts Anthropic's internal "xhigh" (mapped
	// from "max") back to "max" for providers that only recognize "max".
	// Consumed by the OpenAI-shared outbound transformer. Other transformers ignore it
	// for now. Strong-typed to mirror ModelMapping; see llm.ReasoningEffortMapping.
	ReasoningEffortMapping []llm.ReasoningEffortMapping `json:"reasoningEffortMapping,omitempty"`
}

type APIKeyPoolSettings struct {
	// RetryCount is the total number of requests allowed for one same-channel attempt,
	// including the initial request and retries with another key. Nil inherits the
	// system-wide same-channel retry limit.
	RetryCount *int `json:"retryCount,omitempty"`

	// AutoCheckEnabled controls scheduled validation of disabled keys. It is opt-in.
	AutoCheckEnabled bool `json:"autoCheckEnabled,omitempty"`

	// AutoCheckIntervalHours is the scheduled validation interval in whole hours.
	// Values below one hour are rejected when automatic checking is enabled.
	AutoCheckIntervalHours *int `json:"autoCheckIntervalHours,omitempty"`

	// AutoCheckConcurrency limits concurrent key validation within one channel.
	AutoCheckConcurrency *int `json:"autoCheckConcurrency,omitempty"`

	// AutoCheckTimeoutSeconds limits validation time for one key.
	AutoCheckTimeoutSeconds *int `json:"autoCheckTimeoutSeconds,omitempty"`

	// LastAutoCheckAt records the last completed scheduled validation.
	LastAutoCheckAt *time.Time `json:"lastAutoCheckAt,omitempty"`
}

type ChannelSettings struct {
	// ExtraModelPrefix sets the channel accept the model with the extra prefix.
	// e.g. a channel
	// supported_modles is ["deepseek-chat", "deepseek-reasoner"]
	// extraModelPrefix is "deepseek"
	// then the model "deepseek-chat", "deepseek-reasoner", "deepseek/deepseek-chat", "deepseek/deepseek-reasoner"  will be accepted.
	// And if other channel support "deepseek/deepseek-chat", "deepseek/deepseek-reasoner" modles, the two channels can accept the request both.
	ExtraModelPrefix string `json:"extraModelPrefix"`

	// AutoTrimedModelPrefixes configures prefixes to automatically trim the model name when added to supported models.
	// e.g. a channel
	// supported_modles is ["deepseek-ai/deepseek-chat", "openai/gpt-4"]
	// autoTrimedModelPrefixes is ["openai", "deepseek"]
	// then the model "openai/gpt-4", "deepseek/deepseek-chat", "deepseek-chat", "gpt-4" will be accepted.
	AutoTrimedModelPrefixes []string `json:"autoTrimedModelPrefixes"`

	// ModelMappings add model alias for the model in the channels.
	// e.g. {"from": "deepseek-chat", "to": "deepseek/deepseek-chat"} will add a alias "deepseek-chat" for "deepseek/deepseek-chat".
	ModelMappings []ModelMapping `json:"modelMappings"`

	// HideOriginalModels hides the original models from the model list when model mappings are configured.
	// When enabled, only the mapped model names (from field) will be exposed, not the actual model names (to field).
	HideOriginalModels bool `json:"hideOriginalModels"`

	// HideMappedModels hides the mapped models from the model list when model mappings are configured.
	// When enabled, only the original model names (from field) will be exposed, not the mapped model names (to field).
	HideMappedModels bool `json:"hideMappedModels"`

	// LowercaseModelID converts model name matching keys to lowercase.
	// When enabled, only RequestModel (used for matching) is lowercased; ActualModel
	// (sent to provider) preserves original casing. This enables cross-channel load
	// balancing where providers use different casing for the same model.
	LowercaseModelID bool `json:"lowercaseModelId"`

	// OverrideParameters sets the channel override the request body.
	// A json string.
	// e.g. {"max_tokens": 100}, {"temperature": 0.7}
	// Deprecated Use bodyOverrideOperations instead.
	OverrideParameters string `json:"overrideParameters"`

	// BodyOverrideOperations sets the channel override operations for the request body.
	// When present (including an empty array), it takes precedence over OverrideParameters.
	BodyOverrideOperations []OverrideOperation `json:"bodyOverrideOperations,omitempty"`

	// OverrideHeaders sets the channel override the request headers.
	// e.g. [{"key": "User-Agent", "value": "AxonHub"}]
	// Supported ops: set (default), delete, rename, copy.
	// Deprecated Use headerOverrideOperations instead.
	OverrideHeaders []HeaderEntry `json:"overrideHeaders"`

	// HeaderOverrideOperations sets the channel override operations for request headers.
	// When present (including an empty array), it takes precedence over OverrideHeaders.
	HeaderOverrideOperations []OverrideOperation `json:"headerOverrideOperations,omitempty"`

	// Proxy configuration for the channel. If not set, defaults to environment proxy type.
	Proxy *httpclient.ProxyConfig `json:"proxy,omitempty"`

	// TransformOptions configures the transform options for the channel.
	TransformOptions TransformOptions `json:"transformOptions"`

	// PassThroughUserAgent controls whether to pass through the original User-Agent header to upstream AI providers.
	// When set to nil, it inherits from the global system setting.
	// When set to true/false, it overrides the global setting.
	PassThroughUserAgent *bool `json:"passThroughUserAgent,omitempty"`

	// PassThroughBody controls whether to forward the original request body directly
	// to the upstream provider and the raw provider response/stream directly to the client
	// without re-serialization through the transform pipelines.
	// Only effective when the inbound and outbound API formats are identical.
	// When set to nil, it inherits from the global system setting.
	// When set to true/false, it overrides the global setting.
	PassThroughBody *bool `json:"passThroughBody,omitempty"`

	// RateLimit configures the upstream rate limit for the channel.
	// When configured, the load balancer will skip channels that have exceeded their rate limits.
	RateLimit *ChannelRateLimit `json:"rateLimit,omitempty"`

	// RetryableStatusCodes configures additional HTTP status codes that should
	// trigger retry for this channel. Default retryable codes (429 and 5xx) are
	// always handled by the retry policy even when this list is empty.
	RetryableStatusCodes []int `json:"retryableStatusCodes,omitempty"`

	// RetryableErrorPatterns configures additional error text patterns that should
	// trigger retry for this channel. When Regex is false, Pattern is matched as a
	// case-sensitive substring of the error text.
	RetryableErrorPatterns []RetryableErrorPattern `json:"retryableErrorPatterns,omitempty"`

	// APIKeyPool configures behavior that is only used by multi-key channels.
	APIKeyPool *APIKeyPoolSettings `json:"apiKeyPool,omitempty"`

	// CodexSimulation configures the per-channel Codex fingerprint simulation.
	// Only applicable to openai_responses channels; other channel types reject
	// or force-disable it. When unset, simulation is off.
	CodexSimulation *CodexSimulationSettings `json:"codexSimulation,omitempty"`
}

type RetryableErrorPattern struct {
	Pattern string `json:"pattern"`
	Regex   bool   `json:"regex,omitempty"`
}

type ChannelRateLimit struct {
	RPM           *int64 `json:"rpm,omitempty"`           // Requests Per Minute, nil = unlimited
	TPM           *int64 `json:"tpm,omitempty"`           // Tokens Per Minute, nil = unlimited
	MaxConcurrent *int64 `json:"maxConcurrent,omitempty"` // Maximum concurrent requests, nil = unlimited

	// QueueSize controls the limiter mode when MaxConcurrent is set:
	//   nil / 0 = soft mode (count only, no blocking, no rejection — preserves PR #1322 scoring behaviour)
	//   > 0     = hard mode (FIFO wait queue with bounded capacity; excess requests rejected)
	// Has no effect when MaxConcurrent is unset or <= 0.
	QueueSize *int64 `json:"queueSize,omitempty"`

	// QueueTimeoutMs is the per-channel queue wait timeout in milliseconds.
	//   nil / 0 = no per-channel timeout (only the request context bounds the wait)
	//   > 0     = waiters that exceed this duration receive ErrChannelQueueTimeout
	// Only meaningful in hard mode (QueueSize > 0).
	QueueTimeoutMs *int64 `json:"queueTimeoutMs,omitempty"`
}

// ChannelAutoDisableState stores persistent channel-level failure state.
// It is kept separate from user-editable channel settings and keyed by the
// channel itself so threshold tracking remains consistent across instances.
type ChannelAutoDisableState struct {
	FailureCount     int        `json:"failureCount,omitempty"`
	FailurePolicyKey string     `json:"failurePolicyKey,omitempty"`
	LastFailedAt     *time.Time `json:"lastFailedAt,omitempty"`
	LastErrorCode    int        `json:"lastErrorCode,omitempty"`
	LastError        string     `json:"lastError,omitempty"`
}

// AutoDisableAction defines what an automatic channel rule does at its threshold.
type AutoDisableAction string

const (
	AutoDisableActionDisable  AutoDisableAction = "disable"
	AutoDisableActionCooldown AutoDisableAction = "cooldown"
)

// ChannelAutoDisablePolicy configures channel-level automatic error handling.
// An omitted policy or an empty status list in codes mode leaves channel-level
// handling disabled and allows the global policy to be considered.
type ChannelAutoDisablePolicy struct {
	Mode                    string                     `json:"mode,omitempty"`
	Times                   int                        `json:"times,omitempty"`
	Statuses                []ChannelAutoDisableStatus `json:"statuses,omitempty"`
	Action                  AutoDisableAction          `json:"action,omitempty"`
	CooldownDurationMinutes int                        `json:"cooldownDurationMinutes,omitempty"`
}

type ChannelAutoDisableStatus struct {
	Status int `json:"status"`
	Times  int `json:"times"`
}

// DisabledAPIKey 记录被禁用的 API key 信息（敏感，按 credentials 同级保护）
// 注意：禁用判断以 Key 明文为主键。
type DisabledAPIKey struct {
	Key          string     `json:"key"`
	DisabledAt   time.Time  `json:"disabledAt"`
	ErrorCode    int        `json:"errorCode"`
	Reason       string     `json:"reason,omitempty"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	FailureCount int        `json:"failureCount,omitempty"`
	LastFailedAt *time.Time `json:"lastFailedAt,omitempty"`
}

// IsExpired reports whether a temporary API key disable has elapsed.
func (dk DisabledAPIKey) IsExpired() bool {
	return dk.ExpiresAt != nil && time.Now().After(*dk.ExpiresAt)
}

type APIKeyMode string

const (
	APIKeyModeSingle APIKeyMode = "single"
	APIKeyModePool   APIKeyMode = "pool"
)

type ChannelAPIKeyState struct {
	Key              string     `json:"key"`
	FailureCount     int        `json:"failureCount"`
	FailurePolicyKey string     `json:"failurePolicyKey,omitempty"`
	LastFailedAt     *time.Time `json:"lastFailedAt,omitempty"`
	LastErrorCode    int        `json:"lastErrorCode,omitempty"`
	LastError        string     `json:"lastError,omitempty"`
}

type ChannelCredentials struct {
	// Mode distinguishes a single credential from a managed key pool.
	// Empty values are inferred from the configured key count for compatibility.
	Mode APIKeyMode `json:"mode,omitempty"`

	// APIKey is the API key for the channel, for the single key channel, e.g. Codex, Claude code, Antigravity.
	// It is kept for backward compatibility with existing data, recommend to use OAuth instead.
	APIKey string `json:"apiKey,omitempty"`

	// OAuth is the OAuth credentials for the channel, for the OAuth channel, e.g. Codex, Claude code, Antigravity.
	OAuth *OAuthCredentials `json:"oauth,omitempty"`

	// APIKeys is a list of API keys for the channel.
	// When multiple keys are provided, they will be used in a round-robin fashion.
	APIKeys []string `json:"apiKeys,omitempty"`

	// Azure configuration for the channel.
	Azure *AzureCredential `json:"azure,omitempty"`

	// GCP is the GCP credentials for the channel.
	GCP *GCPCredential `json:"gcp,omitempty"`

	// APIKeyStates stores persistent operational state for regular API keys.
	// It is kept with credentials because it is sensitive and keyed by the full key value.
	APIKeyStates []ChannelAPIKeyState `json:"apiKeyStates,omitempty"`
}

// EffectiveAPIKeyMode returns the explicit mode or infers one for legacy channels.
func (c *ChannelCredentials) EffectiveAPIKeyMode() APIKeyMode {
	if c == nil || c.IsOAuth() {
		return APIKeyModeSingle
	}
	if c.Mode == APIKeyModeSingle || c.Mode == APIKeyModePool {
		return c.Mode
	}
	if len(c.GetAllAPIKeys()) > 1 {
		return APIKeyModePool
	}
	return APIKeyModeSingle
}

// IsAPIKeyPool reports whether regular credentials are managed as a key pool.
func (c *ChannelCredentials) IsAPIKeyPool() bool {
	return c != nil && !c.IsOAuth() && c.EffectiveAPIKeyMode() == APIKeyModePool
}

// GetAllAPIKeys returns all API keys for the channel, combining APIKey and APIKeys fields.
// This ensures backward compatibility with old data that only has APIKey set.
func (c *ChannelCredentials) GetAllAPIKeys() []string {
	if c == nil {
		return nil
	}

	var keys []string

	// Add legacy APIKey if present (only if not OAuth credential)
	if c.APIKey != "" && !c.IsOAuth() {
		keys = append(keys, c.APIKey)
	}

	// Add new APIKeys
	keys = append(keys, c.APIKeys...)

	return keys
}

// OAuthCredentialRef identifies the OAuth credential of a channel in the
// auto-disable bookkeeping. A channel holds at most one OAuth credential, and
// its access token is replaced on every refresh, so a fixed sentinel is used as
// the stable identity instead of the token itself.
//
//nolint:gosec // Not a credential: a fixed sentinel that never authenticates anything.
const OAuthCredentialRef = "__oauth__"

// GetAllCredentialRefs returns the identities of every credential the channel
// can be disabled on. Key-based channels are identified by the API keys
// themselves; an OAuth channel is represented by the single OAuthCredentialRef
// so that auto-disable and scheduled recovery treat it as a one-key channel.
//
// This is deliberately separate from GetAllAPIKeys: the sentinel must never
// reach outbound requests, credential management UI, the channel tester or
// backups, all of which consume GetAllAPIKeys.
func (c *ChannelCredentials) GetAllCredentialRefs() []string {
	if c == nil {
		return nil
	}

	if c.IsOAuth() {
		return []string{OAuthCredentialRef}
	}

	return c.GetAllAPIKeys()
}

// GetEnabledCredentialRefs returns the credential refs that are not disabled.
func (c *ChannelCredentials) GetEnabledCredentialRefs(disabledKeys []DisabledAPIKey) []string {
	return filterDisabled(c.GetAllCredentialRefs(), disabledKeys)
}

// GetEnabledAPIKeys returns API keys that are not disabled.
func (c *ChannelCredentials) GetEnabledAPIKeys(disabledKeys []DisabledAPIKey) []string {
	return filterDisabled(c.GetAllAPIKeys(), disabledKeys)
}

// filterDisabled drops every candidate that carries an active disable record.
func filterDisabled(candidates []string, disabledKeys []DisabledAPIKey) []string {
	if len(disabledKeys) == 0 {
		return candidates
	}

	disabledSet := make(map[string]struct{}, len(disabledKeys))
	for _, dk := range disabledKeys {
		if dk.Key == "" || dk.IsExpired() {
			continue
		}

		disabledSet[dk.Key] = struct{}{}
	}

	enabled := make([]string, 0, len(candidates))
	for _, key := range candidates {
		if _, ok := disabledSet[key]; ok {
			continue
		}

		enabled = append(enabled, key)
	}

	return enabled
}

// IsOAuth returns true if OAuth credentials are configured and valid.
// It checks both the new OAuth field and legacy APIKey field for backward compatibility.
func (c *ChannelCredentials) IsOAuth() bool {
	if c == nil {
		return false
	}

	// Check new OAuth field first
	if c.OAuth != nil && c.OAuth.AccessToken != "" {
		return true
	}

	// Backward compatibility: check if APIKey contains OAuth JSON
	return isOAuthJSON(c.APIKey)
}

func (c *ChannelCredentials) ResolveOAuthCredentials() (*OAuthCredentials, error) {
	if c != nil && c.OAuth != nil && strings.TrimSpace(c.OAuth.AccessToken) != "" {
		return c.OAuth, nil
	}
	if c == nil {
		return oauth.ParseCredentialsJSON("")
	}
	return oauth.ParseCredentialsJSON(c.APIKey)
}

// isOAuthJSON checks if a string is an OAuth JSON credential.
func isOAuthJSON(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.Contains(s, "access_token")
}

type OAuthCredentials = oauth.OAuthCredentials

type AzureCredential struct {
	// APIVersion is a optional version for the channel.
	APIVersion string `json:"apiVersion"`
}

type GCPCredential struct {
	Region    string `json:"region"`
	ProjectID string `json:"projectID"`
	JSONData  string `json:"jsonData"`
}

type GCPCredentialsJSON struct {
	Type                    string `json:"type" validate:"required"`
	ProjectID               string `json:"projectID" validate:"required"`
	PrivateKeyID            string `json:"privateKeyID" validate:"required"`
	PrivateKey              string `json:"privateKey" validate:"required"`
	ClientEmail             string `json:"clientEmail" validate:"required"`
	ClientID                string `json:"clientID" validate:"required"`
	AuthURI                 string `json:"authURI" validate:"required"`
	TokenURI                string `json:"tokenURI" validate:"required"`
	AuthProviderX509CertURL string `json:"authProviderX509CertURL" validate:"required"`
	ClientX509CertURL       string `json:"clientX509CertURL" validate:"required"`
	UniverseDomain          string `json:"universeDomain" validate:"required"`
}

type CapabilityPolicy string

const (
	CapabilityPolicyUnlimited CapabilityPolicy = "unlimited"
	CapabilityPolicyRequire   CapabilityPolicy = "require"
	CapabilityPolicyForbid    CapabilityPolicy = "forbid"
)

type ChannelPolicies struct {
	Stream             CapabilityPolicy          `json:"stream,omitempty"`
	ChannelAutoDisable *ChannelAutoDisablePolicy `json:"channelAutoDisable,omitempty"`

	// APIKeyAutoDisableRules are the channel's own auto-disable rules. They are
	// evaluated before the global retry policy and, when one matches, own the
	// credential-level failure. Channels without rules fall back to the global policy.
	APIKeyAutoDisableRules []APIKeyAutoDisableRule `json:"apiKeyAutoDisableRules,omitempty"`
}

type APIKeyAutoDisableAction string

const (
	APIKeyAutoDisableActionTemporary APIKeyAutoDisableAction = "temporary_disable"

	// APIKeyAutoDisableActionPermanentDelete disables the credential and then
	// removes it from the channel's credentials entirely.
	APIKeyAutoDisableActionPermanentDelete APIKeyAutoDisableAction = "permanent_disable_delete"

	// APIKeyAutoDisableActionPermanent disables the credential with no expiry but
	// keeps it on the channel, so an operator can inspect and re-enable it by hand.
	APIKeyAutoDisableActionPermanent APIKeyAutoDisableAction = "permanent_disable"

	// APIKeyAutoDisableActionUntilCron disables the credential until the first
	// occurrence of DisableUntilCron after the failure. It exists because quota
	// resets happen at fixed wall-clock times, which a relative duration cannot
	// express: the delay needed to reach 03:00 depends on when the failure hit.
	APIKeyAutoDisableActionUntilCron APIKeyAutoDisableAction = "disable_until_cron"
)

// APIKeyAutoDisableRule applies to one channel and matches status codes and/or
// error-message patterns. Empty conditions match any upstream error.
//
// Rules act on a single credential. Channels holding several API keys disable
// only the failing key and keep serving on the rest; the channel itself is
// disabled once every credential is unavailable, and recovers as soon as one
// becomes available again. An OAuth channel has exactly one credential
// (OAuthCredentialRef), so for it the two levels coincide.
type APIKeyAutoDisableRule struct {
	StatusCodes     []int                   `json:"statusCodes,omitempty"`
	KeywordPatterns []string                `json:"keywordPatterns,omitempty"`
	Times           int                     `json:"times"`
	Action          APIKeyAutoDisableAction `json:"action"`

	// DisableDurationMinutes applies to APIKeyAutoDisableActionTemporary. A nil
	// value disables the credential indefinitely.
	DisableDurationMinutes *int `json:"disableDurationMinutes,omitempty"`

	// DisableUntilCron and DisableUntilTimezone apply to
	// APIKeyAutoDisableActionUntilCron. DisableUntilCron uses the standard
	// 5-field crontab format; an empty timezone means UTC.
	DisableUntilCron     string `json:"disableUntilCron,omitempty"`
	DisableUntilTimezone string `json:"disableUntilTimezone,omitempty"`
}

// ParseOverrideOperations parses the override parameters string.
// Supports both legacy map format (JSON object) and new operation array format (JSON array).
// Legacy format is automatically converted to OverrideOperation slice.
func ParseOverrideOperations(raw string) ([]OverrideOperation, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "[]" {
		return nil, nil
	}

	if raw[0] == '[' {
		var ops []OverrideOperation
		if err := json.Unmarshal([]byte(raw), &ops); err != nil {
			return nil, fmt.Errorf("invalid override operations: %w", err)
		}

		return ops, nil
	}

	var legacy map[string]any
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return nil, fmt.Errorf("invalid override parameters: %w", err)
	}

	ops := make([]OverrideOperation, 0, len(legacy))
	for key, value := range legacy {
		if strVal, ok := value.(string); ok && strVal == "__AXONHUB_CLEAR__" {
			ops = append(ops, OverrideOperation{Op: OverrideOpDelete, Path: key})
		} else {
			// Convert value to string
			var strValue string

			switch v := value.(type) {
			case string:
				strValue = v
			default:
				strValue = fmt.Sprintf("%v", value)
			}

			ops = append(ops, OverrideOperation{Op: OverrideOpSet, Path: key, Value: strValue})
		}
	}

	return ops, nil
}

// SerializeOverrideOperations converts override operations to a JSON string for storage.
func SerializeOverrideOperations(ops []OverrideOperation) (string, error) {
	if len(ops) == 0 {
		return "[]", nil
	}

	data, err := json.Marshal(ops)
	if err != nil {
		return "", fmt.Errorf("failed to serialize override operations: %w", err)
	}

	return string(data), nil
}
