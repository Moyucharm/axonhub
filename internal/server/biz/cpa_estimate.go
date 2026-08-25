package biz

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/cpausageevent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
)

const (
	// cpaWeeklyPeriodSeconds is the codex weekly (secondary) quota window.
	cpaWeeklyPeriodSeconds = 7 * 24 * 60 * 60
	// cpaEstimateMinUsedPercent is the minimum consumed percentage of the
	// window before an estimation is considered reliable.
	cpaEstimateMinUsedPercent = 3.0
	// cpaMaxUnpricedRatio caps the tolerated share of tokens without a price.
	cpaMaxUnpricedRatio = 0.1
)

// CPAQuotaEstimate is the derived total quota value of a codex credential's
// current long-period (weekly or monthly) window.
type CPAQuotaEstimate struct {
	LimitUSD    float64
	CostUSD     float64
	UsedPercent float64
	// Source records the denominator precision: "precise-header" or "wham-percent".
	Source string
}

// EstimateCredentialQuota estimates the credential's weekly/monthly quota total:
//
//	total = cycle cost sum / used percent * 100
//
// It returns nil whenever prerequisites are missing: no long-period window item,
// used percent below 3%, unknown cycle start, no usage events, or too many
// unpriced tokens. Only codex credentials carry such windows, so callers
// gate on provider == "codex".
func (svc *CPAService) EstimateCredentialQuota(ctx context.Context, instanceID int, authIndex string, snapshot objects.CPAQuotaSnapshot, observed objects.CPAQuotaObserved) *CPAQuotaEstimate {
	item := estimateWindowItem(snapshot)
	return svc.estimateCredentialQuotaForItem(ctx, instanceID, authIndex, item, observed)
}

func (svc *CPAService) estimateCredentialQuotaForItem(ctx context.Context, instanceID int, authIndex string, item *objects.CPAQuotaItem, observed objects.CPAQuotaObserved) *CPAQuotaEstimate {
	if item == nil || item.UsedPercent == nil || item.ResetAt == nil || item.PeriodSeconds == nil {
		return nil
	}
	usedPercent := *item.UsedPercent
	if usedPercent < cpaEstimateMinUsedPercent {
		return nil
	}
	cycleStart := item.ResetAt.Add(-time.Duration(*item.PeriodSeconds) * time.Second)

	ctx = authz.WithSystemBypass(ctx, "cpa-quota-estimate")
	aggregates, err := svc.usageAggregatesByModel(ctx, instanceID, strings.TrimSpace(authIndex), cycleStart, *item.ResetAt)
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

	priceIndex := svc.buildCPAPriceIndex(ctx)
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

	denominator, source := estimateDenominator(item, observed, cycleStart)
	if denominator == nil {
		return nil
	}

	costUSD, _ := totalCost.Float64()
	limitUSD := costUSD / *denominator * 100
	if math.IsNaN(limitUSD) || math.IsInf(limitUSD, 0) || limitUSD <= 0 {
		return nil
	}
	return &CPAQuotaEstimate{
		LimitUSD:    limitUSD,
		CostUSD:     costUSD,
		UsedPercent: *denominator,
		Source:      source,
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
		if period == cpaWeeklyPeriodSeconds || (period >= 28*24*60*60 && period <= 31*24*60*60) {
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

// estimateDenominator prefers the precise percentage observed in upstream
// response headers; it falls back to the integer wham percentage. The precise
// value is only valid for the same cycle (matching reset time). When the
// observed reset timestamp is missing the header is not trusted and the
// integer wham percentage is used instead.
func estimateDenominator(item *objects.CPAQuotaItem, observed objects.CPAQuotaObserved, _ time.Time) (*float64, string) {
	if item.PeriodSeconds != nil && *item.PeriodSeconds == cpaWeeklyPeriodSeconds &&
		observed.SecondaryUsedPercent != nil && observed.ObservedAt != nil && *observed.SecondaryUsedPercent > 0 {
		if observed.SecondaryResetAt != nil && item.ResetAt != nil && observed.SecondaryResetAt.Unix() == item.ResetAt.Unix() {
			value := *observed.SecondaryUsedPercent
			return &value, "precise-header"
		}
	}
	if item.UsedPercent != nil && *item.UsedPercent > 0 {
		value := *item.UsedPercent
		return &value, "wham-percent"
	}
	return nil, ""
}

func (svc *CPAService) usageAggregatesByModel(ctx context.Context, instanceID int, authIndex string, from, to time.Time) (map[string]tokenAggregate, error) {
	var rows []struct {
		Model               string `json:"model"`
		InputTokens         int64  `json:"input_tokens"`
		OutputTokens        int64  `json:"output_tokens"`
		ReasoningTokens     int64  `json:"reasoning_tokens"`
		CachedTokens        int64  `json:"cached_tokens"`
		CacheReadTokens     int64  `json:"cache_read_tokens"`
		CacheCreationTokens int64  `json:"cache_creation_tokens"`
	}
	err := svc.entFromContext(ctx).CpaUsageEvent.Query().
		Where(
			cpausageevent.CpaInstanceIDEQ(instanceID),
			cpausageevent.AuthIndexEQ(authIndex),
			cpausageevent.FailedEQ(false),
			cpausageevent.RequestedAtGTE(from),
			cpausageevent.RequestedAtLTE(to),
		).
		Modify(func(s *sql.Selector) {
			s.Select(
				sql.As(s.C(cpausageevent.FieldModel), "model"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(cpausageevent.FieldInputTokens)), "input_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(cpausageevent.FieldOutputTokens)), "output_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(cpausageevent.FieldReasoningTokens)), "reasoning_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(cpausageevent.FieldCachedTokens)), "cached_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(cpausageevent.FieldCacheReadTokens)), "cache_read_tokens"),
				sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(cpausageevent.FieldCacheCreationTokens)), "cache_creation_tokens"),
			)
		}).
		GroupBy(cpausageevent.FieldModel).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("aggregate CPA usage events: %w", err)
	}
	result := make(map[string]tokenAggregate, len(rows))
	for _, row := range rows {
		result[row.Model] = tokenAggregate{
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			ReasoningTokens:     row.ReasoningTokens,
			CachedTokens:        row.CachedTokens,
			CacheReadTokens:     row.CacheReadTokens,
			CacheCreationTokens: row.CacheCreationTokens,
		}
	}
	return result, nil
}
