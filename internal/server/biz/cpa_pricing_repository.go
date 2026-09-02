package biz

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
)

// cpaPricingRepository owns the short-lived price index used by quota
// estimation. It snapshots already-cached channel prices and fills gaps from
// database and embedded model catalogs; it does not own either source or any
// billing writes.
type cpaPricingRepository struct {
	entFromContext     func(context.Context) *ent.Client
	getEnabledChannels func() []*Channel
	now                func() time.Time

	mu      sync.Mutex
	index   map[string]*objects.ModelPrice
	builtAt time.Time
}

func newCPAPricingRepository(db *ent.Client, getEnabledChannels func() []*Channel, now func() time.Time) *cpaPricingRepository {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &cpaPricingRepository{
		entFromContext: func(ctx context.Context) *ent.Client {
			if client := ent.FromContext(ctx); client != nil {
				return client
			}
			return db
		},
		getEnabledChannels: getEnabledChannels,
		now:                now,
	}
}

// snapshot returns the repository-owned index. Callers must treat the map and
// its values as immutable until the next snapshot rebuild.
func (repository *cpaPricingRepository) snapshot(ctx context.Context) map[string]*objects.ModelPrice {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.index != nil && repository.now().Sub(repository.builtAt) < cpaPriceIndexTTL {
		return repository.index
	}

	winners := make(map[string]*ent.ChannelModelPrice)
	if repository.getEnabledChannels != nil {
		for _, channel := range repository.getEnabledChannels() {
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
	models, err := repository.entFromContext(ctx).Model.Query().All(ctx)
	if err != nil {
		log.Warn(ctx, "load model catalog for CPA price index failed", log.Cause(err))
		if repository.index != nil {
			return repository.index
		}
		mergeCPABuiltinPrices(index)
		return index
	}
	mergeCPACatalogPrices(index, models)
	mergeCPABuiltinPrices(index)
	repository.index = index
	repository.builtAt = repository.now()
	return index
}

func (repository *cpaPricingRepository) setForTest(index map[string]*objects.ModelPrice, builtAt time.Time) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.index = index
	repository.builtAt = builtAt
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

func mergeCPABuiltinPrices(index map[string]*objects.ModelPrice) {
	for modelID, builtinPrice := range cpaBuiltinPrices {
		if _, exists := index[modelID]; exists {
			continue
		}
		price := builtinPrice
		index[modelID] = &price
	}
}
