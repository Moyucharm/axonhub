package cpa

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	managementPath        = "/v0/management"
	defaultRequestTimeout = 30 * time.Second
	maxAuthFilesBodySize  = 32 << 20
	maxAPICallBodySize    = 16 << 20
)

// Client communicates with one CLIProxyAPI management endpoint.
type Client struct {
	baseURL          *url.URL
	managementSecret string
	httpClient       *http.Client
}

// Config configures a CPA management client.
type Config struct {
	BaseURL          string
	ManagementSecret string
	InsecureSkipTLS  bool
}

// ManagementHTTPError reports a non-success response from CPA's management API.
type ManagementHTTPError struct {
	StatusCode int
}

func (e *ManagementHTTPError) Error() string {
	return fmt.Sprintf("CPA management request returned HTTP %d", e.StatusCode)
}

// NormalizeBaseURL validates and canonicalizes a CPA base URL.
func NormalizeBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("CPA base URL is required")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse CPA base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("CPA base URL must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("CPA base URL host is required")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("CPA base URL must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("CPA base URL must not contain query or fragment")
	}

	cleanPath := strings.TrimRight(parsed.EscapedPath(), "/")
	if strings.HasSuffix(cleanPath, managementPath) {
		cleanPath = strings.TrimSuffix(cleanPath, managementPath)
	}
	if cleanPath == "" {
		parsed.Path = ""
		parsed.RawPath = ""
	} else {
		unescaped, errUnescape := url.PathUnescape(cleanPath)
		if errUnescape != nil {
			return "", fmt.Errorf("decode CPA base URL path: %w", errUnescape)
		}
		parsed.Path = path.Clean("/" + strings.TrimPrefix(unescaped, "/"))
		parsed.RawPath = ""
	}
	parsed.Host = strings.ToLower(parsed.Host)

	return strings.TrimRight(parsed.String(), "/"), nil
}

// NewClient creates a dedicated CPA client that never logs management credentials.
func NewClient(cfg Config) (*Client, error) {
	normalized, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.ManagementSecret) == "" {
		return nil, fmt.Errorf("CPA management secret is required")
	}
	baseURL, err := url.Parse(normalized)
	if err != nil {
		return nil, fmt.Errorf("parse normalized CPA base URL: %w", err)
	}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.InsecureSkipTLS, //nolint:gosec // Explicit per-instance user setting with UI warning.
		},
	}

	client := &Client{
		baseURL:          baseURL,
		managementSecret: strings.TrimSpace(cfg.ManagementSecret),
	}
	client.httpClient = &http.Client{
		Transport: transport,
		Timeout:   defaultRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many CPA redirects")
			}
			if !strings.EqualFold(req.URL.Host, baseURL.Host) {
				return fmt.Errorf("CPA redirect changed host")
			}
			if baseURL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("CPA redirect downgraded HTTPS")
			}
			return nil
		},
	}
	return client, nil
}

// CloseIdleConnections closes idle connections owned by this client.
func (c *Client) CloseIdleConnections() {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// ListCredentials returns the current auth-files snapshot and CPA build metadata.
func (c *Client) ListCredentials(ctx context.Context) (*AuthFilesResponse, BuildInfo, error) {
	var raw json.RawMessage
	buildInfo, err := c.doJSON(ctx, http.MethodGet, "auth-files", nil, maxAuthFilesBodySize, &raw)
	if err != nil {
		return nil, buildInfo, err
	}
	var result AuthFilesResponse
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &result.Files); err != nil {
			return nil, buildInfo, fmt.Errorf("decode CPA auth-files array: %w", err)
		}
	} else if err := json.Unmarshal(trimmed, &result); err != nil {
		return nil, buildInfo, fmt.Errorf("decode CPA auth-files response: %w", err)
	}
	if result.Files == nil {
		result.Files = []AuthFile{}
	}
	return &result, buildInfo, nil
}

// CallProvider asks CPA to execute a provider quota request with the selected auth.
func (c *Client) CallProvider(ctx context.Context, call ProviderCall) (*ProviderCallResult, error) {
	method := strings.ToUpper(strings.TrimSpace(call.Method))
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("unsupported CPA provider call method")
	}
	providerURL, err := url.Parse(strings.TrimSpace(call.URL))
	if err != nil || providerURL.Scheme != "https" || providerURL.Host == "" {
		return nil, fmt.Errorf("invalid provider quota URL")
	}
	payload := map[string]any{
		"auth_index": call.AuthIndex,
		"method":     method,
		"url":        providerURL.String(),
		"header":     call.Headers,
		"data":       call.Body,
	}
	var envelope struct {
		StatusCode int                 `json:"status_code"`
		Header     map[string][]string `json:"header"`
		Body       string              `json:"body"`
	}
	if _, err := c.doJSON(ctx, http.MethodPost, "api-call", payload, maxAPICallBodySize, &envelope); err != nil {
		return nil, err
	}
	return &ProviderCallResult{
		StatusCode: envelope.StatusCode,
		Headers:    http.Header(envelope.Header),
		Body:       []byte(envelope.Body),
	}, nil
}

// PatchAuthFileStatus toggles the disabled state of one CPA auth file.
// It maps to CLIProxyAPI's PATCH /v0/management/auth-files/status endpoint;
// name is the remote auth file name (or ID), authIndex optionally disambiguates
// runtime entries sharing the same name.
func (c *Client) PatchAuthFileStatus(ctx context.Context, name, authIndex string, disabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("CPA auth file name is required")
	}
	payload := map[string]any{
		"name":     name,
		"disabled": disabled,
	}
	if trimmed := strings.TrimSpace(authIndex); trimmed != "" {
		payload["auth_index"] = trimmed
	}
	var output struct {
		Status string `json:"status"`
	}
	_, err := c.doJSON(ctx, http.MethodPatch, "auth-files/status", payload, 1<<20, &output)
	return err
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, payload any, maxBody int64, output any) (BuildInfo, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return BuildInfo{}, fmt.Errorf("CPA client is not initialized")
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return BuildInfo{}, fmt.Errorf("encode CPA management request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	target := *c.baseURL
	target.Path = strings.TrimRight(c.baseURL.Path, "/") + managementPath + "/" + strings.TrimLeft(endpoint, "/")
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return BuildInfo{}, fmt.Errorf("build CPA management request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.managementSecret)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BuildInfo{}, fmt.Errorf("CPA management request failed: %w", err)
	}
	defer resp.Body.Close()

	buildInfo := BuildInfo{
		Version:   strings.TrimSpace(resp.Header.Get("X-CPA-VERSION")),
		Commit:    strings.TrimSpace(resp.Header.Get("X-CPA-COMMIT")),
		BuildDate: strings.TrimSpace(resp.Header.Get("X-CPA-BUILD-DATE")),
	}
	limited := io.LimitReader(resp.Body, maxBody+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return buildInfo, fmt.Errorf("read CPA management response: %w", err)
	}
	if int64(len(responseBody)) > maxBody {
		return buildInfo, fmt.Errorf("CPA management response exceeded size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return buildInfo, &ManagementHTTPError{StatusCode: resp.StatusCode}
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return buildInfo, fmt.Errorf("decode CPA management response: %w", err)
	}
	return buildInfo, nil
}
