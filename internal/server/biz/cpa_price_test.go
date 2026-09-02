package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

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
