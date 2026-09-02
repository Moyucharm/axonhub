package cpa

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestClassifyProviderResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		want       ProviderErrorKind
	}{
		{
			name:       "bare unauthorized",
			statusCode: http.StatusUnauthorized,
			want:       ProviderErrorCredentialExpired,
		},
		{
			name:       "invalid grant on bad request",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"invalid_grant","error_description":"refresh token has been revoked"}`,
			want:       ProviderErrorCredentialExpired,
		},
		{
			name:       "descriptive revoked refresh token",
			statusCode: http.StatusBadRequest,
			body:       `{"error_description":"Refresh token has been revoked"}`,
			want:       ProviderErrorCredentialExpired,
		},
		{
			name:       "invalid token on forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"type":"authentication_error","code":"invalid_token"}}`,
			want:       ProviderErrorCredentialExpired,
		},
		{
			name:       "usage limit on unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`,
			want:       ProviderErrorQuotaExhausted,
		},
		{
			name:       "bare too many requests",
			statusCode: http.StatusTooManyRequests,
			want:       ProviderErrorQuotaExhausted,
		},
		{
			name:       "cloudflare challenge",
			statusCode: http.StatusForbidden,
			body:       `{"message":"Cloudflare challenge required"}`,
			want:       ProviderErrorTransient,
		},
		{
			name:       "generic forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"type":"permission_error","code":"forbidden"}}`,
			want:       ProviderErrorUnknown,
		},
		{
			name:       "account missing",
			statusCode: http.StatusNotFound,
			body:       `{"error":{"code":"account_not_found"}}`,
			want:       ProviderErrorCredentialExpired,
		},
		{
			name:       "model missing",
			statusCode: http.StatusNotFound,
			body:       `{"error":{"code":"model_not_found"}}`,
			want:       ProviderErrorUnknown,
		},
		{
			name:       "server failure",
			statusCode: http.StatusBadGateway,
			want:       ProviderErrorTransient,
		},
		{
			name:       "invalid request",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"invalid_request_error"}}`,
			want:       ProviderErrorUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyProviderResponse(tt.statusCode, []byte(tt.body)); got != tt.want {
				t.Fatalf("ClassifyProviderResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderErrorTextKeepsManagementFailuresSeparate(t *testing.T) {
	t.Parallel()

	providerErr := &ProviderHTTPError{
		StatusCode: http.StatusBadRequest,
		Body:       []byte(`{"error":"invalid_grant"}`),
	}
	if got := ClassifyProviderErrorText(providerErr.Error()); got != ProviderErrorCredentialExpired {
		t.Fatalf("provider error classification = %q, want %q", got, ProviderErrorCredentialExpired)
	}
	if !IsCredentialExpiredError(providerErr) {
		t.Fatal("provider invalid_grant should be credential-expired")
	}

	managementErr := &ManagementHTTPError{StatusCode: http.StatusUnauthorized}
	if got := ClassifyProviderErrorText(managementErr.Error()); got != ProviderErrorManagement {
		t.Fatalf("management error classification = %q, want %q", got, ProviderErrorManagement)
	}
	if IsCredentialExpiredError(managementErr) {
		t.Fatal("management unauthorized must not expire a provider credential")
	}

	wrapped := errors.New("provider quota request returned HTTP 429")
	if got := ClassifyProviderErrorText(wrapped.Error()); got != ProviderErrorQuotaExhausted {
		t.Fatalf("quota error classification = %q, want %q", got, ProviderErrorQuotaExhausted)
	}
}

func TestProviderHTTPErrorIncludesSafeErrorSummary(t *testing.T) {
	t.Parallel()

	err := (&ProviderHTTPError{
		StatusCode: http.StatusBadRequest,
		Body:       []byte(`{"error":{"code":"invalid_grant","message":"refresh token has been revoked"}}`),
	}).Error()
	if err != "provider quota request returned HTTP 400 (invalid_grant; refresh token has been revoked)" {
		t.Fatalf("unexpected provider error summary: %q", err)
	}
}

type providerErrorTestClient struct {
	result *ProviderCallResult
}

func (providerErrorTestClient) CloseIdleConnections() {}

func (providerErrorTestClient) ListCredentials(context.Context) (*AuthFilesResponse, BuildInfo, error) {
	return nil, BuildInfo{}, errors.New("unused")
}

func (client providerErrorTestClient) CallProvider(context.Context, ProviderCall) (*ProviderCallResult, error) {
	return client.result, nil
}

func (providerErrorTestClient) ListUsageQueue(context.Context, int) ([]*UsageEvent, error) {
	return nil, errors.New("unused")
}

func (providerErrorTestClient) PatchAuthFileStatus(context.Context, string, string, bool) error {
	return errors.New("unused")
}

func TestCallJSONPreservesProviderResponseError(t *testing.T) {
	t.Parallel()

	body := `{"error":"invalid_grant"}`
	_, err := callJSON(context.Background(), providerErrorTestClient{
		result: &ProviderCallResult{StatusCode: http.StatusBadRequest, Body: []byte(body)},
	}, ProviderCall{Method: http.MethodGet, URL: "https://provider.example/quota"}, nil)
	var providerErr *ProviderHTTPError
	if !errors.As(err, &providerErr) {
		t.Fatalf("callJSON() error = %T %v, want ProviderHTTPError", err, err)
	}
	if providerErr.StatusCode != http.StatusBadRequest || string(providerErr.Body) != body {
		t.Fatalf("unexpected provider error: %#v", providerErr)
	}
}
