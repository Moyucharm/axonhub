package biz

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/pkg/xcache/live"
	"github.com/looplj/axonhub/llm/httpclient"
)

func newTestChannelService(client *ent.Client) *ChannelService {
	mockSysSvc := &SystemService{
		AbstractService: &AbstractService{
			db: client,
		},
		Cache: xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
	}

	svc := &ChannelService{
		AbstractService: &AbstractService{
			db: client,
		},
		SystemService:             mockSysSvc,
		WebhookNotifier:           NewWebhookNotifier(mockSysSvc, httpclient.NewHttpClient()),
		channelPerfMetrics:        make(map[int]*channelMetrics),
		channelErrorCounts:        make(map[int]map[int]int),
		apiKeyErrorCounts:         make(map[int]map[string]map[int]int),
		apiKeyRuleActionsInFlight: make(map[int]map[string]bool),
		perfWindowSeconds:         600,
	}

	svc.enabledChannelsCache = live.NewCache(live.Options[[]*Channel]{
		Name:            "test_enabled_channels",
		InitialValue:    []*Channel{},
		RefreshInterval: time.Hour,
		RefreshFunc:     svc.reloadEnabledChannels,
		OnSwap:          svc.onEnabledChannelsSwap,
	})

	return svc
}

func createTestChannelWithAPIKeys(t *testing.T, client *ent.Client, ctx context.Context, name string, apiKeys []string) *ent.Channel {
	t.Helper()

	creds := objects.ChannelCredentials{
		APIKeys: apiKeys,
	}

	ch, err := client.Channel.Create().
		SetName(name).
		SetType(channel.TypeOpenai).
		SetBaseURL("https://api.openai.com").
		SetCredentials(creds).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	return ch
}

func TestChannelService_checkAndHandleAPIKeyError(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	// Create a channel with multiple API keys
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2", "key3"})

	tests := []struct {
		name             string
		policy           *RetryPolicy
		perf             *PerformanceRecord
		expectedDisabled bool
		setupFunc        func()
	}{
		{
			name: "first error - should not disable",
			policy: &RetryPolicy{
				AutoDisableAPIKey: AutoDisableAPIKey{
					Enabled: true,
					Mode:    AutoDisableModeCodes,
					Statuses: []AutoDisableAPIKeyStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = make(map[int]map[string]map[int]int)
			},
		},
		{
			name: "second error - should not disable",
			policy: &RetryPolicy{
				AutoDisableAPIKey: AutoDisableAPIKey{
					Enabled: true,
					Mode:    AutoDisableModeCodes,
					Statuses: []AutoDisableAPIKeyStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 1}},
				}
			},
		},
		{
			name: "third error - should disable API key",
			policy: &RetryPolicy{
				AutoDisableAPIKey: AutoDisableAPIKey{
					Enabled: true,
					Mode:    AutoDisableModeCodes,
					Statuses: []AutoDisableAPIKeyStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: true,
			setupFunc: func() {
				// Reset channel state first
				_, err := client.Channel.UpdateOneID(ch.ID).
					SetDisabledAPIKeys([]objects.DisabledAPIKey{}).
					SetStatus(channel.StatusEnabled).
					ClearErrorMessage().
					Save(ctx)
				require.NoError(t, err)

				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 2}},
				}
			},
		},
		{
			name: "different status code - should not disable",
			policy: &RetryPolicy{
				AutoDisableAPIKey: AutoDisableAPIKey{
					Enabled: true,
					Mode:    AutoDisableModeCodes,
					Statuses: []AutoDisableAPIKeyStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 500,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 2}},
				}
			},
		},
		{
			name: "different API key - should not disable",
			policy: &RetryPolicy{
				AutoDisableAPIKey: AutoDisableAPIKey{
					Enabled: true,
					Mode:    AutoDisableModeCodes,
					Statuses: []AutoDisableAPIKeyStatus{
						{Status: 401, Times: 3},
					},
				},
			},
			perf: &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key2",
				ResponseStatusCode: 401,
				Success:            false,
			},
			expectedDisabled: false,
			setupFunc: func() {
				svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
					ch.ID: {"key1": {401: 2}},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc()
			}

			result := svc.checkAndHandleAPIKeyError(ctx, tt.perf, tt.policy)
			require.Equal(t, tt.expectedDisabled, result)

			if tt.expectedDisabled {
				// Verify API key is disabled
				updatedCh, err := client.Channel.Get(ctx, ch.ID)
				require.NoError(t, err)
				require.Len(t, updatedCh.DisabledAPIKeys, 1)
				require.Equal(t, tt.perf.APIKey, updatedCh.DisabledAPIKeys[0].Key)

				// Verify error counts are cleared for this API key
				svc.apiKeyErrorCountsLock.Lock()
				_, exists := svc.apiKeyErrorCounts[ch.ID][tt.perf.APIKey]
				svc.apiKeyErrorCountsLock.Unlock()
				require.False(t, exists)
			}
		})
	}
}

func TestChannelService_APIKeyFailureStatePersistsAndResets(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "persistent-key-state", []string{"key1", "key2"})
	policy := &RetryPolicy{AutoDisableAPIKey: AutoDisableAPIKey{Enabled: true, Mode: AutoDisableModeCodes, Statuses: []AutoDisableAPIKeyStatus{{Status: 401, Times: 2}}}}

	require.False(t, svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 401, ErrorMessage: "invalid"}, policy))
	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, 1, persisted.Credentials.APIKeyStates[0].FailureCount)
	require.Equal(t, 401, persisted.Credentials.APIKeyStates[0].LastErrorCode)

	now := time.Now()
	svc.RecordPerformance(ctx, &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", StartTime: now, EndTime: now, Success: true, RequestCompleted: true})
	persisted, err = client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, 0, persisted.Credentials.APIKeyStates[0].FailureCount)
	require.Nil(t, persisted.Credentials.APIKeyStates[0].LastFailedAt)
}

func TestChannelService_checkAndHandleChannelError(t *testing.T) {
	tests := []struct {
		name                string
		initialFailureCount int
		expectedDisabled    bool
	}{
		{name: "first error - should not disable", initialFailureCount: 0, expectedDisabled: false},
		{name: "second error - should disable channel", initialFailureCount: 1, expectedDisabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			defer client.Close()

			ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			svc := newTestChannelService(client)
			ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-no-keys", nil)
			policy := &RetryPolicy{AutoDisableChannel: AutoDisableChannel{
				Enabled:  true,
				Mode:     AutoDisableModeCodes,
				Statuses: []AutoDisableChannelStatus{{Status: 401, Times: 2}},
			}}
			perf := &PerformanceRecord{ChannelID: ch.ID, ResponseStatusCode: 401}

			failurePolicy, matched := channelFailurePolicyFromGlobal(policy.AutoDisableChannel, perf)
			require.True(t, matched)
			if tt.initialFailureCount > 0 {
				_, err := client.Channel.UpdateOneID(ch.ID).
					SetAutoDisableState(objects.ChannelAutoDisableState{
						FailureCount:     tt.initialFailureCount,
						FailurePolicyKey: failurePolicy.Key,
					}).
					Save(ctx)
				require.NoError(t, err)
			}

			acted := svc.checkAndHandleChannelError(ctx, perf, policy)
			require.Equal(t, tt.expectedDisabled, acted)

			updatedCh, err := client.Channel.Get(ctx, ch.ID)
			require.NoError(t, err)
			if tt.expectedDisabled {
				require.Equal(t, channel.StatusDisabled, updatedCh.Status)
				require.NotNil(t, updatedCh.AutoDisabledAt)
				require.Zero(t, updatedCh.AutoDisableState.FailureCount)
				return
			}
			require.Equal(t, channel.StatusEnabled, updatedCh.Status)
			require.Equal(t, 1, updatedCh.AutoDisableState.FailureCount)
		})
	}
}

func TestChannelService_DisableAllAPIKeysDisablesChannel(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	// Create a channel with 2 API keys
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-2-keys", []string{"key1", "key2"})

	// Disable first key
	err := svc.DisableAPIKey(ctx, ch.ID, "key1", 401, "Test reason 1")
	require.NoError(t, err)

	// Verify channel is still enabled
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)

	// Disable second key - should disable the entire channel
	err = svc.DisableAPIKey(ctx, ch.ID, "key2", 401, "Test reason 2")
	require.NoError(t, err)

	// Verify channel is now disabled
	updatedCh, err = client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, updatedCh.Status)
	require.Len(t, updatedCh.DisabledAPIKeys, 2)
	require.NotNil(t, updatedCh.ErrorMessage)
}

func TestChannelService_DisableAllAPIKeysNotifiesWebhook(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	type webhookRequest struct {
		body string
		err  error
	}
	notifications := make(chan webhookRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		notifications <- webhookRequest{body: string(body), err: err}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := WebhookNotifierConfig{
		Targets: []WebhookTarget{
			{
				Name:      "default",
				Enabled:   true,
				URL:       server.URL,
				TimeoutMs: 1000,
				Body:      `{"event":"{{.Event}}","channel_id":{{.Channel.ID}},"channel_name":"{{.Channel.Name}}","provider":"{{.Channel.Provider}}","base_url":"{{.Channel.BaseURL}}","status":"{{.Channel.Status}}","status_code":{{.Trigger.StatusCode}},"reason":"{{.Trigger.Reason}}"}`,
			},
		},
		Subscriptions: []WebhookSubscription{
			{Event: EventChannelAutoDisabled, TargetNames: []string{"default"}},
		},
	}

	svc := newTestChannelService(client)
	svc.SystemService = newTestSystemServiceWithWebhookConfig(t, client, cfg)
	svc.WebhookNotifier = NewWebhookNotifier(svc.SystemService, httpclient.NewHttpClient())

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-webhook", []string{"key1", "key2"})

	require.NoError(t, svc.DisableAPIKey(ctx, ch.ID, "key1", 500, "first key failed"))

	select {
	case notification := <-notifications:
		t.Fatalf("received webhook before channel was disabled: %s", notification.body)
	case <-time.After(100 * time.Millisecond):
	}

	require.NoError(t, svc.DisableAPIKey(ctx, ch.ID, "key2", 500, "second key failed"))

	select {
	case notification := <-notifications:
		require.NoError(t, notification.err)
		require.JSONEq(t, fmt.Sprintf(`{
			"event": "channel.auto_disabled",
			"channel_id": %d,
			"channel_name": "test-channel-webhook",
			"provider": "openai",
			"base_url": "https://api.openai.com",
			"status": "disabled",
			"status_code": 500,
			"reason": "All API keys disabled (last error: 500)"
		}`, ch.ID), notification.body)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel auto-disabled webhook")
	}
}

func TestChannelService_SuccessClearsErrorCounts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1"})

	// Set up some error counts
	svc.channelErrorCounts = map[int]map[int]int{
		ch.ID: {401: 2, 500: 1},
	}
	svc.apiKeyErrorCounts = map[int]map[string]map[int]int{
		ch.ID: {"key1": {401: 2}},
	}

	// Record a successful request
	perf := &PerformanceRecord{
		ChannelID:        ch.ID,
		APIKey:           "key1",
		Success:          true,
		RequestCompleted: true,
		EndTime:          time.Now(),
	}

	svc.IncrementChannelSelection(ch.ID)
	svc.RecordPerformance(ctx, perf)

	// Verify channel error counts are cleared
	svc.channelErrorCountsLock.Lock()
	_, channelExists := svc.channelErrorCounts[ch.ID]
	svc.channelErrorCountsLock.Unlock()
	require.False(t, channelExists)

	// Verify API key error counts are cleared
	svc.apiKeyErrorCountsLock.Lock()
	_, keyExists := svc.apiKeyErrorCounts[ch.ID]["key1"]
	svc.apiKeyErrorCountsLock.Unlock()
	require.False(t, keyExists)
}

func TestChannelService_MultipleStatusCodes(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2"})

	policy := &RetryPolicy{
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled: true,
			Mode:    AutoDisableModeCodes,
			Statuses: []AutoDisableAPIKeyStatus{
				{Status: 401, Times: 2},
				{Status: 403, Times: 1},
			},
		},
	}

	// Test 401 - needs 2 persisted failures.
	perf401 := &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 401,
		Success:            false,
	}

	require.False(t, svc.checkAndHandleAPIKeyError(ctx, perf401, policy))
	result := svc.checkAndHandleAPIKeyError(ctx, perf401, policy)
	require.True(t, result)

	// Reset for 403 test
	_, err := client.Channel.UpdateOneID(ch.ID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{}).
		Save(ctx)
	require.NoError(t, err)

	svc.apiKeyErrorCounts = make(map[int]map[string]map[int]int)

	// Test 403 - needs only 1 time
	perf403 := &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key2",
		ResponseStatusCode: 403,
		Success:            false,
	}

	result = svc.checkAndHandleAPIKeyError(ctx, perf403, policy)
	require.True(t, result)

	// Verify key2 is disabled
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Equal(t, "key2", updatedCh.DisabledAPIKeys[0].Key)
}

func TestChannelService_ConcurrentErrorTracking(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2", "key3"})

	policy := &RetryPolicy{
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled: true,
			Mode:    AutoDisableModeCodes,
			Statuses: []AutoDisableAPIKeyStatus{
				{Status: 401, Times: 5},
			},
		},
	}

	// Simulate concurrent error reporting
	var wg sync.WaitGroup

	numGoroutines := 10

	for i := range numGoroutines {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			perf := &PerformanceRecord{
				ChannelID:          ch.ID,
				APIKey:             "key1",
				ResponseStatusCode: 401,
				Success:            false,
			}
			svc.checkAndHandleAPIKeyError(ctx, perf, policy)
		}(i)
	}

	wg.Wait()

	// Verify counts are tracked correctly (should be at least 5 to trigger disable)
	// The key should be disabled since we had 10 errors and threshold is 5
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)

	// Should have disabled key1
	require.GreaterOrEqual(t, len(updatedCh.DisabledAPIKeys), 1)
}

func TestChannelService_DisableAPIKeyIdempotent(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2"})

	// Disable key1 first time
	err := svc.DisableAPIKey(ctx, ch.ID, "key1", 401, "Reason 1")
	require.NoError(t, err)

	// Disable key1 second time - should be idempotent
	err = svc.DisableAPIKey(ctx, ch.ID, "key1", 401, "Reason 2")
	require.NoError(t, err)

	// Verify only one entry
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
}

func TestChannelService_DisableAPIKeyNotFound(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1", "key2"})

	// Try to disable a key that doesn't exist - should be ignored
	err := svc.DisableAPIKey(ctx, ch.ID, "nonexistent-key", 401, "Reason")
	require.NoError(t, err)

	// Verify no keys are disabled
	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 0)
}

func TestChannelService_DisableAPIKeyEmptyKey(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)

	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel", []string{"key1"})

	// Try to disable an empty key - should return error
	err := svc.DisableAPIKey(ctx, ch.ID, "", 401, "Reason")
	require.Error(t, err)
}

func TestMatchesAPIKeyRule(t *testing.T) {
	tests := []struct {
		name string
		rule objects.APIKeyAutoDisableRule
		perf *PerformanceRecord
		want bool
	}{
		{
			name: "status code only",
			rule: objects.APIKeyAutoDisableRule{StatusCodes: []int{401}},
			perf: &PerformanceRecord{ResponseStatusCode: 401},
			want: true,
		},
		{
			name: "keyword only is case insensitive",
			rule: objects.APIKeyAutoDisableRule{KeywordPatterns: []string{"quota exceeded"}},
			perf: &PerformanceRecord{ResponseStatusCode: 503, ErrorMessage: "QUOTA EXCEEDED for this account"},
			want: true,
		},
		{
			name: "regular expression",
			rule: objects.APIKeyAutoDisableRule{KeywordPatterns: []string{`account (was )?disabled`}},
			perf: &PerformanceRecord{ResponseStatusCode: 400, ErrorMessage: "Account was disabled"},
			want: true,
		},
		{
			name: "invalid regular expression falls back to literal keyword",
			rule: objects.APIKeyAutoDisableRule{KeywordPatterns: []string{"quota["}},
			perf: &PerformanceRecord{ResponseStatusCode: 429, ErrorMessage: "Provider QUOTA[ exceeded"},
			want: true,
		},
		{
			name: "status and keyword must both match",
			rule: objects.APIKeyAutoDisableRule{StatusCodes: []int{401}, KeywordPatterns: []string{"invalid key"}},
			perf: &PerformanceRecord{ResponseStatusCode: 403, ErrorMessage: "invalid key"},
			want: false,
		},
		{
			name: "missing message does not match keyword",
			rule: objects.APIKeyAutoDisableRule{KeywordPatterns: []string{"quota"}},
			perf: &PerformanceRecord{ResponseStatusCode: 429},
			want: false,
		},
		{
			name: "empty conditions match any error",
			rule: objects.APIKeyAutoDisableRule{},
			perf: &PerformanceRecord{ResponseStatusCode: 500, ErrorMessage: ""},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, matchesAPIKeyRule(tt.rule, tt.perf))
		})
	}
}

func TestNormalizeAPIKeyAutoDisableRules(t *testing.T) {
	duration := 30
	policies := &objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
		{
			StatusCodes:            []int{429, 401, 429},
			KeywordPatterns:        []string{" quota ", "quota", ""},
			Times:                  2,
			Action:                 objects.APIKeyAutoDisableActionTemporary,
			DisableDurationMinutes: &duration,
		},
	}}

	require.NoError(t, NormalizeAPIKeyAutoDisableRules(policies))
	require.Equal(t, []int{401, 429}, policies.APIKeyAutoDisableRules[0].StatusCodes)
	require.Equal(t, []string{"quota"}, policies.APIKeyAutoDisableRules[0].KeywordPatterns)

	// Empty conditions (any-error rule) are allowed and stay empty.
	emptyPolicies := &objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
		{Times: 2, Action: objects.APIKeyAutoDisableActionTemporary, DisableDurationMinutes: lo.ToPtr(30)},
	}}
	require.NoError(t, NormalizeAPIKeyAutoDisableRules(emptyPolicies))
	require.Empty(t, emptyPolicies.APIKeyAutoDisableRules[0].StatusCodes)
	require.Empty(t, emptyPolicies.APIKeyAutoDisableRules[0].KeywordPatterns)

	invalidDuration := 0
	tests := []objects.APIKeyAutoDisableRule{
		{Times: 0, Action: objects.APIKeyAutoDisableActionTemporary},
		{Times: 1, Action: objects.APIKeyAutoDisableActionTemporary},
		{StatusCodes: []int{99}, Times: 1, Action: objects.APIKeyAutoDisableActionTemporary},
		{Times: 1, Action: "unsupported"},
		{Times: 1, Action: objects.APIKeyAutoDisableActionTemporary, DisableDurationMinutes: &invalidDuration},
	}
	for _, rule := range tests {
		require.Error(t, NormalizeAPIKeyAutoDisableRules(&objects.ChannelPolicies{
			APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{rule},
		}))
	}
}

func TestChannelService_ChannelAPIKeyRuleTemporaryDisableAfterThreshold(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "temporary-rule", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{429},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	perf := &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 429}
	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.True(t, matched)
	require.False(t, acted)
	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.True(t, matched)
	require.True(t, acted)

	updated, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updated.DisabledAPIKeys, 1)
	require.Equal(t, "key1", updated.DisabledAPIKeys[0].Key)
	require.NotNil(t, updated.DisabledAPIKeys[0].ExpiresAt)
	require.WithinDuration(t, time.Now().Add(30*time.Minute), *updated.DisabledAPIKeys[0].ExpiresAt, 5*time.Second)
}

func TestChannelService_ChannelAPIKeyRulePermanentActionKeepsLastKeyDisabled(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "permanent-rule", []string{"only-key"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetCredentials(objects.ChannelCredentials{Mode: objects.APIKeyModePool, APIKeys: []string{"only-key"}}).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes: []int{401},
				Times:       1,
				Action:      objects.APIKeyAutoDisableActionPermanent,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "only-key", ResponseStatusCode: 401,
	})
	require.True(t, matched)
	require.True(t, acted)

	updated, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, updated.Status)
	require.Equal(t, []string{"only-key"}, updated.Credentials.APIKeys)
	require.Len(t, updated.DisabledAPIKeys, 1)
	require.Nil(t, updated.DisabledAPIKeys[0].ExpiresAt)
}

func TestChannelService_ChannelAPIKeyRuleCountsAlternatingStatusesTogether(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "multi-status-rule", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{401, 403},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 401,
	})
	require.True(t, matched)
	require.False(t, acted)

	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 403,
	})
	require.True(t, matched)
	require.True(t, acted)

	updated, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updated.DisabledAPIKeys, 1)
	require.Equal(t, "key1", updated.DisabledAPIKeys[0].Key)
}

func TestChannelService_ChannelAPIKeyRuleKeepsStreakAfterNonMatchingFailure(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "reset-non-match", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{401},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 401,
	})
	require.True(t, matched)
	require.False(t, acted)

	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 500,
	})
	require.False(t, matched)
	require.False(t, acted)

	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 401,
	})
	require.True(t, matched)
	require.True(t, acted)
}

func TestChannelService_ChannelAPIKeyRuleResetsStreakWhenEarlierRuleOwnsFailure(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "reset-owned-failure", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{401},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
			{
				StatusCodes:            []int{401, 403},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 403,
	})
	require.True(t, matched)
	require.False(t, acted)

	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 401,
	})
	require.True(t, matched)
	require.False(t, acted)

	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 403,
	})
	require.True(t, matched)
	require.False(t, acted)
}

func TestChannelService_ChannelAPIKeyRuleEditStartsNewStreak(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "edited-rule", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{429},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	perf := &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 429}
	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.True(t, matched)
	require.False(t, acted)

	editedDuration := 60
	ch, err = client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{429},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &editedDuration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.True(t, matched)
	require.False(t, acted)
}

func TestChannelService_ChannelAPIKeyRuleKeepsStreakWhenActionFails(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "failed-action", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{429},
				Times:                  1,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})
	require.NoError(t, client.Close())

	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 429,
	})
	require.True(t, matched)
	require.False(t, acted)

	svc.apiKeyErrorCountsLock.Lock()
	defer svc.apiKeyErrorCountsLock.Unlock()
	require.Len(t, svc.apiKeyErrorCounts[ch.ID], 1)
	for _, countsByStatus := range svc.apiKeyErrorCounts[ch.ID] {
		require.Equal(t, 1, countsByStatus[0])
	}
}

func TestChannelService_RemovedAPIKeyRuleKeepsOldStreak(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	rule := objects.APIKeyAutoDisableRule{
		StatusCodes:            []int{429},
		Times:                  2,
		Action:                 objects.APIKeyAutoDisableActionTemporary,
		DisableDurationMinutes: &duration,
	}
	ch := createTestChannelWithAPIKeys(t, client, ctx, "removed-rule", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{rule}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	perf := &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 429}
	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.True(t, matched)
	require.False(t, acted)

	withoutRulesEntity := *ch
	withoutRulesEntity.Policies.APIKeyAutoDisableRules = nil
	withoutRules := buildChannel(&withoutRulesEntity, nil)
	svc.SetEnabledChannelsForTest([]*Channel{withoutRules})
	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.False(t, matched)
	require.False(t, acted)

	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})
	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, perf)
	require.True(t, matched)
	require.True(t, acted)
}

func TestChannelService_ChannelAPIKeyRuleDoesNotStartConcurrentAction(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	rule := objects.APIKeyAutoDisableRule{
		StatusCodes:            []int{429},
		Times:                  1,
		Action:                 objects.APIKeyAutoDisableActionTemporary,
		DisableDurationMinutes: &duration,
	}
	ch := createTestChannelWithAPIKeys(t, client, ctx, "concurrent-action", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{rule}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	ruleKey := apiKeyRuleCounterKey("key1", 0, rule)
	svc.apiKeyRuleActionsInFlight[ch.ID] = map[string]bool{ruleKey: false}

	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 429,
	})
	require.True(t, matched)
	require.False(t, acted)

	svc.apiKeyErrorCountsLock.Lock()
	require.Equal(t, 1, svc.apiKeyErrorCounts[ch.ID][ruleKey][0])
	streakReset, stillInFlight := svc.apiKeyRuleActionsInFlight[ch.ID][ruleKey]
	require.True(t, stillInFlight)
	require.False(t, streakReset)
	svc.apiKeyErrorCountsLock.Unlock()

	now := time.Now()
	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", StartTime: now, EndTime: now,
		Success: true, RequestCompleted: true,
	})

	svc.apiKeyErrorCountsLock.Lock()
	defer svc.apiKeyErrorCountsLock.Unlock()
	require.NotContains(t, svc.apiKeyErrorCounts[ch.ID], ruleKey)
	streakReset, stillInFlight = svc.apiKeyRuleActionsInFlight[ch.ID][ruleKey]
	require.True(t, stillInFlight)
	require.True(t, streakReset)
}

func TestChannelService_ChannelAPIKeyRuleClaimsAndReleasesAction(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	rule := objects.APIKeyAutoDisableRule{
		StatusCodes:            []int{429},
		Times:                  1,
		Action:                 objects.APIKeyAutoDisableActionTemporary,
		DisableDurationMinutes: &duration,
	}
	ch := createTestChannelWithAPIKeys(t, client, ctx, "claim-release", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{rule}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	ruleKey := apiKeyRuleCounterKey("key1", 0, rule)

	// A claim makes the rule in flight regardless of the stored value; the
	// value only records whether a concurrent success already reset the streak.
	svc.claimAPIKeyRuleAction(ch.ID, ruleKey)
	require.True(t, svc.apiKeyRuleActionInFlight(ch.ID, ruleKey))
	svc.apiKeyRuleActionsInFlight[ch.ID][ruleKey] = true
	require.True(t, svc.apiKeyRuleActionInFlight(ch.ID, ruleKey))

	// An executed action must release its claim so later failures can
	// trigger the rule again.
	svc.releaseAPIKeyRuleAction(ch.ID, ruleKey)
	require.False(t, svc.apiKeyRuleActionInFlight(ch.ID, ruleKey))

	// A fresh threshold hit acts and leaves no stale claim behind.
	_, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 429,
	})
	require.True(t, acted)
	require.NotContains(t, svc.apiKeyRuleActionsInFlight, ch.ID)
}

func TestChannelService_ResetAPIKeyFailureExported(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "reset-exported", []string{"key1", "key2"})
	policy := &RetryPolicy{AutoDisableAPIKey: AutoDisableAPIKey{Enabled: true, Mode: AutoDisableModeCodes, Statuses: []AutoDisableAPIKeyStatus{{Status: 401, Times: 2}}}}

	require.False(t, svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 401, ErrorMessage: "invalid"}, policy))
	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, 1, persisted.Credentials.APIKeyStates[0].FailureCount)

	// The exported ResetAPIKeyFailure must clear the streak like the success path.
	require.NoError(t, svc.ResetAPIKeyFailure(ctx, ch.ID, "key1"))
	persisted, err = client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, 0, persisted.Credentials.APIKeyStates[0].FailureCount)
	require.Nil(t, persisted.Credentials.APIKeyStates[0].LastFailedAt)
	require.Equal(t, 0, persisted.Credentials.APIKeyStates[0].LastErrorCode)
}

func TestChannelService_checkAndHandleAPIKeyErrorAnyMode(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-any", []string{"key1", "key2"})

	policy := &RetryPolicy{
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled:                true,
			Mode:                   AutoDisableModeAny,
			Times:                  3,
			DisableDurationMinutes: 30,
		},
	}

	// Three consecutive failures with DIFFERENT status codes (including 0
	// network errors) must accumulate toward one threshold in any mode.
	statuses := []int{500, 0, 502}
	for i, status := range statuses {
		perf := &PerformanceRecord{
			ChannelID:          ch.ID,
			APIKey:             "key1",
			ResponseStatusCode: status,
			Success:            false,
		}
		result := svc.checkAndHandleAPIKeyError(ctx, perf, policy)
		require.Equal(t, i == len(statuses)-1, result, "status %d should trigger only at threshold", status)
	}

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Equal(t, "key1", updatedCh.DisabledAPIKeys[0].Key)
	require.NotNil(t, updatedCh.DisabledAPIKeys[0].ExpiresAt, "temporary disable must set ExpiresAt")
	require.WithinDuration(t, time.Now().Add(30*time.Minute), *updatedCh.DisabledAPIKeys[0].ExpiresAt, time.Minute)
}

func TestChannelService_checkAndHandleAPIKeyErrorDisableDurationZero(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-permanent", []string{"key1", "key2"})

	// DisableDurationMinutes 0 means permanent disable (no ExpiresAt).
	policy := &RetryPolicy{
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled: true,
			Mode:    AutoDisableModeAny,
			Times:   1,
		},
	}

	result := svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 500,
		Success:            false,
	}, policy)
	require.True(t, result)

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updatedCh.DisabledAPIKeys, 1)
	require.Nil(t, updatedCh.DisabledAPIKeys[0].ExpiresAt, "zero duration must be permanent")
}

func TestChannelService_checkAndHandleAPIKeyErrorDisabledFlag(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "test-channel-off", []string{"key1", "key2"})

	policy := &RetryPolicy{
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled: false,
			Mode:    AutoDisableModeAny,
			Times:   1,
		},
	}

	result := svc.checkAndHandleAPIKeyError(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		ResponseStatusCode: 500,
		Success:            false,
	}, policy)
	require.False(t, result, "auto-disable disabled must never disable")

	updatedCh, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Empty(t, updatedCh.DisabledAPIKeys)
}

// Empty-condition rules mean "any error": failures with different status
// codes all count toward one consecutive threshold, mirroring the global
// API key setting's any-error mode.
func TestChannelService_ChannelAPIKeyRuleAnyErrorCountsAcrossStatusCodes(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "any-error-rule", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{
			{
				StatusCodes:            []int{},
				KeywordPatterns:        []string{},
				Times:                  2,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: &duration,
			},
		}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	// First failure (500) must not disable yet.
	matched, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 500})
	require.True(t, matched)
	require.False(t, acted)

	// Second failure with a DIFFERENT status code (0 = network error) reaches
	// the consecutive threshold and disables the key.
	matched, acted = svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{ChannelID: ch.ID, APIKey: "key1", ResponseStatusCode: 0})
	require.True(t, matched)
	require.True(t, acted)

	updated, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, updated.DisabledAPIKeys, 1)
	require.Equal(t, "key1", updated.DisabledAPIKeys[0].Key)
	require.NotNil(t, updated.DisabledAPIKeys[0].ExpiresAt)
	require.WithinDuration(t, time.Now().Add(30*time.Minute), *updated.DisabledAPIKeys[0].ExpiresAt, 5*time.Second)
}
