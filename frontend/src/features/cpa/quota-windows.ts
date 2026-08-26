// CPA quota window adapter: maps backend-normalized CPA quota items onto the
// shared capsule window model (lib/quota-types.ts) so the CPA page can reuse
// QuotaCapsule / QuotaWindowsBlock verbatim, plus pool grouping helpers for
// the summary cell (summarizeQuotaGroups / shortGroupLabel).
//
// This module must stay loadable under bare `node --test`, so it imports from
// ./data only via `import type` (stripped by Node's type stripping) and uses a
// relative path for runtime imports — the '@' alias is not resolved by node,
// only by Vite.
import type { TFunction } from 'i18next';
import type { CPAQuotaItem } from './data';
import type { QuotaWindowItem, QuotaWindowKind } from '../../lib/quota-types.ts';
import { QUOTA_PERIOD_SHORT_LABELS, QUOTA_KIND_PRIORITY, formatQuotaUSD } from '../../lib/quota-types.ts';

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

// Projects CPA quota items onto the shared capsule window model. Summary-cell
// primary selection is handled by summarizeQuotaGroups below.
//
// Items without any computable percentage (both usedPercent and
// remainingPercent are null — e.g. a "Monthly balance" of 0/0 cents) carry no
// renderable usage info, so they are skipped instead of being shown as a
// misleading full remaining bar.
export function cpaQuotaItemsToWindows(items: CPAQuotaItem[], t: TFunction, locale = 'en-US'): QuotaWindowItem[] {
  return items.flatMap((item) => {
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
}

// One summarized resource pool: the representative (tightest) window plus the
// remaining windows of the same group in original order.
export interface QuotaGroupSummary {
  /** Raw backend group name; undefined for ungrouped singleton windows. */
  group?: string;
  rep: QuotaWindowItem;
  rest: QuotaWindowItem[];
}

function compareRepWindows(a: QuotaWindowItem, b: QuotaWindowItem): number {
  // Tightest window first: highest usage wins; ties fall back to the shared
  // kind priority (weekly > monthly > daily > hourly > other) so an all-zero
  // pool still surfaces its long-term ceiling.
  if (a.percent !== b.percent) return b.percent - a.percent;
  const kindDelta = QUOTA_KIND_PRIORITY[b.kind] - QUOTA_KIND_PRIORITY[a.kind];
  if (kindDelta !== 0) return kindDelta;
  return a.id.localeCompare(b.id);
}

function summarizeQuotaBuckets(
  windows: QuotaWindowItem[],
  bucketKey: (window: QuotaWindowItem) => string
): QuotaGroupSummary[] {
  const buckets = new Map<string, { group?: string; members: QuotaWindowItem[] }>();
  for (const window of windows) {
    const key = bucketKey(window);
    const bucket = buckets.get(key);
    if (bucket) bucket.members.push(window);
    else buckets.set(key, { group: window.group, members: [window] });
  }
  return Array.from(buckets.values(), ({ group, members }) => {
    const sorted = [...members].sort(compareRepWindows);
    const rep = sorted[0];
    const rest = members.filter((window) => window !== rep);
    return { group, rep, rest };
  });
}

// Groups projected windows by their backend pool name and picks each pool's
// representative window. Group order follows first appearance so backend
// ordering (e.g. antigravity Gemini before Claude/GPT) is preserved.
export function summarizeQuotaGroups(windows: QuotaWindowItem[]): QuotaGroupSummary[] {
  return summarizeQuotaBuckets(windows, (window) => window.group ?? `\u0000${window.id}`);
}

// Codex restored a 5h primary limit alongside its 7d secondary limit. Both
// windows must stay visible in the credential table only for that exact
// two-window shape. Other providers and all additional/missing-period shapes
// keep the normal one-representative-per-pool summary.
export function summarizeCredentialQuotaGroups(
  windows: QuotaWindowItem[],
  provider: string
): QuotaGroupSummary[] {
  if (provider.trim().toLowerCase() !== 'codex') return summarizeQuotaGroups(windows);

  const windowsByGroup = new Map<string, QuotaWindowItem[]>();
  for (const window of windows) {
    if (!window.group) continue;
    const members = windowsByGroup.get(window.group) ?? [];
    members.push(window);
    windowsByGroup.set(window.group, members);
  }
  const fiveHours = 5 * 60 * 60;
  const sevenDays = 7 * 24 * 60 * 60;
  const splitGroups = new Set(
    Array.from(windowsByGroup.entries())
      .filter(([, members]) =>
        members.length === 2 &&
        members.some((window) => window.periodSeconds === fiveHours) &&
        members.some((window) => window.periodSeconds === sevenDays)
      )
      .map(([group]) => group)
  );
  if (splitGroups.size === 0) return summarizeQuotaGroups(windows);

  return summarizeQuotaBuckets(windows, (window) => {
    if (window.group && splitGroups.has(window.group)) {
      return `${window.group}\u0000${window.periodSeconds}`;
    }
    return window.group ?? `\u0000${window.id}`;
  });
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
