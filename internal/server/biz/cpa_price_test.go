package biz

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func TestCPAUsageForModelMapsCacheTokensPerProvider(t *testing.T) {
	t.Parallel()
	claude := cpaUsageForModel("claude", tokenAggregate{InputTokens: 100, OutputTokens: 10, CachedTokens: 30, CacheReadTokens: 30, CacheCreationTokens: 20})
	require.Equal(t, int64(150), claude.PromptTokens)
	require.Equal(t, int64(30), claude.PromptTokensDetails.CachedTokens)
	require.Equal(t, int64(20), claude.PromptTokensDetails.WriteCachedTokens)

	// OpenAI-style payloads already include the read cache in the prompt count.
	openai := cpaUsageForModel("openai", tokenAggregate{InputTokens: 150, OutputTokens: 10, CachedTokens: 30, CacheReadTokens: 30})
	require.Equal(t, int64(150), openai.PromptTokens)
	require.Equal(t, int64(30), openai.PromptTokensDetails.CachedTokens)
	require.Zero(t, openai.PromptTokensDetails.WriteCachedTokens)

	clamped := cpaUsageForModel("openai", tokenAggregate{InputTokens: 10, CacheCreationTokens: 30})
	require.Equal(t, int64(10), clamped.PromptTokensDetails.WriteCachedTokens)
	require.Zero(t, clamped.PromptTokensDetails.CachedTokens)
}

func TestComputeCPAAggregateCostPricesCacheTokensSeparately(t *testing.T) {
	t.Parallel()
	perMillion := func(value int64) *decimal.Decimal { price := decimal.NewFromInt(value); return &price }
	index := map[string]*objects.ModelPrice{"claude-sonnet": {Items: []objects.ModelPriceItem{
		{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: perMillion(1_000_000)}},
		{ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: perMillion(2_000_000)}},
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: perMillion(3_000_000)}},
		{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: perMillion(4_000_000)}},
	}}}

	// Claude: 100 plain input + 30 read + 20 write + 10 output.
	claudeCost, priced := computeCPAAggregateCost(index, "claude-sonnet", cpaUsageForModel("claude", tokenAggregate{
		InputTokens: 100, OutputTokens: 10, CachedTokens: 30, CacheReadTokens: 30, CacheCreationTokens: 20,
	}), time.Now())
	require.True(t, priced)
	require.Equal(t, "260", claudeCost.String())

	// The read cache is priced once even though both read fields carry it.
	openaiCost, priced := computeCPAAggregateCost(index, "claude-sonnet", cpaUsageForModel("openai", tokenAggregate{
		InputTokens: 150, OutputTokens: 10, CachedTokens: 30, CacheReadTokens: 30,
	}), time.Now())
	require.True(t, priced)
	require.Equal(t, "220", openaiCost.String())
}

func TestBuildCPAPriceIndexKeepsStaleCacheWhenCatalogQueryFails(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_price_failure?mode=memory&_fk=1")
	require.NoError(t, client.Close())

	oldPrice := &objects.ModelPrice{Items: []objects.ModelPriceItem{{}}}
	oldIndex := map[string]*objects.ModelPrice{"gpt-5.2": oldPrice}
	svc := newCPAServiceForTest(client, time.Now)
	svc.pricingRepository.setForTest(oldIndex, time.Now().Add(-2*cpaPriceIndexTTL))

	got := svc.pricingRepository.snapshot(t.Context())
	require.Same(t, oldPrice, got["gpt-5.2"])
}

func TestMergeCPACatalogPricesPreservesCaseInsensitiveChannelPrice(t *testing.T) {
	channelPrice := &objects.ModelPrice{Items: []objects.ModelPriceItem{{}}}
	index := map[string]*objects.ModelPrice{normalizeCPAModelPriceKey(" GPT-5.2 "): channelPrice}
	mergeCPACatalogPrices(index, []*ent.Model{{
		ModelID: "gpt-5.2",
		ModelCard: &objects.ModelCard{Cost: objects.ModelCardCost{
			Input: 1,
		}},
	}})
	mergeCPABuiltinPrices(index)
	require.Same(t, channelPrice, index["gpt-5.2"])
}

func TestCPAPricingRepositoryIncludesBuiltinPrices(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_builtin_prices?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, time.Now)

	index := svc.pricingRepository.snapshot(ctx)
	for modelID, want := range map[string]map[objects.PriceItemCode]string{
		"gpt-5.6-sol": {
			objects.PriceItemCodeUsage:             "5",
			objects.PriceItemCodeCompletion:        "30",
			objects.PriceItemCodePromptCachedToken: "0.5",
			objects.PriceItemCodeWriteCachedTokens: "6.25",
		},
		"gpt-5.6-luna": {
			objects.PriceItemCodeUsage:             "0.2",
			objects.PriceItemCodeCompletion:        "1.2",
			objects.PriceItemCodePromptCachedToken: "0.02",
			objects.PriceItemCodeWriteCachedTokens: "0.25",
		},
	} {
		price, ok := index[normalizeCPAModelPriceKey("  "+modelID+"  ")]
		require.True(t, ok, "missing builtin price for %s", modelID)
		require.Len(t, price.Items, len(want))
		got := make(map[objects.PriceItemCode]string, len(price.Items))
		for _, item := range price.Items {
			require.NotNil(t, item.Pricing.UsagePerUnit)
			got[item.ItemCode] = item.Pricing.UsagePerUnit.String()
		}
		require.Equal(t, want, got)
	}
}

func TestCPAPricingRepositoryKeepsBuiltinPricesWithoutDatabaseCache(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_builtin_prices_db_failure?mode=memory&_fk=1")
	require.NoError(t, client.Close())
	svc := newCPAServiceForTest(client, time.Now)

	index := svc.pricingRepository.snapshot(t.Context())
	_, ok := index["gpt-5.6-sol"]
	require.True(t, ok)
	require.Nil(t, svc.pricingRepository.index)
	require.True(t, svc.pricingRepository.builtAt.IsZero())
}

func TestMergeCPAPricesPreservesDatabaseAndBuiltinPriority(t *testing.T) {
	index := make(map[string]*objects.ModelPrice)
	mergeCPACatalogPrices(index, []*ent.Model{{
		ModelID: " GPT-5.6-SOL ",
		ModelCard: &objects.ModelCard{Cost: objects.ModelCardCost{
			Input: 99,
		}},
	}})
	mergeCPABuiltinPrices(index)
	require.Equal(t, "99", index["gpt-5.6-sol"].Items[0].Pricing.UsagePerUnit.String())
}
