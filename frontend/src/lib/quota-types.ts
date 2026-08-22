// Normalized quota window model shared across UI components.
// Extracted from features/system/data/quota-windows.ts to break the
// circular dependency between components/quota-capsule.tsx and the
// feature layer.

export type QuotaWindowKind = 'weekly' | 'monthly' | 'daily' | 'hourly' | 'other';

export interface QuotaWindowItem {
  /** Stable id unique within one channel (e.g. 'claude-7d', 'minimax:modelA:weekly'). */
  id: string;
  kind: QuotaWindowKind;
  /** Raw backend group/pool name (e.g. antigravity 'Gemini Models'). Windows
   * sharing a group belong to one resource pool; summary views render one bar
   * per group. Omit for providers without pool semantics. */
  group?: string;
  /** i18n key for the tiny period chip (5H / 1D / 7D / 30D). Omit for windows without a period. */
  shortLabelKey?: string;
  /** i18n key for the full window name in the nested popover. */
  fullLabelKey?: string;
  /** Literal full name (backend-provided strings such as Kimi row labels). Takes precedence over fullLabelKey. */
  fullLabel?: string;
  /** Suffix appended to the full name (e.g. a Minimax model name). */
  labelSuffix?: string;
  /** Usage percentage 0-100. */
  percent: number;
  /** Fraction of the reset window elapsed, shown in the tooltip only. */
  durationPercent?: number;
  /** Pre-rendered tooltip lines (reset text, used/limit figures, flags). */
  tooltipExtras?: string[];
  /** Estimated total quota value in USD for this window (CPA codex weekly window). */
  estimatedLimitUSD?: number;
  /** Locally observed cost sum in USD within the current cycle. */
  estimatedCostUSD?: number;
}

// i18n keys for the tiny period chip (5H / 1D / 7D / 30D) rendered by
// CapsuleBar. Shared by the system channel view and the CPA adapter so both
// surfaces label identical window kinds identically.
export const QUOTA_PERIOD_SHORT_LABELS: Record<QuotaWindowKind, string | undefined> = {
  weekly: 'quota.capsule.period.7d',
  monthly: 'quota.capsule.period.30d',
  daily: 'quota.capsule.period.1d',
  hourly: 'quota.capsule.period.5h',
  other: undefined,
};

/** Kind priority used for primary-window selection (higher wins). */
export const QUOTA_KIND_PRIORITY: Record<QuotaWindowKind, number> = {
  weekly: 4,
  monthly: 3,
  daily: 2,
  hourly: 1,
  other: 0,
};

/**
 * Pick the single most important quota window from a list.
 * Priority: weekly > monthly > daily > hourly > other.
 * Within the same kind, the highest usage percentage wins.
 */
export function pickPrimaryQuotaWindow(windows: QuotaWindowItem[]): QuotaWindowItem | undefined {
  if (windows.length === 0) return undefined;
  const pickBest = (kind: QuotaWindowKind) =>
    windows.filter((w) => w.kind === kind).sort((a, b) => b.percent - a.percent)[0];
  return pickBest('weekly') ?? pickBest('monthly') ?? pickBest('daily') ?? pickBest('hourly') ?? pickBest('other');
}

// Compact USD formatting for quota value estimates: integers above $100, one
// decimal below that keeps small estimates readable without noise.
export function formatQuotaUSD(value: number): string {
  const fractionDigits = Math.abs(value) >= 100 ? 0 : 1;
  return `$${value.toLocaleString('en-US', { maximumFractionDigits: fractionDigits })}`;
}
