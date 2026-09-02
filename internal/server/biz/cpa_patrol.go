package biz

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// patrolInstanceEnabled refreshes quotas of all enabled credentials and
// remotely disables any that expired or exhausted a non-five-hour quota.
func (svc *CPAService) patrolInstanceEnabled(ctx context.Context, instance *ent.CPAInstance) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		log.Warn(ctx, "CPA enabled patrol failed to build client",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}
	defer client.CloseIdleConnections()

	credentials, err := svc.repository.patrolCandidates(ctx, instance.ID, false)
	if err != nil {
		log.Warn(ctx, "CPA enabled patrol failed to query credentials",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}

	for _, credential := range credentials {
		if credential.Unavailable || !svc.supportsStoredQuota(credential) {
			continue
		}
		// An Unsupported quota state was produced by a real fetch attempt, so
		// the remote cannot report quotas for this credential; retrying every
		// patrol would only burn requests. A later sync or manual refresh can
		// still flip the state back.
		if objects.CPAQuotaState(credential.QuotaState) == objects.CPAQuotaStateUnsupported {
			continue
		}
		outcome := svc.refreshCredentialOutcome(ctx, client, credential)
		if outcome.err != nil {
			log.Warn(ctx, "CPA enabled patrol quota refresh failed",
				log.Int("cpa_instance_id", instance.ID),
				log.Int("credential_id", credential.ID),
				log.Cause(outcome.err),
			)
			continue
		}
		fresh := outcome.credential
		if fresh == nil {
			continue
		}
		decision := decideCPAAutomation(fresh, svc.now())
		if decision.action != cpaAutomationDisable {
			continue
		}
		if decision.reason == "quota exhausted" && decision.exhaustedItem != nil {
			log.Info(ctx, "CPA credential quota exhaustion confirmed",
				log.Int("cpa_instance_id", instance.ID),
				log.Int("credential_id", fresh.ID),
				log.String("quota_item_id", decision.exhaustedItem.ID),
				log.Any("used_percent", decision.exhaustedItem.UsedPercent),
				log.Any("remaining_percent", decision.exhaustedItem.RemainingPercent),
				log.Any("reset_at", decision.exhaustedItem.ResetAt),
			)
		}
		svc.disableCredentialRemotely(ctx, client, instance, fresh, decision.reason)
	}
}

// patrolInstanceDisabled refreshes quotas of all disabled credentials and
// remotely re-enables those whose quota recovered and that are not expired.
func (svc *CPAService) patrolInstanceDisabled(ctx context.Context, instance *ent.CPAInstance) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		log.Warn(ctx, "CPA disabled patrol failed to build client",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}
	defer client.CloseIdleConnections()

	credentials, err := svc.repository.patrolCandidates(ctx, instance.ID, true)
	if err != nil {
		log.Warn(ctx, "CPA disabled patrol failed to query credentials",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}
	if len(credentials) == 0 {
		return
	}

	// Quota collection works through the api-call proxy regardless of the
	// disabled flag, so a straight batch refresh is enough here.
	_, outcomes, err := svc.refreshCredentialBatchOutcomesWithClient(ctx, instance, client, credentials, nil)
	if err != nil {
		log.Warn(ctx, "CPA disabled patrol quota refresh failed to start",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}

	for _, outcome := range outcomes {
		credential := outcome.credential
		if credential == nil || outcome.status == cpaQuotaExecutionFailure {
			continue
		}
		decision := decideCPAAutomation(credential, svc.now())
		if decision.action != cpaAutomationEnable {
			continue
		}
		svc.enableCredentialRemotely(ctx, client, instance, credential)
	}
}

// cpaCredentialRecovered reports whether a refreshed disabled credential is
// ready to be re-enabled: the quota fetch succeeded (success or unsupported
// counts as usable), no quota window is exhausted, and it has not expired.
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
// scheduled sync. The patrol-owned client is reused for this convergence read.
func (svc *CPAService) syncAfterPatch(ctx context.Context, client cpaclient.ManagementClient, instance *ent.CPAInstance) {
	if _, err := svc.syncInstanceCredentialsWithClient(ctx, instance, client); err != nil {
		log.Warn(ctx, "CPA credential sync after toggle failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
	}
}
