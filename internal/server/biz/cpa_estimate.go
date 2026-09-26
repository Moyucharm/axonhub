package biz

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
)

const (
	cpaFiveHourPeriodSeconds = objects.CPAFiveHourPeriodSeconds
	cpaWeeklyPeriodSeconds   = objects.CPAWeeklyPeriodSeconds
	// cpaEstimateMinPercentDelta is the minimum locally observed percentage
	// change before an interval estimate is considered reliable.
	cpaEstimateMinPercentDelta = 3.0
	// cpaMaxUnpricedRatio caps the tolerated share of tokens without a price.
	cpaMaxUnpricedRatio = 0.1
)

func cpaEstimableQuotaPeriod(periodSeconds int) bool {
	return periodSeconds == cpaFiveHourPeriodSeconds || periodSeconds == cpaWeeklyPeriodSeconds || isMonthlyQuotaPeriod(periodSeconds)
}

// Only Codex reports per-request percentages; other providers use refresh snapshots.
func cpaPreciseWindowPeriod(provider string, periodSeconds int) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "codex") &&
		(periodSeconds == cpaFiveHourPeriodSeconds || periodSeconds == cpaWeeklyPeriodSeconds)
}

// Short windows permit a smaller delta above one integer rounding point.
func cpaEstimateMinPercentDeltaFor(periodSeconds int) float64 {
	if periodSeconds == cpaFiveHourPeriodSeconds {
		return 2.0
	}
	return cpaEstimateMinPercentDelta
}

// CPAQuotaEstimate is the derived total value of a credential's quota window.
type CPAQuotaEstimate struct {
	LimitUSD    float64
	CostUSD     float64
	UsedPercent float64
	// Source records the interval contract: "precise-header-delta" or "refresh-delta".
	Source string
}

// EstimateCredentialQuota estimates the credential's quota total from local
// interval cost divided by the percentage change, multiplied by 100.
func (svc *CPAService) EstimateCredentialQuota(ctx context.Context, instanceID int, authIndex string, snapshot objects.CPAQuotaSnapshot, observed objects.CPAQuotaObserved, provider string) *CPAQuotaEstimate {
	item := estimateWindowItem(snapshot)
	return svc.estimateCredentialQuotaForItem(ctx, instanceID, authIndex, item, observed, provider)
}

func (svc *CPAService) estimateCredentialQuotaForItem(ctx context.Context, instanceID int, authIndex string, item *objects.CPAQuotaItem, observed objects.CPAQuotaObserved, provider string) *CPAQuotaEstimate {
	if item == nil || item.ResetAt == nil || item.PeriodSeconds == nil {
		return nil
	}
	decision := codexEstimateIntervalDecisionForItem(item, observed, provider)
	interval := decision.interval
	if interval == nil {
		log.Debug(ctx, "skip CPA quota estimate: incomplete interval",
			log.Int("cpa_instance_id", instanceID),
			log.String("auth_index", strings.TrimSpace(authIndex)),
			log.String("quota_item_id", item.ID),
			log.Int("period_seconds", *item.PeriodSeconds),
			log.String("reason", decision.skipReason),
			log.Any("observed_windows", observed.ObservedWindows()),
			log.Any("quota_reset_at", item.ResetAt),
			log.String("refresh_collector_session_id", item.EstimateCollectorSessionID),
			log.Any("refresh_baseline_used_percent", item.EstimateBaselineUsedPercent),
			log.Any("refresh_baseline_event_id", item.EstimateBaselineEventID),
			log.Any("refresh_latest_event_id", item.EstimateLatestEventID),
		)
		return nil
	}
	cycleStart := item.ResetAt.Add(-time.Duration(*item.PeriodSeconds) * time.Second)

	ctx = authz.WithSystemBypass(ctx, "cpa-quota-estimate")
	aggregates, err := svc.usageRepository.usageAggregatesByModel(
		ctx,
		instanceID,
		strings.TrimSpace(authIndex),
		cycleStart,
		*item.ResetAt,
		interval.fromEventID,
		interval.toEventID,
	)
	if err != nil {
		log.Warn(ctx, "aggregate CPA usage events failed", log.Cause(err))
		return nil
	}
	if len(aggregates) == 0 {
		log.Debug(ctx, "skip CPA quota estimate: no matching usage events",
			log.Int("cpa_instance_id", instanceID),
			log.String("auth_index", strings.TrimSpace(authIndex)),
			log.String("quota_item_id", item.ID),
		)
		return nil
	}

	priceIndex := svc.pricingRepository.snapshot(ctx)
	now := svc.now()
	var (
		totalCost      decimal.Decimal
		pricedTokens   int64
		unpricedTokens int64
	)
	for model, aggregate := range aggregates {
		cost, priced := computeCPAAggregateCost(priceIndex, model, aggregate, now)
		if !priced {
			unpricedTokens += aggregate.totalTokens()
			log.Debug(ctx, "CPA quota estimate model has no price",
				log.String("model", model),
				log.Int64("tokens", aggregate.totalTokens()),
			)
			continue
		}
		totalCost = totalCost.Add(cost)
		pricedTokens += aggregate.totalTokens()
	}
	allTokens := pricedTokens + unpricedTokens
	if allTokens <= 0 || pricedTokens <= 0 {
		return nil
	}
	if float64(unpricedTokens)/float64(allTokens) > cpaMaxUnpricedRatio {
		log.Debug(ctx, "skip CPA quota estimate: too many unpriced tokens",
			log.Int64("priced", pricedTokens),
			log.Int64("unpriced", unpricedTokens),
		)
		return nil
	}

	costUSD, _ := totalCost.Float64()
	limitUSD := costUSD / interval.usedPercent * 100
	if math.IsNaN(limitUSD) || math.IsInf(limitUSD, 0) || limitUSD <= 0 {
		return nil
	}
	return &CPAQuotaEstimate{
		LimitUSD:    limitUSD,
		CostUSD:     costUSD,
		UsedPercent: interval.usedPercent,
		Source:      interval.source,
	}
}

func estimateWindowItems(snapshot objects.CPAQuotaSnapshot) []*objects.CPAQuotaItem {
	items := make([]*objects.CPAQuotaItem, 0, len(snapshot.Items))
	for i := range snapshot.Items {
		item := &snapshot.Items[i]
		if item.PeriodSeconds != nil && item.ResetAt != nil && cpaEstimableQuotaPeriod(*item.PeriodSeconds) {
			items = append(items, item)
		}
	}
	return items
}

// estimateWindowItem preserves the legacy single-estimate preference: weekly
// first, otherwise the last estimable window.
func estimateWindowItem(snapshot objects.CPAQuotaSnapshot) *objects.CPAQuotaItem {
	var candidate *objects.CPAQuotaItem
	for _, item := range estimateWindowItems(snapshot) {
		if *item.PeriodSeconds == cpaWeeklyPeriodSeconds {
			return item
		}
		candidate = item
	}
	return candidate
}
