package biz

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
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

	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instance.ID),
			cpacredential.DisabledEQ(false),
		).
		All(ctx)
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
		if refreshErr := svc.refreshOneCredential(ctx, client, credential); refreshErr != nil {
			log.Warn(ctx, "CPA enabled patrol quota refresh failed",
				log.Int("cpa_instance_id", instance.ID),
				log.Int("credential_id", credential.ID),
				log.Cause(refreshErr),
			)
			continue
		}
		fresh, err := svc.entFromContext(ctx).CPACredential.Get(ctx, credential.ID)
		if err != nil {
			log.Warn(ctx, "CPA enabled patrol failed to reload credential",
				log.Int("cpa_instance_id", instance.ID),
				log.Int("credential_id", credential.ID),
				log.Cause(err),
			)
			continue
		}
		reason, exhaustedItem := cpaCredentialDisableReason(fresh, svc.now())
		if reason == "" {
			continue
		}
		if reason == "quota exhausted" && exhaustedItem != nil {
			log.Info(ctx, "CPA credential quota exhaustion confirmed",
				log.Int("cpa_instance_id", instance.ID),
				log.Int("credential_id", fresh.ID),
				log.String("quota_item_id", exhaustedItem.ID),
				log.Any("used_percent", exhaustedItem.UsedPercent),
				log.Any("remaining_percent", exhaustedItem.RemainingPercent),
				log.Any("reset_at", exhaustedItem.ResetAt),
			)
		}
		svc.disableCredentialRemotely(ctx, instance, fresh, reason)
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

	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instance.ID),
			cpacredential.DisabledEQ(true),
		).
		All(ctx)
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
	if _, err := svc.refreshCredentialBatch(ctx, instance, credentials, nil); err != nil {
		log.Warn(ctx, "CPA disabled patrol quota refresh failed to start",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}

	fresh, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instance.ID),
			cpacredential.DisabledEQ(true),
		).
		All(ctx)
	if err != nil {
		log.Warn(ctx, "CPA disabled patrol failed to reload credentials",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}

	for _, credential := range fresh {
		if !svc.cpaCredentialRecovered(credential, svc.now()) {
			continue
		}
		svc.enableCredentialRemotely(ctx, instance, credential)
	}
}

// cpaCredentialRecovered reports whether a refreshed disabled credential is
// ready to be re-enabled: the quota fetch succeeded (success or unsupported
// counts as usable), no quota window is exhausted, and it has not expired.
func (svc *CPAService) cpaCredentialRecovered(credential *ent.CPACredential, now time.Time) bool {
	if deriveCPAExpired(credential, now) {
		return false
	}
	state := objects.CPAQuotaState(credential.QuotaState)
	if state != objects.CPAQuotaStateSuccess && state != objects.CPAQuotaStateUnsupported {
		return false
	}
	cooling, _ := cpaQuotaCooldown(credential.QuotaData, now)
	return !cooling
}

func (svc *CPAService) disableCredentialRemotely(ctx context.Context, instance *ent.CPAInstance, credential *ent.CPACredential, reason string) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		log.Warn(ctx, "CPA auto-disable failed to build client",
			log.Int("cpa_instance_id", instance.ID),
			log.Int("credential_id", credential.ID),
			log.String("reason", reason),
			log.Cause(err),
		)
		return
	}
	defer client.CloseIdleConnections()

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
	svc.syncAfterPatch(ctx, instance)
}

func (svc *CPAService) enableCredentialRemotely(ctx context.Context, instance *ent.CPAInstance, credential *ent.CPACredential) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		log.Warn(ctx, "CPA auto-enable failed to build client",
			log.Int("cpa_instance_id", instance.ID),
			log.Int("credential_id", credential.ID),
			log.Cause(err),
		)
		return
	}
	defer client.CloseIdleConnections()

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
	svc.syncAfterPatch(ctx, instance)
}

// syncAfterPatch refreshes the local snapshot right after a successful remote
// toggle so the panel converges immediately instead of waiting for the next
// scheduled sync.
func (svc *CPAService) syncAfterPatch(ctx context.Context, instance *ent.CPAInstance) {
	if _, err := svc.syncInstanceCredentials(ctx, instance); err != nil {
		log.Warn(ctx, "CPA credential sync after toggle failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
	}
}
