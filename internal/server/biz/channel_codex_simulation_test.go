package biz

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func TestNormalizeCodexSimulation(t *testing.T) {
	t.Run("nil settings", func(t *testing.T) {
		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, nil))
	})

	t.Run("no simulation config", func(t *testing.T) {
		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, &objects.ChannelSettings{}))
	})

	t.Run("disabled passes on any channel type", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: false},
		}
		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenai, settings))
	})

	t.Run("enabled rejected on non-target channel", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Preset: objects.CodexSimulationPresetNormal},
		}
		require.Error(t, NormalizeCodexSimulation(channel.TypeCodex, settings))
		require.Error(t, NormalizeCodexSimulation(channel.TypeOpenai, settings))
		require.Error(t, NormalizeCodexSimulation(channel.TypeNanogptResponses, settings))
	})

	t.Run("empty preset defaults to normal", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true},
		}
		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		require.Equal(t, objects.CodexSimulationPresetNormal, settings.CodexSimulation.Preset)
	})

	t.Run("invalid preset rejected", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Preset: "turbo"},
		}
		require.Error(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
	})

	t.Run("strategy generated when missing", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Preset: objects.CodexSimulationPresetEnhanced},
		}
		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		require.True(t, IsValidCodexSimulationStrategy(settings.CodexSimulation.Strategy))
		require.NotEmpty(t, settings.CodexSimulation.Strategy.InstallationID)
		require.Regexp(t, `^thread_[0-9a-f]{32}$`, settings.CodexSimulation.Strategy.ThreadID)
	})

	t.Run("explicit all-false options are preserved", func(t *testing.T) {
		allDisabled := &objects.CodexSimulationOptions{}
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled: true,
				Preset:  objects.CodexSimulationPresetNormal,
				Options: allDisabled,
			},
		}

		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		require.Same(t, allDisabled, settings.CodexSimulation.Options)
		require.Equal(t, objects.CodexSimulationOptions{}, settings.CodexSimulation.EffectiveOptions())
	})

	t.Run("missing options materialize preset defaults", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled: true,
				Preset:  objects.CodexSimulationPresetUA,
			},
		}

		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		require.NotNil(t, settings.CodexSimulation.Options)
		require.Equal(t, objects.PresetDefaultOptions(objects.CodexSimulationPresetUA), *settings.CodexSimulation.Options)
	})

	t.Run("profile defaults are materialized", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true},
		}

		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		sim := settings.CodexSimulation
		standardUA, liteUA := objects.DefaultCodexSimulationUserAgents(objects.DefaultCodexSimulationVersion, sim.Platform)
		require.Equal(t, objects.DefaultCodexSimulationVersion, sim.Version)
		require.True(t, objects.IsCodexSimulationPlatform(sim.Platform))
		require.NotEqual(t, objects.CodexSimulationPlatformCustom, sim.Platform)
		require.Equal(t, standardUA, sim.StandardUserAgent)
		require.Equal(t, liteUA, sim.LiteUserAgent)
	})

	t.Run("custom profile is preserved", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled:           true,
				Version:           "0.145.0-custom",
				Platform:          objects.CodexSimulationPlatformCustom,
				StandardUserAgent: "custom-standard/0.145.0 (MyOS 1.0; x86_64)",
				LiteUserAgent:     "custom-lite/0.145.0 (MyOS 1.0; x86_64)",
			},
		}

		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		sim := settings.CodexSimulation
		require.Equal(t, "0.145.0-custom", sim.Version)
		require.Equal(t, objects.CodexSimulationPlatformCustom, sim.Platform)
		require.Equal(t, "custom-standard/0.145.0 (MyOS 1.0; x86_64)", sim.StandardUserAgent)
		require.Equal(t, "custom-lite/0.145.0 (MyOS 1.0; x86_64)", sim.LiteUserAgent)
	})

	t.Run("unsafe profile values are rejected", func(t *testing.T) {
		for _, sim := range []*objects.CodexSimulationSettings{
			{Enabled: true, Version: "0.145.0 bad"},
			{Enabled: true, Platform: "plan9"},
			{Enabled: true, StandardUserAgent: "codex\r\ninjected: true"},
			{Enabled: true, LiteUserAgent: "codex\nlite"},
			{
				Enabled:           true,
				Platform:          objects.CodexSimulationPlatformCustom,
				StandardUserAgent: "codex_cli_rs/0.144.2 (Windows 11; x86_64)",
				LiteUserAgent:     "codex_exec/0.144.2 (Ubuntu 24.04; x86_64) tmux/3.5a (codex_exec; 0.144.2)",
			},
			{
				Enabled:           true,
				Platform:          objects.CodexSimulationPlatformUbuntu,
				StandardUserAgent: "codex_cli_rs/0.144.2 (Windows 11; x86_64)",
				LiteUserAgent:     "codex_exec/0.144.2 (Windows 11; x86_64) tmux/3.5a (codex_exec; 0.144.2)",
			},
		} {
			settings := &objects.ChannelSettings{CodexSimulation: sim}
			require.Error(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		}
	})

	t.Run("existing strategy preserved", func(t *testing.T) {
		strategy := objects.NewCodexSimulationStrategy()
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled:  true,
				Preset:   objects.CodexSimulationPresetUA,
				Strategy: strategy,
			},
		}
		require.NoError(t, NormalizeCodexSimulation(channel.TypeOpenaiResponses, settings))
		require.Equal(t, strategy, settings.CodexSimulation.Strategy)
	})
}

func TestCodexSimulationUserAgentsSharePlatform(t *testing.T) {
	cases := map[objects.CodexSimulationPlatform]string{
		objects.CodexSimulationPlatformWindows10: "Windows 10",
		objects.CodexSimulationPlatformWindows11: "Windows 11",
		objects.CodexSimulationPlatformDebian:    "Debian 13.0.0",
		objects.CodexSimulationPlatformUbuntu:    "Ubuntu 24.04",
		objects.CodexSimulationPlatformMacOS:     "macOS 15.0",
	}

	for platform, expectedSystem := range cases {
		t.Run(string(platform), func(t *testing.T) {
			standardUA, liteUA := objects.DefaultCodexSimulationUserAgents("0.145.0", platform)
			require.Contains(t, standardUA, "("+expectedSystem+"; x86_64)")
			require.Contains(t, liteUA, "("+expectedSystem+"; x86_64)")
		})
	}

	for range 100 {
		platform := objects.RandomCodexSimulationPlatform()
		require.True(t, objects.IsCodexSimulationPlatform(platform))
		require.NotEqual(t, objects.CodexSimulationPlatformCustom, platform)
	}
}

func TestRandomizeCodexSimulation(t *testing.T) {
	sim := &objects.CodexSimulationSettings{
		Enabled:           true,
		Preset:            objects.CodexSimulationPresetUA,
		Options:           lo.ToPtr(objects.CodexSimulationOptions{UserAgent: true}),
		Version:           "0.145.0",
		Platform:          objects.CodexSimulationPlatformCustom,
		StandardUserAgent: "custom-standard",
		LiteUserAgent:     "custom-lite",
		Strategy:          objects.NewCodexSimulationStrategy(),
	}
	oldStrategy := sim.Strategy

	RandomizeCodexSimulation(sim)

	// 随机化保留请求能力与版本，但统一轮换身份、系统画像和两种 UA。
	require.Equal(t, objects.CodexSimulationPresetUA, sim.Preset)
	require.Equal(t, objects.CodexSimulationOptions{UserAgent: true}, *sim.Options)
	require.Equal(t, "0.145.0", sim.Version)
	require.True(t, objects.IsCodexSimulationPlatform(sim.Platform))
	require.NotEqual(t, objects.CodexSimulationPlatformCustom, sim.Platform)
	standardUA, liteUA := objects.DefaultCodexSimulationUserAgents(sim.Version, sim.Platform)
	require.Equal(t, standardUA, sim.StandardUserAgent)
	require.Equal(t, liteUA, sim.LiteUserAgent)
	require.NotEqual(t, oldStrategy.InstallationID, sim.Strategy.InstallationID)
	require.NotEqual(t, oldStrategy.ThreadID, sim.Strategy.ThreadID)
}

func TestEnsureCodexSimulationStrategy(t *testing.T) {
	t.Run("invalid input reuses persisted strategy", func(t *testing.T) {
		persisted := objects.NewCodexSimulationStrategy()
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Preset: objects.CodexSimulationPresetNormal},
		}
		existing := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Strategy: persisted},
		}

		ensureCodexSimulationStrategy(settings, existing)

		require.Equal(t, persisted, settings.CodexSimulation.Strategy)
	})

	t.Run("generates fresh when nothing persisted", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Preset: objects.CodexSimulationPresetNormal},
		}

		ensureCodexSimulationStrategy(settings, nil)

		require.True(t, IsValidCodexSimulationStrategy(settings.CodexSimulation.Strategy))
	})

	t.Run("noop when disabled", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: false},
		}

		ensureCodexSimulationStrategy(settings, nil)

		require.False(t, IsValidCodexSimulationStrategy(settings.CodexSimulation.Strategy))
		require.Equal(t, "", settings.CodexSimulation.Strategy.InstallationID)
	})
}

func TestChannelCodexSimulationService(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	channelSvc := NewChannelServiceForTest(client)

	createChannel := func(t *testing.T, typeValue channel.Type, name string, enableSimulation bool) int {
		t.Helper()

		settings := &objects.ChannelSettings{}
		if enableSimulation {
			settings.CodexSimulation = &objects.CodexSimulationSettings{
				Enabled: true,
				Preset:  objects.CodexSimulationPresetNormal,
			}
		}

		entChannel, err := channelSvc.CreateChannel(ctx, ent.CreateChannelInput{
			Name:             name,
			Type:             typeValue,
			BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
			Credentials:      objects.ChannelCredentials{APIKey: "test-key"},
			SupportedModels:  []string{"gpt-5.5"},
			DefaultTestModel: "gpt-5.5",
			Settings:         settings,
		})
		require.NoError(t, err)

		return entChannel.ID
	}

	t.Run("creating openai_responses channel materializes strategy", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-target", true)

		loaded, err := client.Channel.Get(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, loaded.Settings.CodexSimulation)
		require.True(t, loaded.Settings.CodexSimulation.Enabled)
		require.True(t, IsValidCodexSimulationStrategy(loaded.Settings.CodexSimulation.Strategy))
	})

	t.Run("createChannel rejects simulation on non-target type", func(t *testing.T) {
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{Enabled: true, Preset: objects.CodexSimulationPresetNormal},
		}

		_, err := channelSvc.CreateChannel(ctx, ent.CreateChannelInput{
			Name:             "sim-rejected",
			Type:             channel.TypeCodex,
			BaseURL:          lo.ToPtr("https://chatgpt.com/backend-api/codex#"),
			Credentials:      objects.ChannelCredentials{APIKey: "test-key"},
			SupportedModels:  []string{"gpt-5.5"},
			DefaultTestModel: "gpt-5.5",
			Settings:         settings,
		})
		require.Error(t, err)
	})

	t.Run("update preserves strategy across saves", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-preserve", true)

		loaded, err := client.Channel.Get(ctx, id)
		require.NoError(t, err)
		originalStrategy := loaded.Settings.CodexSimulation.Strategy

		// 模拟前端只提交 enabled/preset/options（无 strategy）。
		next := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled: true,
				Preset:  objects.CodexSimulationPresetEnhanced,
				Options: lo.ToPtr(objects.PresetDefaultOptions(objects.CodexSimulationPresetEnhanced)),
			},
		}

		updated, err := channelSvc.UpdateChannel(ctx, id, &ent.UpdateChannelInput{Settings: next})
		require.NoError(t, err)
		require.Equal(t, originalStrategy, updated.Settings.CodexSimulation.Strategy)
		require.Equal(t, objects.CodexSimulationPresetEnhanced, updated.Settings.CodexSimulation.Preset)
	})

	t.Run("randomize rotates the complete fingerprint", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-randomize", true)

		loaded, err := client.Channel.Get(ctx, id)
		require.NoError(t, err)
		oldStrategy := loaded.Settings.CodexSimulation.Strategy

		updated, err := channelSvc.RandomizeChannelCodexSimulation(ctx, id)
		require.NoError(t, err)
		require.Equal(t, loaded.Settings.CodexSimulation.Preset, updated.Settings.CodexSimulation.Preset)
		require.Equal(t, loaded.Settings.CodexSimulation.Options, updated.Settings.CodexSimulation.Options)
		standardUA, liteUA := objects.DefaultCodexSimulationUserAgents(updated.Settings.CodexSimulation.Version, updated.Settings.CodexSimulation.Platform)
		require.Equal(t, standardUA, updated.Settings.CodexSimulation.StandardUserAgent)
		require.Equal(t, liteUA, updated.Settings.CodexSimulation.LiteUserAgent)
		require.NotEqual(t, oldStrategy.InstallationID, updated.Settings.CodexSimulation.Strategy.InstallationID)
	})

	t.Run("type changes disable simulation and preserve the draft", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-type-change", true)

		before, err := client.Channel.Get(ctx, id)
		require.NoError(t, err)
		beforeSimulation := *before.Settings.CodexSimulation

		changed, err := channelSvc.UpdateChannel(ctx, id, &ent.UpdateChannelInput{Type: lo.ToPtr(channel.TypeOpenai)})
		require.NoError(t, err)
		require.Equal(t, channel.TypeOpenai, changed.Type)
		require.NotNil(t, changed.Settings.CodexSimulation)
		require.False(t, changed.Settings.CodexSimulation.Enabled)
		require.Equal(t, beforeSimulation.Preset, changed.Settings.CodexSimulation.Preset)
		require.Equal(t, beforeSimulation.Options, changed.Settings.CodexSimulation.Options)
		require.Equal(t, beforeSimulation.Strategy, changed.Settings.CodexSimulation.Strategy)

		changedBack, err := channelSvc.UpdateChannel(ctx, id, &ent.UpdateChannelInput{Type: lo.ToPtr(channel.TypeOpenaiResponses)})
		require.NoError(t, err)
		require.Equal(t, channel.TypeOpenaiResponses, changedBack.Type)
		require.False(t, changedBack.Settings.CodexSimulation.Enabled)
		require.Equal(t, beforeSimulation.Strategy, changedBack.Settings.CodexSimulation.Strategy)
	})

	t.Run("type change with submitted settings keeps custom draft", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-type-settings", true)
		before, err := client.Channel.Get(ctx, id)
		require.NoError(t, err)

		customOptions := &objects.CodexSimulationOptions{UserAgent: true, Prompt: true}
		incoming := &objects.ChannelSettings{
			RetryableStatusCodes: []int{418},
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled: true,
				Preset:  objects.CodexSimulationPresetEnhanced,
				Options: customOptions,
			},
		}

		changed, err := channelSvc.UpdateChannel(ctx, id, &ent.UpdateChannelInput{
			Type:     lo.ToPtr(channel.TypeOpenai),
			Settings: incoming,
		})
		require.NoError(t, err)
		require.False(t, changed.Settings.CodexSimulation.Enabled)
		require.Equal(t, objects.CodexSimulationPresetEnhanced, changed.Settings.CodexSimulation.Preset)
		require.Equal(t, *customOptions, *changed.Settings.CodexSimulation.Options)
		require.Equal(t, before.Settings.CodexSimulation.Strategy, changed.Settings.CodexSimulation.Strategy)
		require.Equal(t, []int{418}, changed.Settings.RetryableStatusCodes)
	})

	t.Run("non-target channel cannot be enabled directly", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenai, "sim-direct-rejected", false)
		settings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled: true,
				Preset:  objects.CodexSimulationPresetNormal,
				Options: lo.ToPtr(objects.PresetDefaultOptions(objects.CodexSimulationPresetNormal)),
			},
		}

		_, err := channelSvc.UpdateChannel(ctx, id, &ent.UpdateChannelInput{Settings: settings})
		require.Error(t, err)
	})

	t.Run("disabled simulation rejects reset and randomize", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-disabled-actions", true)
		disabledSettings := &objects.ChannelSettings{
			CodexSimulation: &objects.CodexSimulationSettings{
				Enabled: false,
				Preset:  objects.CodexSimulationPresetNormal,
				Options: lo.ToPtr(objects.PresetDefaultOptions(objects.CodexSimulationPresetNormal)),
			},
		}

		disabled, err := channelSvc.UpdateChannel(ctx, id, &ent.UpdateChannelInput{Settings: disabledSettings})
		require.NoError(t, err)
		beforeStrategy := disabled.Settings.CodexSimulation.Strategy

		_, err = channelSvc.RandomizeChannelCodexSimulation(ctx, id)
		require.Error(t, err)

		after, err := client.Channel.Get(ctx, id)
		require.NoError(t, err)
		require.False(t, after.Settings.CodexSimulation.Enabled)
		require.Equal(t, beforeStrategy, after.Settings.CodexSimulation.Strategy)
	})

	t.Run("mutations rejected on non-target channel", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenai, "sim-other", false)

		_, err := channelSvc.RandomizeChannelCodexSimulation(ctx, id)
		require.Error(t, err)
	})

	t.Run("mutations rejected when simulation never enabled", func(t *testing.T) {
		id := createChannel(t, channel.TypeOpenaiResponses, "sim-never-enabled", false)

		_, err := channelSvc.RandomizeChannelCodexSimulation(ctx, id)
		require.Error(t, err)
	})
}
