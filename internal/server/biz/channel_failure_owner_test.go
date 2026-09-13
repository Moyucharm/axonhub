package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func configureFailureOwnerPolicy(t *testing.T, svc *ChannelService, ctx context.Context, policy *RetryPolicy) {
	t.Helper()
	require.NoError(t, svc.SystemService.SetRetryPolicy(ctx, policy))
}

func recordFailureForOwnerTest(t *testing.T, svc *ChannelService, ctx context.Context, channelID int, apiKey string, statusCode int) {
	t.Helper()
	now := time.Now()
	svc.RecordPerformance(ctx, &PerformanceRecord{
		ChannelID:          channelID,
		APIKey:             apiKey,
		StartTime:          now,
		EndTime:            now,
		Success:            false,
		RequestCompleted:   true,
		ResponseStatusCode: statusCode,
		ErrorMessage:       "provider failure",
	})
}

func TestRecordPerformance_KeyPoolPolicyOwnsFailure(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	configureFailureOwnerPolicy(t, svc, ctx, &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableChannelStatus{{Status: 401, Times: 1}},
		},
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled:                true,
			Mode:                   AutoDisableModeCodes,
			Statuses:               []AutoDisableAPIKeyStatus{{Status: 401, Times: 1}},
			DisableDurationMinutes: 30,
		},
	})

	ch := createTestChannelWithAPIKeys(t, client, ctx, "key-pool-owner", []string{"key1", "key2"})
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	recordFailureForOwnerTest(t, svc, ctx, ch.ID, "key1", 401)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, persisted.Status)
	require.Empty(t, persisted.AutoDisableState)
	require.Len(t, persisted.DisabledAPIKeys, 1)
	require.Equal(t, "key1", persisted.DisabledAPIKeys[0].Key)
	require.Len(t, persisted.Credentials.APIKeyStates, 1)
	require.Equal(t, 1, persisted.Credentials.APIKeyStates[0].FailureCount)
}

func TestRecordPerformance_UnmatchedKeyPolicyFallsBackToChannel(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	configureFailureOwnerPolicy(t, svc, ctx, &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableChannelStatus{{Status: 401, Times: 1}},
		},
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableAPIKeyStatus{{Status: 403, Times: 1}},
		},
	})

	ch := createTestChannelWithAPIKeys(t, client, ctx, "channel-fallback", []string{"key1", "key2"})
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	recordFailureForOwnerTest(t, svc, ctx, ch.ID, "key1", 401)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, persisted.Status)
	require.NotNil(t, persisted.AutoDisabledAt)
	require.Empty(t, persisted.DisabledAPIKeys)
	require.Empty(t, persisted.Credentials.APIKeyStates)
}

func TestRecordPerformance_ChannelAPIKeyRuleOwnsFailureBeforeThreshold(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	configureFailureOwnerPolicy(t, svc, ctx, &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableChannelStatus{{Status: 401, Times: 1}},
		},
	})

	duration := 30
	ch := createTestChannelWithAPIKeys(t, client, ctx, "channel-key-rule-owner", []string{"key1", "key2"})
	ch, err := client.Channel.UpdateOneID(ch.ID).
		SetPolicies(objects.ChannelPolicies{APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{{
			StatusCodes:            []int{401},
			Times:                  2,
			Action:                 objects.APIKeyAutoDisableActionTemporary,
			DisableDurationMinutes: &duration,
		}}}).
		Save(ctx)
	require.NoError(t, err)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	recordFailureForOwnerTest(t, svc, ctx, ch.ID, "key1", 401)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, persisted.Status)
	require.Empty(t, persisted.AutoDisableState)
	require.Empty(t, persisted.DisabledAPIKeys)
	require.Len(t, persisted.Credentials.APIKeyStates, 1)
	require.Equal(t, 1, persisted.Credentials.APIKeyStates[0].FailureCount)
}

func TestRecordPerformance_SingleKeyFallsBackToChannelPolicy(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	configureFailureOwnerPolicy(t, svc, ctx, &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableChannelStatus{{Status: 401, Times: 1}},
		},
		AutoDisableAPIKey: AutoDisableAPIKey{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableAPIKeyStatus{{Status: 401, Times: 1}},
		},
	})

	ch := createTestChannelWithAPIKeys(t, client, ctx, "single-key-owner", []string{"key1"})
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	recordFailureForOwnerTest(t, svc, ctx, ch.ID, "key1", 401)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, persisted.Status)
	require.NotNil(t, persisted.AutoDisabledAt)
	require.Empty(t, persisted.DisabledAPIKeys)
	require.Empty(t, persisted.Credentials.APIKeyStates)
}

func TestRecordPerformance_NoCredentialUsesChannelPolicy(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newTestChannelService(client)
	configureFailureOwnerPolicy(t, svc, ctx, &RetryPolicy{
		AutoDisableChannel: AutoDisableChannel{
			Enabled:  true,
			Mode:     AutoDisableModeCodes,
			Statuses: []AutoDisableChannelStatus{{Status: 401, Times: 1}},
		},
	})

	ch := createTestChannelWithAPIKeys(t, client, ctx, "no-credential-owner", nil)
	svc.SetEnabledChannelsForTest([]*Channel{buildChannel(ch, nil)})

	recordFailureForOwnerTest(t, svc, ctx, ch.ID, "", 401)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusDisabled, persisted.Status)
	require.NotNil(t, persisted.AutoDisabledAt)
	require.Zero(t, persisted.AutoDisableState.FailureCount)
	require.Equal(t, 401, persisted.AutoDisableState.LastErrorCode)
	require.Equal(t, "provider failure", persisted.AutoDisableState.LastError)
}
