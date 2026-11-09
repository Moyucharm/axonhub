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

const cpaRuntimeTaskName = "cpa-runtime"

type cpaRuntimeJob struct {
	instance       *ent.CPAInstance
	refresh        bool
	enabledPatrol  bool
	disabledPatrol bool
}

// RegisterScheduledTasks initializes all CPA schedules and registers the single
// runtime dispatcher plus the usage-event retention task.
func (svc *CPAService) RegisterScheduledTasks(ctx context.Context, schedulerService *scheduler.Scheduler) error {
	ctx = authz.WithSystemBypass(ctx, "cpa-runtime-register")
	if err := svc.backfillCPACredentialProjections(ctx); err != nil {
		return err
	}
	if err := svc.ensureCPAUsageCollectorIdentities(ctx); err != nil {
		return err
	}
	if err := svc.initializeCPASchedules(ctx); err != nil {
		return err
	}
	if err := schedulerService.Register(ctx, scheduler.TaskSpec{
		Name:        cpaRuntimeTaskName,
		Description: "Dispatch CPA snapshot refresh and credential patrol operations",
		FixRate:     cpaRefreshDispatcherInterval,
	}, svc.runCPARuntime); err != nil {
		return err
	}
	return svc.RegisterUsageCleanupTask(ctx, schedulerService)
}

func (svc *CPAService) initializeCPASchedules(ctx context.Context) error {
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(cpainstance.EnabledEQ(true)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("initialize CPA runtime schedules: %w", err)
	}
	for _, instance := range instances {
		needsRefresh := instance.AutoRefreshEnabled && instance.NextRefreshAt == nil
		needsEnabledPatrol := instance.AutoRefreshEnabled && instance.NextEnabledPatrolAt == nil
		needsDisabledPatrol := instance.AutoRefreshEnabled && instance.NextDisabledPatrolAt == nil
		if !needsRefresh && !needsEnabledPatrol && !needsDisabledPatrol {
			continue
		}
		update := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID).
			Where(cpainstance.UpdatedAtEQ(instance.UpdatedAt))
		if needsRefresh {
			update.SetNextRefreshAt(svc.now().Add(svc.jitter()))
		}
		if needsEnabledPatrol {
			update.SetNextEnabledPatrolAt(svc.now().Add(svc.jitter()))
		}
		if needsDisabledPatrol {
			update.SetNextDisabledPatrolAt(svc.now().Add(svc.jitter()))
		}
		if err := update.Exec(ctx); err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("initialize CPA instance %d schedules: %w", instance.ID, err)
		}
	}
	return nil
}

func (svc *CPAService) runCPARuntime(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, cpaRuntimeTaskName)
	now := svc.now()
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Where(
			cpainstance.EnabledEQ(true),
			cpainstance.AutoRefreshEnabledEQ(true),
			cpainstance.Or(
				cpainstance.NextRefreshAtIsNil(),
				cpainstance.NextRefreshAtLTE(now),
				cpainstance.NextEnabledPatrolAtIsNil(),
				cpainstance.NextEnabledPatrolAtLTE(now),
				cpainstance.NextDisabledPatrolAtIsNil(),
				cpainstance.NextDisabledPatrolAtLTE(now),
			),
		).
		All(ctx)
	if err != nil {
		log.Error(ctx, "failed to query due CPA runtime instances", log.Cause(err))
		return
	}

	jobs := make([]cpaRuntimeJob, 0, len(instances))
	for _, instance := range instances {
		job, claimed, claimErr := svc.claimCPARuntimeInstance(ctx, instance, now)
		if claimErr != nil {
			log.Warn(ctx, "failed to claim CPA runtime instance",
				log.Int("cpa_instance_id", instance.ID),
				log.Cause(claimErr),
			)
			continue
		}
		if claimed {
			jobs = append(jobs, job)
		}
	}

	runCPARuntimeJobs(ctx, jobs, svc.executeCPARuntimeJob)
}

func runCPARuntimeJobs(ctx context.Context, jobs []cpaRuntimeJob, execute func(context.Context, cpaRuntimeJob)) {
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxCPAInstanceConcurrency)
	for _, job := range jobs {
		job := job
		group.Go(func() error {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Error(groupCtx, "CPA runtime job panicked",
						log.Int("cpa_instance_id", job.instance.ID),
						log.String("task", cpaRuntimeTaskName),
						log.Any("panic", recovered),
					)
				}
			}()
			execute(groupCtx, job)
			return nil
		})
	}
	_ = group.Wait()
}

// claimCPARuntimeInstance uses updated_at as an optimistic database lease. All
// operations that were due in the observed row are advanced by one update, so
// another process cannot split the same instance's due work across dispatchers.
func (svc *CPAService) claimCPARuntimeInstance(ctx context.Context, instance *ent.CPAInstance, now time.Time) (cpaRuntimeJob, bool, error) {
	job := cpaRuntimeJob{
		instance:       instance,
		refresh:        instance.AutoRefreshEnabled && dueCPATime(instance.NextRefreshAt, now),
		enabledPatrol:  instance.AutoRefreshEnabled && dueCPATime(instance.NextEnabledPatrolAt, now),
		disabledPatrol: instance.AutoRefreshEnabled && dueCPATime(instance.NextDisabledPatrolAt, now),
	}
	if !job.refresh && !job.enabledPatrol && !job.disabledPatrol {
		return cpaRuntimeJob{}, false, nil
	}
	update := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID).
		Where(
			cpainstance.UpdatedAtEQ(instance.UpdatedAt),
			cpainstance.EnabledEQ(true),
		)
	if job.refresh {
		update.SetNextRefreshAt(now.Add(time.Duration(instance.RefreshIntervalMinutes) * time.Minute))
	}
	if job.enabledPatrol {
		update.SetNextEnabledPatrolAt(now.Add(time.Duration(instance.EnabledPatrolIntervalMinutes) * time.Minute))
	}
	if job.disabledPatrol {
		update.SetNextDisabledPatrolAt(now.Add(time.Duration(instance.DisabledPatrolIntervalMinutes) * time.Minute))
	}
	if err := update.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return cpaRuntimeJob{}, false, nil
		}
		return cpaRuntimeJob{}, false, err
	}
	return job, true, nil
}

func dueCPATime(next *time.Time, now time.Time) bool {
	return next == nil || !next.After(now)
}

// executeCPARuntimeJob collects read-only snapshots before running optional writes.
func (svc *CPAService) executeCPARuntimeJob(ctx context.Context, job cpaRuntimeJob) {
	if !job.instance.Enabled || !job.instance.AutoRefreshEnabled {
		return
	}
	executeCPARuntimeOperations(
		job,
		func() {
			if _, err := svc.syncInstanceCredentials(ctx, job.instance); err != nil {
				log.Warn(ctx, "CPA scheduled credential sync failed",
					log.Int("cpa_instance_id", job.instance.ID), log.Cause(err))
			}
		},
		func() []cpaQuotaExecutionOutcome { return svc.patrolInstanceEnabled(ctx, job.instance) },
		func() []cpaQuotaExecutionOutcome { return svc.patrolInstanceDisabled(ctx, job.instance) },
		func(outcomes []cpaQuotaExecutionOutcome) {
			svc.automateRefreshedCredentials(ctx, job.instance, outcomes)
		},
	)
}

func executeCPARuntimeOperations(job cpaRuntimeJob, refresh func(), enabledPatrol, disabledPatrol func() []cpaQuotaExecutionOutcome, automate func([]cpaQuotaExecutionOutcome)) {
	if job.refresh {
		refresh()
	}
	var outcomes []cpaQuotaExecutionOutcome
	if job.enabledPatrol {
		outcomes = append(outcomes, enabledPatrol()...)
	}
	if job.disabledPatrol {
		outcomes = append(outcomes, disabledPatrol()...)
	}
	// An auth-files-only cycle never authorizes writes from old quota snapshots.
	if len(outcomes) > 0 && automate != nil {
		automate(outcomes)
	}
}
