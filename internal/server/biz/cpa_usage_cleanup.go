package biz

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/cpausageevent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/scheduler"
)

// cpaUsageEventRetention keeps events long enough to cover the longest quota
// window (monthly) plus a safety margin.
const cpaUsageEventRetention = 45 * 24 * time.Hour

// RegisterUsageCleanupTask schedules the daily purge of stale usage events.
func (svc *CPAService) RegisterUsageCleanupTask(ctx context.Context, sched *scheduler.Scheduler) error {
	return sched.Register(ctx, scheduler.TaskSpec{
		Name:        "cpa-usage-cleanup",
		Description: "Delete CPA usage events older than the retention window",
		CronExpr:    "30 4 * * *",
		Timezone:    "UTC",
	}, svc.cleanupUsageEvents)
}

func (svc *CPAService) cleanupUsageEvents(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, "cpa-usage-cleanup")
	cutoff := time.Now().UTC().Add(-cpaUsageEventRetention)
	deleted, err := svc.entFromContext(ctx).CpaUsageEvent.Delete().
		Where(cpausageevent.RequestedAtLT(cutoff)).
		Exec(ctx)
	if err != nil {
		log.Warn(ctx, "purge stale CPA usage events failed", log.Cause(err))
		return
	}
	if deleted > 0 {
		log.Info(ctx, "purged stale CPA usage events", log.Int("count", deleted))
	}
}
