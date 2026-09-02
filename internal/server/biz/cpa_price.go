package biz

import (
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

func (a *tokenAggregate) totalTokens() int64 {
	return a.InputTokens + a.OutputTokens
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
