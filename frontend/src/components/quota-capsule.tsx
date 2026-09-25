import { memo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { QuotaWindowItem, QuotaWindowKind } from '@/lib/quota-types';
import { formatQuotaUSD } from '@/lib/quota-types';
import { cn } from '@/lib/utils';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

// Estimate badge rendered to the right of the quota bar capsule: "≈ $100".
// Only present when a backend attaches estimatedLimitUSD (currently CPA codex
// weekly windows).
const EstimateBadge = memo(function EstimateBadge({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  if (window.estimatedLimitUSD == null) return null;
  return (
    <span
      className={cn(
        'bg-foreground/5 text-muted-foreground shrink-0 rounded-sm px-1.5 font-medium tabular-nums',
        size === 'sm' ? 'text-[9px]' : 'text-[10px]'
      )}
      title={window.estimatedCostUSD != null ? `≈ ${formatQuotaUSD(window.estimatedCostUSD)} used` : undefined}
    >
      ≈ {formatQuotaUSD(window.estimatedLimitUSD)}
    </span>
  );
});

// Compact quota capsule: period chip + thin remaining track + percentage on
// one line. The track is a REMAINING bar: longer bar = more quota left, color
// follows remaining sufficiency (green = plenty, red = about to run out).
// size 'md' is the full-height capsule; 'sm' is the compact table variant used
// by the CPA credential list. `window.percent` stays the USED percentage
// internally (pickPrimaryQuotaWindow and tests depend on it); the remaining
// semantics are applied at render time only.

type CapsuleSize = 'md' | 'sm';

// Remaining-quota severity tones. The track fill and the percentage share one
// accent per window, so a panel mixes at most these three semantic colors
// instead of tinting every element separately. The scale is stepped rather
// than continuous: interpolating a green→red hue ramp paints a half-used window
// chartreuse, which reads as a bug, not as "healthy".
// Palette matches the channel quota badges (green / amber / red 500).
const TONE_CLASSES = {
  ok: { fill: 'bg-green-500', text: 'text-green-600 dark:text-green-400' },
  warn: { fill: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400' },
  low: { fill: 'bg-red-500', text: 'text-red-600 dark:text-red-400' },
} as const;

// Remaining thresholds: at least half a window left is comfortable, under a
// fifth is about to run out.
function remainingTone(percentUsed: number): keyof typeof TONE_CLASSES {
  const remaining = 100 - Math.min(Math.max(percentUsed || 0, 0), 100);
  if (remaining >= 50) return 'ok';
  if (remaining >= 20) return 'warn';
  return 'low';
}

// Period chip tones per window kind (dark-mode aware). A flat 10% tint keeps
// the chip legible next to the severity accent without competing with it.
const CHIP_TONES: Record<QuotaWindowKind, string> = {
  weekly: 'bg-sky-500/10 text-sky-600 dark:text-sky-400',
  monthly: 'bg-violet-500/10 text-violet-600 dark:text-violet-400',
  daily: 'bg-teal-500/10 text-teal-600 dark:text-teal-400',
  hourly: 'bg-amber-500/10 text-amber-600 dark:text-amber-400',
  other: 'bg-foreground/5 text-muted-foreground',
};

// Tiny period chip (5H / 1D / 7D / 30D); windows without a period render none.
function PeriodChip({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  const { t } = useTranslation();
  if (!window.shortLabelKey) return null;
  return (
    <span
      className={cn(
        'shrink-0 rounded-sm px-1.5 font-medium tabular-nums',
        CHIP_TONES[window.kind],
        size === 'sm' ? 'text-[9px]' : 'text-[10px]'
      )}
    >
      {t(window.shortLabelKey)}
    </span>
  );
}

const CapsuleTrack = memo(function CapsuleTrack({ used, size = 'md' }: { used: number; size?: CapsuleSize }) {
  const clampedUsed = Math.min(Math.max(used || 0, 0), 100);
  const remaining = 100 - clampedUsed;
  return (
    <div className={cn('bg-foreground/10 flex-1 overflow-hidden rounded-full', size === 'sm' ? 'h-1' : 'h-1.5')}>
      <div
        className={cn('h-full rounded-full transition-all duration-500', TONE_CLASSES[remainingTone(clampedUsed)].fill)}
        style={{ width: `${remaining}%` }}
      />
    </div>
  );
});

const CapsuleBar = memo(function CapsuleBar({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  const { t } = useTranslation();
  const used = Math.min(Math.max(window.percent || 0, 0), 100);
  const remaining = Math.round(100 - used);
  return (
    <>
      <PeriodChip window={window} size={size} />
      <CapsuleTrack used={used} size={size} />
      <span className={cn('font-semibold tabular-nums', TONE_CLASSES[remainingTone(used)].text, size === 'sm' ? 'text-[10px]' : 'text-xs')}>
        {t('quota.capsule.percent', { percent: remaining })}
      </span>
    </>
  );
});

function windowFullName(window: QuotaWindowItem, t: (key: string) => string): string {
  const base = window.fullLabel ?? (window.fullLabelKey ? t(window.fullLabelKey) : '');
  return window.labelSuffix ? `${base} · ${window.labelSuffix}` : base;
}

function CapsuleTooltip({ window, children }: { window: QuotaWindowItem; children: ReactNode }) {
  const { t } = useTranslation();
  const used = Math.min(Math.max(window.percent || 0, 0), 100);
  const remaining = Math.round(100 - used);
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent side='top'>
        <div className='space-y-0.5'>
          <div className='font-medium'>{windowFullName(window, t)}</div>
          <div>{t('quota.capsule.remaining', { percent: remaining })}</div>
          <div>{t('quota.label.percent_used', { percent: Math.round(used) })}</div>
          {window.durationPercent !== undefined && (
            <div>
              {t('quota.label.time_elapsed')}: {Math.round(window.durationPercent)}%
            </div>
          )}
          {window.tooltipExtras?.map((line) => (
            <div key={line}>{line}</div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

// Standalone capsule (with hover tooltip). The CPA table uses the compact 'sm'
// variant. The 'md' bar fills whatever width is left beside the estimate badge
// (a full-width bar plus the badge would overflow its container).
export function QuotaCapsule({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  return (
    <CapsuleTooltip window={window}>
      <div className={cn('flex w-max min-w-full items-center gap-1.5', size === 'sm' ? 'min-w-52' : undefined)}>
        <div
          className={cn(
            'bg-muted/50 hover:bg-muted/70 flex shrink-0 items-center gap-1.5 rounded-full px-2 transition-colors',
            size === 'sm' ? 'h-7 w-52' : 'h-8 flex-1'
          )}
        >
          <CapsuleBar window={window} size={size} />
        </div>
        <EstimateBadge window={window} size={size} />
      </div>
    </CapsuleTooltip>
  );
}

// Shared overflow popover: trigger + scrollable list of the remaining windows.
// Used by the CPA summary cell. `children` must be a DOM-attachable element
// (it receives the trigger props).
export function QuotaMorePopover({
  windows,
  size = 'md',
  children,
}: {
  windows: QuotaWindowItem[];
  size?: CapsuleSize;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <Popover modal={false}>
      <PopoverTrigger asChild>{children}</PopoverTrigger>
      <PopoverContent className='w-[min(28rem,calc(100vw-2rem))]' align='start' side='top'>
        <div className='space-y-2 overflow-x-auto'>
          <div className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>{t('quota.capsule.more')}</div>
          {windows.map((window) => (
            <QuotaCapsuleRow key={window.id} window={window} size={size} />
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

// Full-name row used inside the overflow popover. The label column adapts and
// wraps instead of reserving fixed right-aligned whitespace; the quota column
// reuses QuotaCapsule so styling, sizing, estimates, and hover details cannot
// drift from the external bar.
function QuotaCapsuleRow({ window, size }: { window: QuotaWindowItem; size: CapsuleSize }) {
  const { t } = useTranslation();
  return (
    <div className='grid grid-cols-[minmax(0,1fr)_max-content] items-center gap-2'>
      <span className='min-w-0 text-left text-xs font-medium break-words'>{windowFullName(window, t)}</span>
      <span className='w-max min-w-52'>
        <QuotaCapsule window={window} size={size} />
      </span>
    </div>
  );
}

// Detail block for one window: period chip, full name and remaining
// percentage, the remaining track, then the pre-rendered detail lines
// (used/limit, reset time, estimate) that the compact capsule only reveals on
// hover. No surface, border or radius: like the channel and model expanded
// rows, the block sits bare on the expanded row's muted band, and only the
// track carries color. The block resets white-space because table cells force
// nowrap on their content.
function QuotaWindowCard({ window }: { window: QuotaWindowItem }) {
  const { t } = useTranslation();
  const used = Math.min(Math.max(window.percent || 0, 0), 100);
  const name = windowFullName(window, t);
  return (
    <div data-testid='quota-window-card' className='space-y-2 whitespace-normal'>
      <div className='flex items-center gap-1.5'>
        <PeriodChip window={window} />
        <span className='min-w-0 flex-1 truncate text-xs font-medium' title={name}>
          {name}
        </span>
        <EstimateBadge window={window} />
        <span className={cn('shrink-0 text-xs font-semibold tabular-nums', TONE_CLASSES[remainingTone(used)].text)}>
          {t('quota.capsule.remaining', { percent: Math.round(100 - used) })}
        </span>
      </div>
      <CapsuleTrack used={used} />
      {window.tooltipExtras?.length ? (
        <div className='text-muted-foreground space-y-0.5 text-[11px] leading-4 break-words'>
          {window.tooltipExtras.map((line) => (
            <div key={line}>{line}</div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

// Expanded detail view listing every window as its own block. Columns are
// capped at 20rem and auto-filled, so a very wide host (e.g. a colSpan table
// row) places blocks side by side instead of stretching one bar edge to edge.
// The 24px gutter matches the padding and spacing of the other expanded rows.
export function QuotaWindowsBlock({ windows }: { windows: QuotaWindowItem[] }) {
  const { t } = useTranslation();

  if (windows.length === 0) {
    return (
      <p data-testid='quota-capsule' className='text-muted-foreground text-sm'>
        {t('quota.capsule.empty')}
      </p>
    );
  }

  return (
    <div data-testid='quota-capsule' className='grid grid-cols-[repeat(auto-fill,minmax(16rem,20rem))] gap-6'>
      {windows.map((window) => (
        <QuotaWindowCard key={window.id} window={window} />
      ))}
    </div>
  );
}
