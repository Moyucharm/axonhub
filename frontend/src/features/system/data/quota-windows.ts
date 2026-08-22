import type { TFunction } from 'i18next';
import type {
  ProviderQuotaChannel,
  ProviderClaudeQuotaData,
  ProviderCodexQuotaData,
  ProviderClineQuotaData,
  ProviderClinePassQuotaData,
  ClineQuotaWindow,
  ProviderGitHubCopilotQuotaData,
  ProviderNanoGPTQuotaData,
  ProviderOpenCodeGoQuotaData,
  OpenCodeGoQuotaWindow,
  ProviderSyntheticQuotaData,
  ProviderNeuralWattQuotaData,
  ProviderApertisQuotaData,
  ProviderWaferQuotaData,
  ProviderMinimaxQuotaData,
  ProviderZhipuQuotaData,
  ProviderKimiCodeQuotaData,
} from './quotas';
import type { QuotaWindowKind, QuotaWindowItem } from '../../../lib/quota-types.ts';
import { QUOTA_PERIOD_SHORT_LABELS as PERIOD_SHORT_LABELS } from '../../../lib/quota-types.ts';
export type { QuotaWindowKind, QuotaWindowItem } from '../../../lib/quota-types.ts';

// ---------------------------------------------------------------------------
// Shared formatting helpers (moved out of quota-badges.tsx so the window
// projection can build tooltip text without duplicating logic).
// ---------------------------------------------------------------------------

export function calcDurationPercent(limit?: number, resetAfter?: number): number {
  if (!limit || resetAfter === undefined) return 0;
  const elapsed = limit - resetAfter;
  return Math.max(0, Math.min(100, (elapsed / limit) * 100));
}

export function formatWindowDuration(seconds?: number, t?: TFunction): string {
  if (!seconds || !t) return '';
  const hours = Math.floor(seconds / 3600);
  const days = hours >= 24 ? Math.floor(hours / 24) : 0;
  if (days > 0) return `${days}${t(days > 1 ? 'quota.label.days' : 'quota.label.day')}`;
  if (hours > 0) return `${hours}${t(hours > 1 ? 'quota.label.hours' : 'quota.label.hour')}`;
  return `${Math.floor(seconds / 60)}${t('quota.label.mins')}`;
}

export function formatTimeToReset(
  resetAtOrSeconds?: string | number | null,
  t?: TFunction,
  usedPercent?: number,
  regenerates?: boolean | number
): string {
  if (!resetAtOrSeconds || !t) return '';

  let resetTimeMs: number;
  if (typeof resetAtOrSeconds === 'number') {
    resetTimeMs = Date.now() + resetAtOrSeconds * 1000;
  } else {
    resetTimeMs = new Date(resetAtOrSeconds).getTime();
  }

  if (usedPercent === 0) return t('quota.label.no_usage_yet');

  const now = Date.now();
  const diffMs = resetTimeMs - now;
  const isRegen = regenerates != null && regenerates !== false;
  const regenPct = typeof regenerates === 'number' ? Math.round(regenerates * 100) : null;
  if (diffMs < 0) return isRegen ? t('quota.label.regenerating_now') : t('quota.label.reset_now');

  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  const d = t('quota.label.d');
  const h = t('quota.label.h');
  const m = t('quota.label.m');

  let timeStr: string;
  if (diffDays > 0) timeStr = `${diffDays}${d} ${diffHours % 24}${h}`;
  else if (diffHours > 0) timeStr = `${diffHours}${h} ${diffMins % 60}${m}`;
  else timeStr = `${diffMins}${m}`;

  if (isRegen && regenPct != null) {
    return t('quota.label.regenerates_pct_in_time', { percent: regenPct, time: timeStr });
  }
  return isRegen ? t('quota.label.regenerates_in_time', { time: timeStr }) : t('quota.label.resets_in_time', { time: timeStr });
}

export function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

export function getClineUsagePercent(window?: ClineQuotaWindow): number {
  return window?.usage_percent ?? (window?.usage_ratio ?? 0) * 100;
}

export function formatClineCost(units?: number, scale?: number, t?: TFunction, locale?: string): string {
  if (units == null || !scale || !t) return '';
  return t('currencies.format', {
    val: units / scale,
    currency: 'USD',
    locale: locale?.startsWith('zh') ? 'zh-CN' : 'en-US',
    minimumFractionDigits: 6,
  });
}

const getClaudeDurationPercent = (windowKey: string, resetTs?: number): number | undefined => {
  if (!resetTs) return undefined;
  let limit = 0;
  if (windowKey === '5h') limit = 5 * 3600;
  else if (windowKey === '7d') limit = 7 * 24 * 3600;
  else return undefined;

  const now = Date.now() / 1000;
  const resetAfter = resetTs - now;
  return calcDurationPercent(limit, resetAfter);
};

// Fraction of the reset window that has elapsed; mirrors the old "time elapsed"
// marker so the tooltip keeps that information without a second bar.
export function getOpenCodeGoDurationPercent(key: 'rolling' | 'weekly' | 'monthly', window: OpenCodeGoQuotaWindow): number | undefined {
  const limits: Record<string, number> = {
    rolling: 5 * 3600,
    weekly: 7 * 24 * 3600,
    monthly: 30 * 24 * 3600,
  };
  const limit = limits[key];

  // Prefer the absolute reset_time so the value keeps advancing as the user
  // looks; fall back to the snapshot reset_in_seconds from the last poll.
  let resetAfter: number | undefined;
  if (window.reset_time) {
    resetAfter = (new Date(window.reset_time).getTime() - Date.now()) / 1000;
  } else if (window.reset_in_seconds != null) {
    resetAfter = window.reset_in_seconds;
  }
  if (resetAfter === undefined) return undefined;

  return calcDurationPercent(limit, resetAfter);
}

export function getClineDurationPercent(key: 'last5h' | 'last7d' | 'last30d', window: ClineQuotaWindow): number | undefined {
  const limits: Record<'last5h' | 'last7d' | 'last30d', number> = {
    last5h: 5 * 3600,
    last7d: 7 * 24 * 3600,
    last30d: 30 * 24 * 3600,
  };
  if (!window.next_reset_at) return undefined;
  const resetAfter = (new Date(window.next_reset_at).getTime() - Date.now()) / 1000;
  return calcDurationPercent(limits[key], resetAfter);
}

// ---------------------------------------------------------------------------
// Primary window selection
// ---------------------------------------------------------------------------

export { pickPrimaryQuotaWindow } from '../../../lib/quota-types.ts';

// ---------------------------------------------------------------------------
// Per-channel window projection
// ---------------------------------------------------------------------------

function hasClineWindows(qd: ProviderClineQuotaData): qd is ProviderClinePassQuotaData {
  return (qd as { pool?: string }).pool === 'cline_pass' && (qd as { windows?: unknown }).windows != null;
}

function codexWindowKind(seconds?: number, fallback: QuotaWindowKind = 'other'): QuotaWindowKind {
  if (!seconds) return fallback;
  const DAY = 24 * 3600;
  if (seconds >= 20 * DAY) return 'monthly';
  if (seconds >= 6 * DAY && seconds <= 8 * DAY) return 'weekly';
  if (seconds >= 12 * 3600 && seconds <= 30 * 3600) return 'daily';
  if (seconds >= 3 * 3600 && seconds <= 7 * 3600) return 'hourly';
  return 'other';
}

function kimiWindowKind(label: string): QuotaWindowKind {
  if (/week|7\s*d/i.test(label)) return 'weekly';
  if (/month|30\s*d/i.test(label)) return 'monthly';
  if (/5\s*h/i.test(label)) return 'hourly';
  return 'other';
}

function copilotLabelKey(key: string): string {
  return key === 'completions'
    ? 'quota.label.inline_suggestions'
    : key === 'chat'
      ? 'quota.label.chat_messages'
      : key === 'premium_interactions'
        ? 'quota.label.premium_interactions'
        : key === 'premium_models'
          ? 'quota.label.premium_models'
          : '';
}

function isOpenaiType(t: string): t is 'openai' | 'openai_responses' {
  return t === 'openai' || t === 'openai_responses';
}

function isOpenCodeGoType(t: string): t is 'opencode_go' | 'opencode_go_anthropic' {
  return t === 'opencode_go' || t === 'opencode_go_anthropic';
}

function isMinimaxType(t: string): t is 'minimax' | 'minimax_anthropic' {
  return t === 'minimax' || t === 'minimax_anthropic';
}

export function getChannelQuotaWindows(channel: ProviderQuotaChannel, t: TFunction, locale?: string): QuotaWindowItem[] {
  const windows: QuotaWindowItem[] = [];

  if (channel.type === 'claudecode') {
    const qd = channel.quotaStatus.quotaData as ProviderClaudeQuotaData;
    const push = (key: '5h' | '7d' | 'overage', kind: QuotaWindowKind, shortLabelKey: string | undefined, fullLabelKey: string) => {
      const window = qd.windows?.[key];
      if (!window) return;
      windows.push({
        id: `claude-${key}`,
        kind,
        shortLabelKey,
        fullLabelKey,
        percent: Math.round((window.utilization || 0) * 100),
        durationPercent: kind === 'other' ? undefined : getClaudeDurationPercent(key, window.reset),
      });
    };
    push('5h', 'hourly', PERIOD_SHORT_LABELS.hourly, 'quota.window.5h');
    push('7d', 'weekly', PERIOD_SHORT_LABELS.weekly, 'quota.window.7d');
    push('overage', 'other', undefined, 'quota.label.overage_window');
  } else if (channel.type === 'codex') {
    const qd = channel.quotaStatus.quotaData as ProviderCodexQuotaData;
    const push = (key: 'primary_window' | 'secondary_window', fallbackKind: QuotaWindowKind, fullLabelKey: string) => {
      const window = qd.rate_limit?.[key];
      if (!window || window.used_percent === undefined) return;
      const kind = codexWindowKind(window.limit_window_seconds, fallbackKind);
      const durationPercent =
        window.limit_window_seconds != null
          ? calcDurationPercent(window.limit_window_seconds, window.reset_after_seconds)
          : undefined;
      const extras: string[] = [];
      if (window.limit_window_seconds) {
        extras.push(formatWindowDuration(window.limit_window_seconds, t));
      }
      if (window.reset_after_seconds != null) {
        extras.push(formatTimeToReset(window.reset_after_seconds, t));
      }
      windows.push({
        id: `codex-${key}`,
        kind,
        shortLabelKey: PERIOD_SHORT_LABELS[kind],
        fullLabelKey,
        percent: Math.round(window.used_percent || 0),
        durationPercent,
        tooltipExtras: extras,
      });
    };
    push('primary_window', 'hourly', 'quota.label.primary_window');
    push('secondary_window', 'weekly', 'quota.label.secondary_window');
  } else if (channel.type === 'cline') {
    const qd = channel.quotaStatus.quotaData as ProviderClineQuotaData;
    if (hasClineWindows(qd)) {
      const entries: Array<['last5h' | 'last7d' | 'last30d', QuotaWindowKind, string]> = [
        ['last5h', 'hourly', 'quota.window.5h'],
        ['last7d', 'weekly', 'quota.window.7d'],
        ['last30d', 'monthly', 'quota.window.30d'],
      ];
      for (const [key, kind, fullLabelKey] of entries) {
        const window = qd.windows[key];
        if (!window) continue;
        const usedPct = getClineUsagePercent(window);
        const hasUsagePercent = window.usage_percent != null || window.usage_ratio != null;
        if (!hasUsagePercent) continue;
        const durationPct = getClineDurationPercent(key, window);
        const used = formatClineCost(window.used_cost_units, qd.cost_scale, t, locale);
        const limit = formatClineCost(window.limit_cost_units, qd.cost_scale, t, locale);
        const extras: string[] = [];
        if (used && limit) extras.push(`${used} / ${limit}`);
        if (window.next_reset_at) extras.push(formatTimeToReset(window.next_reset_at, t));
        windows.push({
          id: `cline-${key}`,
          kind,
          shortLabelKey: PERIOD_SHORT_LABELS[kind],
          fullLabelKey,
          percent: usedPct,
          durationPercent: durationPct,
          tooltipExtras: extras,
        });
      }
    }
  } else if (channel.type === 'github_copilot') {
    const qd = channel.quotaStatus.quotaData as ProviderGitHubCopilotQuotaData;
    const push = (key: string, usedPct: number, usedText: string, totalText: string, unlimited: boolean) => {
      if (unlimited) return;
      const labelKey = copilotLabelKey(key);
      const item: QuotaWindowItem = {
        id: `copilot-${key}`,
        kind: 'other',
        fullLabelKey: labelKey || undefined,
        fullLabel: labelKey ? undefined : key.replace(/_/g, ' '),
        percent: Math.round(usedPct),
        tooltipExtras: [`${usedText} / ${totalText}`],
      };
      windows.push(item);
    };
    const limited = qd.limited_user_quotas;
    const total = qd.total_quotas;
    if (limited) {
      Object.entries(limited).forEach(([key, rem]) => {
        if (typeof rem === 'number') {
          const tot = total?.[key] ?? rem;
          const usedPct = tot > 0 ? (1 - rem / tot) * 100 : 0;
          push(key, usedPct, String(Math.round(rem / 10)), String(Math.round(tot / 10)), false);
        }
      });
    }
    if (qd.quota_snapshots) {
      Object.entries(qd.quota_snapshots).forEach(([key, snapshot]) => {
        if (!snapshot) return;
        const usedPct = snapshot.unlimited ? 0 : 100 - (snapshot.percent_remaining || 0);
        push(key, usedPct, String(Math.round(snapshot.quota_remaining || snapshot.remaining || 0)), String(Math.round(snapshot.entitlement || 0)), Boolean(snapshot.unlimited));
      });
    }
  } else if (channel.type === 'nanogpt' || channel.type === 'nanogpt_responses') {
    const qd = channel.quotaStatus.quotaData as ProviderNanoGPTQuotaData;
    const push = (key: 'weeklyInputTokens' | 'dailyImages' | 'dailyInputTokens', kind: QuotaWindowKind, fullLabelKey: string, isTokens: boolean) => {
      const window = qd.windows?.[key];
      if (!window) return;
      const total = (window.used ?? 0) + (window.remaining ?? 0);
      const usedStr = isTokens ? formatTokenCount(window.used ?? 0) : `${window.used ?? 0}`;
      const totalStr = isTokens ? formatTokenCount(total) : `${total}`;
      const extras: string[] = [`${usedStr} / ${totalStr}`];
      if (window.resetAt) extras.push(formatTimeToReset(new Date(window.resetAt).toISOString(), t));
      windows.push({
        id: `nanogpt-${key}`,
        kind,
        shortLabelKey: PERIOD_SHORT_LABELS[kind],
        fullLabelKey,
        percent: Math.round((window.percentUsed ?? 0) * 100),
        tooltipExtras: extras,
      });
    };
    push('weeklyInputTokens', 'weekly', 'quota.window.weekly_input_tokens', true);
    push('dailyImages', 'daily', 'quota.window.daily_images', false);
    push('dailyInputTokens', 'daily', 'quota.window.daily_input_tokens', true);
  } else if (isOpenCodeGoType(channel.type)) {
    const qd = channel.quotaStatus.quotaData as ProviderOpenCodeGoQuotaData;
    const entries: Array<['rolling' | 'weekly' | 'monthly', QuotaWindowKind, string]> = [
      ['rolling', 'hourly', 'quota.window.5h'],
      ['weekly', 'weekly', 'quota.window.weekly'],
      ['monthly', 'monthly', 'quota.window.monthly'],
    ];
    for (const [key, kind, fullLabelKey] of entries) {
      const window = qd.windows?.[key];
      if (!window) continue;
      const extras: string[] = [];
      if (window.reset_time || window.reset_in_seconds != null) {
        extras.push(formatTimeToReset(window.reset_time ?? window.reset_in_seconds, t));
      }
      windows.push({
        id: `opencodego-${key}`,
        kind,
        shortLabelKey: PERIOD_SHORT_LABELS[kind],
        fullLabelKey,
        percent: Math.round(window.usage_percent ?? 0),
        durationPercent: getOpenCodeGoDurationPercent(key, window),
        tooltipExtras: extras,
      });
    }
  } else if (channel.type === 'moonshot_coding') {
    const qd = channel.quotaStatus.quotaData as ProviderKimiCodeQuotaData;
    for (const [index, row] of (qd.rows ?? []).entries()) {
      const kind = kimiWindowKind(row.label);
      const extras: string[] = [`${row.used.toLocaleString()} / ${row.limit.toLocaleString()}`];
      if (row.resetAt || row.resetAfterSeconds) extras.push(formatTimeToReset(row.resetAt ?? row.resetAfterSeconds, t));
      windows.push({
        id: `kimi-${index}`,
        kind,
        shortLabelKey: PERIOD_SHORT_LABELS[kind],
        fullLabel: row.label,
        percent: Math.round(row.limit > 0 ? Math.min(100, (row.used / row.limit) * 100) : 0),
        tooltipExtras: extras,
      });
    }
  } else if (isMinimaxType(channel.type)) {
    const qd = channel.quotaStatus.quotaData as ProviderMinimaxQuotaData;
    for (const [index, row] of (qd.rows ?? []).entries()) {
      const suffix = qd.rows && qd.rows.length > 1 ? row.modelName : undefined;
      const intervalExtras: string[] = [];
      if (row.intervalTotalPercent !== 100) intervalExtras.push(`${Math.round(row.intervalUsedPercent)}% / ${Math.round(row.intervalTotalPercent)}%`);
      if (row.intervalResetAt) intervalExtras.push(formatTimeToReset(row.intervalResetAt, t));
      windows.push({
        id: `minimax-${index}-interval`,
        kind: 'hourly',
        shortLabelKey: PERIOD_SHORT_LABELS.hourly,
        fullLabelKey: 'quota.window.5h',
        labelSuffix: suffix,
        percent: Math.round(row.intervalPercent),
        tooltipExtras: intervalExtras,
      });
      if (row.weeklyStatus && row.weeklyStatus !== '') {
        const weeklyExtras: string[] = [];
        if (row.weeklyTotalPercent !== 100) weeklyExtras.push(`${Math.round(row.weeklyUsedPercent)}% / ${Math.round(row.weeklyTotalPercent)}%`);
        if (row.weeklyResetAt) weeklyExtras.push(formatTimeToReset(row.weeklyResetAt, t));
        windows.push({
          id: `minimax-${index}-weekly`,
          kind: 'weekly',
          shortLabelKey: PERIOD_SHORT_LABELS.weekly,
          fullLabelKey: 'quota.window.weekly',
          labelSuffix: suffix,
          percent: Math.round(row.weeklyPercent),
          tooltipExtras: weeklyExtras,
        });
      }
    }
  } else if (channel.type === 'zhipu' || channel.type === 'zhipu_anthropic') {
    const qd = channel.quotaStatus.quotaData as ProviderZhipuQuotaData;
    const known: Record<string, { kind: QuotaWindowKind; fullLabelKey: string }> = {
      five_hour: { kind: 'hourly', fullLabelKey: 'quota.window.5h' },
      weekly_limit: { kind: 'weekly', fullLabelKey: 'quota.window.weekly' },
    };
    for (const [index, row] of (qd.rows ?? []).entries()) {
      const knownWindow = known[row.window];
      const extras: string[] = [];
      if (row.resetAt) extras.push(formatTimeToReset(row.resetAt, t));
      windows.push({
        id: `zhipu-${index}`,
        kind: knownWindow?.kind ?? 'other',
        shortLabelKey: knownWindow ? PERIOD_SHORT_LABELS[knownWindow.kind] : undefined,
        fullLabelKey: knownWindow?.fullLabelKey,
        fullLabel: knownWindow ? undefined : row.window,
        percent: Math.min(100, row.usedPercent),
        tooltipExtras: extras,
      });
    }
  } else if (isOpenaiType(channel.type) && channel.providerType === 'wafer') {
    const qd = channel.quotaStatus.quotaData as ProviderWaferQuotaData;
    const usedPct = qd.current_period_used_percent ?? 0;
    const usedRequests = (qd.included_request_limit ?? 0) - (qd.remaining_included_requests ?? 0);
    const totalRequests = qd.included_request_limit ?? 0;
    const extras: string[] = [`${usedRequests} / ${totalRequests}`];
    if (qd.window_end) extras.push(formatTimeToReset(qd.window_end, t, usedPct));
    windows.push({
      id: 'wafer-usage',
      kind: 'monthly',
      shortLabelKey: PERIOD_SHORT_LABELS.monthly,
      fullLabelKey: 'quota.label.requests',
      percent: Math.round(usedPct),
      tooltipExtras: extras,
    });
  } else if (isOpenaiType(channel.type) && channel.providerType === 'synthetic') {
    const qd = channel.quotaStatus.quotaData as ProviderSyntheticQuotaData;
    if (qd.weeklyTokenLimit) {
      const pctRemaining = qd.weeklyTokenLimit.percentRemaining ?? 100;
      const usedPct = 100 - pctRemaining;
      const remainingCredits = qd.weeklyTokenLimit.remainingCredits;
      const maxCredits = qd.weeklyTokenLimit.maxCredits;
      const extras: string[] = [];
      if (remainingCredits != null && maxCredits != null) {
        const usedCredits = parseFloat(maxCredits.replace('$', '')) - parseFloat(remainingCredits.replace('$', ''));
        extras.push(`$${usedCredits.toFixed(2)} / ${maxCredits}`);
      }
      if (qd.weeklyTokenLimit.nextRegenAt) {
        extras.push(formatTimeToReset(qd.weeklyTokenLimit.nextRegenAt, t, usedPct, 0.02));
      }
      windows.push({
        id: 'synthetic-weekly',
        kind: 'weekly',
        shortLabelKey: PERIOD_SHORT_LABELS.weekly,
        fullLabelKey: 'quota.label.weekly_token_limit',
        percent: Math.round(usedPct),
        tooltipExtras: extras,
      });
    }
    if (qd.rollingFiveHourLimit) {
      const fiveHrRemaining = qd.rollingFiveHourLimit.remaining ?? 0;
      const fiveHrMax = qd.rollingFiveHourLimit.max ?? 0;
      const fiveHrUsedPct = fiveHrMax > 0 ? ((fiveHrMax - fiveHrRemaining) / fiveHrMax) * 100 : 0;
      const extras: string[] = [`${Math.round(fiveHrMax - fiveHrRemaining)} / ${Math.round(fiveHrMax)}`];
      if (qd.rollingFiveHourLimit.limited) extras.push(t('quota.status.limited'));
      if (qd.rollingFiveHourLimit.nextTickAt) {
        extras.push(formatTimeToReset(qd.rollingFiveHourLimit.nextTickAt, t, fiveHrUsedPct, qd.rollingFiveHourLimit.tickPercent ?? 0.05));
      }
      windows.push({
        id: 'synthetic-rolling',
        kind: 'hourly',
        shortLabelKey: PERIOD_SHORT_LABELS.hourly,
        fullLabelKey: 'quota.label.rolling_5h_limit',
        percent: Math.round(fiveHrUsedPct),
        tooltipExtras: extras,
      });
    }
  } else if (isOpenaiType(channel.type) && channel.providerType === 'neuralwatt') {
    const qd = channel.quotaStatus.quotaData as ProviderNeuralWattQuotaData;
    if (qd.subscription) {
      const kwhIncluded = qd.subscription.kwh_included ?? 0;
      const kwhUsed = qd.subscription.kwh_used ?? 0;
      const usedPct = kwhIncluded > 0 ? (kwhUsed / kwhIncluded) * 100 : 0;
      windows.push({
        id: 'neuralwatt-kwh',
        kind: 'monthly',
        shortLabelKey: PERIOD_SHORT_LABELS.monthly,
        fullLabelKey: 'quota.label.kwh_remaining',
        percent: Math.round(usedPct),
        tooltipExtras: [`${kwhUsed} / ${kwhIncluded} kWh`],
      });
    }
  } else if (isOpenaiType(channel.type) && channel.providerType === 'apertis') {
    const qd = channel.quotaStatus.quotaData as ProviderApertisQuotaData;
    const hasPaygCredits =
      qd.payg && (qd.payg.account_credits > 0 || (typeof qd.payg.token_used === 'number' && qd.payg.token_used > 0));
    const isPaygRelevant =
      !qd.is_subscriber || qd.subscription?.status !== 'active' || qd.subscription?.payg_fallback_enabled || hasPaygCredits;

    if (qd.is_subscriber && qd.subscription && qd.subscription.cycle_quota_limit > 0) {
      const subUsed = qd.subscription.cycle_quota_used;
      const subTotal = qd.subscription.cycle_quota_limit;
      const planLabel = qd.subscription.plan_type
        ? `${qd.subscription.plan_type.charAt(0).toUpperCase() + qd.subscription.plan_type.slice(1)} Plan`
        : t('quota.label.subscription');
      const extras: string[] = [`${subUsed} / ${subTotal}`];
      if (qd.subscription.payg_fallback_enabled) {
        const spent = qd.subscription.payg_spent_usd;
        const limit = qd.subscription.payg_limit_usd;
        if (spent != null && limit != null && limit > 0) {
          extras.push(`${t('quota.label.payg_fallback')}: $${spent.toFixed(2)} / $${limit.toFixed(2)}`);
        }
      }
      windows.push({
        id: 'apertis-cycle',
        kind: 'monthly',
        shortLabelKey: PERIOD_SHORT_LABELS.monthly,
        fullLabel: planLabel,
        percent: Math.round((subUsed / subTotal) * 100),
        tooltipExtras: extras,
      });
    }

    if (
      isPaygRelevant &&
      qd.payg &&
      !qd.payg.token_is_unlimited &&
      typeof qd.payg.token_total === 'number' &&
      typeof qd.payg.token_used === 'number' &&
      qd.payg.token_total > 0
    ) {
      windows.push({
        id: 'apertis-token',
        kind: 'other',
        fullLabelKey: 'quota.label.token_usage',
        percent: Math.round((qd.payg.token_used / qd.payg.token_total) * 100),
        tooltipExtras: [`$${qd.payg.token_used.toFixed(2)} / $${qd.payg.token_total.toFixed(2)}`],
      });
    }

    if (isPaygRelevant && qd.payg?.token_monthly_limit_usd != null && qd.payg.token_monthly_used_usd != null) {
      const monthlyPct =
        qd.payg.token_monthly_limit_usd > 0 ? (qd.payg.token_monthly_used_usd / qd.payg.token_monthly_limit_usd) * 100 : 0;
      windows.push({
        id: 'apertis-monthly',
        kind: 'monthly',
        shortLabelKey: PERIOD_SHORT_LABELS.monthly,
        fullLabelKey: 'quota.label.monthly_limit',
        percent: Math.round(monthlyPct),
        tooltipExtras: [`$${qd.payg.token_monthly_used_usd.toFixed(2)} / $${qd.payg.token_monthly_limit_usd.toFixed(2)}`],
      });
    }
  }

  return windows;
}
