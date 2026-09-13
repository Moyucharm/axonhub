package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
)

const (
	allKeysDisabledErrorPrefix  = "All API keys disabled"
	apiKeyStateUpdateMaxRetries = 5
)

type apiKeyFailurePolicy struct {
	Key               string
	Threshold         int
	DeleteOnThreshold bool
	DisableDuration   time.Duration
	DisableUntil      *time.Time
	Reason            string
}

// DisableAPIKey 禁用指定 key；若所有 key 都不可用则禁用 channel.
func (svc *ChannelService) DisableAPIKey(
	ctx context.Context,
	channelID int,
	key string,
	errorCode int,
	reason string,
	expiresAt ...*time.Time,
) error {
	if key == "" {
		return fmt.Errorf("api key cannot be empty")
	}

	var expiry *time.Time
	if len(expiresAt) > 0 {
		expiry = expiresAt[0]
	}
	_, err := svc.disableAPIKeys(ctx, channelID, []string{key}, errorCode, reason, expiry)
	return err
}

// DisableSelectedAPIKeys disables multiple keys in one channel state mutation.
func (svc *ChannelService) DisableSelectedAPIKeys(ctx context.Context, channelID int, keys []string) error {
	_, err := svc.disableAPIKeys(ctx, channelID, keys, 0, "Manually disabled by user", nil)
	return err
}

func (svc *ChannelService) disableAPIKeys(
	ctx context.Context,
	channelID int,
	keys []string,
	errorCode int,
	reason string,
	expiresAt *time.Time,
) (int, error) {
	keysToDisable := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key != "" {
			keysToDisable[key] = struct{}{}
		}
	}
	if len(keysToDisable) == 0 {
		return 0, nil
	}

	disabledCount := 0
	channelDisabled := false
	var disabledEvent *ChannelAutoDisabledEvent
	changed, err := svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		activeDisabledKeys := lo.Filter(ch.DisabledAPIKeys, func(dk objects.DisabledAPIKey, _ int) bool {
			return !dk.IsExpired()
		})
		disabledSet := make(map[string]struct{}, len(activeDisabledKeys))
		for _, disabled := range activeDisabledKeys {
			disabledSet[disabled.Key] = struct{}{}
		}

		now := time.Now()
		newDisabledKeys := cloneDisabledAPIKeys(activeDisabledKeys)
		disabledCount = 0
		for _, key := range ch.Credentials.GetAllCredentialRefs() {
			if _, requested := keysToDisable[key]; !requested {
				continue
			}
			if _, disabled := disabledSet[key]; disabled {
				continue
			}

			disabledKey := objects.DisabledAPIKey{
				Key:        key,
				DisabledAt: now,
				ErrorCode:  errorCode,
				Reason:     reason,
				ExpiresAt:  expiresAt,
			}
			for _, state := range ch.Credentials.APIKeyStates {
				if state.Key == key {
					disabledKey.FailureCount = state.FailureCount
					disabledKey.LastFailedAt = state.LastFailedAt
					break
				}
			}
			newDisabledKeys = append(newDisabledKeys, disabledKey)
			disabledSet[key] = struct{}{}
			disabledCount++
		}
		if disabledCount == 0 && len(activeDisabledKeys) == len(ch.DisabledAPIKeys) {
			return nil, nil
		}

		channelDisabled = len(ch.Credentials.GetEnabledCredentialRefs(newDisabledKeys)) == 0
		mutation := &channelAPIKeyStateMutation{disabledAPIKeys: &newDisabledKeys}
		if channelDisabled {
			status := channel.StatusDisabled
			errorMessage := fmt.Sprintf("%s (last error: %d)", allKeysDisabledErrorPrefix, errorCode)
			mutation.status = &status
			mutation.errorMessage = &errorMessage
			mutation.autoDisabledAt = &now
			mutation.refreshLocalCache = true
			disabledEvent = &ChannelAutoDisabledEvent{
				ChannelID:       ch.ID,
				ChannelName:     ch.Name,
				ChannelProvider: ch.Type.String(),
				ChannelBaseURL:  ch.BaseURL,
				ChannelStatus:   channel.StatusDisabled.String(),
				StatusCode:      errorCode,
				Reason:          errorMessage,
				OccurredAt:      now,
			}
		}
		return mutation, nil
	})
	if err != nil {
		return 0, err
	}
	if !changed {
		return 0, nil
	}

	log.Info(ctx, "API keys disabled",
		log.Int("channel_id", channelID),
		log.Int("error_code", errorCode),
		log.Int("count", disabledCount),
	)
	if channelDisabled {
		log.Warn(ctx, "Channel disabled because all API keys are disabled", log.Int("channel_id", channelID))
		if disabledEvent != nil {
			svc.asyncNotifyChannelAutoDisabled(ctx, *disabledEvent)
		}
	}
	return disabledCount, nil
}

// EnableAPIKey 重新启用指定 key（从 disabled_api_keys 中移除）.
func (svc *ChannelService) EnableAPIKey(ctx context.Context, channelID int, key string) error {
	return svc.EnableSelectedAPIKeys(ctx, channelID, []string{key})
}

// EnableAllAPIKeys 清空 disabled_api_keys.
func (svc *ChannelService) EnableAllAPIKeys(ctx context.Context, channelID int) error {
	return svc.enableAPIKeys(ctx, channelID, nil)
}

// EnableSelectedAPIKeys re-enables multiple specific keys from disabled_api_keys.
func (svc *ChannelService) EnableSelectedAPIKeys(ctx context.Context, channelID int, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	keysToEnable := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keysToEnable[key] = struct{}{}
	}
	return svc.enableAPIKeys(ctx, channelID, keysToEnable)
}

func (svc *ChannelService) enableAPIKeys(ctx context.Context, channelID int, keysToEnable map[string]struct{}) error {
	_, err := svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		if len(ch.DisabledAPIKeys) == 0 {
			return nil, nil
		}

		newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
		for _, disabled := range ch.DisabledAPIKeys {
			if keysToEnable != nil {
				if _, selected := keysToEnable[disabled.Key]; !selected {
					newDisabledKeys = append(newDisabledKeys, disabled)
				}
			}
		}
		if len(newDisabledKeys) == len(ch.DisabledAPIKeys) {
			return nil, nil
		}

		mutation := &channelAPIKeyStateMutation{disabledAPIKeys: &newDisabledKeys}
		if shouldRecoverAPIKeyExhaustedChannel(ch, ch.Credentials, newDisabledKeys) {
			status := channel.StatusEnabled
			mutation.status = &status
			mutation.clearErrorMessage = true
			mutation.clearAutoDisabledAt = true
			mutation.refreshLocalCache = true
		}
		return mutation, nil
	})
	return err
}

type ImportChannelAPIKeysResult struct {
	Added   int
	Ignored int
	Total   int
}

type ExportChannelAPIKeyStatus string

const (
	ExportChannelAPIKeyStatusAll      ExportChannelAPIKeyStatus = "all"
	ExportChannelAPIKeyStatusEnabled  ExportChannelAPIKeyStatus = "enabled"
	ExportChannelAPIKeyStatusDisabled ExportChannelAPIKeyStatus = "disabled"
)

// ImportChannelAPIKeys parses, deduplicates, and appends regular API keys.
func (svc *ChannelService) ImportChannelAPIKeys(ctx context.Context, channelID int, text string) (*ImportChannelAPIKeysResult, error) {
	parsedKeys := parseChannelAPIKeys(text)
	result := &ImportChannelAPIKeysResult{Total: len(parsedKeys)}
	if len(parsedKeys) == 0 {
		return result, nil
	}

	_, err := svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		if ch.Credentials.IsOAuth() {
			return nil, fmt.Errorf("cannot import API keys for OAuth channels")
		}

		credentials := cloneChannelCredentials(ch.Credentials)
		credentials.APIKeys = slices.Clone(credentials.GetAllAPIKeys())
		credentials.APIKey = ""
		credentials.Mode = objects.APIKeyModePool
		existing := make(map[string]struct{}, len(credentials.APIKeys))
		for _, key := range credentials.APIKeys {
			existing[key] = struct{}{}
		}

		added := 0
		ignored := 0
		for _, key := range parsedKeys {
			if _, found := existing[key]; found {
				ignored++
				continue
			}
			existing[key] = struct{}{}
			credentials.APIKeys = append(credentials.APIKeys, key)
			credentials.APIKeyStates = append(credentials.APIKeyStates, objects.ChannelAPIKeyState{Key: key})
			added++
		}
		result.Added = added
		result.Ignored = ignored
		if added == 0 {
			return nil, nil
		}
		mutation := &channelAPIKeyStateMutation{credentials: &credentials}
		if shouldRecoverAPIKeyExhaustedChannel(ch, credentials, ch.DisabledAPIKeys) {
			status := channel.StatusEnabled
			mutation.status = &status
			mutation.clearErrorMessage = true
			mutation.clearAutoDisabledAt = true
			mutation.refreshLocalCache = true
		}
		return mutation, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to import channel API keys: %w", err)
	}
	return result, nil
}

func parseChannelAPIKeys(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '\n', '\r', '\t', ',', ';':
			return true
		default:
			return false
		}
	})
	keys := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		key := strings.TrimSpace(field)
		if key == "" {
			continue
		}
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func (svc *ChannelService) ExportChannelAPIKeys(ctx context.Context, channelID int, status ExportChannelAPIKeyStatus) (string, error) {
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("failed to get channel: %w", err)
	}
	if ch.Credentials.IsOAuth() {
		return "", fmt.Errorf("cannot export API keys for OAuth channels")
	}

	disabled := make(map[string]struct{}, len(ch.DisabledAPIKeys))
	for _, entry := range ch.DisabledAPIKeys {
		if !entry.IsExpired() {
			disabled[entry.Key] = struct{}{}
		}
	}
	keys := lo.Filter(ch.Credentials.GetAllAPIKeys(), func(key string, _ int) bool {
		_, isDisabled := disabled[key]
		switch status {
		case ExportChannelAPIKeyStatusAll, "":
			return true
		case ExportChannelAPIKeyStatusEnabled:
			return !isDisabled
		case ExportChannelAPIKeyStatusDisabled:
			return isDisabled
		default:
			return false
		}
	})
	if status != "" && status != ExportChannelAPIKeyStatusAll && status != ExportChannelAPIKeyStatusEnabled && status != ExportChannelAPIKeyStatusDisabled {
		return "", fmt.Errorf("unsupported API key export status %q", status)
	}
	return strings.Join(keys, "\n"), nil
}

// RemoveChannelAPIKeys removes enabled or disabled keys while preserving one credential.
func (svc *ChannelService) RemoveChannelAPIKeys(ctx context.Context, channelID int, keys []string) (*DeleteDisabledAPIKeysResult, error) {
	return svc.DeleteDisabledAPIKeys(ctx, channelID, keys)
}

// DeleteDisabledAPIKeysResult is the result of deleting disabled API keys.
type DeleteDisabledAPIKeysResult struct {
	Success bool
	Message string
}

// DeleteDisabledAPIKeys removes disabled API keys from both disabled_api_keys list and credentials.
// It ensures at least one API key remains and prevents deletion for OAuth channels.
func (svc *ChannelService) DeleteDisabledAPIKeys(ctx context.Context, channelID int, keys []string) (*DeleteDisabledAPIKeysResult, error) {
	result := &DeleteDisabledAPIKeysResult{Success: true}
	if len(keys) == 0 {
		return result, nil
	}

	requested := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key != "" {
			requested[key] = struct{}{}
		}
	}
	if len(requested) == 0 {
		return result, nil
	}

	preserved := false
	removedCount := 0
	_, err := svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		if ch.Credentials.IsOAuth() {
			return nil, fmt.Errorf("cannot delete API keys for OAuth channels")
		}

		allKeys := ch.Credentials.GetAllAPIKeys()
		matched := make(map[string]struct{}, len(requested))
		for _, key := range allKeys {
			if _, found := requested[key]; found {
				matched[key] = struct{}{}
			}
		}
		if len(matched) == 0 {
			preserved = false
			removedCount = 0
			return nil, nil
		}

		keysToRemove := make(map[string]struct{}, len(matched))
		for key := range matched {
			keysToRemove[key] = struct{}{}
		}
		remaining := 0
		for _, key := range allKeys {
			if _, remove := keysToRemove[key]; !remove {
				remaining++
			}
		}
		preserved = remaining == 0
		if preserved {
			// Preserve a real key in channel order. Request-only values must never
			// become credentials.
			delete(keysToRemove, allKeys[0])
		}
		removedCount = len(keysToRemove)

		credentials := cloneChannelCredentials(ch.Credentials)
		if _, remove := keysToRemove[credentials.APIKey]; remove {
			credentials.APIKey = ""
		}
		credentials.APIKeys = lo.Reject(credentials.APIKeys, func(key string, _ int) bool {
			_, remove := keysToRemove[key]
			return remove
		})
		credentials.APIKeyStates = lo.Reject(credentials.APIKeyStates, func(state objects.ChannelAPIKeyState, _ int) bool {
			_, requestedKey := matched[state.Key]
			return requestedKey
		})

		// Every requested real key leaves the disabled list, including the one
		// preserved to satisfy the last-key compatibility contract.
		disabledKeys := lo.Reject(ch.DisabledAPIKeys, func(disabled objects.DisabledAPIKey, _ int) bool {
			_, requestedKey := matched[disabled.Key]
			return requestedKey
		})
		mutation := &channelAPIKeyStateMutation{
			credentials:     &credentials,
			disabledAPIKeys: &disabledKeys,
		}
		if shouldRecoverAPIKeyExhaustedChannel(ch, credentials, disabledKeys) {
			status := channel.StatusEnabled
			mutation.status = &status
			mutation.clearErrorMessage = true
			mutation.clearAutoDisabledAt = true
			mutation.refreshLocalCache = true
		}
		return mutation, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete channel API keys: %w", err)
	}
	if preserved {
		result.Message = "ONE_KEY_PRESERVED"
	}
	if removedCount > 0 {
		log.Info(ctx, "Channel API keys deleted",
			log.Int("channel_id", channelID),
			log.Int("count", removedCount),
		)
	}
	return result, nil
}

func (svc *ChannelService) recordAPIKeyFailure(
	ctx context.Context,
	channelID int,
	key string,
	errorCode int,
	errorMessage string,
	policy apiKeyFailurePolicy,
) (count int, acted bool, err error) {
	if key == "" || policy.Threshold < 1 {
		return 0, false, nil
	}

	_, err = svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		if !slices.Contains(ch.Credentials.GetAllCredentialRefs(), key) {
			count = 0
			acted = false
			return nil, nil
		}
		if lo.ContainsBy(ch.DisabledAPIKeys, func(disabled objects.DisabledAPIKey) bool {
			return disabled.Key == key && !disabled.IsExpired()
		}) {
			count = 0
			for _, state := range ch.Credentials.APIKeyStates {
				if state.Key == key {
					count = state.FailureCount
					break
				}
			}
			acted = false
			return nil, nil
		}

		now := time.Now()
		credentials := cloneChannelCredentials(ch.Credentials)
		stateIndex := slices.IndexFunc(credentials.APIKeyStates, func(state objects.ChannelAPIKeyState) bool {
			return state.Key == key
		})
		if stateIndex < 0 {
			credentials.APIKeyStates = append(credentials.APIKeyStates, objects.ChannelAPIKeyState{Key: key})
			stateIndex = len(credentials.APIKeyStates) - 1
		}
		state := &credentials.APIKeyStates[stateIndex]
		if state.FailurePolicyKey != policy.Key {
			state.FailureCount = 0
			state.FailurePolicyKey = policy.Key
		}
		state.FailureCount++
		state.LastFailedAt = &now
		state.LastErrorCode = errorCode
		state.LastError = errorMessage
		count = state.FailureCount

		disabledKeys := lo.Filter(ch.DisabledAPIKeys, func(disabled objects.DisabledAPIKey, _ int) bool {
			return !disabled.IsExpired()
		})
		acted = count >= policy.Threshold
		if acted && policy.DeleteOnThreshold && len(credentials.GetAllAPIKeys()) > 1 {
			credentials.APIKeys = lo.Reject(credentials.APIKeys, func(candidate string, _ int) bool {
				return candidate == key
			})
			if credentials.APIKey == key {
				credentials.APIKey = ""
			}
			credentials.APIKeyStates = lo.Reject(credentials.APIKeyStates, func(candidate objects.ChannelAPIKeyState, _ int) bool {
				return candidate.Key == key
			})
		} else if acted {
			disabled := objects.DisabledAPIKey{
				Key:          key,
				DisabledAt:   now,
				ErrorCode:    errorCode,
				Reason:       policy.Reason,
				FailureCount: count,
				LastFailedAt: &now,
			}
			if policy.DisableUntil != nil {
				expiresAt := *policy.DisableUntil
				disabled.ExpiresAt = &expiresAt
			} else if policy.DisableDuration > 0 {
				expiresAt := now.Add(policy.DisableDuration)
				disabled.ExpiresAt = &expiresAt
			}
			disabledKeys = append(disabledKeys, disabled)
		}

		mutation := &channelAPIKeyStateMutation{
			credentials:         &credentials,
			suppressCacheReload: !acted,
		}
		channelDisabled := acted && len(credentials.GetEnabledCredentialRefs(disabledKeys)) == 0
		if acted {
			mutation.disabledAPIKeys = &disabledKeys
			if channelDisabled {
				status := channel.StatusDisabled
				errorMessage := fmt.Sprintf("%s (last error: %d)", allKeysDisabledErrorPrefix, errorCode)
				mutation.status = &status
				mutation.errorMessage = &errorMessage
				mutation.autoDisabledAt = &now
				mutation.refreshLocalCache = true
			}
		}
		return mutation, nil
	})
	if err != nil {
		return 0, false, fmt.Errorf("failed to persist api key failure state: %w", err)
	}
	return count, acted, nil
}

// ResetAPIKeyFailure resets the consecutive failure streak for a channel API key.
// It is exposed for manual/automatic check paths so a successful probe clears the streak.
func (svc *ChannelService) ResetAPIKeyFailure(ctx context.Context, channelID int, key string) error {
	return svc.resetAPIKeyFailure(ctx, channelID, key)
}

func (svc *ChannelService) resetAPIKeyFailure(ctx context.Context, channelID int, key string) error {
	if key == "" || svc.db == nil {
		return nil
	}

	_, err := svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		if !slices.Contains(ch.Credentials.GetAllCredentialRefs(), key) {
			return nil, nil
		}

		credentials := cloneChannelCredentials(ch.Credentials)
		stateIndex := slices.IndexFunc(credentials.APIKeyStates, func(state objects.ChannelAPIKeyState) bool {
			return state.Key == key
		})
		if stateIndex < 0 || credentials.APIKeyStates[stateIndex].FailureCount == 0 {
			return nil, nil
		}
		credentials.APIKeyStates[stateIndex].FailureCount = 0
		credentials.APIKeyStates[stateIndex].FailurePolicyKey = ""
		credentials.APIKeyStates[stateIndex].LastFailedAt = nil
		credentials.APIKeyStates[stateIndex].LastErrorCode = 0
		credentials.APIKeyStates[stateIndex].LastError = ""
		return &channelAPIKeyStateMutation{
			credentials:         &credentials,
			suppressCacheReload: true,
		}, nil
	})
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to reset api key failure state: %w", err)
	}
	return nil
}

func (svc *ChannelService) CheckChannelAPIKeys(
	ctx context.Context,
	channelID int,
	status ExportChannelAPIKeyStatus,
) ([]ChannelAPIKeyCheckResult, error) {
	tester := svc.getAPIKeyTester()
	if tester == nil {
		return nil, fmt.Errorf("channel API key tester is not configured")
	}

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}
	keys, err := selectChannelAPIKeysByStatus(ch, status)
	if err != nil {
		return nil, err
	}
	concurrency, timeout := 4, 20*time.Second
	if ch.Settings != nil && ch.Settings.APIKeyPool != nil {
		if ch.Settings.APIKeyPool.AutoCheckConcurrency != nil {
			concurrency = *ch.Settings.APIKeyPool.AutoCheckConcurrency
		}
		if ch.Settings.APIKeyPool.AutoCheckTimeoutSeconds != nil {
			timeout = time.Duration(*ch.Settings.APIKeyPool.AutoCheckTimeoutSeconds) * time.Second
		}
	}
	checkCtx := contexts.WithSource(ctx, request.SourceTest)
	results := tester.CheckChannelAPIKeys(checkCtx, channelID, keys, concurrency, timeout)
	disabledSet := make(map[string]struct{}, len(ch.DisabledAPIKeys))
	for _, entry := range ch.DisabledAPIKeys {
		if !entry.IsExpired() {
			disabledSet[entry.Key] = struct{}{}
		}
	}
	for _, result := range results {
		if result.Success {
			if _, disabled := disabledSet[result.Key]; disabled {
				if err := svc.EnableAPIKey(ctx, channelID, result.Key); err != nil {
					log.Warn(ctx, "Failed to restore validated API key", log.Int("channel_id", channelID), log.Cause(err))
				}
			}
			// A successful probe means the key is healthy again — clear the streak
			// recorded by the request path so the panel shows a clean state.
			if err := svc.resetAPIKeyFailure(ctx, channelID, result.Key); err != nil {
				log.Warn(ctx, "Failed to reset API key failure streak after successful check",
					log.Int("channel_id", channelID),
					log.Cause(err),
				)
			}
		}
	}
	return results, nil
}

func selectChannelAPIKeysByStatus(ch *ent.Channel, status ExportChannelAPIKeyStatus) ([]string, error) {
	disabled := make(map[string]struct{}, len(ch.DisabledAPIKeys))
	for _, entry := range ch.DisabledAPIKeys {
		if !entry.IsExpired() {
			disabled[entry.Key] = struct{}{}
		}
	}
	if status != "" && status != ExportChannelAPIKeyStatusAll && status != ExportChannelAPIKeyStatusEnabled && status != ExportChannelAPIKeyStatusDisabled {
		return nil, fmt.Errorf("unsupported API key status %q", status)
	}
	return lo.Filter(ch.Credentials.GetAllAPIKeys(), func(key string, _ int) bool {
		_, isDisabled := disabled[key]
		switch status {
		case ExportChannelAPIKeyStatusDisabled:
			return isDisabled
		case ExportChannelAPIKeyStatusEnabled:
			return !isDisabled
		default:
			return true
		}
	}), nil
}

func (svc *ChannelService) runAPIKeyPoolAutoCheck(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, "channel-api-key-pool-auto-check")
	tester := svc.getAPIKeyTester()
	if tester == nil {
		return
	}

	channels, err := svc.entFromContext(ctx).Channel.Query().
		Where(channel.StatusIn(channel.StatusEnabled, channel.StatusDisabled)).
		All(ctx)
	if err != nil {
		log.Error(ctx, "Failed to query channels for API key pool auto check", log.Cause(err))
		return
	}

	now := time.Now()
	for _, ch := range channels {
		pool := ch.Settings
		if !ch.Credentials.IsAPIKeyPool() || pool == nil || pool.APIKeyPool == nil || !pool.APIKeyPool.AutoCheckEnabled {
			continue
		}
		intervalHours := 24
		if pool.APIKeyPool.AutoCheckIntervalHours != nil {
			intervalHours = *pool.APIKeyPool.AutoCheckIntervalHours
		}
		if pool.APIKeyPool.LastAutoCheckAt != nil && now.Sub(*pool.APIKeyPool.LastAutoCheckAt) < time.Duration(intervalHours)*time.Hour {
			continue
		}

		claimed, err := svc.claimAPIKeyPoolAutoCheck(ctx, ch, now)
		if err != nil {
			log.Warn(ctx, "Failed to claim channel API key pool auto check", log.Int("channel_id", ch.ID), log.Cause(err))
			continue
		}
		if !claimed {
			continue
		}

		if _, err := svc.CheckChannelAPIKeys(ctx, ch.ID, ExportChannelAPIKeyStatusDisabled); err != nil {
			log.Warn(ctx, "Failed to auto check channel API keys", log.Int("channel_id", ch.ID), log.Cause(err))
		}
	}
}

func (svc *ChannelService) claimAPIKeyPoolAutoCheck(ctx context.Context, ch *ent.Channel, now time.Time) (bool, error) {
	settings := *ch.Settings
	pool := *settings.APIKeyPool
	pool.LastAutoCheckAt = &now
	settings.APIKeyPool = &pool
	updated, err := svc.entFromContext(ctx).Channel.UpdateOneID(ch.ID).
		Where(channel.UpdatedAtEQ(ch.UpdatedAt)).
		SetSettings(&settings).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return updated != nil, nil
}

// cleanupExpiredDisabledAPIKeys prunes elapsed temporary disables and restores
// channels that were disabled only because all of their keys were unavailable.
func (svc *ChannelService) cleanupExpiredDisabledAPIKeys(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = authz.WithSystemBypass(ctx, "channel-cleanup-expired-disabled-api-keys")

	channelIDs, err := svc.entFromContext(ctx).Channel.Query().
		Where(channel.StatusIn(channel.StatusEnabled, channel.StatusDisabled)).
		IDs(ctx)
	if err != nil {
		log.Error(ctx, "Failed to query channels for expired API key cleanup", log.Cause(err))
		return
	}

	needsReload := false
	for _, channelID := range channelIDs {
		cleaned, removed, err := svc.cleanupChannelExpiredDisabledAPIKeys(ctx, channelID)
		if err != nil {
			log.Error(ctx, "Failed to cleanup expired disabled API keys",
				log.Int("channel_id", channelID),
				log.Cause(err),
			)
			continue
		}
		if !cleaned {
			continue
		}

		log.Info(ctx, "Cleaned up expired disabled API keys",
			log.Int("channel_id", channelID),
			log.Int("removed", removed),
		)
		needsReload = true
	}

	if needsReload {
		svc.reloadChannelsAfterCommit(ctx)
	}
}

func (svc *ChannelService) cleanupChannelExpiredDisabledAPIKeys(ctx context.Context, channelID int) (bool, int, error) {
	removed := 0
	changed, err := svc.mutateChannelAPIKeyState(ctx, channelID, func(ch *ent.Channel) (*channelAPIKeyStateMutation, error) {
		active := lo.Filter(ch.DisabledAPIKeys, func(dk objects.DisabledAPIKey, _ int) bool {
			return !dk.IsExpired()
		})
		removed = len(ch.DisabledAPIKeys) - len(active)
		if removed == 0 {
			return nil, nil
		}

		mutation := &channelAPIKeyStateMutation{
			disabledAPIKeys:     &active,
			suppressCacheReload: true,
		}
		if shouldRecoverAPIKeyExhaustedChannel(ch, ch.Credentials, active) {
			status := channel.StatusEnabled
			mutation.status = &status
			mutation.clearErrorMessage = true
			mutation.clearAutoDisabledAt = true
		}
		return mutation, nil
	})
	if err != nil {
		return false, 0, fmt.Errorf("failed to cleanup expired disabled API keys: %w", err)
	}
	return changed, removed, nil
}
