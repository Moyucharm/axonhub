package biz

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

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

// cpaUsageForModel converts an aggregate into the llm.Usage that
// ComputeUsageCost prices. Claude's CPA payload reports a raw input count that
// excludes the cache tokens, so its prompt total is rebuilt from the read and
// write cache; every other provider keeps input-includes-cache semantics.
func cpaUsageForModel(provider string, aggregate tokenAggregate) *llm.Usage {
	promptTokens := aggregate.InputTokens
	cachedTokens := aggregate.CachedTokens
	writeCachedTokens := aggregate.CacheCreationTokens
	if strings.EqualFold(strings.TrimSpace(provider), "claude") {
		// The payload repeats the read cache in CachedTokens; the dedicated
		// counters are the billed values.
		cachedTokens = aggregate.CacheReadTokens
		promptTokens += cachedTokens + writeCachedTokens
	} else if aggregate.CacheReadTokens > cachedTokens {
		cachedTokens = aggregate.CacheReadTokens
	}
	// Clamp malformed provider details so the plain input price never goes
	// negative: the write cache is capped first, then the read remainder.
	if writeCachedTokens > promptTokens {
		writeCachedTokens = promptTokens
	}
	if cachedTokens > promptTokens-writeCachedTokens {
		cachedTokens = promptTokens - writeCachedTokens
	}
	usage := &llm.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: aggregate.OutputTokens,
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = aggregate.ReasoningTokens
	}
	usage.PromptTokensDetails = &llm.PromptTokensDetails{
		CachedTokens:      cachedTokens,
		WriteCachedTokens: writeCachedTokens,
	}
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
func computeCPAAggregateCost(index map[string]*objects.ModelPrice, model string, usage *llm.Usage, now time.Time) (decimal.Decimal, bool) {
	price, ok := index[normalizeCPAModelPriceKey(model)]
	if !ok || price == nil || len(price.Items) == 0 {
		return decimal.Zero, false
	}
	items, total := ComputeUsageCost(usage, *price, now)
	if len(items) == 0 && total.IsZero() {
		return decimal.Zero, false
	}
	return total, true
}
