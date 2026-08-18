package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestNormalizeChannelPolicies_ValidatesCooldownBounds(t *testing.T) {
	valid := &objects.ChannelPolicies{ChannelAutoDisable: &objects.ChannelAutoDisablePolicy{
		Mode:                    AutoDisableModeAny,
		Times:                   1,
		Action:                  objects.AutoDisableActionCooldown,
		CooldownDurationMinutes: maxChannelCooldownDurationMinutes,
	}}
	require.NoError(t, NormalizeChannelPolicies(valid))

	invalid := &objects.ChannelPolicies{ChannelAutoDisable: &objects.ChannelAutoDisablePolicy{
		Mode:                    AutoDisableModeAny,
		Times:                   1,
		Action:                  objects.AutoDisableActionCooldown,
		CooldownDurationMinutes: maxChannelCooldownDurationMinutes + 1,
	}}
	require.ErrorContains(t, NormalizeChannelPolicies(invalid), "must be between")
}

func TestChannelService_RecordChannelFailure_CooldownPersistsWithoutDisabling(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "cooldown-channel", nil)

	policy := channelFailurePolicy{
		Key:              "test:cooldown",
		Threshold:        2,
		Action:           objects.AutoDisableActionCooldown,
		CooldownDuration: 30 * time.Minute,
	}
	perf := &PerformanceRecord{ChannelID: ch.ID, ResponseStatusCode: 503, ErrorMessage: "provider unavailable"}

	count, acted, err := svc.recordChannelFailure(ctx, ch.ID, perf, policy)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.False(t, acted)

	count, acted, err = svc.recordChannelFailure(ctx, ch.ID, perf, policy)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.True(t, acted)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, persisted.Status)
	require.Nil(t, persisted.ErrorMessage)
	require.NotNil(t, persisted.CooldownUntil)
	require.True(t, persisted.CooldownUntil.After(time.Now()))
	require.Equal(t, 0, persisted.AutoDisableState.FailureCount)
	require.Empty(t, persisted.AutoDisableState.FailurePolicyKey)
	require.Equal(t, 503, persisted.AutoDisableState.LastErrorCode)
	require.Equal(t, "provider unavailable", persisted.AutoDisableState.LastError)
}

func TestChannelService_RecoverChannelCooldown_ClearsOnlyAutomaticState(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	until := time.Now().Add(30 * time.Minute)
	ch, err := client.Channel.Create().
		SetName("recover-cooldown").
		SetType(channel.TypeOpenai).
		SetBaseURL("https://api.openai.com").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusDisabled).
		SetErrorMessage("manual reason").
		SetCooldownUntil(until).
		SetAutoDisableState(objects.ChannelAutoDisableState{FailureCount: 2, FailurePolicyKey: "test"}).
		Save(ctx)
	require.NoError(t, err)

	recovered, err := svc.RecoverChannelCooldown(ctx, ch.ID)
	require.NoError(t, err)
	require.True(t, recovered)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, persisted.Status)
	require.Equal(t, "manual reason", *persisted.ErrorMessage)
	require.Nil(t, persisted.CooldownUntil)
	require.Equal(t, objects.ChannelAutoDisableState{}, persisted.AutoDisableState)

	recovered, err = svc.RecoverChannelCooldown(ctx, ch.ID)
	require.NoError(t, err)
	require.False(t, recovered)
}

func TestChannelService_RecoverChannelCooldown_DoesNotClearPendingFailureState(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	state := objects.ChannelAutoDisableState{FailureCount: 2, FailurePolicyKey: "test", LastErrorCode: 503}
	ch := createTestChannelWithAPIKeys(t, client, ctx, "pending-failure", nil)
	_, err := client.Channel.UpdateOneID(ch.ID).SetAutoDisableState(state).Save(ctx)
	require.NoError(t, err)

	recovered, err := svc.RecoverChannelCooldown(ctx, ch.ID)
	require.NoError(t, err)
	require.False(t, recovered)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, state, persisted.AutoDisableState)
}

func TestChannelService_GetEnabledChannels_FiltersActiveCooldownLazily(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	svc := newTestChannelService(client)
	activeUntil := time.Now().Add(30 * time.Minute)
	expiredUntil := time.Now().Add(-time.Minute)

	active := &Channel{Channel: &ent.Channel{ID: 1, Status: channel.StatusEnabled, CooldownUntil: &activeUntil}}
	expired := &Channel{Channel: &ent.Channel{ID: 2, Status: channel.StatusEnabled, CooldownUntil: &expiredUntil}}
	svc.SetEnabledChannelsForTest([]*Channel{active, expired})

	channels := svc.GetEnabledChannels()
	require.Len(t, channels, 1)
	require.Equal(t, 2, channels[0].ID)
}

func TestChannelService_UpdateChannel_PoolToSingleClearsAPIKeyRules(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch, err := client.Channel.Create().
		SetName("pool-to-single").
		SetType(channel.TypeOpenai).
		SetBaseURL("https://api.openai.com").
		SetCredentials(objects.ChannelCredentials{Mode: objects.APIKeyModePool, APIKeys: []string{"key1", "key2"}}).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{{
			Times:                  2,
			Action:                 objects.APIKeyAutoDisableActionTemporary,
			DisableDurationMinutes: lo.ToPtr(30),
		}}}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{
		Credentials: &objects.ChannelCredentials{Mode: objects.APIKeyModeSingle, APIKeys: []string{"key1"}},
	})
	require.NoError(t, err)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, objects.APIKeyModeSingle, persisted.Credentials.Mode)
	require.Empty(t, persisted.Policies.APIKeyAutoDisableRules)
}

func TestChannelService_RecordPerformance_ExecutesChannelAndKeyDimensionsIndependently(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch, err := client.Channel.Create().
		SetName("independent-dimensions").
		SetType(channel.TypeOpenai).
		SetBaseURL("https://api.openai.com").
		SetCredentials(objects.ChannelCredentials{Mode: objects.APIKeyModePool, APIKeys: []string{"key1", "key2"}}).
		SetPolicies(objects.ChannelPolicies{
			ChannelAutoDisable: &objects.ChannelAutoDisablePolicy{
				Mode:                    AutoDisableModeCodes,
				Statuses:                []objects.ChannelAutoDisableStatus{{Status: 401, Times: 1}},
				Action:                  objects.AutoDisableActionDisable,
				CooldownDurationMinutes: 0,
			},
			APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{{
				StatusCodes:            []int{401},
				Times:                  1,
				Action:                 objects.APIKeyAutoDisableActionTemporary,
				DisableDurationMinutes: lo.ToPtr(30),
			}},
		}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	now := time.Now()
	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:          ch.ID,
		APIKey:             "key1",
		StartTime:          now,
		EndTime:            now,
		Success:            false,
		RequestCompleted:   true,
		ResponseStatusCode: 401,
		ErrorMessage:       "unauthorized",
	})

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, persisted.Status)
	require.Len(t, persisted.DisabledAPIKeys, 1)
	require.Equal(t, "key1", persisted.DisabledAPIKeys[0].Key)
}

type sourceRecordingAPIKeyTester struct {
	source request.Source
}

func (tester *sourceRecordingAPIKeyTester) CheckChannelAPIKeys(
	ctx context.Context,
	_ int,
	keys []string,
	_ int,
	_ time.Duration,
) []ChannelAPIKeyCheckResult {
	tester.source = contexts.GetSourceOrDefault(ctx, request.SourceAPI)
	return lo.Map(keys, func(key string, _ int) ChannelAPIKeyCheckResult {
		return ChannelAPIKeyCheckResult{Key: key}
	})
}

func TestChannelService_CheckChannelAPIKeys_MarksProbeTrafficAsTest(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	tester := &sourceRecordingAPIKeyTester{}
	svc.SetAPIKeyTester(tester)
	ch, err := client.Channel.Create().
		SetName("probe-source").
		SetType(channel.TypeOpenai).
		SetBaseURL("https://api.openai.com").
		SetCredentials(objects.ChannelCredentials{Mode: objects.APIKeyModePool, APIKeys: []string{"key1", "key2"}}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	_, err = svc.CheckChannelAPIKeys(ctx, ch.ID, ExportChannelAPIKeyStatusAll)
	require.NoError(t, err)
	require.Equal(t, request.SourceTest, tester.source)
}

func TestChannelService_RecordChannelFailure_DoesNotWaitForWebhook(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	requestStarted := make(chan struct{}, 1)
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestStarted <- struct{}{}
		<-releaseRequest
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := WebhookNotifierConfig{
		Targets: []WebhookTarget{{
			Name:      "slow",
			Enabled:   true,
			URL:       server.URL,
			TimeoutMs: 2000,
			Body:      `{"event":"{{.Event}}"}`,
		}},
		Subscriptions: []WebhookSubscription{{Event: EventChannelAutoCooled, TargetNames: []string{"slow"}}},
	}
	systemService := newTestSystemServiceWithWebhookConfig(t, client, cfg)
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	svc.SystemService = systemService
	svc.WebhookNotifier = NewWebhookNotifier(systemService, httpclient.NewHttpClient())
	ch := createTestChannelWithAPIKeys(t, client, ctx, "async-webhook", nil)

	startedAt := time.Now()
	_, acted, err := svc.recordChannelFailure(ctx, ch.ID, &PerformanceRecord{
		ChannelID:          ch.ID,
		ResponseStatusCode: 503,
		ErrorMessage:       "provider unavailable\nsecret response body",
	}, channelFailurePolicy{
		Key:              "test:async-webhook",
		Threshold:        1,
		Action:           objects.AutoDisableActionCooldown,
		CooldownDuration: time.Minute,
	})
	require.NoError(t, err)
	require.True(t, acted)
	require.Less(t, time.Since(startedAt), 500*time.Millisecond)

	select {
	case <-requestStarted:
		close(releaseRequest)
	case <-time.After(2 * time.Second):
		close(releaseRequest)
		t.Fatal("webhook request did not start")
	}
}

func TestSummarizeChannelAutoActionReason_RemovesResponseBodyAndCapsLength(t *testing.T) {
	reason := summarizeChannelAutoActionReason("provider unavailable\nsecret response body", 503)
	require.Equal(t, "provider unavailable", reason)

	longReason := summarizeChannelAutoActionReason(strings.Repeat("界", channelAutoActionReasonMaxRunes+10), 500)
	require.Len(t, []rune(longReason), channelAutoActionReasonMaxRunes)
}

func TestChannelService_RecordPerformance_SkipsAutomaticStateForDiagnostics(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	ch := createTestChannelWithAPIKeys(t, client, ctx, "diagnostic-channel", []string{"key"})
	state := objects.ChannelAutoDisableState{FailureCount: 1, FailurePolicyKey: "test", LastErrorCode: 500, LastError: "failed"}
	_, err := client.Channel.UpdateOneID(ch.ID).SetAutoDisableState(state).Save(ctx)
	require.NoError(t, err)

	now := time.Now()
	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:        ch.ID,
		APIKey:           "key",
		StartTime:        now,
		EndTime:          now,
		Success:          true,
		RequestCompleted: true,
		SkipAutoDisable:  true,
	})

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, state, persisted.AutoDisableState)
}
