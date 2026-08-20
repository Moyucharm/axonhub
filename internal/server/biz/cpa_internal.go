package biz

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpainstance"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/scheduler"
)

// RegisterScheduledTasks registers the CPA refresh dispatcher.
func (svc *CPAService) RegisterScheduledTasks(ctx context.Context, schedulerService *scheduler.Scheduler) error {
	ctx = authz.WithSystemBypass(ctx, "cpa-refresh-register")
	now := svc.now()
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(
			cpainstance.EnabledEQ(true),
			cpainstance.AutoRefreshEnabledEQ(true),
			cpainstance.NextRefreshAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("initialize CPA refresh schedule: %w", err)
	}
	for _, instance := range instances {
		if err := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID).
			SetNextRefreshAt(now.Add(svc.jitter())).
			Exec(ctx); err != nil {
			return fmt.Errorf("schedule CPA instance %d: %w", instance.ID, err)
		}
	}

	return schedulerService.Register(ctx, scheduler.TaskSpec{
		Name:        "cpa-quota-refresh",
		Description: "Refresh CLIProxyAPI credential quota snapshots",
		FixRate:     cpaRefreshDispatcherInterval,
	}, svc.runScheduledRefresh)
}

func (svc *CPAService) runScheduledRefresh(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, "cpa-quota-refresh")
	now := svc.now()
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(
			cpainstance.EnabledEQ(true),
			cpainstance.AutoRefreshEnabledEQ(true),
			cpainstance.Or(
				cpainstance.NextRefreshAtIsNil(),
				cpainstance.NextRefreshAtLTE(now),
			),
		).
		All(ctx)
	if err != nil {
		log.Error(ctx, "failed to query due CPA instances", log.Cause(err))
		return
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, instance := range instances {
		instance := instance
		group.Go(func() error {
			svc.refreshScheduledInstance(groupCtx, instance)
			return nil
		})
	}
	_ = group.Wait()
}

func (svc *CPAService) refreshScheduledInstance(ctx context.Context, instance *ent.CPAInstance) {
	next := svc.now().Add(time.Duration(instance.RefreshIntervalMinutes) * time.Minute)
	svc.scheduleNextRefresh(ctx, instance, next)

	credentials, err := svc.syncInstanceCredentials(ctx, instance)
	if err != nil {
		log.Warn(ctx, "CPA credential sync failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}
	eligible := make([]*ent.CPACredential, 0, len(credentials))
	for _, credential := range credentials {
		if credential.Disabled || credential.Unavailable || !svc.supportsStoredQuota(credential) {
			continue
		}
		eligible = append(eligible, credential)
	}
	result, err := svc.refreshCredentialBatch(ctx, instance, eligible)
	if err != nil {
		log.Warn(ctx, "CPA quota refresh failed to start",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
		return
	}
	if result.Failed > 0 {
		log.Warn(ctx, "CPA quota refresh completed with failures",
			log.Int("cpa_instance_id", instance.ID),
			log.Int("requested", result.Requested),
			log.Int("failed", result.Failed),
		)
		return
	}
	log.Debug(ctx, "CPA quota refresh completed",
		log.Int("cpa_instance_id", instance.ID),
		log.Int("requested", result.Requested),
		log.Int("succeeded", result.Succeeded),
	)
}
