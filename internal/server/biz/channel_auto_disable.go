package biz

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
)

var compiledAPIKeyRuleRegexes sync.Map

func (svc *ChannelService) markChannelUnavailable(ctx context.Context, channelID int, responseStatusCode int, threshold int, actualCount int) {
	ctx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Only disable channels that are currently enabled to avoid repeated disabling
	// of the same channel under sustained error traffic, which would keep resetting
	// the cache debounce timer and prevent the cache from ever refreshing.
	affected, err := svc.db.Channel.Update().
		Where(
			channel.ID(channelID),
			channel.StatusEQ(channel.StatusEnabled),
		).
		SetStatus(channel.StatusDisabled).
		SetErrorMessage(deriveErrorMessage(responseStatusCode)).
		Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to disable channel on unrecoverable error",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
			log.Cause(err),
		)

		return
	}

	if affected == 0 {
		log.Debug(ctx, "Channel already disabled, skipping",
			log.Int("channel_id", channelID),
			log.Int("error_code", responseStatusCode),
		)

		// Another instance may have already disabled the channel in DB while this
		// instance still serves it from a stale in-memory cache. Force a local
		// refresh so candidate selection stops using the channel immediately.
		if err := svc.enabledChannelsCache.Load(ctx, true); err != nil {
			log.Warn(ctx, "Failed to refresh local cache for already-disabled channel",
				log.Int("channel_id", channelID),
				log.Cause(err),
			)
		}

		return
	}

	log.Warn(ctx, "Channel disabled due to unrecoverable error",
		log.Int("channel_id", channelID),
		log.Int("error_code", responseStatusCode),
	)

	// Fetch the updated channel for webhook notification
	updatedChannel, err := svc.db.Channel.Get(ctx, channelID)
	if err != nil {
		log.Error(ctx, "Failed to fetch disabled channel for webhook notification",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	} else {
		notifyCtx := context.WithoutCancel(ctx)
		go svc.WebhookNotifier.NotifyChannelAutoDisabled(notifyCtx, ChannelAutoDisabledEvent{
			ChannelID:       updatedChannel.ID,
			ChannelName:     updatedChannel.Name,
			ChannelProvider: updatedChannel.Type.String(),
			ChannelBaseURL:  updatedChannel.BaseURL,
			ChannelStatus:   updatedChannel.Status.String(),
			StatusCode:      responseStatusCode,
			Threshold:       threshold,
			ActualCount:     actualCount,
			Reason:          deriveErrorMessage(responseStatusCode),
			OccurredAt:      time.Now(),
		})
	}

	// Synchronously reload the local cache to immediately stop selecting this channel.
	// This avoids the debounce delay that could keep the disabled channel in the candidate pool.
	if err := svc.enabledChannelsCache.Load(ctx, true); err != nil {
		log.Warn(ctx, "Failed to synchronously reload channels after auto-disable",
			log.Int("channel_id", channelID),
			log.Cause(err),
		)
	}

	// Also notify other instances via the watcher for cross-instance cache invalidation.
	svc.asyncReloadChannels()
}

// checkAndHandleChannelError checks if the channel should be disabled based on the error status code.
func (svc *ChannelService) checkAndHandleChannelError(ctx context.Context, perf *PerformanceRecord, policy *RetryPolicy) bool {
	if !policy.AutoDisableChannel.Enabled {
		return false
	}

	// "any" mode: count every failed request (no status code filter).
	// The special key 0 in channelErrorCounts holds the any-mode counter.
	if policy.AutoDisableChannel.Mode == AutoDisableModeAny {
		threshold := max(policy.AutoDisableChannel.Times, 1)

		svc.channelErrorCountsLock.Lock()
		if svc.channelErrorCounts[perf.ChannelID] == nil {
			svc.channelErrorCounts[perf.ChannelID] = make(map[int]int)
		}
		svc.channelErrorCounts[perf.ChannelID][0]++
		count := svc.channelErrorCounts[perf.ChannelID][0]
		svc.channelErrorCountsLock.Unlock()

		if count >= threshold {
			svc.markChannelUnavailable(ctx, perf.ChannelID, 0, threshold, count)
			svc.channelErrorCountsLock.Lock()
			delete(svc.channelErrorCounts, perf.ChannelID)
			svc.channelErrorCountsLock.Unlock()
			return true
		}
		return false
	}

	for _, statusConfig := range policy.AutoDisableChannel.Statuses {
		if statusConfig.Status != perf.ResponseStatusCode {
			continue
		}

		svc.channelErrorCountsLock.Lock()

		if svc.channelErrorCounts[perf.ChannelID] == nil {
			svc.channelErrorCounts[perf.ChannelID] = make(map[int]int)
		}

		svc.channelErrorCounts[perf.ChannelID][perf.ResponseStatusCode]++
		count := svc.channelErrorCounts[perf.ChannelID][perf.ResponseStatusCode]
		svc.channelErrorCountsLock.Unlock()

		if count >= statusConfig.Times {
			svc.markChannelUnavailable(ctx, perf.ChannelID, perf.ResponseStatusCode, statusConfig.Times, count)
			svc.channelErrorCountsLock.Lock()
			delete(svc.channelErrorCounts, perf.ChannelID)
			svc.channelErrorCountsLock.Unlock()

			return true
		}
	}

	return false
}

// checkAndHandleAPIKeyError persists per-key failure state and disables the key
// when the matching global threshold is reached. The global API key setting is
// used only when no channel-scoped API key rule matched the failure.
func (svc *ChannelService) checkAndHandleAPIKeyError(ctx context.Context, perf *PerformanceRecord, policy *RetryPolicy) bool {
	cfg := policy.AutoDisableAPIKey
	if !cfg.Enabled {
		return false
	}

	duration := time.Duration(cfg.DisableDurationMinutes) * time.Minute

	// "any" mode: every failed request counts toward a single threshold.
	if cfg.Mode == AutoDisableModeAny {
		threshold := max(cfg.Times, 1)
		reason := fmt.Sprintf("Auto-disabled after %d consecutive errors", threshold)
		_, acted, err := svc.recordAPIKeyFailure(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, perf.ErrorMessage, apiKeyFailurePolicy{
			Key:             "system:any",
			Threshold:       threshold,
			Reason:          reason,
			DisableDuration: duration,
		})
		if err != nil {
			log.Error(ctx, "Failed to record API key failure",
				log.Int("channel_id", perf.ChannelID),
				log.Int("error_code", perf.ResponseStatusCode),
				log.Cause(err),
			)
			return false
		}
		if acted {
			svc.apiKeyErrorCountsLock.Lock()
			delete(svc.apiKeyErrorCounts[perf.ChannelID], perf.APIKey)
			svc.apiKeyErrorCountsLock.Unlock()
		}
		return acted
	}

	for _, statusConfig := range cfg.Statuses {
		if statusConfig.Status != perf.ResponseStatusCode {
			continue
		}

		reason := fmt.Sprintf("Auto-disabled after %d consecutive errors with status %d", statusConfig.Times, perf.ResponseStatusCode)
		_, acted, err := svc.recordAPIKeyFailure(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, perf.ErrorMessage, apiKeyFailurePolicy{
			Key:             fmt.Sprintf("system:%d:%d", statusConfig.Status, statusConfig.Times),
			Threshold:       statusConfig.Times,
			Reason:          reason,
			DisableDuration: duration,
		})
		if err != nil {
			log.Error(ctx, "Failed to record API key failure",
				log.Int("channel_id", perf.ChannelID),
				log.Int("error_code", perf.ResponseStatusCode),
				log.Cause(err),
			)
			return false
		}
		if acted {
			svc.apiKeyErrorCountsLock.Lock()
			delete(svc.apiKeyErrorCounts[perf.ChannelID], perf.APIKey)
			svc.apiKeyErrorCountsLock.Unlock()
		}
		return acted
	}

	return false
}

// EvaluateAPIKeyRulesForFailure evaluates channel-scoped API key rules for a
// failure that was persisted outside the normal performance middleware path.
func (svc *ChannelService) EvaluateAPIKeyRulesForFailure(
	ctx context.Context,
	channelID int,
	apiKey string,
	responseStatusCode int,
	errorMessage string,
) bool {
	if channelID == 0 || apiKey == "" {
		return false
	}

	_, acted := svc.checkAndHandleChannelAPIKeyRules(ctx, &PerformanceRecord{
		ChannelID:          channelID,
		APIKey:             apiKey,
		ResponseStatusCode: responseStatusCode,
		ErrorMessage:       errorMessage,
	})
	return acted
}

// checkAndHandleChannelAPIKeyRules evaluates rules in declaration order. The
// first matching rule owns the failure so one request cannot increment several
// overlapping counters or execute multiple actions.
func (svc *ChannelService) checkAndHandleChannelAPIKeyRules(ctx context.Context, perf *PerformanceRecord) (matched, acted bool) {
	ch := svc.GetEnabledChannel(perf.ChannelID)
	if ch == nil || len(ch.Policies.APIKeyAutoDisableRules) == 0 {
		if err := svc.resetAPIKeyFailure(ctx, perf.ChannelID, perf.APIKey); err != nil {
			log.Warn(ctx, "Failed to reset API key failure streak without matching rules",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)
		}
		return false, false
	}

	for ruleIndex, rule := range ch.Policies.APIKeyAutoDisableRules {
		if !matchesAPIKeyRule(rule, perf) {
			continue
		}

		policyKey := apiKeyRuleCounterKey(perf.APIKey, ruleIndex, rule)
		if svc.apiKeyRuleActionInFlight(perf.ChannelID, policyKey) {
			svc.rememberFailedAPIKeyRuleAction(perf.ChannelID, policyKey)
			return true, false
		}

		failurePolicy := apiKeyFailurePolicy{
			Key:               policyKey,
			Threshold:         max(rule.Times, 1),
			DeleteOnThreshold: rule.Action == objects.APIKeyAutoDisableActionPermanent,
			Reason:            fmt.Sprintf("Disabled by channel API key rule after %d consecutive errors", max(rule.Times, 1)),
		}
		if rule.Action == objects.APIKeyAutoDisableActionTemporary && rule.DisableDurationMinutes != nil {
			failurePolicy.DisableDuration = time.Duration(*rule.DisableDurationMinutes) * time.Minute
			failurePolicy.Reason = fmt.Sprintf("Temporarily disabled for %d minutes by channel API key rule after %d consecutive errors", *rule.DisableDurationMinutes, max(rule.Times, 1))
		}

		_, acted, err := svc.recordAPIKeyFailure(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, perf.ErrorMessage, failurePolicy)
		if err != nil {
			svc.rememberFailedAPIKeyRuleAction(perf.ChannelID, policyKey)
			log.Error(ctx, "Failed to persist channel API key rule failure",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)
			return true, false
		}
		return true, acted
	}

	if err := svc.resetAPIKeyFailure(ctx, perf.ChannelID, perf.APIKey); err != nil {
		log.Warn(ctx, "Failed to reset API key failure streak after non-matching error",
			log.Int("channel_id", perf.ChannelID),
			log.Cause(err),
		)
	}
	return false, false
}

func (svc *ChannelService) apiKeyRuleActionInFlight(channelID int, ruleKey string) bool {
	svc.apiKeyErrorCountsLock.Lock()
	defer svc.apiKeyErrorCountsLock.Unlock()
	return svc.apiKeyRuleActionsInFlight[channelID] != nil && !svc.apiKeyRuleActionsInFlight[channelID][ruleKey] && func() bool {
		_, ok := svc.apiKeyRuleActionsInFlight[channelID][ruleKey]
		return ok
	}()
}

func (svc *ChannelService) rememberFailedAPIKeyRuleAction(channelID int, ruleKey string) {
	svc.apiKeyErrorCountsLock.Lock()
	defer svc.apiKeyErrorCountsLock.Unlock()
	if svc.apiKeyErrorCounts[channelID] == nil {
		svc.apiKeyErrorCounts[channelID] = make(map[string]map[int]int)
	}
	if svc.apiKeyErrorCounts[channelID][ruleKey] == nil {
		svc.apiKeyErrorCounts[channelID][ruleKey] = make(map[int]int)
	}
	svc.apiKeyErrorCounts[channelID][ruleKey][0] = 1
}

func (svc *ChannelService) clearAPIKeyRuleCounts(channelID int, rulePrefix string) {
	svc.apiKeyErrorCountsLock.Lock()
	defer svc.apiKeyErrorCountsLock.Unlock()

	for key := range svc.apiKeyErrorCounts[channelID] {
		if strings.HasPrefix(key, rulePrefix) {
			delete(svc.apiKeyErrorCounts[channelID], key)
		}
	}
	for key := range svc.apiKeyRuleActionsInFlight[channelID] {
		if strings.HasPrefix(key, rulePrefix) {
			delete(svc.apiKeyRuleActionsInFlight[channelID], key)
		}
	}
}

func apiKeyRuleCounterKey(apiKey string, ruleIndex int, rule objects.APIKeyAutoDisableRule) string {
	disableDurationMinutes := 0
	if rule.DisableDurationMinutes != nil {
		disableDurationMinutes = *rule.DisableDurationMinutes
	}

	return fmt.Sprintf(
		"%s:rule:%d:%v:%v:%d:%s:%d",
		apiKey,
		ruleIndex,
		rule.StatusCodes,
		rule.KeywordPatterns,
		rule.Times,
		rule.Action,
		disableDurationMinutes,
	)
}

func matchesAPIKeyRule(rule objects.APIKeyAutoDisableRule, perf *PerformanceRecord) bool {
	if len(rule.StatusCodes) > 0 && !slices.Contains(rule.StatusCodes, perf.ResponseStatusCode) {
		return false
	}
	if len(rule.KeywordPatterns) == 0 {
		return true
	}
	if perf.ErrorMessage == "" {
		return false
	}

	lowerMessage := strings.ToLower(perf.ErrorMessage)
	for _, pattern := range rule.KeywordPatterns {
		if re := compiledAPIKeyRuleRegex(pattern); re != nil {
			if re.MatchString(perf.ErrorMessage) {
				return true
			}
			continue
		}
		// Patterns may be plain keywords or regular expressions. Treat syntax
		// that is not a valid expression as a case-insensitive literal keyword.
		if strings.Contains(lowerMessage, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

func compiledAPIKeyRuleRegex(pattern string) *regexp.Regexp {
	cacheKey := "(?i)" + pattern
	if cached, ok := compiledAPIKeyRuleRegexes.Load(cacheKey); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}

	re, err := regexp.Compile(cacheKey)
	if err != nil {
		compiledAPIKeyRuleRegexes.Store(cacheKey, (*regexp.Regexp)(nil))
		return nil
	}
	compiledAPIKeyRuleRegexes.Store(cacheKey, re)
	return re
}

func (svc *ChannelService) executeAPIKeyRuleAction(
	ctx context.Context,
	perf *PerformanceRecord,
	rule objects.APIKeyAutoDisableRule,
	count int,
) bool {
	reason := fmt.Sprintf("Disabled by channel API key rule after %d consecutive errors", count)
	if rule.Action == objects.APIKeyAutoDisableActionPermanent {
		if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason); err != nil {
			log.Error(ctx, "Failed to permanently disable API key by channel rule",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)
			return false
		}
		result, err := svc.DeleteDisabledAPIKeys(ctx, perf.ChannelID, []string{perf.APIKey})
		if err != nil {
			log.Error(ctx, "Failed to delete API key disabled by channel rule",
				log.Int("channel_id", perf.ChannelID),
				log.Cause(err),
			)
			return false
		}

		// Channels must retain at least one credential. Keep that last key
		// permanently disabled when deletion cannot remove it, otherwise the
		// delete helper would make the rule a no-op by re-enabling the channel.
		if result.Message == "ONE_KEY_PRESERVED" {
			if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason); err != nil {
				log.Error(ctx, "Failed to keep preserved API key disabled by channel rule",
					log.Int("channel_id", perf.ChannelID),
					log.Cause(err),
				)
				return false
			}
		}
		return true
	}

	var expiresAt *time.Time
	if rule.DisableDurationMinutes != nil {
		disabledUntil := time.Now().Add(time.Duration(*rule.DisableDurationMinutes) * time.Minute)
		expiresAt = &disabledUntil
		reason = fmt.Sprintf("Temporarily disabled for %d minutes by channel API key rule after %d consecutive errors", *rule.DisableDurationMinutes, count)
	}

	if err := svc.DisableAPIKey(ctx, perf.ChannelID, perf.APIKey, perf.ResponseStatusCode, reason, expiresAt); err != nil {
		log.Error(ctx, "Failed to temporarily disable API key by channel rule",
			log.Int("channel_id", perf.ChannelID),
			log.Cause(err),
		)
		return false
	}

	return true
}
