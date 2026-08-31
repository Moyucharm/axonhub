package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
)

type channelAPIKeyStateMutation struct {
	credentials         *objects.ChannelCredentials
	disabledAPIKeys     *[]objects.DisabledAPIKey
	status              *channel.Status
	errorMessage        *string
	clearErrorMessage   bool
	autoDisabledAt      *time.Time
	clearAutoDisabledAt bool
	refreshLocalCache   bool
	suppressCacheReload bool
}

func (mutation *channelAPIKeyStateMutation) apply(update *ent.ChannelUpdateOne) *ent.ChannelUpdateOne {
	if mutation.credentials != nil {
		update.SetCredentials(*mutation.credentials)
	}
	if mutation.disabledAPIKeys != nil {
		update.SetDisabledAPIKeys(*mutation.disabledAPIKeys)
	}
	if mutation.status != nil {
		update.SetStatus(*mutation.status)
	}
	if mutation.errorMessage != nil {
		update.SetErrorMessage(*mutation.errorMessage)
	} else if mutation.clearErrorMessage {
		update.ClearErrorMessage()
	}
	if mutation.autoDisabledAt != nil {
		update.SetAutoDisabledAt(*mutation.autoDisabledAt)
	} else if mutation.clearAutoDisabledAt {
		update.ClearAutoDisabledAt()
	}
	return update
}

func shouldRecoverAPIKeyExhaustedChannel(
	ch *ent.Channel,
	credentials objects.ChannelCredentials,
	disabledKeys []objects.DisabledAPIKey,
) bool {
	return ch.Status == channel.StatusDisabled &&
		ch.ErrorMessage != nil &&
		strings.HasPrefix(*ch.ErrorMessage, allKeysDisabledErrorPrefix) &&
		len(credentials.GetEnabledCredentialRefs(disabledKeys)) > 0
}

func cloneChannelCredentials(credentials objects.ChannelCredentials) objects.ChannelCredentials {
	cloned := credentials
	cloned.APIKeys = slices.Clone(credentials.APIKeys)
	cloned.APIKeyStates = slices.Clone(credentials.APIKeyStates)
	return cloned
}

func cloneDisabledAPIKeys(disabled []objects.DisabledAPIKey) []objects.DisabledAPIKey {
	return slices.Clone(disabled)
}

func (svc *ChannelService) mutateChannelAPIKeyState(
	ctx context.Context,
	channelID int,
	mutate func(*ent.Channel) (*channelAPIKeyStateMutation, error),
) (bool, error) {
	lock := svc.channelAPIKeyOpsLock(channelID)
	lock.Lock()
	defer lock.Unlock()

	for attempt := 0; attempt < apiKeyStateUpdateMaxRetries; attempt++ {
		current, err := svc.entFromContext(ctx).Channel.Get(ctx, channelID)
		if err != nil {
			return false, fmt.Errorf("failed to get channel: %w", err)
		}

		mutation, err := mutate(current)
		if err != nil {
			return false, err
		}
		if mutation == nil {
			return false, nil
		}

		update := svc.entFromContext(ctx).Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(current.UpdatedAt))
		mutation.apply(update)
		if _, err := update.Save(ctx); err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("failed to persist channel API key state: %w", err)
		}

		if !mutation.suppressCacheReload {
			svc.reloadChannelAPIKeyStateAfterCommit(ctx, channelID, mutation.refreshLocalCache)
		}
		return true, nil
	}

	return false, fmt.Errorf("failed to persist channel API key state after %d retries", apiKeyStateUpdateMaxRetries)
}

func (svc *ChannelService) reloadChannelAPIKeyStateAfterCommit(ctx context.Context, channelID int, refreshLocalCache bool) {
	runAfterCommit(ctx, func(callbackCtx context.Context) {
		if refreshLocalCache {
			// The callback context may still carry the already committed Ent transaction
			// client. Cache reloads must always query through the base client.
			reloadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := svc.enabledChannelsCache.Load(reloadCtx, true); err != nil {
				log.Warn(callbackCtx, "Failed to synchronously reload channels after API key state change",
					log.Int("channel_id", channelID),
					log.Cause(err),
				)
			}
			cancel()
		}
		svc.asyncReloadChannels()
	})
}
