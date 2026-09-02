package cpa

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ProviderErrorKind describes the actionable meaning of a provider response.
type ProviderErrorKind string

const (
	ProviderErrorUnknown           ProviderErrorKind = "unknown"
	ProviderErrorCredentialExpired ProviderErrorKind = "credential_expired"
	ProviderErrorQuotaExhausted    ProviderErrorKind = "quota_exhausted"
	ProviderErrorTransient         ProviderErrorKind = "transient"
	ProviderErrorManagement        ProviderErrorKind = "management"
)

// ProviderHTTPError preserves the provider response relayed by CPA while
// keeping it distinct from errors returned by CPA's management API itself.
type ProviderHTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *ProviderHTTPError) Error() string {
	if e == nil {
		return "provider quota request failed"
	}
	summary := providerErrorSummary(e.Body)
	if summary == "" {
		return fmt.Sprintf("provider quota request returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("provider quota request returned HTTP %d (%s)", e.StatusCode, summary)
}

// Kind classifies the provider response without treating generic 4xx errors
// as credential expiration.
func (e *ProviderHTTPError) Kind() ProviderErrorKind {
	if e == nil {
		return ProviderErrorUnknown
	}
	return ClassifyProviderResponse(e.StatusCode, e.Body)
}

// IsCredentialExpiredError reports whether err proves that the provider
// credential itself is permanently unusable.
func IsCredentialExpiredError(err error) bool {
	var providerErr *ProviderHTTPError
	return errors.As(err, &providerErr) && providerErr.Kind() == ProviderErrorCredentialExpired
}

// ClassifyProviderResponse classifies one provider response returned inside
// CPA's api-call envelope. Body semantics take precedence over status codes so
// quota exhaustion and temporary challenges cannot become credential expiry.
func ClassifyProviderResponse(statusCode int, body []byte) ProviderErrorKind {
	text := strings.ToLower(strings.TrimSpace(string(body)))
	summary := strings.ToLower(providerErrorSummary(body))
	return classifyProviderSignal(statusCode, text+" "+summary)
}

// ClassifyProviderErrorText classifies the canonical error text persisted by
// AxonHub. CPA management errors are deliberately kept separate from provider
// errors even when both use HTTP 401 or 403.
func ClassifyProviderErrorText(raw string) ProviderErrorKind {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return ProviderErrorUnknown
	}
	if strings.Contains(text, "cpa management request returned http") {
		return ProviderErrorManagement
	}
	statusCode := providerHTTPStatus(text)
	if strings.Contains(text, "provider quota request returned http") {
		return classifyProviderSignal(statusCode, text)
	}
	return classifyProviderSignal(0, text)
}

func classifyProviderSignal(statusCode int, text string) ProviderErrorKind {
	if containsAny(text,
		"usage_limit_reached",
		"usage limit reached",
		"usage limit has been reached",
		"rate_limit_error",
		"rate_limit_exceeded",
		"rate limit exceeded",
		"insufficient_quota",
		"insufficient quota",
		"quota_exceeded",
		"quota exceeded",
		"too many requests",
	) {
		return ProviderErrorQuotaExhausted
	}
	if containsAny(text,
		"cloudflare",
		"challenge",
		"temporarily unavailable",
		"temporary failure",
		"timeout",
		"timed out",
	) {
		return ProviderErrorTransient
	}
	if containsAny(text,
		"invalid_grant",
		"invalid grant",
		"invalid_token",
		"invalid token",
		"invalid_api_key",
		"invalid api key",
		"token_expired",
		"token expired",
		"token_invalidated",
		"token invalidated",
		"token revoked",
		"token has been revoked",
		"token is revoked",
		"token is expired",
		"token is invalid",
		"refresh token revoked",
		"refresh token has been revoked",
		"refresh token is revoked",
		"refresh token expired",
		"refresh token has expired",
		"refresh token is expired",
		"refresh token invalid",
		"refresh token is invalid",
		"access_revoked",
		"access revoked",
		"access has been revoked",
		"bad_credentials",
		"bad credentials",
		"unauthenticated",
		"unauthorized",
		"authentication_error",
		"account_deactivated",
		"account deactivated",
		"account has been deactivated",
		"account_disabled",
		"account disabled",
		"account is disabled",
		"account has been disabled",
		"account_suspended",
		"account suspended",
		"account_deleted",
		"account deleted",
		"credential_revoked",
		"credential revoked",
		"credential_not_found",
		"credential not found",
		"account_not_found",
		"account not found",
		"subscription_expired",
		"subscription expired",
		"subscription has expired",
		"plan_expired",
		"plan expired",
		"plan has expired",
		"payment_required",
		"payment required",
	) {
		return ProviderErrorCredentialExpired
	}

	switch statusCode {
	case http.StatusUnauthorized:
		return ProviderErrorCredentialExpired
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if statusCode == http.StatusTooManyRequests {
			return ProviderErrorQuotaExhausted
		}
		return ProviderErrorTransient
	default:
		return ProviderErrorUnknown
	}
}

func providerHTTPStatus(text string) int {
	const marker = "provider quota request returned http "
	index := strings.Index(text, marker)
	if index < 0 {
		return 0
	}
	value := text[index+len(marker):]
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	statusCode, err := strconv.Atoi(value[:end])
	if err != nil {
		return 0
	}
	return statusCode
}

func providerErrorSummary(body []byte) string {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	values := make([]string, 0, 6)
	appendFields := func(value any) {
		record, ok := value.(map[string]any)
		if !ok {
			if text := stringValue(value); text != "" {
				values = appendUniqueString(values, text)
			}
			return
		}
		for _, key := range []string{"code", "type", "error_code", "error_type", "message", "error_description"} {
			if text := stringValue(record[key]); text != "" {
				values = appendUniqueString(values, text)
			}
		}
	}
	for _, key := range []string{"code", "type", "error_code", "error_type", "message", "error_description"} {
		if text := stringValue(root[key]); text != "" {
			values = appendUniqueString(values, text)
		}
	}
	if nested, ok := root["error"]; ok {
		appendFields(nested)
	}
	if len(values) > 6 {
		values = values[:6]
	}
	return strings.Join(values, "; ")
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
