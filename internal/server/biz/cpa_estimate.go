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
	// cpaWeeklyPeriodSeconds is the Codex 7d quota window. Primary and
	// secondary are wire slots; window duration determines weekly semantics.
	cpaWeeklyPeriodSeconds = 7 * 24 * 60 * 60
	// cpaEstimateMinPercentDelta is the minimum locally observed percentage
	// change before an interval estimate is considered reliable.
	cpaEstimateMinPercentDelta = 3.0
	// cpaMaxUnpricedRatio caps the tolerated share of tokens without a price.
	cpaMaxUnpricedRatio = 0.1
)

// CPAQuotaEstimate is the derived total quota value of a codex credential's
// current long-period (weekly or monthly) window.
type CPAQuotaEstimate struct {
	LimitUSD    float64
	CostUSD     float64
	UsedPercent float64
	// Source records the interval contract: "precise-header-delta" or "refresh-delta".
	Source string
}

// EstimateCredentialQuota estimates the credential's weekly/monthly quota total:
//
//	total = locally observed interval cost / percentage delta * 100
//
// It returns nil whenever prerequisites are missing: no complete local interval,
// percentage delta below 3%, no matching usage events, or too many unpriced
// tokens. Only Codex credentials carry such intervals, so callers gate on
// provider == "codex".
func (svc *CPAService) EstimateCredentialQuota(ctx context.Context, instanceID int, authIndex string, snapshot objects.CPAQuotaSnapshot, observed objects.CPAQuotaObserved) *CPAQuotaEstimate {
	item := estimateWindowItem(snapshot)
	return svc.estimateCredentialQuotaForItem(ctx, instanceID, authIndex, item, observed)
}

func (svc *CPAService) estimateCredentialQuotaForItem(ctx context.Context, instanceID int, authIndex string, item *objects.CPAQuotaItem, observed objects.CPAQuotaObserved) *CPAQuotaEstimate {
	if item == nil || item.ResetAt == nil || item.PeriodSeconds == nil {
		return nil
	}
	decision := codexEstimateIntervalDecisionForItem(item, observed)
	interval := decision.interval
	if interval == nil {
		log.Debug(ctx, "skip CPA quota estimate: incomplete interval",
			log.Int("cpa_instance_id", instanceID),
			log.String("auth_index", strings.TrimSpace(authIndex)),
			log.String("quota_item_id", item.ID),
			log.Int("period_seconds", *item.PeriodSeconds),
			log.String("reason", decision.skipReason),
			log.String("collector_session_id", observed.SecondaryCollectorSessionID),
			log.Any("baseline_used_percent", observed.SecondaryBaselineUsedPercent),
			log.Any("latest_used_percent", observed.SecondaryUsedPercent),
			log.Any("baseline_event_id", observed.SecondaryBaselineEventID),
			log.Any("latest_event_id", observed.SecondaryLatestEventID),
			log.Any("observed_reset_at", observed.SecondaryResetAt),
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
	items := make([]*objects.CPAQuotaItem, 0, 2)
	for i := range snapshot.Items {
		item := &snapshot.Items[i]
		if item.PeriodSeconds == nil || item.ResetAt == nil {
			continue
		}
		period := *item.PeriodSeconds
		if period == cpaWeeklyPeriodSeconds || isMonthlyQuotaPeriod(period) {
			items = append(items, item)
		}
	}
	return items
}

// estimateWindowItem preserves the legacy single-estimate preference: weekly
// first, otherwise the available monthly window.
func estimateWindowItem(snapshot objects.CPAQuotaSnapshot) *objects.CPAQuotaItem {
	var monthly *objects.CPAQuotaItem
	for _, item := range estimateWindowItems(snapshot) {
		if *item.PeriodSeconds == cpaWeeklyPeriodSeconds {
			return item
		}
		monthly = item
	}
	return monthly
}
