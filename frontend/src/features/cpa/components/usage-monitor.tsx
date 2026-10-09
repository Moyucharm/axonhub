import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import { ServerSidePagination } from '@/components/server-side-pagination';
import { UpstreamModelHint } from '@/components/upstream-model-hint';
import { useCPAUsageEvents } from '../api/queries';
import { providerLabel } from '../labels';
import { useCPAPagination } from '../use-cpa-pagination';

const HEAD_CLASS = 'text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase';
const CELL_CLASS = 'border-0 bg-inherit px-4 py-3';

export function CPAUsageMonitor({ instanceID, locale }: { instanceID: number; locale: string }) {
  const { t } = useTranslation();
  const { pageSize, after, setAfter, cursorHistory, setCursorHistory, resetPagination, setPageSize } = useCPAPagination();
  const query = useCPAUsageEvents({ instanceID, first: pageSize, after });
  const events = query.data?.edges.map((edge) => edge.node) ?? [];

  return (
    <>
      <div
        data-testid='cpa-usage-table'
        className='shadow-soft relative min-h-0 flex-1 overflow-auto rounded-2xl border border-[var(--table-border)]'
      >
        <Table className='border-separate border-spacing-0 rounded-2xl bg-[var(--table-background)]'>
          <TableHeader className='sticky top-0 z-20 bg-[var(--table-header)] shadow-sm'>
            <TableRow className='border-0'>
              <TableHead className={`${HEAD_CLASS} w-[180px]`}>{t('cpa.usage.columns.time')}</TableHead>
              <TableHead className={`${HEAD_CLASS} w-[220px]`}>{t('cpa.usage.columns.credential')}</TableHead>
              <TableHead className={`${HEAD_CLASS} w-[130px]`}>{t('cpa.usage.columns.provider')}</TableHead>
              <TableHead className={HEAD_CLASS}>{t('cpa.usage.columns.model')}</TableHead>
              <TableHead className={`${HEAD_CLASS} w-[180px]`}>{t('cpa.usage.columns.tokens')}</TableHead>
              <TableHead className={`${HEAD_CLASS} w-[100px]`}>{t('cpa.usage.columns.status')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className='!bg-[var(--table-background)]'>
            {query.isLoading ? (
              <TableSkeleton rows={pageSize} />
            ) : events.length === 0 ? (
              <TableRow className='!bg-[var(--table-background)]'>
                <TableCell colSpan={6} className='text-muted-foreground h-24 !bg-[var(--table-background)] text-center'>
                  {t('cpa.usage.empty')}
                </TableCell>
              </TableRow>
            ) : (
              events.map((event) => (
                <TableRow key={event.id} className='table-row-hover border-0 !bg-[var(--table-background)]'>
                  <TableCell className={`${CELL_CLASS} text-xs whitespace-nowrap`}>{new Date(event.requestedAt).toLocaleString(locale)}</TableCell>
                  <TableCell className={`${CELL_CLASS} text-sm`}>{event.credentialName || event.authIndex || '-'}</TableCell>
                  <TableCell className={`${CELL_CLASS} text-sm`}>{providerLabel(event.provider, t)}</TableCell>
                  <TableCell className={CELL_CLASS}>
                    <div className='flex flex-wrap items-center gap-1.5'>
                      <span className='font-mono text-xs'>{event.model || '-'}</span>
                      <UpstreamModelHint routedModel={event.model} responseModel={event.responseModel} />
                    </div>
                  </TableCell>
                  <TableCell className={`${CELL_CLASS} font-mono text-xs whitespace-nowrap`}>
                    ↓{event.inputTokens} ↑{event.outputTokens}
                    {event.cachedTokens > 0 && <span className='text-muted-foreground'> ⚡{event.cachedTokens}</span>}
                  </TableCell>
                  <TableCell className={CELL_CLASS}>
                    {event.failed ? (
                      <Badge className='border-red-200 bg-red-100 text-red-800 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300'>
                        {t('cpa.usage.status.failed')}
                      </Badge>
                    ) : (
                      <Badge className='border-green-200 bg-green-100 text-green-800 dark:border-green-800 dark:bg-green-900/20 dark:text-green-300'>
                        {t('cpa.usage.status.success')}
                      </Badge>
                    )}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <div className='flex-shrink-0'>
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
    </>
  );
}
