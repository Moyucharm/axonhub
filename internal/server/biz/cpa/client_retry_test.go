package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newRetryTestClient(t *testing.T, handler func(call int32, w http.ResponseWriter, r *http.Request)) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(calls.Add(1), w, r)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(Config{BaseURL: server.URL, ManagementSecret: "secret"})
	require.NoError(t, err)
	client.retryBaseDelay = time.Millisecond
	t.Cleanup(client.CloseIdleConnections)
	return client, &calls
}

func TestCallProviderRetriesIdempotentReads(t *testing.T) {
	client, calls := newRetryTestClient(t, func(call int32, w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		status := http.StatusInternalServerError
		if call >= int32(readRetryAttempts) {
			status = http.StatusOK
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": status, "body": `{"credits":[]}`}))
	})
	result, err := client.CallProvider(context.Background(), ProviderCall{
		AuthIndex: "auth", Method: http.MethodGet, URL: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, int32(readRetryAttempts), calls.Load())
}

func TestCallProviderRetriesTransportFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte(`{"status`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, ManagementSecret: "secret"})
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	client.retryBaseDelay = time.Millisecond
	_, err = client.CallProvider(context.Background(), ProviderCall{
		AuthIndex: "auth", Method: http.MethodGet, URL: "https://chatgpt.com/backend-api/wham/usage",
	})
	require.Error(t, err)
	require.Equal(t, int32(readRetryAttempts), attempts.Load())
}

func TestCallProviderNeverRetriesWrites(t *testing.T) {
	client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": http.StatusInternalServerError, "body": `{}`}))
	})
	result, err := client.CallProvider(context.Background(), ProviderCall{
		AuthIndex: "auth", Method: http.MethodPost, URL: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, result.StatusCode)
	require.Equal(t, int32(1), calls.Load())
}

func TestListCredentialsRetriesTransientFailures(t *testing.T) {
	client, calls := newRetryTestClient(t, func(call int32, w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		if call < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"files": []any{}}))
	})
	files, _, err := client.ListCredentials(context.Background())
	require.NoError(t, err)
	require.Empty(t, files.Files)
	require.Equal(t, int32(2), calls.Load())
}

func TestListCredentialsStopsOnClientErrors(t *testing.T) {
	client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, _, err := client.ListCredentials(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestPatchAuthFileStatusIsNotRetried(t *testing.T) {
	client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	require.Error(t, client.PatchAuthFileStatus(context.Background(), "codex.json", "auth", true))
	require.Equal(t, int32(1), calls.Load())
}

func TestListUsageQueueIsNotRetried(t *testing.T) {
	client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := client.ListUsageQueue(context.Background(), 10)
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestCallProviderStopsOnManagementAuthFailure(t *testing.T) {
	client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := client.CallProvider(context.Background(), ProviderCall{
		AuthIndex: "auth", Method: http.MethodGet, URL: "https://chatgpt.com/backend-api/wham/usage",
	})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
}
