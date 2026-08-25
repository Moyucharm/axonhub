package biz

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

const cpaPriceIndexTTL = 5 * time.Minute

// tokenAggregate sums usage tokens of one model within a query window.
type tokenAggregate struct {
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func (a *tokenAggregate) totalTokens() int64 {
	return a.InputTokens + a.OutputTokens
}

// buildCPAPriceIndex snapshots model prices from all enabled channels. The
// same price table that bills axonhub traffic is reused so CPA quota value
// estimation stays consistent with local pricing (DRY). When several channels
// define the same model id the most recently updated price wins.
//
// Concurrency: GetEnabledChannels returns a point-in-time snapshot of channel
// pointers whose cachedModelPrices are treated as immutable after the live
// cache swap, so this read does not race with channel cache refresh. The
// priceIndex itself is guarded by priceIndexMu.
func (svc *CPAService) buildCPAPriceIndex(ctx context.Context) map[string]*objects.ModelPrice {
	svc.priceIndexMu.Lock()
	defer svc.priceIndexMu.Unlock()
	if svc.priceIndex != nil && time.Since(svc.priceIndexBuiltAt) < cpaPriceIndexTTL {
		return svc.priceIndex
	}

	winners := make(map[string]*ent.ChannelModelPrice)
	if svc.ChannelService != nil {
		for _, channel := range svc.ChannelService.GetEnabledChannels() {
			for modelID, cached := range channel.cachedModelPrices {
				if cached == nil || len(cached.Price.Items) == 0 {
					continue
				}
				if current, ok := winners[modelID]; ok && !cached.UpdatedAt.After(current.UpdatedAt) {
					continue
				}
				winners[modelID] = cached
			}
		}
	}
	index := make(map[string]*objects.ModelPrice, len(winners))
	for modelID, entry := range winners {
		key := normalizeCPAModelPriceKey(modelID)
		if key == "" {
			continue
		}
		price := entry.Price
		index[key] = &price
	}

	// CPA reports the actual upstream model, which may not be configured on an
	// enabled AxonHub channel. Fall back to the model catalog's reference cost
	// while preserving explicit channel prices as the highest-priority source.
	models, err := svc.entFromContext(ctx).Model.Query().All(ctx)
	if err != nil {
		log.Warn(ctx, "load model catalog for CPA price index failed", log.Cause(err))
		if svc.priceIndex != nil {
			return svc.priceIndex
		}
		return index
	}
	mergeCPACatalogPrices(index, models)
	svc.priceIndex = index
	svc.priceIndexBuiltAt = time.Now()
	return index
}

func normalizeCPAModelPriceKey(modelID string) string {
	return strings.ToLower(strings.TrimSpace(modelID))
}

func mergeCPACatalogPrices(index map[string]*objects.ModelPrice, models []*ent.Model) {
	for _, catalogModel := range models {
		modelID := normalizeCPAModelPriceKey(catalogModel.ModelID)
		if modelID == "" || index[modelID] != nil {
			continue
		}
		price, ok := modelCardToChannelModelPrice(catalogModel.ModelCard)
		if ok {
			index[modelID] = &price
		}
	}
}

// cpaUsageForModel converts an aggregate into llm.Usage following OpenAI
// semantics: prompt tokens include cached tokens; completion tokens include
// reasoning tokens.
func cpaUsageForModel(aggregate tokenAggregate) *llm.Usage {
	cached := aggregate.CachedTokens + aggregate.CacheReadTokens
	if cached > aggregate.InputTokens {
		cached = aggregate.InputTokens
	}
	usage := &llm.Usage{
		PromptTokens:     aggregate.InputTokens,
		CompletionTokens: aggregate.OutputTokens,
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = aggregate.ReasoningTokens
	}
	usage.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: cached}
	if aggregate.ReasoningTokens > 0 {
		usage.CompletionTokensDetails = &llm.CompletionTokensDetails{
			ReasoningTokens: aggregate.ReasoningTokens,
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

// computeCPAAggregateCost prices one model's aggregated tokens with the given
// price index. priced is false when the model has no configured price.
func computeCPAAggregateCost(index map[string]*objects.ModelPrice, model string, aggregate tokenAggregate, now time.Time) (decimal.Decimal, bool) {
	price, ok := index[normalizeCPAModelPriceKey(model)]
	if !ok || price == nil || len(price.Items) == 0 {
		return decimal.Zero, false
	}
	items, total := ComputeUsageCost(cpaUsageForModel(aggregate), *price, now)
	if len(items) == 0 && total.IsZero() {
		return decimal.Zero, false
	}
	return total, true
}
