package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListCredentialsSelectsManagementAPI(t *testing.T) {
	for _, fallbackStatus := range []int{0, http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(fmt.Sprint(fallbackStatus), func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				if fallbackStatus != 0 && r.URL.Path == "/proxy/v8/management/credentials" {
					w.WriteHeader(fallbackStatus)
					return
				}
				w.Header().Set("X-CPA-VERSION", "selected-build")
				_, _ = w.Write([]byte(`{"observed_at":"2026-10-23T01:02:03Z","files":[{"auth_index":"a","cooldowns":[]},{"auth_index":"b","cooldowns":null}]}`))
			}))
			defer server.Close()
			client, err := NewClient(Config{BaseURL: server.URL + "/proxy/v8/management/", ManagementSecret: "secret"})
			require.NoError(t, err)
			defer client.CloseIdleConnections()
			response, build, err := client.ListCredentials(context.Background())
			require.NoError(t, err)
			require.Equal(t, "selected-build", build.Version)
			expectedPaths := []string{"/proxy/v8/management/credentials"}
			expectedAPI := ManagementAPIV8
			if fallbackStatus != 0 {
				expectedPaths = append(expectedPaths, "/proxy/v0/management/auth-files")
				expectedAPI = ManagementAPIV0
			}
			require.Equal(t, expectedPaths, paths)
			require.Equal(t, expectedAPI, response.API)
			require.Len(t, response.Files, 2)
			require.Equal(t, "[]", string(response.Files[0].Cooldowns))
			require.Equal(t, "null", string(response.Files[1].Cooldowns))
			observed := time.Date(2026, 10, 23, 1, 2, 3, 0, time.UTC)
			require.NotNil(t, response.ObservedAt)
			require.Equal(t, observed, *response.ObservedAt)
			for _, file := range response.Files {
				require.Equal(t, response.ObservedAt, file.CooldownObservedAt)
			}
		})
	}
}

func TestListCredentialsDoesNotFallbackOnOtherFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v8/management/credentials", r.URL.Path)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{invalid`))
			})
			_, _, err := client.ListCredentials(context.Background())
			require.Error(t, err)
			if status == http.StatusServiceUnavailable || status == http.StatusOK {
				require.Equal(t, int32(readRetryAttempts), calls.Load())
			} else {
				require.Equal(t, int32(1), calls.Load())
			}
		})
	}
}

func TestListCredentialsV0ArrayWithoutObservationTime(t *testing.T) {
	client, calls := newRetryTestClient(t, func(call int32, w http.ResponseWriter, r *http.Request) {
		if call == 1 {
			require.Equal(t, "/v8/management/credentials", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		require.Equal(t, "/v0/management/auth-files", r.URL.Path)
		_, _ = w.Write([]byte(`[{"auth_index":"a","cooldowns":[]}]`))
	})
	response, _, err := client.ListCredentials(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, ManagementAPIV0, response.API)
	require.Nil(t, response.ObservedAt)
	require.Len(t, response.Files, 1)
	require.Nil(t, response.Files[0].CooldownObservedAt)
}

func TestResetCooldownSendsOnlyAuthIndexToSelectedAPI(t *testing.T) {
	for _, api := range []ManagementAPI{ManagementAPIV8, ManagementAPIV0} {
		t.Run(string(api), func(t *testing.T) {
			client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
				expectedPath := "/v8/management/routing/cooldown/reset"
				if api == ManagementAPIV0 {
					expectedPath = "/v0/management/reset-quota"
				}
				require.Equal(t, expectedPath, r.URL.Path)
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, map[string]any{"auth_index": "auth-a"}, body)
				_, _ = w.Write([]byte(`{"status":"ok","auth_index":"auth-a","models":["m"]}`))
			})
			require.NoError(t, client.ResetCooldown(context.Background(), "auth-a", api))
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestResetCooldownRejectsInvalidLocalInputs(t *testing.T) {
	client, calls := newRetryTestClient(t, func(_ int32, _ http.ResponseWriter, _ *http.Request) {
		t.Error("invalid reset reached remote CPA")
	})
	require.Error(t, client.ResetCooldown(context.Background(), " \t", ManagementAPIV8))
	require.Error(t, client.ResetCooldown(context.Background(), "a", ManagementAPI("v9")))
	require.Equal(t, int32(0), calls.Load())
}

func TestResetCooldownValidatesSuccessEnvelope(t *testing.T) {
	for _, body := range []string{
		`{invalid`, ``, `null`, `{}`, `[]`,
		`{"status":"error","auth_index":"a"}`,
		`{"status":"ok","auth_index":"other"}`,
		`{"status":"ok","auth_index":"a","models":{}}`,
	} {
		t.Run(body, func(t *testing.T) {
			client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			err := client.ResetCooldown(context.Background(), "a", ManagementAPIV8)
			require.EqualError(t, err, "CPA cooldown reset response is invalid; refresh state before retrying")
			require.Equal(t, int32(1), calls.Load())
		})
	}
	for _, body := range []string{`{"status":"ok","auth_index":"a"}`, `{"status":"ok","auth_index":"a","models":null}`, `{"status":"ok","auth_index":"a","models":[]}`} {
		client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		})
		require.NoError(t, client.ResetCooldown(context.Background(), "a", ManagementAPIV8))
		require.Equal(t, int32(1), calls.Load())
	}
}

func TestResetCooldownNeverRetriesOrFallsBack(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v8/management/routing/cooldown/reset", r.URL.Path)
				w.Header().Set("Location", "/v0/management/reset-quota")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"private upstream secret"}`))
			})
			err := client.ResetCooldown(context.Background(), "a", ManagementAPIV8)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private upstream secret")
			require.Equal(t, int32(1), calls.Load())
		})
	}
	client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		require.NoError(t, err)
		_ = connection.Close()
	})
	require.Error(t, client.ResetCooldown(context.Background(), "a", ManagementAPIV8))
	require.Equal(t, int32(1), calls.Load())
}

func TestManagementHTTPErrorAllowsOnlySafeReasons(t *testing.T) {
	for _, reason := range []string{"auth not found", "core auth manager unavailable", "auth_index is required", "invalid request body", "secret-token", "Auth not found"} {
		t.Run(reason, func(t *testing.T) {
			client, calls := newRetryTestClient(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"error": reason, "token": "private-token", "message": "private-message"}))
			})
			err := client.ResetCooldown(context.Background(), "a", ManagementAPIV8)
			var httpErr *ManagementHTTPError
			require.ErrorAs(t, err, &httpErr)
			require.Equal(t, http.StatusNotFound, httpErr.StatusCode)
			if reason == "secret-token" || reason == "Auth not found" {
				require.Empty(t, httpErr.Reason)
				require.NotContains(t, err.Error(), reason)
			} else {
				require.Equal(t, reason, httpErr.Reason)
				require.Contains(t, err.Error(), reason)
			}
			require.NotContains(t, err.Error(), "private-")
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestNormalizeBaseURLStripsBothManagementRoots(t *testing.T) {
	for _, root := range []string{"v0", "v8"} {
		normalized, err := NormalizeBaseURL("https://EXAMPLE.COM/proxy/" + root + "/management/")
		require.NoError(t, err)
		require.Equal(t, "https://example.com/proxy", normalized)
	}
}

func TestManagementTimeoutDoesNotChangeAPIVersionOrReplayReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			client, calls := newRetryTestClient(t, func(_ int32, _ http.ResponseWriter, r *http.Request) {
				if reset {
					require.Equal(t, "/v8/management/routing/cooldown/reset", r.URL.Path)
				} else {
					require.Equal(t, "/v8/management/credentials", r.URL.Path)
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			})
			client.httpClient.Timeout = 20 * time.Millisecond
			if reset {
				require.Error(t, client.ResetCooldown(context.Background(), "a", ManagementAPIV8))
				require.Equal(t, int32(1), calls.Load())
			} else {
				_, _, err := client.ListCredentials(context.Background())
				require.Error(t, err)
				require.Equal(t, int32(readRetryAttempts), calls.Load())
			}
		})
	}
}
