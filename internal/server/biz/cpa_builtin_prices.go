package biz

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/objects"
)

type cpaBuiltinModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

//go:embed cpa_model_prices.json
var cpaBuiltinPriceData []byte

// cpaBuiltinPrices is immutable after package initialization. The generated
// snapshot only contains base prices that can be evaluated without request
// context, so tiered provider pricing is intentionally excluded.
var cpaBuiltinPrices = mustLoadCPABuiltinPrices()

func mustLoadCPABuiltinPrices() map[string]objects.ModelPrice {
	var costs map[string]cpaBuiltinModelCost
	if err := json.Unmarshal(cpaBuiltinPriceData, &costs); err != nil {
		panic(fmt.Errorf("load embedded CPA model prices: %w", err))
	}

	prices := make(map[string]objects.ModelPrice, len(costs))
	for modelID, cost := range costs {
		key := normalizeCPAModelPriceKey(modelID)
		if key == "" {
			panic("embedded CPA model price has an empty model ID")
		}
		if _, exists := prices[key]; exists {
			panic(fmt.Sprintf("embedded CPA model prices contain duplicate model ID: %s", key))
		}

		price, ok := modelCardToChannelModelPrice(&objects.ModelCard{Cost: objects.ModelCardCost{
			Input:      cost.Input,
			Output:     cost.Output,
			CacheRead:  cost.CacheRead,
			CacheWrite: cost.CacheWrite,
		}})
		if !ok {
			panic(fmt.Sprintf("embedded CPA model price has no usable cost: %s", key))
		}
		prices[key] = price
	}
	return prices
}
