import { useTranslation } from 'react-i18next';
import type { ReactNode } from 'react';
import { QuotaCapsule, QuotaMorePopover } from '@/components/quota-capsule';
import type { CPAQuotaItem } from '@/features/cpa/data';
import { cpaQuotaItemsToWindows, shortGroupLabel, summarizeCredentialQuotaGroups } from '@/features/cpa/quota-windows';

// Compact quota cell for the CPA credential table. Windows are normally
// grouped by backend pool (antigravity: Gemini vs Claude/GPT), while Codex
// groups containing both 5h and 7d keep both windows visible. Rows are stacked:
//   - 1 window/pool  -> single plain capsule (no pool chip)
//   - <=2 pools      -> one bar per pool, all visible
//   - >2 pools       -> first two bars inline, everything else behind a +N
//                       overflow button placed after the last bar.
// Grid columns [auto_1fr_auto]: the label column shrinks to the widest actual
// group name of THIS cell (no reserved dead space for short names like
// "Code"), the capsule takes all remaining width, and the trailing auto
// column hosts the inline +N overflow button.
const MAX_INLINE_GROUPS = 2;

export function QuotaSummaryCapsule({
  items,
  provider,
  fallback = null,
}: {
  items: CPAQuotaItem[];
  provider: string;
  fallback?: ReactNode;
}) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language === 'zh' ? 'zh-CN' : 'en-US';
  const windows = cpaQuotaItemsToWindows(items, t, locale);
  if (windows.length === 0) return fallback;

  const groups = summarizeCredentialQuotaGroups(windows, provider);
  const inline = groups.slice(0, MAX_INLINE_GROUPS);
  // Every window without its own inline bar: non-representative windows of
  // shown pools plus all windows of truncated pools.
  const hidden = [
    ...inline.flatMap(({ rest }) => rest),
    ...groups.slice(MAX_INLINE_GROUPS).flatMap(({ rep, rest }) => [rep, ...rest]),
  ];

  // Single ungrouped window keeps the bare capsule, matching the old layout.
  if (inline.length === 1 && !inline[0].group && hidden.length === 0) {
    return (
      <span className='flex w-52 min-w-0'>
        <QuotaCapsule window={inline[0].rep} size='sm' />
      </span>
    );
  }

  return (
    <div className='grid w-60 min-w-0 grid-cols-[auto_1fr_auto] items-center gap-x-1.5 gap-y-1'>
      {inline.map(({ group, rep }, index) => {
        const label = shortGroupLabel(group, t);
        const isLast = index === inline.length - 1;
        return [
          // Explicit placeholder spans keep grid auto-placement aligned:
          // row = [group label][capsule][+N on the last row only]. A null
          // child would NOT occupy a grid cell and would shift later items.
          label ? (
            <span
              key={`${rep.id}-label`}
              className='text-muted-foreground max-w-20 truncate text-center text-[10px] font-semibold'
              title={group}
            >
              {label}
            </span>
          ) : (
            <span key={`${rep.id}-label`} />
          ),
          <span key={`${rep.id}-capsule`} className='min-w-0'>
            <QuotaCapsule window={rep} size='sm' />
          </span>,
          isLast && hidden.length > 0 ? (
            <QuotaMorePopover key='more' windows={hidden} size='sm'>
              <button
                type='button'
                aria-label={t('quota.capsule.more')}
                className='text-muted-foreground hover:bg-muted/60 hover:text-foreground rounded-full border bg-muted/40 px-1.5 py-0.5 text-[10px] font-semibold tabular-nums transition-colors'
              >
                +{hidden.length}
              </button>
            </QuotaMorePopover>
          ) : (
            <span key={`more-${rep.id}`} />
          ),
        ];
      })}
    </div>
  );
}
