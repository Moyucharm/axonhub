package biz

import (
	"context"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
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

// autoResetCodexInstance consumes Codex reset cards that expire within the
// automatic window. It runs as part of the instance runtime cycle, right after
// the quota refresh phases, so the candidates come from the snapshots those
// refreshes just wrote instead of a separate per-minute sweep. Every candidate
// is still confirmed against the provider once before its consume claim, and
// failures skip only that card.
//
// The credential patrol owns the scheduled quota refresh that also reads reset
// credits, so auto use requires it: without the patrol the snapshots would go
// stale and the feature would act on outdated cards.
func (svc *CPAService) autoResetCodexInstance(ctx context.Context, instance *ent.CPAInstance) {
	if !instance.Enabled || !instance.AutoManageEnabled || !instance.AutoResetEnabled {
		return
	}
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().Where(
		cpacredential.CpaInstanceIDEQ(instance.ID), cpacredential.ProviderEQ("codex"),
		cpacredential.UnavailableEQ(false),
	).All(ctx)
	if err != nil {
		log.Warn(ctx, "CPA Codex auto reset credential query failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
		return
	}
	now := svc.now()
	deadline := now.Add(codexResetAutoWindow)
	var client cpaclient.ManagementClient
	defer func() {
		if client != nil {
			client.CloseIdleConnections()
		}
	}()
	for _, credential := range credentials {
		if strings.TrimSpace(credential.AuthIndex) == "" || strings.TrimSpace(credential.QuotaContext.CodexAccountID) == "" {
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
			if err := svc.consumeCodexResetCredit(ctx, client, credential, candidate.ID); err != nil {
				log.Warn(ctx, "CPA Codex auto reset attempt failed", log.Int("credential_id", credential.ID), log.Cause(err))
				continue
			}
			if outcome := svc.refreshCredentialOutcome(ctx, client, credential); outcome.err != nil {
				log.Warn(ctx, "CPA Codex auto reset quota refresh failed", log.Int("credential_id", credential.ID), log.Cause(outcome.err))
			}
		}
	}
}
