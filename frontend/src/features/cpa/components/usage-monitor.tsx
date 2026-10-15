import { format } from 'date-fns';
import { IconAlertTriangle, IconCheck, IconQuestionMark } from '@tabler/icons-react';
import { zhCN, enUS } from 'date-fns/locale';
import { motion, AnimatePresence } from 'framer-motion';
import { useTranslation } from 'react-i18next';
import { isModelMismatch } from '@/lib/model-mismatch';
import { Badge } from '@/components/ui/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ServerSidePagination } from '@/components/server-side-pagination';
import { getStatusColor } from '@/features/requests/components/help';
import { useCPAUsageEvents } from '../api/queries';
import { providerLabel } from '../labels';
import type { CPAUsageEvent } from '../types';
import { useCPAPagination } from '../use-cpa-pagination';

const MotionTableRow = motion.create(TableRow);

const HEAD_CLASS = 'text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase';
const CELL_CLASS = 'border-b border-[var(--table-border)] py-3 group-last/row:border-0';
const COLUMN_COUNT = 7;

// Same icon language as the AX request list: a failed request is never a
// success verdict, and a missing model on either side is "unknown".
function ModelAuditIcon({ event }: { event: CPAUsageEvent }) {
  const { t } = useTranslation();
  const routedModel = event.model?.trim() ?? '';
  const responseModel = event.responseModel?.trim() ?? '';

  let icon = <IconQuestionMark className='h-3.5 w-3.5' />;
  // Records collected before CPA reported response_model are all unknown, so
  // keep this state muted and let real mismatches stand out.
  let className = 'text-muted-foreground/45';
  let tooltip = t('cpa.usage.modelAudit.unknown');
  if (event.failed) {
    icon = <IconAlertTriangle className='h-3.5 w-3.5' />;
    className = 'text-red-600 dark:text-red-400';
    tooltip = t('cpa.usage.modelAudit.failed');
  } else if (routedModel && responseModel) {
    if (isModelMismatch(routedModel, responseModel)) {
      icon = <IconAlertTriangle className='h-3.5 w-3.5' />;
      className = 'text-red-700 dark:text-red-400';
      tooltip = t('cpa.usage.modelAudit.mismatch', { model: responseModel });
    } else {
      icon = <IconCheck className='h-3.5 w-3.5' />;
      className = 'text-emerald-700 dark:text-emerald-300';
      tooltip = t('cpa.usage.modelAudit.matched', { model: responseModel });
    }
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={`inline-flex h-5 w-5 items-center justify-center ${className}`} tabIndex={0} role='img' aria-label={tooltip}>
          {icon}
        </span>
      </TooltipTrigger>
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  );
}

function TokensCell({ event }: { event: CPAUsageEvent }) {
  const { t } = useTranslation();
  return (
    <div className='space-y-0.5 text-xs'>
      <div className='text-sm font-medium'>
        {t('cpa.usage.tokens.total')}
        {event.totalTokens.toLocaleString()}
      </div>
      <div className='text-muted-foreground'>
        {t('cpa.usage.tokens.input')}: {event.inputTokens.toLocaleString()} | {t('cpa.usage.tokens.output')}: {event.outputTokens.toLocaleString()}
      </div>
    </div>
  );
}

function ReadCacheCell({ event }: { event: CPAUsageEvent }) {
  const { t } = useTranslation();
  if (event.cachedTokens === 0) return <div className='text-muted-foreground text-xs'>-</div>;

  // Providers that count cached tokens outside the input would yield a rate
  // above 100%; only show the rate when it is a true share of the input.
  const showRate = event.inputTokens > 0 && event.cachedTokens <= event.inputTokens;
  const hitRate = showRate ? (event.cachedTokens / event.inputTokens) * 100 : 0;
  const isLowHitRate = showRate && hitRate < 80 && event.inputTokens >= 40000;

  return (
    <div className='text-xs'>
      <div className='text-sm font-medium'>{event.cachedTokens.toLocaleString()}</div>
      {showRate && (
        <div className={isLowHitRate ? 'font-medium text-red-600 dark:text-red-400' : 'text-muted-foreground'}>
          {t('cpa.usage.tokens.cacheHitRate', { rate: hitRate.toFixed(1) })}
        </div>
      )}
    </div>
  );
}

export function CPAUsageMonitor({ instanceID }: { instanceID: number }) {
  const { t, i18n } = useTranslation();
  const dateLocale = i18n.language === 'zh' ? zhCN : enUS;
  const { pageSize, after, setAfter, cursorHistory, setCursorHistory, resetPagination, setPageSize } = useCPAPagination();
  const query = useCPAUsageEvents({ instanceID, first: pageSize, after });
  const events = query.data?.edges.map((edge) => edge.node) ?? [];

  return (
    <div className='flex min-h-0 flex-1 flex-col overflow-hidden'>
      <div className='shadow-soft relative flex-1 overflow-auto rounded-2xl border border-[var(--table-border)]'>
        <div className='min-w-max'>
          <Table data-testid='cpa-usage-table' className='border-separate border-spacing-0 rounded-2xl bg-[var(--table-background)]'>
            <TableHeader className='sticky top-0 z-20 bg-[var(--table-header)] shadow-sm'>
              <TableRow className='group/row border-0'>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.id')}</TableHead>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.model')}</TableHead>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.credential')}</TableHead>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.provider')}</TableHead>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.tokens')}</TableHead>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.readCache')}</TableHead>
                <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.time')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody className='space-y-1 !bg-[var(--table-background)] p-2'>
              {query.isLoading ? (
                <TableSkeleton rows={pageSize} columns={COLUMN_COUNT} />
              ) : events.length === 0 ? (
                <TableRow className='!bg-[var(--table-background)]'>
                  <TableCell colSpan={COLUMN_COUNT} className='text-muted-foreground h-24 !bg-[var(--table-background)] text-center'>
                    {t('cpa.usage.empty')}
                  </TableCell>
                </TableRow>
              ) : (
                <AnimatePresence initial={false} mode='popLayout'>
                  {events.map((event) => (
                    <MotionTableRow
                      key={event.id}
                      initial={{ opacity: 0, y: -20, height: 0 }}
                      animate={{ opacity: 1, y: 0, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      transition={{ type: 'spring', stiffness: 500, damping: 30, mass: 1, opacity: { duration: 0.2 } }}
                      layout
                      className='group/row hover:bg-muted/50'
                    >
                      <TableCell className={CELL_CLASS}>
                        <div className='flex min-w-[120px] flex-col gap-1.5'>
                          <span className='text-primary w-fit font-mono text-xs'>#{event.id}</span>
                          <Badge className={`${getStatusColor(event.failed ? 'failed' : 'completed')} w-fit`}>
                            {t(event.failed ? 'cpa.usage.status.failed' : 'cpa.usage.status.success')}
                          </Badge>
                        </div>
                      </TableCell>
                      <TableCell className={CELL_CLASS}>
                        <div className='flex min-w-[160px] flex-col gap-1'>
                          <span className='font-mono text-xs font-medium'>{event.model || '-'}</span>
                          <ModelAuditIcon event={event} />
                        </div>
                      </TableCell>
                      <TableCell className={CELL_CLASS}>
                        <span className='font-mono text-xs'>{event.credentialName || event.authIndex || '-'}</span>
                      </TableCell>
                      <TableCell className={CELL_CLASS}>
                        <span className='text-xs'>{providerLabel(event.provider, t)}</span>
                      </TableCell>
                      <TableCell className={CELL_CLASS}>
                        <TokensCell event={event} />
                      </TableCell>
                      <TableCell className={CELL_CLASS}>
                        <ReadCacheCell event={event} />
                      </TableCell>
                      <TableCell className={CELL_CLASS}>
                        <span className='text-xs whitespace-nowrap'>
                          {format(new Date(event.requestedAt), 'yyyy-MM-dd HH:mm:ss', { locale: dateLocale })}
                        </span>
                      </TableCell>
                    </MotionTableRow>
                  ))}
                </AnimatePresence>
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      <div className='mt-2 flex-shrink-0 sm:mt-4'>
        <ServerSidePagination
          pageInfo={query.data?.pageInfo}
          totalCount={query.data?.totalCount}
          pageSize={pageSize}
          dataLength={events.length}
          selectedRows={0}
          onNextPage={() => {
            if (!query.data?.pageInfo.hasNextPage || !query.data.pageInfo.endCursor) return;
            setCursorHistory((history) => [...history, after]);
            setAfter(query.data.pageInfo.endCursor ?? undefined);
          }}
          onPreviousPage={() => {
            const history = [...cursorHistory];
            setAfter(history.pop());
            setCursorHistory(history);
          }}
          onPageSizeChange={setPageSize}
          onResetCursor={resetPagination}
          testIdPrefix='cpa-usage'
        />
      </div>
    </div>
  );
}
