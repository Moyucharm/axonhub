import type { ProviderModel, ProvidersData } from '@/features/models/data/providers.schema';

/** Catalog cost fields the price editor can turn into channel price items. */
const priceCostFields = ['input', 'output', 'cache_read', 'cache_write'] as const;

/**
 * Suffixes a served model name may append to express a reasoning effort.
 * `max` keeps the qwen guard from internal/server/orchestrator/auto_reasoning_effort.go,
 * where a trailing `-max` belongs to the upstream model name for qwen models.
 */
const reasoningEffortSuffixes: Record<string, true> = {
  low: true,
  medium: true,
  high: true,
  xhigh: true,
  max: true,
  thinking: true,
};

export type CatalogPriceMatchMethod = 'exact' | 'prefix' | 'suffix' | 'prefix_suffix';

export type CatalogPriceMatch = {
  /** Catalog entry the price is copied from; it is never saved as the channel model id. */
  model: ProviderModel;
  providerId: string;
  /** How the catalog entry name was derived from the channel model id. */
  method: CatalogPriceMatchMethod;
};

/**
 * A catalog entry is only a price source when it prices at least one token direction.
 * All-zero / missing costs (e.g. self-hosted catalog entries) must not become a
 * zero-priced channel price.
 */
export function hasUsableCatalogCost(model: ProviderModel): boolean {
  const cost = model.cost;
  if (!cost) return false;
  return priceCostFields.some((field) => {
    const value = cost[field];
    return typeof value === 'number' && Number.isFinite(value) && value > 0;
  });
}

function pathSegment(modelId: string): string | null {
  const lastSlash = modelId.lastIndexOf('/');
  if (lastSlash < 0) return null;
  const segment = modelId.slice(lastSlash + 1);
  return segment === '' ? null : segment;
}

function isQwenMaxModel(modelId: string): boolean {
  let normalized = modelId.trim().toLowerCase();
  if (!normalized.endsWith('-max')) return false;

  const lastSlash = normalized.lastIndexOf('/');
  if (lastSlash >= 0) {
    normalized = normalized.slice(lastSlash + 1);
  }

  return normalized.startsWith('qwen');
}

function stripReasoningEffortSuffix(modelId: string): string | null {
  const lastDash = modelId.lastIndexOf('-');
  if (lastDash <= 0 || lastDash === modelId.length - 1) return null;

  const suffix = modelId.slice(lastDash + 1).toLowerCase();
  if (!reasoningEffortSuffixes[suffix]) return null;

  if (suffix === 'max' && isQwenMaxModel(modelId)) return null;

  return modelId.slice(0, lastDash);
}

type CatalogNameCandidate = {
  name: string;
  method: CatalogPriceMatchMethod;
};

/**
 * Candidate names are tried in order: the name the channel actually serves first, then
 * the same name with the path prefix and/or the reasoning-effort suffix removed. A
 * guessed name never wins over an exact one, and only the last suffix is stripped once.
 */
function buildCatalogNameCandidates(targetModelId: string): CatalogNameCandidate[] {
  const candidates: CatalogNameCandidate[] = [{ name: targetModelId, method: 'exact' }];

  const segment = pathSegment(targetModelId);
  if (segment) {
    candidates.push({ name: segment, method: 'prefix' });
  }

  const bareName = stripReasoningEffortSuffix(targetModelId);
  if (bareName) {
    candidates.push({ name: bareName, method: 'suffix' });
  }

  if (segment) {
    const bareSegment = stripReasoningEffortSuffix(segment);
    if (bareSegment) {
      candidates.push({ name: bareSegment, method: 'prefix_suffix' });
    }
  }

  return candidates;
}

/** Preferred provider first, remaining providers by id, so duplicate ids resolve stably. */
function buildProviderSearchOrder(data: ProvidersData, preferredProviderId?: string): string[] {
  const preferred = preferredProviderId && data.providers[preferredProviderId] ? preferredProviderId : null;
  const others = Object.keys(data.providers)
    .filter((providerId) => providerId !== preferred)
    .sort();

  return preferred ? [preferred, ...others] : others;
}

/**
 * Resolve the catalog price to copy for a channel model the catalog does not price
 * verbatim. Returns null when no candidate name has a priced catalog entry.
 */
export function findCatalogPriceMatch(
  data: ProvidersData,
  targetModelId: string,
  preferredProviderId?: string
): CatalogPriceMatch | null {
  if (!targetModelId) return null;

  const providerOrder = buildProviderSearchOrder(data, preferredProviderId);

  for (const candidate of buildCatalogNameCandidates(targetModelId)) {
    for (const providerId of providerOrder) {
      const model = (data.providers[providerId]?.models || []).find(
        (entry) => entry.id === candidate.name && hasUsableCatalogCost(entry)
      );
      if (model) {
        return { model, providerId, method: candidate.method };
      }
    }
  }

  return null;
}
