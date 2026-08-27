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
	"github.com/looplj/axonhub/internal/pkg/xcontext"
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

	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	// 读取 channel
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	// 检查 key 是否在 credentials 中。OAuth 渠道用固定的 OAuthCredentialRef 作为
	// 唯一凭证标识，所以这里按凭证引用而非明文 key 匹配。
	allKeys := ch.Credentials.GetAllCredentialRefs()

	found := slices.Contains(allKeys, key)
	if !found {
		// key 不在 credentials 中，忽略
		return nil
	}

	activeDisabledKeys := lo.Filter(ch.DisabledAPIKeys, func(dk objects.DisabledAPIKey, _ int) bool {
		return !dk.IsExpired()
	})

	disabled := lo.ContainsBy(activeDisabledKeys, func(dk objects.DisabledAPIKey) bool {
		return dk.Key == key
	})

	if disabled {
		// 已禁用，忽略
		return nil
	}

	// 追加到 disabled_api_keys
	disabledKey := objects.DisabledAPIKey{
		Key:        key,
		DisabledAt: time.Now(),
		ErrorCode:  errorCode,
		Reason:     reason,
	}
	for _, state := range ch.Credentials.APIKeyStates {
		if state.Key == key {
			disabledKey.FailureCount = state.FailureCount
			disabledKey.LastFailedAt = state.LastFailedAt
			break
		}
	}
	if len(expiresAt) > 0 {
		disabledKey.ExpiresAt = expiresAt[0]
	}

	newDisabledKeys := append(activeDisabledKeys, disabledKey)

	// 计算 enabled 凭证
	enabledKeys := ch.Credentials.GetEnabledCredentialRefs(newDisabledKeys)

	// 更新 channel
	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys)

	// 如果没有可用 key 了，禁用整个 channel
	channelDisabled := len(enabledKeys) == 0
	if channelDisabled {
		update.SetStatus(channel.StatusDisabled)
		update.SetErrorMessage(fmt.Sprintf("%s (last error: %d)", allKeysDisabledErrorPrefix, errorCode))
		update.SetAutoDisabledAt(time.Now())
		log.Warn(ctx, "Channel disabled because all API keys are disabled",
			log.Int("channel_id", channelID),
			log.String("channel_name", ch.Name),
		)
	}

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to disable api key: %w", err)
	}

	log.Info(ctx, "API key disabled",
		log.Int("channel_id", channelID),
		log.Int("error_code", errorCode),
	)

	if channelDisabled {
		// Synchronously reload the local cache to immediately stop selecting this channel.
		// This matches channel-level automatic disposition behavior.
		reloadCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
		defer cancel()

		if err := svc.enabledChannelsCache.Load(reloadCtx, true); err != nil {
			log.Warn(ctx, "Failed to synchronously reload channels after API key exhaustion",
				log.Int("channel_id", channelID),
				log.Cause(err),
			)
		}
	}

	// Also notify other instances via the watcher for cross-instance cache invalidation.
	svc.asyncReloadChannels()

	return nil
}

// EnableAPIKey 重新启用指定 key（从 disabled_api_keys 中移除）.
func (svc *ChannelService) EnableAPIKey(ctx context.Context, channelID int, key string) error {
	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	// 读取 channel
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	if len(ch.DisabledAPIKeys) == 0 {
		// 没有禁用的 key，忽略
		return nil
	}

	// 从 disabled_api_keys 中移除指定 key
	newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
	found := false

	for _, dk := range ch.DisabledAPIKeys {
		if dk.Key == key {
			found = true
			continue
		}

		newDisabledKeys = append(newDisabledKeys, dk)
	}

	if !found {
		// key 不在禁用列表中，忽略
		return nil
	}

	// 更新 channel
	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys)
	update = applyRecoveredChannelStatus(ctx, update, ch, ch.Credentials, newDisabledKeys)

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to enable api key: %w", err)
	}

	svc.asyncReloadChannels()

	return nil
}

// EnableAllAPIKeys 清空 disabled_api_keys.
func (svc *ChannelService) EnableAllAPIKeys(ctx context.Context, channelID int) error {
	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	// 读取 channel
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	if len(ch.DisabledAPIKeys) == 0 {
		// 没有禁用的 key，忽略
		return nil
	}

	// 更新 channel，清空 disabled_api_keys
	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{})
	update = applyRecoveredChannelStatus(ctx, update, ch, ch.Credentials, nil)

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to enable all api keys: %w", err)
	}

	log.Info(ctx, "All API keys enabled",
		log.Int("channel_id", channelID),
	)

	svc.asyncReloadChannels()

	return nil
}

// EnableSelectedAPIKeys re-enables multiple specific keys from disabled_api_keys.
func (svc *ChannelService) EnableSelectedAPIKeys(ctx context.Context, channelID int, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	if len(ch.DisabledAPIKeys) == 0 {
		return nil
	}

	keysToEnable := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keysToEnable[k] = struct{}{}
	}

	newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
	for _, dk := range ch.DisabledAPIKeys {
		if _, found := keysToEnable[dk.Key]; !found {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}

	if len(newDisabledKeys) == len(ch.DisabledAPIKeys) {
		return nil
	}

	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys)
	update = applyRecoveredChannelStatus(ctx, update, ch, ch.Credentials, newDisabledKeys)

	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("failed to enable selected api keys: %w", err)
	}

	log.Info(ctx, "Selected API keys enabled",
		log.Int("channel_id", channelID),
		log.Int("count", len(keys)),
	)

	svc.asyncReloadChannels()

	return nil
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

	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}
	if ch.Credentials.IsOAuth() {
		return nil, fmt.Errorf("cannot import API keys for OAuth channels")
	}

	credentials := ch.Credentials
	credentials.APIKeys = slices.Clone(credentials.GetAllAPIKeys())
	credentials.APIKey = ""
	credentials.Mode = objects.APIKeyModePool
	credentials.APIKeyStates = slices.Clone(credentials.APIKeyStates)
	existing := make(map[string]struct{}, len(credentials.APIKeys))
	for _, key := range credentials.APIKeys {
		existing[key] = struct{}{}
	}
	for _, key := range parsedKeys {
		if _, found := existing[key]; found {
			result.Ignored++
			continue
		}
		existing[key] = struct{}{}
		credentials.APIKeys = append(credentials.APIKeys, key)
		credentials.APIKeyStates = append(credentials.APIKeyStates, objects.ChannelAPIKeyState{Key: key})
		result.Added++
	}
	if result.Added == 0 {
		return result, nil
	}

	if _, err := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		Where(channel.UpdatedAtEQ(ch.UpdatedAt)).
		SetCredentials(credentials).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to import channel API keys: %w", err)
	}
	svc.asyncReloadChannels()
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
	if len(keys) == 0 {
		return &DeleteDisabledAPIKeysResult{Success: true}, nil
	}

	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}

	// Check if channel uses OAuth - cannot delete keys for OAuth channels
	if ch.Credentials.IsOAuth() {
		return nil, fmt.Errorf("cannot delete API keys for OAuth channels")
	}

	keysToDelete := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keysToDelete[k] = struct{}{}
	}

	// Remove from disabled_api_keys
	newDisabledKeys := make([]objects.DisabledAPIKey, 0, len(ch.DisabledAPIKeys))
	for _, dk := range ch.DisabledAPIKeys {
		if _, found := keysToDelete[dk.Key]; !found {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}

	// Remove from credentials
	newCredentials := ch.Credentials
	newCredentials.APIKeys = slices.Clone(newCredentials.APIKeys)
	newCredentials.APIKeyStates = slices.Clone(newCredentials.APIKeyStates)
	if len(newCredentials.APIKeys) > 0 {
		filteredKeys := make([]string, 0, len(newCredentials.APIKeys))
		for _, k := range newCredentials.APIKeys {
			if _, found := keysToDelete[k]; !found {
				filteredKeys = append(filteredKeys, k)
			}
		}

		newCredentials.APIKeys = filteredKeys
	}

	if newCredentials.APIKey != "" {
		if _, found := keysToDelete[newCredentials.APIKey]; found {
			newCredentials.APIKey = ""
		}
	}

	newCredentials.APIKeyStates = lo.Filter(newCredentials.APIKeyStates, func(state objects.ChannelAPIKeyState, _ int) bool {
		_, found := keysToDelete[state.Key]
		return !found
	})

	// Ensure at least one API key remains
	allKeys := newCredentials.GetAllAPIKeys()
	if len(allKeys) == 0 {
		// Restore at least one key from the keys being deleted
		// Prefer the first key that was supposed to be deleted
		restoredKey := keys[0]
		newCredentials.APIKeys = []string{restoredKey}
	}

	update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
		SetDisabledAPIKeys(newDisabledKeys).
		SetCredentials(newCredentials)
	update = applyRecoveredChannelStatus(ctx, update, ch, newCredentials, newDisabledKeys)

	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to delete disabled api keys: %w", err)
	}

	log.Info(ctx, "Disabled API keys deleted",
		log.Int("channel_id", channelID),
		log.Int("count", len(keys)),
	)

	// Check if we had to preserve a key
	result := &DeleteDisabledAPIKeysResult{Success: true}
	if len(allKeys) == 0 {
		result.Message = "ONE_KEY_PRESERVED"
	}

	svc.asyncReloadChannels()

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

	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	for attempt := 0; attempt < apiKeyStateUpdateMaxRetries; attempt++ {
		ch, getErr := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
		if getErr != nil {
			return 0, false, fmt.Errorf("failed to get channel: %w", getErr)
		}
		if !slices.Contains(ch.Credentials.GetAllCredentialRefs(), key) {
			return 0, false, nil
		}
		if lo.ContainsBy(ch.DisabledAPIKeys, func(disabled objects.DisabledAPIKey) bool {
			return disabled.Key == key && !disabled.IsExpired()
		}) {
			for _, state := range ch.Credentials.APIKeyStates {
				if state.Key == key {
					return state.FailureCount, false, nil
				}
			}
			return 0, false, nil
		}

		now := time.Now()
		credentials := ch.Credentials
		credentials.APIKeys = slices.Clone(credentials.APIKeys)
		credentials.APIKeyStates = slices.Clone(credentials.APIKeyStates)
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

		update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(ch.UpdatedAt)).
			SetCredentials(credentials)
		channelDisabled := acted && len(credentials.GetEnabledCredentialRefs(disabledKeys)) == 0
		if acted {
			update.SetDisabledAPIKeys(disabledKeys)
			if channelDisabled {
				update.SetStatus(channel.StatusDisabled).
					SetErrorMessage(fmt.Sprintf("%s (last error: %d)", allKeysDisabledErrorPrefix, errorCode)).
					SetAutoDisabledAt(now)
			}
		}

		if _, saveErr := update.Save(ctx); saveErr != nil {
			if ent.IsNotFound(saveErr) {
				continue
			}
			return 0, false, fmt.Errorf("failed to persist api key failure state: %w", saveErr)
		}

		if acted {
			if channelDisabled {
				reloadCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
				if loadErr := svc.enabledChannelsCache.Load(reloadCtx, true); loadErr != nil {
					log.Warn(ctx, "Failed to reload channels after API key pool exhaustion",
						log.Int("channel_id", channelID),
						log.Cause(loadErr),
					)
				}
				cancel()
			}
			svc.asyncReloadChannels()
		}
		return count, acted, nil
	}

	return 0, false, fmt.Errorf("failed to persist api key failure state after %d retries", apiKeyStateUpdateMaxRetries)
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

	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	for attempt := 0; attempt < apiKeyStateUpdateMaxRetries; attempt++ {
		ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get channel: %w", err)
		}
		if !slices.Contains(ch.Credentials.GetAllCredentialRefs(), key) {
			return nil
		}

		credentials := ch.Credentials
		credentials.APIKeyStates = slices.Clone(credentials.APIKeyStates)
		stateIndex := slices.IndexFunc(credentials.APIKeyStates, func(state objects.ChannelAPIKeyState) bool {
			return state.Key == key
		})
		if stateIndex < 0 || credentials.APIKeyStates[stateIndex].FailureCount == 0 {
			return nil
		}
		credentials.APIKeyStates[stateIndex].FailureCount = 0
		credentials.APIKeyStates[stateIndex].FailurePolicyKey = ""
		credentials.APIKeyStates[stateIndex].LastFailedAt = nil
		credentials.APIKeyStates[stateIndex].LastErrorCode = 0
		credentials.APIKeyStates[stateIndex].LastError = ""

		_, err = svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(ch.UpdatedAt)).
			SetCredentials(credentials).
			Save(ctx)
		if err == nil {
			return nil
		}
		if !ent.IsNotFound(err) {
			return fmt.Errorf("failed to reset api key failure state: %w", err)
		}
	}

	return fmt.Errorf("failed to reset api key failure state after %d retries", apiKeyStateUpdateMaxRetries)
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

func applyRecoveredChannelStatus(
	ctx context.Context,
	update *ent.ChannelUpdateOne,
	ch *ent.Channel,
	credentials objects.ChannelCredentials,
	disabledKeys []objects.DisabledAPIKey,
) *ent.ChannelUpdateOne {
	if ch.Status != channel.StatusDisabled || ch.ErrorMessage == nil ||
		!strings.HasPrefix(*ch.ErrorMessage, allKeysDisabledErrorPrefix) ||
		len(credentials.GetEnabledCredentialRefs(disabledKeys)) == 0 {
		return update
	}

	log.Info(ctx, "Re-enabled channel after API key availability recovered",
		log.Int("channel_id", ch.ID),
		log.String("channel_name", ch.Name),
	)

	return update.SetStatus(channel.StatusEnabled).ClearErrorMessage().ClearAutoDisabledAt()
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
		svc.asyncReloadChannels()
	}
}

func (svc *ChannelService) cleanupChannelExpiredDisabledAPIKeys(ctx context.Context, channelID int) (bool, int, error) {
	svc.apiKeyOpsLock.Lock()
	defer svc.apiKeyOpsLock.Unlock()

	entClient := svc.entFromContext(ctx)
	ch, err := entClient.Channel.Get(ctx, channelID)
	if err != nil {
		return false, 0, fmt.Errorf("failed to get channel: %w", err)
	}

	active := lo.Filter(ch.DisabledAPIKeys, func(dk objects.DisabledAPIKey, _ int) bool {
		return !dk.IsExpired()
	})
	removed := len(ch.DisabledAPIKeys) - len(active)
	if removed == 0 {
		return false, 0, nil
	}

	update := entClient.Channel.UpdateOneID(ch.ID).SetDisabledAPIKeys(active)
	update = applyRecoveredChannelStatus(ctx, update, ch, ch.Credentials, active)
	if _, err := update.Save(ctx); err != nil {
		return false, 0, fmt.Errorf("failed to update channel: %w", err)
	}

	return true, removed, nil
}
