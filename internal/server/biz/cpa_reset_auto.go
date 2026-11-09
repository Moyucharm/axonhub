package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// codexResetAutoWindow is the lead time within which an expiring card is used
// automatically.
const codexResetAutoWindow = 30 * time.Minute

// expiringCodexResetCredits selects the cards a refresh observed inside the
// automatic window, in expiry order. Cards without a parseable expiry stay
// manual-only and are never selected here.
func expiringCodexResetCredits(credits []objects.CPAQuotaResetCredit, now, deadline time.Time) []objects.CPAQuotaResetCredit {
	selected := make([]objects.CPAQuotaResetCredit, 0, len(credits))
	for _, credit := range credits {
		if credit.ExpiresAt == nil || !credit.ExpiresAt.After(now) || credit.ExpiresAt.After(deadline) {
			continue
		}
		selected = append(selected, credit)
	}
	return selected
}

// confirmExpiringCodexResetCredit re-reads the provider list so a snapshot that
// went stale can never authorize consuming a card that is gone or already
// outside the window.
func (svc *CPAService) confirmExpiringCodexResetCredit(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential, creditID string, deadline time.Time) (bool, error) {
	credits, err := svc.liveCodexResetCredits(ctx, client, credential)
	if err != nil {
		return false, err
	}
	now := svc.now()
	for _, credit := range credits {
		if credit.ID != creditID {
			continue
		}
		return credit.ExpiresAt != nil && credit.ExpiresAt.After(now) && !credit.ExpiresAt.After(deadline), nil
	}
	return false, nil
}

// autoResetCodexInstance only consumes candidates collected successfully this cycle.
// Replacement outcomes invalidate pre-reset snapshots if consumption or its
// follow-up refresh fails. Live confirmation and persistent claims remain mandatory.
func (svc *CPAService) autoResetCodexInstance(ctx context.Context, instance *ent.CPAInstance, credentials []*ent.CPACredential) (refreshed map[int]cpaQuotaExecutionOutcome) {
	refreshed = make(map[int]cpaQuotaExecutionOutcome)
	if !instance.Enabled || !instance.AutoRefreshEnabled || !instance.AutoResetEnabled {
		return
	}
	now := svc.now()
	deadline := now.Add(codexResetAutoWindow)
	var err error
	var client cpaclient.ManagementClient
	defer func() {
		if client != nil {
			client.CloseIdleConnections()
		}
	}()
	for _, credential := range credentials {
		if credential.Provider != "codex" || credential.Unavailable || credential.QuotaData.ResetCreditsFailed || credential.QuotaState != string(objects.CPAQuotaStateSuccess) || strings.TrimSpace(credential.AuthIndex) == "" || strings.TrimSpace(credential.QuotaContext.CodexAccountID) == "" {
			continue
		}
		candidates := expiringCodexResetCredits(credential.QuotaData.ResetCredits, now, deadline)
		if len(candidates) == 0 {
			continue
		}
		// Skip cards that already have a claim record: confirming them live
		// would only burn a request per cycle and end in a rejected claim.
		candidates, err = svc.unclaimedCodexResetCredits(ctx, credential.QuotaContext.CodexAccountID, candidates)
		if err != nil {
			log.Warn(ctx, "CPA Codex auto reset claim lookup failed", log.Int("credential_id", credential.ID), log.Cause(err))
			continue
		}
		if len(candidates) == 0 {
			continue
		}
		if client == nil {
			client, err = svc.clientForInstance(ctx, instance)
			if err != nil {
				log.Warn(ctx, "CPA Codex auto reset connection failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
				return
			}
		}
		for _, candidate := range candidates {
			confirmed, confirmErr := svc.confirmExpiringCodexResetCredit(ctx, client, credential, candidate.ID, deadline)
			if confirmErr != nil {
				log.Warn(ctx, "CPA Codex auto reset confirmation failed", log.Int("credential_id", credential.ID), log.Cause(confirmErr))
				continue
			}
			if !confirmed {
				continue
			}
			// Once consumption is attempted, old quota no longer authorizes a toggle.
			refreshed[credential.ID] = newCPAQuotaFailureOutcome(instance.ID, credential.ID, svc.now(), fmt.Errorf("Codex reset outcome requires a successful refresh"))
			if err := svc.consumeCodexResetCredit(ctx, client, credential, candidate.ID); err != nil {
				log.Warn(ctx, "CPA Codex auto reset attempt failed", log.Int("credential_id", credential.ID), log.Cause(err))
				break
			}
			outcome := svc.refreshCredentialOutcome(ctx, client, credential)
			refreshed[credential.ID] = outcome
			if outcome.err != nil {
				log.Warn(ctx, "CPA Codex auto reset quota refresh failed", log.Int("credential_id", credential.ID), log.Cause(outcome.err))
				break
			}
			if outcome.credential != nil {
				credential = outcome.credential
			}
		}
	}
	return
}
