import { Fragment } from 'react';
import { AnimatePresence, motion } from 'framer-motion';
import { ChevronDown, ChevronRight, Power, PowerOff, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { QuotaWindowsBlock } from '@/components/quota-capsule';
import { ServerSidePagination } from '@/components/server-side-pagination';
import { planLabel, providerLabel } from '../labels';
import { cpaQuotaItemsToWindows, formatTime } from '../quota-windows';
import { SUPPORTED_QUOTA_PROVIDERS } from '../types';
import type { CPACredential, CPACredentialConnection } from '../types';
import { QuotaSummaryCapsule } from './quota-summary-capsule';

const MotionExpandedRow = motion.create(TableRow);

interface CPACredentialTableProps {
  credentials: CPACredential[];
  pageInfo?: CPACredentialConnection['pageInfo'];
  totalCount?: number;
  isLoading: boolean;
  pageSize: number;
  expanded: Set<number>;
  canWrite: boolean;
  refreshPending: boolean;
  togglePending: boolean;
  locale: string;
  onToggleExpanded: (id: number) => void;
  onRefreshCredential: (id: number) => void;
  onRequestToggle: (credential: CPACredential) => void;
  onNextPage: () => void;
  onPreviousPage: () => void;
  onPageSizeChange: (size: number) => void;
  onResetCursor: () => void;
}

function quotaStateText(credential: CPACredential, t: ReturnType<typeof useTranslation>['t']): string | null {
  if (credential.quotaState === 'unsupported') return t('cpa.quota.unsupported');
  if (credential.quotaState === 'insufficient_data') return t('cpa.quota.insufficient');
  if (credential.quotaState === 'pending') return t('cpa.quota.pending');
  return null;
}

function QuotaSummaryCell({ credential }: { credential: CPACredential }) {
  const { t } = useTranslation();
  const stateText = quotaStateText(credential, t);
  if (stateText) return <span>{stateText}</span>;
  if (credential.quotaState === 'error') return <span>{t('cpa.quota.error')}</span>;
  if (credential.quotaData.items.length === 0) return <span>—</span>;
  return <QuotaSummaryCapsule items={credential.quotaData.items} provider={credential.provider} fallback={<span>—</span>} />;
}

export function CPACredentialTable({
  credentials,
  pageInfo,
  totalCount,
  isLoading,
  pageSize,
  expanded,
  canWrite,
  refreshPending,
  togglePending,
  locale,
  onToggleExpanded,
  onRefreshCredential,
  onRequestToggle,
  onNextPage,
  onPreviousPage,
  onPageSizeChange,
  onResetCursor,
}: CPACredentialTableProps) {
  const { t } = useTranslation();

  return (
    <>
      <div
        data-testid='cpa-credential-table'
        className='shadow-soft relative min-h-0 flex-1 overflow-auto rounded-2xl border border-[var(--table-border)]'
      >
        <div className='min-w-max'>
          <Table className='border-separate border-spacing-0 rounded-2xl bg-[var(--table-background)]'>
            {/* Column widths: the fixed-content columns are pinned to what their
                content needs, so the leftover width lands in the quota column
                and is absorbed by its bars instead of showing up as dead space
                after the email and next to the refreshed-at column. */}
            <TableHeader className='sticky top-0 z-20 bg-[var(--table-header)] shadow-sm'>
              <TableRow className='group/row border-0'>
                <TableHead className='text-muted-foreground w-10 border-0 text-xs font-semibold tracking-wider uppercase' />
                <TableHead className='text-muted-foreground w-[240px] border-0 text-xs font-semibold tracking-wider uppercase'>
                  {t('cpa.columns.credential')}
                </TableHead>
                <TableHead className='text-muted-foreground w-[150px] border-0 text-xs font-semibold tracking-wider uppercase'>
                  {t('cpa.columns.provider')}
                </TableHead>
                <TableHead className='text-muted-foreground w-[120px] border-0 text-xs font-semibold tracking-wider uppercase'>
                  {t('cpa.columns.status')}
                </TableHead>
                <TableHead className='text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase'>
                  {t('cpa.columns.quota')}
                </TableHead>
                <TableHead className='text-muted-foreground w-[170px] border-0 text-xs font-semibold tracking-wider uppercase'>
                  {t('cpa.columns.refreshedAt')}
                </TableHead>
                <TableHead className='text-muted-foreground sticky right-0 z-30 w-24 border-0 bg-[var(--table-header)] text-xs font-semibold tracking-wider uppercase shadow-[-1px_0_0_0_var(--border)]' />
              </TableRow>
            </TableHeader>
            <TableBody className='!bg-[var(--table-background)]'>
              {isLoading ? (
                <TableSkeleton rows={pageSize} />
              ) : credentials.length === 0 ? (
                <TableRow className='!bg-[var(--table-background)]'>
                  <TableCell colSpan={7} className='h-24 !bg-[var(--table-background)] text-center'>
                    {t('common.noData')}
                  </TableCell>
                </TableRow>
              ) : (
                credentials.map((credential) => (
                  <Fragment key={credential.id}>
                    <TableRow
                      data-testid={`cpa-credential-row-${credential.id}`}
                      className='group/row table-row-hover rounded-xl border-0 !bg-[var(--table-background)] hover:!transform-none'
                    >
                      <TableCell className='border-0 bg-inherit px-4 py-3'>
                        <Button variant='ghost' size='icon' onClick={() => onToggleExpanded(credential.id)}>
                          {expanded.has(credential.id) ? <ChevronDown className='h-4 w-4' /> : <ChevronRight className='h-4 w-4' />}
                        </Button>
                      </TableCell>
                      <TableCell className='border-0 bg-inherit px-4 py-3'>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <div className='max-w-[200px] cursor-default truncate font-medium'>
                              {credential.email || credential.displayName || '—'}
                            </div>
                          </TooltipTrigger>
                          <TooltipContent side='top'>
                            {credential.remoteName}
                            {credential.displayName && credential.displayName !== credential.remoteName
                              ? ` · ${credential.displayName}`
                              : ''}
                          </TooltipContent>
                        </Tooltip>
                      </TableCell>
                      <TableCell className='border-0 bg-inherit px-4 py-3'>
                        <div>{providerLabel(credential.provider, t)}</div>
                        {credential.planType && (
                          <Badge variant='outline' className='mt-1'>
                            {planLabel(credential.provider, credential.planType, t)}
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell className='border-0 bg-inherit px-4 py-3'>
                        <div className='flex flex-col items-start gap-1'>
                          {credential.disabled && <Badge variant='secondary'>{t('cpa.status.disabled')}</Badge>}
                          {credential.unavailable && <Badge variant='destructive'>{t('cpa.status.unavailable')}</Badge>}
                          {credential.abnormal && <Badge variant='destructive'>{t('cpa.status.abnormal')}</Badge>}
                          {credential.expired && <Badge variant='destructive'>{t('cpa.status.expired')}</Badge>}
                          {credential.cooling &&
                            (credential.cooldownUntil ? (
                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Badge variant='secondary'>{t('cpa.status.cooldown')}</Badge>
                                </TooltipTrigger>
                                <TooltipContent side='top'>
                                  {t('cpa.status.cooldownUntil', { time: formatTime(credential.cooldownUntil) })}
                                </TooltipContent>
                              </Tooltip>
                            ) : (
                              <Badge variant='secondary'>{t('cpa.status.cooldown')}</Badge>
                            ))}
                          {credential.available && <Badge>{t('cpa.status.available')}</Badge>}
                          {credential.stale && <Badge variant='outline'>{t('cpa.status.stale')}</Badge>}
                        </div>
                        {credential.statusMessage && (
                          <p className='text-muted-foreground mt-1 max-w-[220px] truncate text-xs' title={credential.statusMessage}>
                            {credential.statusMessage}
                          </p>
                        )}
                      </TableCell>
                      <TableCell className='border-0 bg-inherit px-4 py-3'>
                        <span className={credential.quotaState === 'error' ? 'text-destructive' : ''}>
                          <QuotaSummaryCell credential={credential} />
                        </span>
                        {credential.quotaLastError && (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <p className='text-destructive line-clamp-2 max-w-[240px] cursor-default text-xs break-all'>
                                {credential.quotaLastError}
                              </p>
                            </TooltipTrigger>
                            <TooltipContent side='top' className='max-w-80 break-all'>
                              {credential.quotaLastError}
                            </TooltipContent>
                          </Tooltip>
                        )}
                      </TableCell>
                      <TableCell className='text-muted-foreground border-0 bg-inherit px-4 py-3 text-sm'>
                        {formatTime(credential.quotaLastSuccessAt ?? credential.quotaLastAttemptAt)}
                      </TableCell>
                      <TableCell className='sticky right-0 z-10 border-0 bg-inherit px-4 py-3 shadow-[-1px_0_0_0_var(--border)]'>
                        <div className='flex items-center justify-center gap-1'>
                          <Button
                            data-testid={`cpa-refresh-credential-${credential.id}`}
                            variant='outline'
                            size='sm'
                            className='h-8 w-8 p-0'
                            title={t('common.refresh')}
                            disabled={
                              !canWrite ||
                              credential.quotaState === 'unsupported' ||
                              !SUPPORTED_QUOTA_PROVIDERS.has(credential.provider) ||
                              refreshPending
                            }
                            onClick={() => onRefreshCredential(credential.id)}
                          >
                            <RefreshCw className='h-4 w-4' />
                          </Button>
                          {canWrite && (
                            <Button
                              data-testid={`cpa-toggle-credential-${credential.id}`}
                              variant='outline'
                              size='sm'
                              className='h-8 w-8 p-0'
                              title={credential.disabled ? t('cpa.credential.enable') : t('cpa.credential.disable')}
                              disabled={togglePending}
                              onClick={() => onRequestToggle(credential)}
                            >
                              {credential.disabled ? <Power className='h-4 w-4' /> : <PowerOff className='text-destructive h-4 w-4' />}
                            </Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                    <AnimatePresence initial={false}>
                      {expanded.has(credential.id) && (
                        <MotionExpandedRow
                          key={`${credential.id}-expanded`}
                          initial={{ opacity: 0 }}
                          animate={{ opacity: 1 }}
                          exit={{ opacity: 0 }}
                          className='border-0'
                        >
                          <TableCell colSpan={6} className='border-0 p-0'>
                            <motion.div
                              initial={{ height: 0, opacity: 0 }}
                              animate={{ height: 'auto', opacity: 1 }}
                              exit={{ height: 0, opacity: 0 }}
                              transition={{ duration: 0.2, ease: 'easeInOut' }}
                              className='overflow-hidden'
                            >
                              {/* Band and padding live on an inner element: the animated
                                  wrapper must stay padding-free or a collapsed row keeps
                                  the padding as an empty strip. */}
                              <div className='bg-muted/30 hover:bg-muted/50 p-6'>
                                {credential.quotaData.items.length > 0 ? (
                                  <QuotaWindowsBlock windows={cpaQuotaItemsToWindows(credential.quotaData.items, t, locale)} />
                                ) : (
                                  <p className='text-muted-foreground text-sm'>
                                    {quotaStateText(credential, t) ?? (credential.quotaState === 'error' ? t('cpa.quota.error') : '—')}
                                  </p>
                                )}
                              </div>
                            </motion.div>
                          </TableCell>
                          {/* Frozen strip for the expanded row: without it the actions
                              column stops at the panel and its divider breaks. Opaque
                              band tone (mixed, not translucent) so scrolling content
                              stays hidden behind it. */}
                          <TableCell className='sticky right-0 z-10 border-0 bg-[color-mix(in_srgb,var(--muted)_30%,var(--card))] p-0 shadow-[-1px_0_0_0_var(--border)] hover:bg-[color-mix(in_srgb,var(--muted)_50%,var(--card))]' />
                        </MotionExpandedRow>
                      )}
                    </AnimatePresence>
                  </Fragment>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      <div className='flex-shrink-0'>
        <ServerSidePagination
          pageInfo={pageInfo}
          pageSize={pageSize}
          dataLength={credentials.length}
          totalCount={totalCount}
          selectedRows={0}
          onNextPage={onNextPage}
          onPreviousPage={onPreviousPage}
          onPageSizeChange={onPageSizeChange}
          selectedInfoLabel={t('cpa.pagination.total', { count: totalCount ?? 0 })}
          onResetCursor={onResetCursor}
          testIdPrefix='cpa-pagination'
        />
      </div>
    </>
  );
}
