// CPA quota window adapter: maps backend-normalized CPA quota items onto the
// shared capsule window model (lib/quota-types.ts) so the CPA page can reuse
// QuotaCapsule / QuotaWindowsBlock verbatim, ordered shortest period first for
// the summary cell (shortGroupLabel supplies its pool chips).
//
// This module must stay loadable under bare `node --test`, so it imports from
// ./data only via `import type` (stripped by Node's type stripping) and uses a
// relative path for runtime imports — the '@' alias is not resolved by node,
// only by Vite.
import type { TFunction } from 'i18next';
import type { CPAQuotaItem } from './types';
import type { QuotaWindowItem, QuotaWindowKind } from '../../lib/quota-types.ts';
import { QUOTA_PERIOD_SHORT_LABELS, formatQuotaUSD } from '../../lib/quota-types.ts';

export { formatQuotaUSD };

export function formatTime(value?: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

type CpaWindowLike = Pick<CPAQuotaItem, 'periodSeconds' | 'label' | 'group'>;

// Window period classification. Backend adapters only fill periodSeconds for
// some providers (Codex, Antigravity, Kimi), so labels like "5 hour" / "7 day"
// / "Monthly balance" act as the fallback channel.
export function cpaWindowKind(item: CpaWindowLike): QuotaWindowKind {
  if (item.periodSeconds && item.periodSeconds > 0) {
    const seconds = item.periodSeconds;
    if (seconds >= 20 * 86400) return 'monthly';
    if (seconds >= 6 * 86400) return 'weekly';
    if (seconds >= 86400) return 'daily';
    return 'hourly';
  }

  const text = `${item.label} ${item.group ?? ''}`.toLowerCase();
  if (/(5 ?hour|five[ _-]?hour|5h)/.test(text)) return 'hourly';
  if (/(7 ?day|weekly|week|7d)/.test(text)) return 'weekly';
  if (/(30 ?day|monthly|month|30d)/.test(text)) return 'monthly';
  if (/(daily|1 ?day|1d)/.test(text)) return 'daily';
  return 'other';
}

// Projects CPA quota items onto the shared capsule window model, sorted
// shortest period first (ties broken alphabetically) so the summary cell's
// inline budget always holds the shortest windows.
//
// Items without any computable percentage (both usedPercent and
// remainingPercent are null — e.g. a "Monthly balance" of 0/0 cents) carry no
// renderable usage info, so they are skipped instead of being shown as a
// misleading full remaining bar.
export function cpaQuotaItemsToWindows(items: CPAQuotaItem[], t: TFunction, locale = 'en-US'): QuotaWindowItem[] {
  const windows = items.flatMap((item) => {
    if (item.usedPercent == null && item.remainingPercent == null) return [];
    const usedPct = item.usedPercent ?? (item.remainingPercent != null ? 100 - item.remainingPercent : 0);
    const percent = Math.max(0, Math.min(100, usedPct));
    const group = item.group?.trim() || undefined;
    const kind = cpaWindowKind(item);
    const extras: string[] = [];
    if (item.used != null || item.limit != null) {
      const usedText = item.used != null ? item.used.toLocaleString(locale) : '—';
      const limitText = item.limit != null ? item.limit.toLocaleString(locale) : '—';
      const unit = item.unit ? ` ${item.unit}` : '';
      extras.push(`${t('cpa.quota.used')}: ${usedText}${unit} / ${t('cpa.quota.limit')}: ${limitText}${unit}`);
    }
    if (item.resetAt) extras.push(`${t('cpa.quota.resetAt')}: ${formatTime(item.resetAt)}`);
    if (item.estimatedLimitUSD != null) {
      const costText = item.estimatedCostUSD != null ? formatQuotaUSD(item.estimatedCostUSD) : '—';
      extras.push(t('cpa.quota.estimateDetail', { limit: formatQuotaUSD(item.estimatedLimitUSD), cost: costText }));
    }
    if (item.description) extras.push(item.description);
    return [
      {
        id: item.id,
        kind,
        // Pool prefix keeps windows distinguishable once listed outside their
        // group context (overflow popover, expanded row).
        fullLabel: group ? `${group} · ${item.label}` : item.label,
        shortLabelKey: QUOTA_PERIOD_SHORT_LABELS[kind],
        group,
        periodSeconds: item.periodSeconds ?? undefined,
        percent,
        tooltipExtras: extras.length > 0 ? extras : undefined,
        estimatedLimitUSD: item.estimatedLimitUSD ?? undefined,
        estimatedCostUSD: item.estimatedCostUSD ?? undefined,
      },
    ];
  });
  return windows.sort(compareQuotaWindows);
}

export function getQuotaWindowDuration(window: QuotaWindowItem): number {
  if (typeof window.periodSeconds === 'number' && Number.isFinite(window.periodSeconds) && window.periodSeconds > 0) {
    return window.periodSeconds;
  }
  switch (window.kind) {
    case 'hourly':
      return 5 * 3600;
    case 'daily':
      return 24 * 3600;
    case 'weekly':
      return 7 * 24 * 3600;
    case 'monthly':
      return 30 * 24 * 3600;
    default:
      return Number.MAX_SAFE_INTEGER;
  }
}

export function compareQuotaWindows(a: QuotaWindowItem, b: QuotaWindowItem): number {
  // 1. 时间额度区间：较短周期排在上面/前面，较长周期排在下面/后面 (e.g. 5h < 7d)
  const durA = getQuotaWindowDuration(a);
  const durB = getQuotaWindowDuration(b);
  if (durA !== durB) {
    return durA - durB;
  }

  // 2. 如果时间一致，则根据字母升序排序
  const labelA = a.fullLabel || a.labelSuffix || a.group || a.id;
  const labelB = b.fullLabel || b.labelSuffix || b.group || b.id;
  const comp = labelA.localeCompare(labelB);
  if (comp !== 0) return comp;

  return a.id.localeCompare(b.id);
}

// Short chip labels for known backend pools. Brand names are locale-neutral,
// so the map points at cpa.json keys whose translations intentionally match;
// unknown pools fall back to a truncated raw name (full name stays available
// in the capsule tooltip via fullLabel).
const GROUP_SHORT_LABEL_KEYS: Record<string, string> = {
  'gemini models': 'cpa.quota.group.gemini',
  'claude and gpt models': 'cpa.quota.group.claude_gpt',
};

export function shortGroupLabel(group: string | undefined, t: TFunction): string | undefined {
  if (!group) return undefined;
  const key = GROUP_SHORT_LABEL_KEYS[group.toLowerCase()];
  if (key) return t(key);
  return group.length <= 12 ? group : `${group.slice(0, 11)}…`;
}
