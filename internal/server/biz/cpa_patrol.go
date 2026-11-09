package biz

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// patrolInstanceEnabled collects enabled credentials without changing remote status.
func (svc *CPAService) patrolInstanceEnabled(ctx context.Context, instance *ent.CPAInstance) []cpaQuotaExecutionOutcome {
	return svc.refreshPatrolCredentials(ctx, instance, false)
}

// patrolInstanceDisabled also collects disabled credentials through the api-call proxy.
func (svc *CPAService) patrolInstanceDisabled(ctx context.Context, instance *ent.CPAInstance) []cpaQuotaExecutionOutcome {
	return svc.refreshPatrolCredentials(ctx, instance, true)
}

func (svc *CPAService) refreshPatrolCredentials(ctx context.Context, instance *ent.CPAInstance, disabled bool) []cpaQuotaExecutionOutcome {
	if !instance.Enabled || !instance.AutoRefreshEnabled {
		return nil
	}
	credentials, err := svc.repository.patrolCandidates(ctx, instance.ID, disabled)
	if err != nil {
		log.Warn(ctx, "CPA refresh candidates query failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
		return nil
	}
	filtered := make([]*ent.CPACredential, 0, len(credentials))
	for _, credential := range credentials {
		if credential.Unavailable || !svc.supportsStoredQuota(credential) {
			continue
		}
		// Preserve the enabled patrol's unsupported-provider backoff.
		if !disabled && objects.CPAQuotaState(credential.QuotaState) == objects.CPAQuotaStateUnsupported {
			continue
		}
		filtered = append(filtered, credential)
	}
	if len(filtered) == 0 {
		return nil
	}
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		log.Warn(ctx, "CPA scheduled quota refresh connection failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
		return nil
	}
	defer client.CloseIdleConnections()
	_, outcomes, err := svc.refreshCredentialBatchOutcomesWithClient(ctx, instance, client, filtered, nil)
	if err != nil {
		log.Warn(ctx, "CPA scheduled quota refresh failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
	}
	for _, outcome := range outcomes {
		if outcome.err != nil {
			log.Warn(ctx, "CPA scheduled credential quota refresh failed",
				log.Int("cpa_instance_id", instance.ID), log.Int("credential_id", outcome.credentialID), log.Cause(outcome.err))
		}
	}
	return outcomes
}

// automateRefreshedCredentials never queries a stale instance-wide quota snapshot.
// Reset consumers return replacement outcomes so a failed post-reset refresh
// cannot authorize a status change using the pre-reset quota.
func (svc *CPAService) automateRefreshedCredentials(ctx context.Context, instance *ent.CPAInstance, outcomes []cpaQuotaExecutionOutcome) {
	if !instance.Enabled || !instance.AutoRefreshEnabled {
		return
	}
	credentials := make([]*ent.CPACredential, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.status == cpaQuotaExecutionSuccess && outcome.credential != nil && !outcome.credential.Unavailable {
			credentials = append(credentials, outcome.credential)
		}
	}
	codex := svc.autoResetCodexInstance(ctx, instance, credentials)
	claude := svc.autoResetClaudeInstance(ctx, instance, credentials)
	if !instance.AutoManageEnabled {
		return
	}
	var client cpaclient.ManagementClient
	defer func() {
		if client != nil {
			client.CloseIdleConnections()
		}
	}()
	for _, outcome := range outcomes {
		if replacement, ok := codex[outcome.credentialID]; ok {
			outcome = replacement
		}
		if replacement, ok := claude[outcome.credentialID]; ok {
			outcome = replacement
		}
		if outcome.status != cpaQuotaExecutionSuccess || outcome.credential == nil || outcome.credential.Unavailable {
			continue
		}
		credential := outcome.credential
		decision := decideCPAAutomation(credential, svc.now())
		if decision.action == cpaAutomationNone {
			continue
		}
		if client == nil {
			var err error
			client, err = svc.clientForInstance(ctx, instance)
			if err != nil {
				log.Warn(ctx, "CPA automatic status change connection failed", log.Int("credential_id", credential.ID), log.Cause(err))
				return
			}
		}
		svc.autoManageCredential(ctx, client, instance, credential, decision)
	}
}

func (svc *CPAService) autoManageCredential(ctx context.Context, client cpaclient.ManagementClient, instance *ent.CPAInstance, credential *ent.CPACredential, decision cpaAutomationDecision) {
	if decision.action == cpaAutomationEnable {
		svc.enableCredentialRemotely(ctx, client, instance, credential)
		return
	}
	if decision.reason == "quota exhausted" && decision.exhaustedItem != nil {
		log.Info(ctx, "CPA credential quota exhaustion confirmed",
			log.Int("cpa_instance_id", instance.ID), log.Int("credential_id", credential.ID),
			log.String("quota_item_id", decision.exhaustedItem.ID),
			log.Any("used_percent", decision.exhaustedItem.UsedPercent),
			log.Any("remaining_percent", decision.exhaustedItem.RemainingPercent),
			log.Any("reset_at", decision.exhaustedItem.ResetAt))
	}
	svc.disableCredentialRemotely(ctx, client, instance, credential, decision.reason)
}

// cpaCredentialRecovered reports whether a refreshed disabled credential is
// ready to be re-enabled: the quota fetch succeeded (success or unsupported
// counts as usable), no credential-wide quota cooldown remains, and it has
// not expired.
func cpaCredentialRecovered(credential *ent.CPACredential, now time.Time) bool {
	if deriveCPAExpired(credential) {
		return false
	}
	state := objects.CPAQuotaState(credential.QuotaState)
	if state != objects.CPAQuotaStateSuccess && state != objects.CPAQuotaStateUnsupported {
		return false
	}
	cooling, _ := cpaQuotaCooldown(credential.QuotaData, now)
	return !cooling
}

func (svc *CPAService) disableCredentialRemotely(ctx context.Context, client cpaclient.ManagementClient, instance *ent.CPAInstance, credential *ent.CPACredential, reason string) {
	if err := client.PatchAuthFileStatus(ctx, credential.RemoteName, credential.AuthIndex, true); err != nil {
		// Leave the credential untouched locally; the next patrol retries.
		log.Warn(ctx, "CPA auto-disable request failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Int("credential_id", credential.ID),
			log.String("reason", reason),
			log.Cause(err),
		)
		return
	}
	log.Info(ctx, "CPA credential auto-disabled",
		log.Int("cpa_instance_id", instance.ID),
		log.Int("credential_id", credential.ID),
		log.String("reason", reason),
	)
	svc.syncAfterPatch(ctx, client, instance)
}

func (svc *CPAService) enableCredentialRemotely(ctx context.Context, client cpaclient.ManagementClient, instance *ent.CPAInstance, credential *ent.CPACredential) {
	if err := client.PatchAuthFileStatus(ctx, credential.RemoteName, credential.AuthIndex, false); err != nil {
		log.Warn(ctx, "CPA auto-enable request failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Int("credential_id", credential.ID),
			log.Cause(err),
		)
		return
	}
	log.Info(ctx, "CPA credential auto-enabled",
		log.Int("cpa_instance_id", instance.ID),
		log.Int("credential_id", credential.ID),
	)
	svc.syncAfterPatch(ctx, client, instance)
}

// syncAfterPatch refreshes the local snapshot right after a successful remote
// toggle so the panel converges immediately instead of waiting for the next
// scheduled sync. The automation-owned client is reused for this convergence read.
func (svc *CPAService) syncAfterPatch(ctx context.Context, client cpaclient.ManagementClient, instance *ent.CPAInstance) {
	if _, err := svc.syncInstanceCredentialsWithClient(ctx, instance, client); err != nil {
		log.Warn(ctx, "CPA credential sync after toggle failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
	}
}
