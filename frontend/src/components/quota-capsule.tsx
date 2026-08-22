import { memo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { QuotaWindowItem, QuotaWindowKind } from '@/lib/quota-types';
import { pickPrimaryQuotaWindow, formatQuotaUSD } from '@/lib/quota-types';

// Estimate badge rendered behind the quota bar: "≈ $100". Only present when a
// backend attaches estimatedLimitUSD (currently CPA codex weekly windows).
const EstimateBadge = memo(function EstimateBadge({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  if (window.estimatedLimitUSD == null) return null;
  return (
    <span
      className={cn(
        'text-emerald-600 shrink-0 rounded-full border border-emerald-300/70 bg-emerald-100/60 px-1.5 font-semibold tabular-nums dark:border-emerald-400/30 dark:bg-emerald-400/10 dark:text-emerald-300',
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

// HSL stop for the usage severity ramp (green → yellow → red as `percent`
// rises). `lightnessShift` tweaks the stop for gradient shading.
function severityHsl(percent: number, lightnessShift = 0): string {
  const u = Math.min(Math.max(percent, 0), 100) / 100;
  // Tailwind 500 colors approximation for a modern, theme-friendly gradient:
  // Green (142, 71%, 45%), Yellow (45, 93%, 47%), Red (0, 84%, 60%)
  let h: number;
  let s: number;
  let l: number;
  if (u < 0.5) {
    const n = u * 2; // 0 to 1
    h = 142 - n * (142 - 45);
    s = 71 + n * (93 - 71);
    l = 45 + n * (47 - 45);
  } else {
    const n = (u - 0.5) * 2; // 0 to 1
    h = 45 - n * 45;
    s = 93 - n * (93 - 84);
    l = 47 + n * (60 - 47);
  }
  return `hsl(${Math.round(h)}, ${Math.round(s)}%, ${Math.round(l + lightnessShift)}%)`;
}

function severityColor(percent: number): string {
  return severityHsl(percent);
}

// Vertical gradient (lighter on top) for the remaining track fill.
function severityGradient(percent: number): string {
  return `linear-gradient(180deg, ${severityHsl(percent, 8)} 0%, ${severityHsl(percent, -6)} 100%)`;
}

// Period chip tones per window kind (dark-mode aware).
const CHIP_TONES: Record<Exclude<QuotaWindowKind, 'other'>, string> = {
  weekly:
    'border-sky-300/70 bg-sky-100/80 text-sky-700 dark:border-sky-400/30 dark:bg-sky-400/10 dark:text-sky-300',
  monthly:
    'border-violet-300/70 bg-violet-100/80 text-violet-700 dark:border-violet-400/30 dark:bg-violet-400/10 dark:text-violet-300',
  daily:
    'border-teal-300/70 bg-teal-100/80 text-teal-700 dark:border-teal-400/30 dark:bg-teal-400/10 dark:text-teal-300',
  hourly:
    'border-amber-300/70 bg-amber-100/80 text-amber-700 dark:border-amber-400/30 dark:bg-amber-400/10 dark:text-amber-300',
};

const CapsuleTrack = memo(function CapsuleTrack({ used, size = 'md' }: { used: number; size?: CapsuleSize }) {
  const clampedUsed = Math.min(Math.max(used || 0, 0), 100);
  const remaining = 100 - clampedUsed;
  return (
    <div className={`bg-muted flex-1 overflow-hidden rounded-full ${size === 'sm' ? 'h-1' : 'h-1.5'}`}>
      <div
        className='h-full transition-all duration-500'
        style={{ width: `${remaining}%`, backgroundImage: severityGradient(clampedUsed) }}
      />
    </div>
  );
});

const CapsuleBar = memo(function CapsuleBar({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  const { t } = useTranslation();
  const shortLabel = window.shortLabelKey ? t(window.shortLabelKey) : null;
  const used = Math.min(Math.max(window.percent || 0, 0), 100);
  const remaining = Math.round(100 - used);
  const tone = window.kind !== 'other' ? CHIP_TONES[window.kind] : null;
  return (
    <>
      {shortLabel && (
        <span className={cn('rounded-full border px-1.5 font-semibold tabular-nums', tone, size === 'sm' ? 'text-[9px]' : 'text-[10px]')}>
          {shortLabel}
        </span>
      )}
      <CapsuleTrack used={used} size={size} />
      <span
        className={`font-semibold tabular-nums ${size === 'sm' ? 'text-[10px]' : 'text-xs'}`}
        style={{ color: severityColor(used) }}
      >
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

// Standalone capsule (with hover tooltip). QuotaWindowsBlock composes this for
// the single-window case; the CPA table uses the compact 'sm' variant.
export function QuotaCapsule({ window, size = 'md' }: { window: QuotaWindowItem; size?: CapsuleSize }) {
  return (
    <CapsuleTooltip window={window}>
      <div
        className={cn('flex w-full items-center gap-1.5 rounded-full border bg-muted/40 px-1.5 transition-colors hover:bg-muted/60', size === 'sm' ? 'h-7' : 'h-8')}
      >
        <CapsuleBar window={window} size={size} />
        <EstimateBadge window={window} size={size} />
      </div>
    </CapsuleTooltip>
  );
}

// Shared overflow popover: trigger + scrollable list of the remaining windows.
// Used by QuotaWindowsBlock (system channels) and the CPA summary cell.
// `children` must be a DOM-attachable element (it receives the trigger props);
// pass `tooltipWindow` to wrap it in the standard hover tooltip first.
export function QuotaMorePopover({
  windows,
  tooltipWindow,
  children,
}: {
  windows: QuotaWindowItem[];
  tooltipWindow?: QuotaWindowItem;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const trigger = tooltipWindow ? (
    <CapsuleTooltip window={tooltipWindow}>
      <PopoverTrigger asChild>{children}</PopoverTrigger>
    </CapsuleTooltip>
  ) : (
    <PopoverTrigger asChild>{children}</PopoverTrigger>
  );
  return (
    <Popover modal={false}>
      {trigger}
      <PopoverContent className='w-80' align='start' side='top'>
        <div className='space-y-2'>
          <div className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>{t('quota.capsule.more')}</div>
          {windows.map((window) => (
            <QuotaCapsuleRow key={window.id} window={window} />
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

// Full-name row used inside the overflow popover: window name as plain text
// OUTSIDE the capsule (truncated with a native title tooltip for the full
// name), capsule holds only chip + track + percentage.
function QuotaCapsuleRow({ window }: { window: QuotaWindowItem }) {
  const { t } = useTranslation();
  const used = Math.min(Math.max(window.percent || 0, 0), 100);
  const remaining = Math.round(100 - used);
  const tone = window.kind !== 'other' ? CHIP_TONES[window.kind] : null;
  return (
    <div className='flex items-center gap-2'>
      <span className='w-36 shrink-0 truncate text-right text-xs font-medium' title={windowFullName(window, t)}>
        {windowFullName(window, t)}
      </span>
      <div className='flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-full border bg-muted/40 px-1.5'>
        {window.shortLabelKey && (
          <span
            className={cn(
              'shrink-0 rounded-full border px-1.5 text-[10px] font-semibold tabular-nums',
              tone
            )}
          >
            {t(window.shortLabelKey)}
          </span>
        )}
        <CapsuleTrack used={used} />
        <span
          className='text-xs font-semibold tabular-nums'
          style={{ color: severityColor(used) }}
        >
          {t('quota.capsule.percent', { percent: remaining })}
        </span>
        <EstimateBadge window={window} size='sm' />
      </div>
    </div>
  );
}

export function QuotaWindowsBlock({ windows }: { windows: QuotaWindowItem[] }) {
  const { t } = useTranslation();
  const primary = pickPrimaryQuotaWindow(windows);

  if (!primary) {
    return (
      <div
        data-testid="quota-capsule"
        className='text-muted-foreground flex h-8 w-full items-center justify-center rounded-full border bg-muted/40 text-sm'
      >
        {t('quota.capsule.empty')}
      </div>
    );
  }

  const rest = windows.filter((w) => w.id !== primary.id);

  if (rest.length === 0) {
    return (
      <div data-testid="quota-capsule">
        <QuotaCapsule window={primary} />
      </div>
    );
  }

  return (
    <div data-testid="quota-capsule">
      <QuotaMorePopover windows={rest} tooltipWindow={primary}>
        <button
          type='button'
          data-testid="quota-capsule-more"
          aria-label={t('quota.capsule.more')}
          className='hover:bg-muted/60 flex h-8 w-full items-center gap-1.5 rounded-full border bg-muted/40 px-1.5 transition-colors'
        >
          <CapsuleBar window={primary} />
          <span className='text-muted-foreground text-[10px] font-semibold tabular-nums'>+{rest.length}</span>
        </button>
      </QuotaMorePopover>
    </div>
  );
}
