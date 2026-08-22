package biz

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpainstance"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/scheduler"
)

// CPA credential auto-manage patrols. The enabled patrol watches active
// credentials for quota exhaustion or expiry and disables them remotely; the
// disabled patrol re-checks disabled credentials and re-enables them once their
// quota recovered. Recovery is decided purely by patrol results, never by
// computing reset times.
const (
	cpaEnabledPatrolTaskName  = "cpa-enabled-patrol"
	cpaDisabledPatrolTaskName = "cpa-disabled-patrol"
)

// RegisterCPAPatrolTasks initializes patrol schedules and registers both patrol
// dispatchers with the scheduler service.
func (svc *CPAService) RegisterCPAPatrolTasks(ctx context.Context, schedulerService *scheduler.Scheduler) error {
	ctx = authz.WithSystemBypass(ctx, "cpa-patrol-register")
	now := svc.now()
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(
			cpainstance.EnabledEQ(true),
			cpainstance.AutoManageEnabledEQ(true),
			cpainstance.Or(
				cpainstance.NextEnabledPatrolAtIsNil(),
				cpainstance.NextDisabledPatrolAtIsNil(),
			),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("initialize CPA patrol schedule: %w", err)
	}
	for _, instance := range instances {
		update := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID)
		if instance.NextEnabledPatrolAt == nil {
			update.SetNextEnabledPatrolAt(now.Add(svc.jitter()))
		}
		if instance.NextDisabledPatrolAt == nil {
			update.SetNextDisabledPatrolAt(now.Add(svc.jitter()))
		}
		if err := update.Exec(ctx); err != nil {
			return fmt.Errorf("schedule CPA instance %d patrols: %w", instance.ID, err)
		}
	}

	if err := schedulerService.Register(ctx, scheduler.TaskSpec{
		Name:        cpaEnabledPatrolTaskName,
		Description: "Patrol enabled CPA credentials for quota exhaustion and expiry",
		FixRate:     cpaRefreshDispatcherInterval,
	}, svc.runEnabledPatrol); err != nil {
		return err
	}
	return schedulerService.Register(ctx, scheduler.TaskSpec{
		Name:        cpaDisabledPatrolTaskName,
		Description: "Patrol disabled CPA credentials for quota recovery",
		FixRate:     cpaRefreshDispatcherInterval,
	}, svc.runDisabledPatrol)
}

func (svc *CPAService) runEnabledPatrol(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, cpaEnabledPatrolTaskName)
	now := svc.now()
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(
			cpainstance.EnabledEQ(true),
			cpainstance.AutoManageEnabledEQ(true),
			cpainstance.Or(
				cpainstance.NextEnabledPatrolAtIsNil(),
				cpainstance.NextEnabledPatrolAtLTE(now),
			),
		).
		All(ctx)
	if err != nil {
		log.Error(ctx, "failed to query CPA instances for enabled patrol", log.Cause(err))
		return
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxCPAInstanceConcurrency)
	for _, instance := range instances {
		instance := instance
		group.Go(func() error {
			svc.patrolInstanceEnabled(groupCtx, instance)
			return nil
		})
	}
	_ = group.Wait()
}

func (svc *CPAService) runDisabledPatrol(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, cpaDisabledPatrolTaskName)
	now := svc.now()
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(
			cpainstance.EnabledEQ(true),
			cpainstance.AutoManageEnabledEQ(true),
			cpainstance.Or(
				cpainstance.NextDisabledPatrolAtIsNil(),
				cpainstance.NextDisabledPatrolAtLTE(now),
			),
		).
		All(ctx)
	if err != nil {
		log.Error(ctx, "failed to query CPA instances for disabled patrol", log.Cause(err))
		return
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxCPAInstanceConcurrency)
	for _, instance := range instances {
		instance := instance
		group.Go(func() error {
			svc.patrolInstanceDisabled(groupCtx, instance)
			return nil
		})
	}
	_ = group.Wait()
}

// patrolInstanceEnabled refreshes quotas of all enabled credentials and
// remotely disables any that expired or exhausted their quota.
func (svc *CPAService) patrolInstanceEnabled(ctx context.Context, instance *ent.CPAInstance) {
	next := svc.now().Add(time.Duration(instance.EnabledPatrolIntervalMinutes) * time.Minute)
	svc.scheduleNextEnabledPatrol(ctx, instance, next)

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
		// The disable decision is made purely from the refreshed snapshot so a
		// successful quota fetch overrides stale JWT subscription dates, matching
		// deriveCPAExpired's documented semantics.
		exhausted, _ := cpaQuotaCooldown(fresh.QuotaData, svc.now())
		expired := deriveCPAExpired(fresh, svc.now())
		if !expired && !exhausted {
			continue
		}
		reason := "quota exhausted"
		if expired {
			reason = "expired"
		}
		svc.disableCredentialRemotely(ctx, instance, fresh, reason)
	}
}

// patrolInstanceDisabled refreshes quotas of all disabled credentials and
// remotely re-enables those whose quota recovered and that are not expired.
func (svc *CPAService) patrolInstanceDisabled(ctx context.Context, instance *ent.CPAInstance) {
	next := svc.now().Add(time.Duration(instance.DisabledPatrolIntervalMinutes) * time.Minute)
	svc.scheduleNextDisabledPatrol(ctx, instance, next)

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
	if _, err := svc.refreshCredentialBatch(ctx, instance, credentials); err != nil {
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

func (svc *CPAService) scheduleNextEnabledPatrol(ctx context.Context, instance *ent.CPAInstance, next time.Time) {
	if !instance.Enabled || !instance.AutoManageEnabled {
		return
	}
	if err := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID).SetNextEnabledPatrolAt(next).Exec(ctx); err != nil {
		log.Warn(ctx, "failed to schedule CPA next enabled patrol",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
	}
}

func (svc *CPAService) scheduleNextDisabledPatrol(ctx context.Context, instance *ent.CPAInstance, next time.Time) {
	if !instance.Enabled || !instance.AutoManageEnabled {
		return
	}
	if err := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID).SetNextDisabledPatrolAt(next).Exec(ctx); err != nil {
		log.Warn(ctx, "failed to schedule CPA next disabled patrol",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
	}
}
