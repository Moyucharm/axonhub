const DATE_SNAPSHOT_SUFFIX = /-(\d{4}-\d{2}-\d{2}|\d{8})$/;
const THINKING_SUFFIX = /\([^()]*\)$/;

/**
 * Reduces a model name to the part that identifies the model itself, so cosmetic
 * differences (provider prefix, case, thinking suffix, -latest alias, date
 * snapshot) do not count as a mismatch. Variant suffixes like -high are kept.
 */
function normalizeModelName(name: string): string {
  let normalized = name.trim().toLowerCase();
  normalized = normalized.slice(normalized.lastIndexOf('/') + 1);
  normalized = normalized.replace(THINKING_SUFFIX, '').trim();
  normalized = normalized.replace(/-latest$/, '');
  normalized = normalized.replace(DATE_SNAPSHOT_SUFFIX, '');
  return normalized;
}

/** A missing model on either side is "unknown", never a mismatch. */
export function isModelMismatch(routedModel?: string | null, responseModel?: string | null): boolean {
  if (!routedModel?.trim() || !responseModel?.trim()) return false;
  return normalizeModelName(routedModel) !== normalizeModelName(responseModel);
}
