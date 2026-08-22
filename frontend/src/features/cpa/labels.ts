// Shared display-label helpers for CPA providers and plans. Kept in one place
// so the management table and the toolbar filters render identical text.
import type { TFunction } from 'i18next';

export function providerLabel(provider: string, t: TFunction) {
  return t(`cpa.providerLabels.${provider}`, { defaultValue: provider });
}

// Falls back to a capitalized raw value for unregistered tiers (e.g. a future
// "super" plan renders as "Super") instead of leaking lowercase backend data.
export function planLabel(provider: string, plan: string, t: TFunction) {
  const key = plan.toLowerCase();
  return t(`cpa.planLabels.${provider}.${key}`, {
    defaultValue: key.charAt(0).toUpperCase() + key.slice(1),
  });
}
