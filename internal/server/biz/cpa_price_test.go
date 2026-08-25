package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func TestBuildCPAPriceIndexKeepsStaleCacheWhenCatalogQueryFails(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_price_failure?mode=memory&_fk=1")
	require.NoError(t, client.Close())

	oldPrice := &objects.ModelPrice{Items: []objects.ModelPriceItem{{}}}
	oldIndex := map[string]*objects.ModelPrice{"gpt-5.2": oldPrice}
	svc := &CPAService{
		AbstractService:   &AbstractService{db: client},
		priceIndex:        oldIndex,
		priceIndexBuiltAt: time.Now().Add(-2 * cpaPriceIndexTTL),
	}

	got := svc.buildCPAPriceIndex(t.Context())
	require.Same(t, oldPrice, got["gpt-5.2"])
	require.Equal(t, oldIndex, svc.priceIndex)
	require.True(t, time.Since(svc.priceIndexBuiltAt) >= cpaPriceIndexTTL)
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
	require.Same(t, channelPrice, index["gpt-5.2"])
}
