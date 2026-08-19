package biz

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xerrors"
	"github.com/samber/lo"
)

// Codex 指纹模拟仅允许 openai_responses 渠道启用。
// 与 codex-disguise 的全局策略不同，本项目的模拟策略为渠道独立随机生成，
// 并持久化在各自 ChannelSettings.CodexSimulation.Strategy 中。

var (
	codexSimulationVersionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	codexSimulationThreadIDPattern = regexp.MustCompile(`^thread_[0-9a-f]{32}$`)
)

func codexSimulationUserAgentSystem(userAgent string) (string, bool) {
	open := strings.Index(userAgent, " (")
	if open < 0 {
		return "", false
	}
	start := open + 2
	relativeEnd := strings.IndexByte(userAgent[start:], ';')
	if relativeEnd < 0 {
		return "", false
	}

	system := strings.TrimSpace(userAgent[start : start+relativeEnd])
	return system, system != ""
}

func validateCodexSimulationUserAgentSystems(sim *objects.CodexSimulationSettings, standardUA, liteUA string) error {
	standardSystem, standardOK := codexSimulationUserAgentSystem(standardUA)
	liteSystem, liteOK := codexSimulationUserAgentSystem(liteUA)
	if !standardOK || !liteOK || standardSystem != liteSystem {
		return xerrors.ValidationError("codex simulation standard and Lite User-Agents must use the same system")
	}
	if sim.Platform != objects.CodexSimulationPlatformCustom &&
		standardSystem != objects.CodexSimulationPlatformName(sim.Platform) {
		return xerrors.ValidationError("codex simulation User-Agent system does not match the selected platform")
	}

	return nil
}

func normalizeCodexSimulationProfile(sim *objects.CodexSimulationSettings) error {
	version := strings.TrimSpace(sim.Version)
	if version == "" {
		version = objects.DefaultCodexSimulationVersion
	}
	if !codexSimulationVersionPattern.MatchString(version) {
		return xerrors.ValidationError("codex simulation version must contain only letters, numbers, dots, underscores, or hyphens")
	}

	platformWasUnset := sim.Platform == ""
	if sim.Platform == "" {
		if strings.TrimSpace(sim.StandardUserAgent) == "" && strings.TrimSpace(sim.LiteUserAgent) == "" {
			sim.Platform = objects.RandomCodexSimulationPlatform()
		} else {
			sim.Platform = objects.CodexSimulationPlatformCustom
		}
	}
	if !objects.IsCodexSimulationPlatform(sim.Platform) {
		return xerrors.ValidationError("codex simulation platform is invalid")
	}

	standardDefault, liteDefault := objects.DefaultCodexSimulationUserAgents(version, sim.Platform)
	standardUA := strings.TrimSpace(sim.StandardUserAgent)
	if standardUA == "" {
		standardUA = standardDefault
	}
	liteUA := strings.TrimSpace(sim.LiteUserAgent)
	if liteUA == "" {
		liteUA = liteDefault
	}

	for name, value := range map[string]string{"standard user agent": standardUA, "lite user agent": liteUA} {
		if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
			return xerrors.ValidationError(fmt.Sprintf("codex simulation %s is invalid", name))
		}
	}

	if platformWasUnset {
		standardSystem, standardOK := codexSimulationUserAgentSystem(standardUA)
		liteSystem, liteOK := codexSimulationUserAgentSystem(liteUA)
		if !standardOK || !liteOK || standardSystem != liteSystem {
			sim.Platform = objects.RandomCodexSimulationPlatform()
			standardUA, liteUA = objects.DefaultCodexSimulationUserAgents(version, sim.Platform)
		}
	}

	if err := validateCodexSimulationUserAgentSystems(sim, standardUA, liteUA); err != nil {
		return err
	}

	sim.Version = version
	sim.StandardUserAgent = standardUA
	sim.LiteUserAgent = liteUA

	return nil
}

// validateCodexSimulationStrategy validates a complete browser-generated identity
// before it can replace the persisted channel fingerprint.
func validateCodexSimulationStrategy(strategy objects.CodexSimulationStrategy) error {
	installationID, err := uuid.Parse(strategy.InstallationID)
	if err != nil || installationID.String() != strategy.InstallationID {
		return xerrors.ValidationError("codex simulation installation ID must be a canonical UUID")
	}
	if !codexSimulationThreadIDPattern.MatchString(strategy.ThreadID) {
		return xerrors.ValidationError("codex simulation thread ID is invalid")
	}
	if strategy.WindowGeneration < 0 {
		return xerrors.ValidationError("codex simulation window generation must be non-negative")
	}

	return nil
}

func isZeroCodexSimulationStrategy(strategy objects.CodexSimulationStrategy) bool {
	return strategy.InstallationID == "" && strategy.ThreadID == "" && strategy.WindowGeneration == 0
}

// NormalizeCodexSimulation validates and normalizes per-channel Codex simulation settings.
// Simulation may only be enabled on openai_responses channels; any other channel type is
// rejected with an error so the UI restriction cannot be bypassed by writing settings directly.
func NormalizeCodexSimulation(channelType channel.Type, settings *objects.ChannelSettings) error {
	if settings == nil || settings.CodexSimulation == nil {
		return nil
	}

	sim := settings.CodexSimulation

	switch sim.Preset {
	case "", objects.CodexSimulationPresetNormal:
		sim.Preset = objects.CodexSimulationPresetNormal
	case objects.CodexSimulationPresetUA, objects.CodexSimulationPresetEnhanced:
		// Valid.
	default:
		return fmt.Errorf("invalid codex simulation preset %q", sim.Preset)
	}

	if sim.Options == nil {
		sim.Options = lo.ToPtr(objects.PresetDefaultOptions(sim.Preset))
	}
	if err := normalizeCodexSimulationProfile(sim); err != nil {
		return err
	}

	strategyIsZero := isZeroCodexSimulationStrategy(sim.Strategy)
	if !strategyIsZero {
		if err := validateCodexSimulationStrategy(sim.Strategy); err != nil {
			return err
		}
	}

	if !sim.Enabled {
		return nil
	}

	if channelType != channel.TypeOpenaiResponses {
		return fmt.Errorf("codex simulation is only supported on openai_responses channels")
	}

	// A complete submitted strategy is validated and preserved exactly. When no
	// strategy is submitted, the caller restores the persisted identity or this
	// normalization path generates a fresh fallback.
	if strategyIsZero {
		sim.Strategy = objects.NewCodexSimulationStrategy()
	}

	return nil
}

// IsValidCodexSimulationStrategy reports whether the per-channel fingerprint is usable.
func IsValidCodexSimulationStrategy(strategy objects.CodexSimulationStrategy) bool {
	return validateCodexSimulationStrategy(strategy) == nil
}

// RandomizeCodexSimulation rotates the channel identity and UA system profile while
// preserving the current preset, sub-options, and version. Request capabilities never
// change unexpectedly, but the full client fingerprint is regenerated together.
func RandomizeCodexSimulation(sim *objects.CodexSimulationSettings) {
	if sim == nil {
		return
	}

	sim.Version = sim.EffectiveVersion()
	sim.Platform = objects.RandomCodexSimulationPlatform()
	sim.StandardUserAgent, sim.LiteUserAgent = objects.DefaultCodexSimulationUserAgents(sim.Version, sim.Platform)
	sim.Strategy = objects.NewCodexSimulationStrategy()
}

// ensureCodexSimulationStrategy preserves the persisted per-channel fingerprint when
// the update omits it. A complete submitted draft passes through for strict validation;
// an enabled channel with no persisted identity receives a fresh fallback.
func ensureCodexSimulationStrategy(settings *objects.ChannelSettings, existing *objects.ChannelSettings) {
	if settings == nil || settings.CodexSimulation == nil {
		return
	}

	sim := settings.CodexSimulation
	if !isZeroCodexSimulationStrategy(sim.Strategy) {
		return
	}

	if existing != nil && existing.CodexSimulation != nil &&
		IsValidCodexSimulationStrategy(existing.CodexSimulation.Strategy) {
		sim.Strategy = existing.CodexSimulation.Strategy

		return
	}

	if sim.Enabled {
		sim.Strategy = objects.NewCodexSimulationStrategy()
	}
}

// cloneChannelSettingsForCodexSimulation copies the settings and simulation sub-objects
// before normalization so update preparation never mutates an Ent-loaded entity.
func cloneChannelSettingsForCodexSimulation(settings *objects.ChannelSettings) *objects.ChannelSettings {
	if settings == nil {
		return &objects.ChannelSettings{}
	}

	cloned := *settings
	if settings.CodexSimulation != nil {
		sim := *settings.CodexSimulation
		if sim.Options != nil {
			options := *sim.Options
			sim.Options = &options
		}
		cloned.CodexSimulation = &sim
	}

	return &cloned
}

// prepareCodexSimulationUpdate applies the channel-type transition semantics and
// normalizes an incoming settings update. Any real type change disables simulation
// while retaining its preset, options, and stable fingerprint as a draft.
func prepareCodexSimulationUpdate(
	existingType channel.Type,
	nextType *channel.Type,
	existingSettings *objects.ChannelSettings,
	incomingSettings *objects.ChannelSettings,
) (*objects.ChannelSettings, error) {
	effectiveType := existingType
	typeChanged := false
	if nextType != nil {
		effectiveType = *nextType
		typeChanged = *nextType != existingType
	}

	settings := incomingSettings
	if typeChanged && ((existingSettings != nil && existingSettings.CodexSimulation != nil) ||
		(incomingSettings != nil && incomingSettings.CodexSimulation != nil)) {
		baseSettings := incomingSettings
		if baseSettings == nil {
			baseSettings = existingSettings
		}
		settings = cloneChannelSettingsForCodexSimulation(baseSettings)
		if settings.CodexSimulation == nil && existingSettings != nil && existingSettings.CodexSimulation != nil {
			existingSimulation := cloneChannelSettingsForCodexSimulation(existingSettings).CodexSimulation
			settings.CodexSimulation = existingSimulation
		}
		settings.CodexSimulation.Enabled = false
	}

	if settings == nil || settings.CodexSimulation == nil {
		return settings, nil
	}

	ensureCodexSimulationStrategy(settings, existingSettings)
	if err := NormalizeCodexSimulation(effectiveType, settings); err != nil {
		return nil, err
	}

	return settings, nil
}

// RandomizeChannelCodexSimulation rotates the complete fingerprint of an openai_responses
// channel, keeping the current preset, sub-options and version untouched.
func (svc *ChannelService) RandomizeChannelCodexSimulation(ctx context.Context, id int) (*ent.Channel, error) {
	return svc.updateChannelCodexSimulation(ctx, id, func(sim *objects.CodexSimulationSettings) error {
		RandomizeCodexSimulation(sim)

		return nil
	})
}

// updateChannelCodexSimulation loads the channel, applies the mutation to its simulation
// settings and persists the result. Non-target channel types are rejected.
func (svc *ChannelService) updateChannelCodexSimulation(
	ctx context.Context,
	id int,
	mutate func(*objects.CodexSimulationSettings) error,
) (*ent.Channel, error) {
	var updated *ent.Channel

	err := svc.RunInTransaction(ctx, func(ctx context.Context) error {
		db := svc.entFromContext(ctx)

		existing, err := db.Channel.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to load channel: %w", err)
		}

		if existing.Type != channel.TypeOpenaiResponses {
			return xerrors.ValidationError("codex simulation is only supported on openai_responses channels")
		}

		if existing.Settings == nil || existing.Settings.CodexSimulation == nil {
			return xerrors.ValidationError("codex simulation is not enabled on this channel")
		}

		sim := *existing.Settings.CodexSimulation
		if !sim.Enabled {
			return xerrors.ValidationError("codex simulation is not enabled on this channel")
		}
		if err := mutate(&sim); err != nil {
			return err
		}

		settings := *existing.Settings
		settings.CodexSimulation = &sim

		updated, err = db.Channel.UpdateOneID(id).
			Where(channel.UpdatedAtEQ(existing.UpdatedAt)).
			SetSettings(&settings).
			Save(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return fmt.Errorf("channel was updated concurrently; retry the randomization")
			}
			return fmt.Errorf("failed to update channel simulation settings: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if ent.TxFromContext(ctx) == nil {
		updated.Unwrap()
	}

	svc.reloadChannelsAfterCommit(ctx)

	return updated, nil
}
