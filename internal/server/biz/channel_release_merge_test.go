package biz

import (
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

func TestReleaseSettingsPatchPreservesSelfHostedChannelState(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	created, err := svc.CreateChannel(ctx, ent.CreateChannelInput{
		Type:            channel.TypeOpenaiResponses,
		Name:            "release-pool-simulation",
		BaseURL:         lo.ToPtr("https://relay.example.com/v1"),
		Credentials:     objects.ChannelCredentials{Mode: objects.APIKeyModePool, APIKeys: []string{"key-a", "key-b"}},
		SupportedModels: []string{"gpt-test"},
		Policies: &objects.ChannelPolicies{ChannelAutoDisable: &objects.ChannelAutoDisablePolicy{
			Mode: AutoDisableModeAny, Times: 3, Action: "cooldown", CooldownDurationMinutes: 10,
		}},
		Settings: &objects.ChannelSettings{
			APIKeyPool:      &objects.APIKeyPoolSettings{RetryCount: lo.ToPtr(3)},
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true},
			RateLimit:       &objects.ChannelRateLimit{RPM: lo.ToPtr(int64(60))},
		},
	})
	require.NoError(t, err)

	credentials := created.Credentials
	credentials.APIKeyStates[0].FailureCount = 2
	cooldown := time.Now().Add(time.Hour).Truncate(time.Second)
	created = client.Channel.UpdateOneID(created.ID).SetCredentials(credentials).SetCooldownUntil(cooldown).SaveX(ctx)

	updated, err := svc.UpdateChannelSettings(ctx, created.ID, &UpdateChannelSettingsInput{
		MaxSingleChannelRetries: lo.ToPtr(7),
		ResponseTimeout: &objects.ChannelResponseTimeoutSettings{
			Mode: objects.ChannelResponseTimeoutModeCustom, StreamFirstEventTimeoutSeconds: lo.ToPtr(20),
		},
		APIKeyStrategy: lo.ToPtr(objects.APIKeyStrategyRoundRobin),
	})
	require.NoError(t, err)
	require.Equal(t, created.Credentials, updated.Credentials)
	require.Equal(t, created.Policies, updated.Policies)
	require.True(t, updated.CooldownUntil.Equal(cooldown))
	require.Equal(t, created.Settings.APIKeyPool, updated.Settings.APIKeyPool)
	require.Equal(t, created.Settings.CodexSimulation, updated.Settings.CodexSimulation)
	require.Equal(t, created.Settings.RateLimit, updated.Settings.RateLimit)
	require.Equal(t, 7, *updated.Settings.MaxSingleChannelRetries)
	require.Equal(t, 20, *updated.Settings.ResponseTimeout.StreamFirstEventTimeoutSeconds)

	updated, err = svc.UpdateChannelSettings(ctx, created.ID, &UpdateChannelSettingsInput{
		APIKeyPool: &objects.APIKeyPoolSettings{RetryCount: lo.ToPtr(2)},
	})
	require.NoError(t, err)
	require.Equal(t, 2, *updated.Settings.APIKeyPool.RetryCount)
	require.Equal(t, created.Settings.CodexSimulation, updated.Settings.CodexSimulation)
	require.Equal(t, 7, *updated.Settings.MaxSingleChannelRetries)
	require.Equal(t, created.Credentials, updated.Credentials)
}

func TestReleaseVertexKeyMutationPreservesProjectBindings(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	bindings := []objects.VertexAPIKey{
		{APIKey: "vertex-a", ProjectID: "project-alpha", Region: "global"},
		{APIKey: "vertex-b", ProjectID: "project-beta", Region: "us-central1"},
	}
	created, err := svc.CreateChannel(ctx, ent.CreateChannelInput{
		Type:            channel.TypeGeminiVertex,
		Name:            "release-vertex-bindings",
		BaseURL:         lo.ToPtr("https://aiplatform.googleapis.com/v1"),
		Credentials:     objects.ChannelCredentials{Mode: objects.APIKeyModePool, VertexAPIKeys: bindings},
		SupportedModels: []string{"gemini-test"},
	})
	require.NoError(t, err)
	require.Empty(t, created.Credentials.APIKeys, "typed credentials must not be flattened")
	require.Equal(t, bindings, created.Credentials.VertexAPIKeys)

	_, err = svc.ImportChannelAPIKeys(ctx, created.ID, "unbound-key")
	require.ErrorContains(t, err, "project and region bindings")
	require.Equal(t, bindings, client.Channel.GetX(ctx, created.ID).Credentials.VertexAPIKeys)

	require.NoError(t, svc.DisableSelectedAPIKeys(ctx, created.ID, []string{"vertex-a", "vertex-b"}))
	result, err := svc.RemoveChannelAPIKeys(ctx, created.ID, []string{"vertex-a", "vertex-b"})
	require.NoError(t, err)
	require.Equal(t, "ONE_KEY_PRESERVED", result.Message)
	updated := client.Channel.GetX(ctx, created.ID)
	require.Equal(t, bindings[:1], updated.Credentials.VertexAPIKeys)
	require.Empty(t, updated.Credentials.APIKeys)
	require.Empty(t, updated.DisabledAPIKeys)
	require.Equal(t, channel.StatusEnabled, updated.Status)
}
