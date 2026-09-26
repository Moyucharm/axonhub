import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { QuotaCapsule, QuotaMorePopover } from '@/components/quota-capsule';
import { cpaQuotaItemsToWindows, shortGroupLabel } from '@/features/cpa/quota-windows';
import type { CPAQuotaItem } from '../types';

// Compact quota cell for the CPA credential table. The adapter already orders
// windows shortest period first, so the two-bar inline budget always shows the
// tightest windows (Codex 5h before 7d, Antigravity 5h pools before 7d pools)
// and everything past the budget goes into the +N overflow popover.
// Rows are stacked:
//   - 1 window       -> single plain capsule (no pool chip)
//   - <=2 windows    -> one bar per window, all visible
//   - >2 windows     -> first two bars inline, everything else behind a +N
//                       overflow button placed after the last bar.
// Grid columns [4rem_267px_auto]: the label column is a fixed 4rem, the
// middle column keeps a fixed 267px width so remaining bars stay compact
// without stretching or shrinking dynamically.
const MAX_INLINE_WINDOWS = 2;

export function QuotaSummaryCapsule({ items, fallback = null }: { items: CPAQuotaItem[]; fallback?: ReactNode }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language === 'zh' ? 'zh-CN' : 'en-US';
  const windows = cpaQuotaItemsToWindows(items, t, locale);
  if (windows.length === 0) return fallback;

  const inline = windows.slice(0, MAX_INLINE_WINDOWS);
  const hidden = windows.slice(MAX_INLINE_WINDOWS);

  // Single ungrouped window: same grid with an empty label slot, so its bar
  // lines up with the labelled rows above and below.
  if (inline.length === 1 && !inline[0].group && hidden.length === 0) {
    return (
      <div className='grid w-max grid-cols-[4rem_267px_auto] items-center gap-x-2'>
        <span />
        <QuotaCapsule window={inline[0]} size='sm' />
        <span />
      </div>
    );
  }

  return (
    <div className='grid w-max grid-cols-[4rem_267px_auto] items-center gap-x-2 gap-y-1'>
      {inline.map((window, index) => {
        const label = shortGroupLabel(window.group, t);
        const isLast = index === inline.length - 1;
        return [
          // Explicit placeholder spans keep grid auto-placement aligned:
          // row = [group label][capsule][+N on the last row only]. A null
          // child would NOT occupy a grid cell and would shift later items.
          label ? (
            <span key={`${window.id}-label`} className='text-muted-foreground truncate text-[10px] font-semibold' title={window.group}>
              {label}
            </span>
          ) : (
            <span key={`${window.id}-label`} />
          ),
          <span key={`${window.id}-capsule`}>
            <QuotaCapsule window={window} size='sm' />
          </span>,
          isLast && hidden.length > 0 ? (
            <QuotaMorePopover key='more' windows={hidden} size='sm'>
              <button
                type='button'
                aria-label={t('quota.capsule.more')}
                className='bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground rounded-sm px-1.5 py-0.5 text-[10px] font-medium tabular-nums transition-colors'
              >
                +{hidden.length}
              </button>
            </QuotaMorePopover>
          ) : (
            <span key={`more-${window.id}`} />
          ),
        ];
      })}
    </div>
  );
}
