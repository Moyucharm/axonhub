package biz

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aptible/supercronic/cronexpr"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
)

var compiledAPIKeyRuleRegexes sync.Map

type channelFailurePolicy struct {
	Key              string
	Threshold        int
	Action           objects.AutoDisableAction
	CooldownDuration time.Duration
}

const (
	channelAutoActionReasonMaxRunes = 512
	channelAutoActionNotifyTimeout  = 30 * time.Second
)

func (svc *ChannelService) checkAndHandleChannelError(ctx context.Context, perf *PerformanceRecord, policy *RetryPolicy) bool {
	if perf == nil || perf.ChannelID == 0 {
		return false
	}

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, perf.ChannelID)
	if err != nil {
		log.Warn(ctx, "Failed to load channel for auto-disable evaluation", log.Int("channel_id", perf.ChannelID), log.Cause(err))
		return false
	}

	if local, matched := channelFailurePolicyFromLocal(ch.Policies.ChannelAutoDisable, perf); matched {
		_, acted, err := svc.recordChannelFailure(ctx, ch.ID, perf, local)
		if err != nil {
			log.Error(ctx, "Failed to record channel-local failure", log.Int("channel_id", ch.ID), log.Cause(err))
		}
		return acted
	}

	if policy == nil || !policy.AutoDisableChannel.Enabled {
		return false
	}

	global, matched := channelFailurePolicyFromGlobal(policy.AutoDisableChannel, perf)
	if !matched {
		return false
	}
	_, acted, err := svc.recordChannelFailure(ctx, ch.ID, perf, global)
	if err != nil {
		log.Error(ctx, "Failed to record global channel failure", log.Int("channel_id", ch.ID), log.Cause(err))
	}
	return acted
}

func channelFailurePolicyFromLocal(cfg *objects.ChannelAutoDisablePolicy, perf *PerformanceRecord) (channelFailurePolicy, bool) {
	if cfg == nil {
		return channelFailurePolicy{}, false
	}

	action := cfg.Action
	if action == "" {
		action = objects.AutoDisableActionDisable
	}
	duration := time.Duration(cfg.CooldownDurationMinutes) * time.Minute
	if action != objects.AutoDisableActionCooldown {
		duration = 0
	}

	if cfg.Mode == AutoDisableModeAny {
		return channelFailurePolicy{
			Key:              fmt.Sprintf("channel:local:any:%d:%s:%d", cfg.Times, action, cfg.CooldownDurationMinutes),
			Threshold:        cfg.Times,
			Action:           action,
			CooldownDuration: duration,
		}, true
	}
	for _, status := range cfg.Statuses {
		if status.Status == perf.ResponseStatusCode {
			return channelFailurePolicy{
				Key:              fmt.Sprintf("channel:local:status:%d:%d:%s:%d", status.Status, status.Times, action, cfg.CooldownDurationMinutes),
				Threshold:        status.Times,
				Action:           action,
				CooldownDuration: duration,
			}, true
		}
	}
	return channelFailurePolicy{}, false
}

func channelFailurePolicyFromGlobal(cfg AutoDisableChannel, perf *PerformanceRecord) (channelFailurePolicy, bool) {
	action := cfg.Action
	if action == "" {
		action = objects.AutoDisableActionDisable
	}
	duration := time.Duration(cfg.CooldownDurationMinutes) * time.Minute
	if action != objects.AutoDisableActionCooldown {
		duration = 0
	}

	if cfg.Mode == AutoDisableModeAny {
		return channelFailurePolicy{
			Key:              fmt.Sprintf("channel:global:any:%d:%s:%d", cfg.Times, action, cfg.CooldownDurationMinutes),
			Threshold:        cfg.Times,
			Action:           action,
			CooldownDuration: duration,
		}, true
	}
	for _, status := range cfg.Statuses {
		if status.Status == perf.ResponseStatusCode {
			return channelFailurePolicy{
				Key:              fmt.Sprintf("channel:global:status:%d:%d:%s:%d", status.Status, status.Times, action, cfg.CooldownDurationMinutes),
				Threshold:        status.Times,
				Action:           action,
				CooldownDuration: duration,
			}, true
		}
	}
	return channelFailurePolicy{}, false
}

func (svc *ChannelService) recordChannelFailure(ctx context.Context, channelID int, perf *PerformanceRecord, policy channelFailurePolicy) (int, bool, error) {
	if policy.Threshold < 1 {
		return 0, false, nil
	}

	ch, count, acted, occurredAt, err := svc.persistChannelFailureLocked(ctx, channelID, perf, policy)
	if err != nil || !acted {
		return count, acted, err
	}

	svc.channelErrorCountsLock.Lock()
	delete(svc.channelErrorCounts, channelID)
	svc.channelErrorCountsLock.Unlock()

	if err := svc.enabledChannelsCache.Load(ctx, true); err != nil {
		log.Warn(ctx, "Failed to refresh channels after automatic action", log.Int("channel_id", channelID), log.Cause(err))
	}
	svc.asyncReloadChannels()
	svc.dispatchChannelAutoAction(ctx, ch, perf, policy, count, occurredAt)

	return count, true, nil
}

func (svc *ChannelService) persistChannelFailureLocked(
	ctx context.Context,
	channelID int,
	perf *PerformanceRecord,
	policy channelFailurePolicy,
) (*ent.Channel, int, bool, time.Time, error) {
	lock := svc.channelAutoDisableLock(channelID)
	lock.Lock()
	defer lock.Unlock()

	return svc.persistChannelFailure(ctx, channelID, perf, policy)
}

func (svc *ChannelService) persistChannelFailure(
	ctx context.Context,
	channelID int,
	perf *PerformanceRecord,
	policy channelFailurePolicy,
) (*ent.Channel, int, bool, time.Time, error) {
	for attempt := 0; attempt < apiKeyStateUpdateMaxRetries; attempt++ {
		ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
		if err != nil {
			return nil, 0, false, time.Time{}, fmt.Errorf("failed to get channel: %w", err)
		}

		now := time.Now()
		if ch.Status != channel.StatusEnabled || (ch.CooldownUntil != nil && ch.CooldownUntil.After(now)) {
			return ch, 0, false, now, nil
		}

		cooldownExpired := ch.CooldownUntil != nil
		state := ch.AutoDisableState
		if state.FailurePolicyKey != policy.Key {
			state.FailureCount = 0
			state.FailurePolicyKey = policy.Key
		}
		state.FailureCount++
		state.LastFailedAt = &now
		state.LastErrorCode = perf.ResponseStatusCode
		state.LastError = perf.ErrorMessage
		if state.LastError == "" {
			state.LastError = deriveErrorMessage(perf.ResponseStatusCode)
		}

		count := state.FailureCount
		acted := count >= policy.Threshold
		update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(ch.UpdatedAt))
		if cooldownExpired {
			update.ClearCooldownUntil()
		}
		if acted {
			state.FailureCount = 0
			state.FailurePolicyKey = ""
			switch policy.Action {
			case objects.AutoDisableActionCooldown:
				update.SetCooldownUntil(now.Add(policy.CooldownDuration))
			case objects.AutoDisableActionDisable:
				update.SetStatus(channel.StatusDisabled).
					SetErrorMessage(deriveErrorMessage(perf.ResponseStatusCode)).
					ClearCooldownUntil().
					SetAutoDisabledAt(now)
			default:
				return nil, 0, false, time.Time{}, fmt.Errorf("unsupported channel auto-disable action %q", policy.Action)
			}
		}
		update.SetAutoDisableState(state)
		if _, err := update.Save(ctx); err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return nil, 0, false, time.Time{}, fmt.Errorf("failed to persist channel failure state: %w", err)
		}

		return ch, count, acted, now, nil
	}

	return nil, 0, false, time.Time{}, fmt.Errorf("failed to persist channel failure state after %d retries", apiKeyStateUpdateMaxRetries)
}

func (svc *ChannelService) dispatchChannelAutoAction(
	ctx context.Context,
	ch *ent.Channel,
	perf *PerformanceRecord,
	policy channelFailurePolicy,
	actualCount int,
	occurredAt time.Time,
) {
	reason := summarizeChannelAutoActionReason(perf.ErrorMessage, perf.ResponseStatusCode)
	disabledEvent := ChannelAutoDisabledEvent{
		ChannelID:       ch.ID,
		ChannelName:     ch.Name,
		ChannelProvider: ch.Type.String(),
		ChannelBaseURL:  ch.BaseURL,
		ChannelStatus:   string(channel.StatusDisabled),
		StatusCode:      perf.ResponseStatusCode,
		Threshold:       policy.Threshold,
		ActualCount:     actualCount,
		Reason:          reason,
		OccurredAt:      occurredAt,
	}

	notifyCtx, cancel := xcontext.DetachWithTimeout(ctx, channelAutoActionNotifyTimeout)
	go func() {
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error(notifyCtx, "panic while notifying channel automatic action",
					log.Int("channel_id", ch.ID),
					log.Any("panic", recovered),
				)
			}
		}()

		if policy.Action == objects.AutoDisableActionCooldown {
			svc.WebhookNotifier.NotifyChannelAutoCooled(notifyCtx, ChannelAutoCooledEvent{
				ChannelID:       disabledEvent.ChannelID,
				ChannelName:     disabledEvent.ChannelName,
				ChannelProvider: disabledEvent.ChannelProvider,
				ChannelBaseURL:  disabledEvent.ChannelBaseURL,
				ChannelStatus:   string(channel.StatusEnabled),
				StatusCode:      disabledEvent.StatusCode,
				Threshold:       disabledEvent.Threshold,
				ActualCount:     disabledEvent.ActualCount,
				Reason:          disabledEvent.Reason,
				CooldownUntil:   occurredAt.Add(policy.CooldownDuration),
				OccurredAt:      disabledEvent.OccurredAt,
			})
			return
		}

		svc.WebhookNotifier.NotifyChannelAutoDisabled(notifyCtx, disabledEvent)
	}()
}

func summarizeChannelAutoActionReason(errorMessage string, statusCode int) string {
	reason := strings.TrimSpace(errorMessage)
	if firstLine, _, found := strings.Cut(reason, "\n"); found {
		reason = strings.TrimSpace(firstLine)
	}
	if reason == "" {
		reason = deriveErrorMessage(statusCode)
	}

	runes := []rune(reason)
	if len(runes) > channelAutoActionReasonMaxRunes {
		reason = string(runes[:channelAutoActionReasonMaxRunes])
	}
	return reason
}

func (svc *ChannelService) resetChannelFailure(ctx context.Context, channelID int) error {
	if svc.db == nil {
		return nil
	}

	lock := svc.channelAutoDisableLock(channelID)
	lock.Lock()
	defer lock.Unlock()

	for attempt := 0; attempt < apiKeyStateUpdateMaxRetries; attempt++ {
		ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
		if err != nil {
			return err
		}
		if ch.AutoDisableState.FailureCount == 0 && ch.AutoDisableState.FailurePolicyKey == "" {
			return nil
		}
		if _, err := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(ch.UpdatedAt)).
			SetAutoDisableState(objects.ChannelAutoDisableState{}).
			Save(ctx); err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("failed to reset channel failure state after %d retries", apiKeyStateUpdateMaxRetries)
}

func (svc *ChannelService) isAPIKeyPool(ctx context.Context, channelID int) bool {
	if svc.db == nil || channelID == 0 {
		return false
	}

	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
	return err == nil && ch.Credentials.IsAPIKeyPool()
}

// checkAndHandleAPIKeyError persists per-key failure state and disables the key
// when the matching global threshold is reached. The global API key setting is
// used only when no channel-scoped API key rule matched the failure.
func (svc *ChannelService) checkAndHandleAPIKeyError(ctx context.Context, perf *PerformanceRecord, policy *RetryPolicy) bool {
	if perf == nil || perf.APIKey == "" || policy == nil {
		return false
	}
	ch, err := svc.entFromContext(ctx).Channel.Get(ctx, perf.ChannelID)
	if err != nil || !ch.Credentials.IsAPIKeyPool() {
		return false
	}

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
	if perf == nil || perf.APIKey == "" {
		return false, false
	}

	return svc.checkAndHandleChannelAPIKeyRulesWithChannel(ctx, perf, svc.GetEnabledChannel(perf.ChannelID))
}

func (svc *ChannelService) checkAndHandleChannelAPIKeyRulesWithChannel(
	ctx context.Context,
	perf *PerformanceRecord,
	cachedChannel *Channel,
) (matched, acted bool) {
	if perf == nil || perf.APIKey == "" {
		return false, false
	}

	ch := cachedChannel
	if ch == nil && svc.db != nil {
		entity, err := svc.entFromContext(ctx).Channel.Get(ctx, perf.ChannelID)
		if err == nil {
			ch = &Channel{Channel: entity}
		}
	}
	if ch == nil || len(ch.Credentials.GetAllCredentialRefs()) == 0 || len(ch.Policies.APIKeyAutoDisableRules) == 0 {
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
			Key:       policyKey,
			Threshold: max(rule.Times, 1),
			Reason:    fmt.Sprintf("Disabled by channel API key rule after %d consecutive errors", max(rule.Times, 1)),
		}
		switch rule.Action {
		case objects.APIKeyAutoDisableActionTemporary:
			if rule.DisableDurationMinutes != nil {
				failurePolicy.DisableDuration = time.Duration(*rule.DisableDurationMinutes) * time.Minute
				failurePolicy.Reason = fmt.Sprintf("Temporarily disabled for %d minutes by channel API key rule after %d consecutive errors", *rule.DisableDurationMinutes, max(rule.Times, 1))
			}
		case objects.APIKeyAutoDisableActionUntilCron:
			expiresAt, err := nextAPIKeyRuleCronOccurrence(rule, time.Now())
			if err != nil {
				log.Error(ctx, "Failed to resolve API key rule cron schedule",
					log.Int("channel_id", perf.ChannelID),
					log.String("cron", rule.DisableUntilCron),
					log.Cause(err),
				)
				return true, false
			}
			failurePolicy.DisableUntil = &expiresAt
			failurePolicy.Reason = fmt.Sprintf("Disabled until %s by channel API key rule after %d consecutive errors", expiresAt.Format(time.RFC3339), max(rule.Times, 1))
		case objects.APIKeyAutoDisableActionPermanentDelete:
			// OAuth credentials cannot be removed from the channel; retaining the
			// sentinel as permanently disabled has the same observable result.
			failurePolicy.DeleteOnThreshold = perf.APIKey != objects.OAuthCredentialRef
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
		"%s:rule:%d:%v:%v:%d:%s:%d:%s:%s",
		apiKey,
		ruleIndex,
		rule.StatusCodes,
		rule.KeywordPatterns,
		rule.Times,
		rule.Action,
		disableDurationMinutes,
		rule.DisableUntilCron,
		rule.DisableUntilTimezone,
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

// nextAPIKeyRuleCronOccurrence returns the first cron occurrence strictly after
// the failure. The absolute instant is persisted with the disabled credential.
func nextAPIKeyRuleCronOccurrence(rule objects.APIKeyAutoDisableRule, now time.Time) (time.Time, error) {
	loc := time.UTC
	if rule.DisableUntilTimezone != "" {
		parsed, err := time.LoadLocation(rule.DisableUntilTimezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid timezone %q: %w", rule.DisableUntilTimezone, err)
		}
		loc = parsed
	}

	expr, err := cronexpr.Parse(rule.DisableUntilCron)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", rule.DisableUntilCron, err)
	}
	next := expr.Next(now.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron expression %q never fires again", rule.DisableUntilCron)
	}
	return next, nil
}
