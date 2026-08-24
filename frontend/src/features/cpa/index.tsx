import { Fragment, useEffect, useMemo, useState } from 'react';
import type { TFunction } from 'i18next';
import { IconPlus } from '@tabler/icons-react';
import { AlertTriangle, ChevronDown, ChevronRight, Pencil, Power, PowerOff, RefreshCw, Server, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '@/hooks/use-debounce';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { ServerSidePagination } from '@/components/server-side-pagination';
import { QuotaWindowsBlock } from '@/components/quota-capsule';
import { CPAInstanceDialog } from './components/instance-dialog';
import { CPAProviderTabs } from './components/provider-tabs';
import { CPAToolbar } from './components/cpa-toolbar';
import { QuotaSummaryCapsule } from './components/quota-summary-capsule';
import { formatTime, cpaQuotaItemsToWindows } from './quota-windows';
import { planLabel, providerLabel } from './labels';
import {
  CPAInstance,
  CPACredential,
  SUPPORTED_QUOTA_PROVIDERS,
  compareCPAProviders,
  useCPAInstances,
  useCPAOverview,
  useCPAPlanTypes,
  useCPACredentials,
  useDeleteCPAInstance,
  useRefreshCPACredential,
  useRefreshCPAInstance,
  useToggleCPACredential,
} from './data';

function versionBelow(version: string, minimum: string) {
  const parse = (value: string) =>
    value
      .replace(/^v/, '')
      .split(/[+-]/)[0]
      .split('.')
      .map((part) => Number(part) || 0);
  const left = parse(version);
  const right = parse(minimum);
  for (let index = 0; index < Math.max(left.length, right.length); index += 1) {
    if ((left[index] ?? 0) < (right[index] ?? 0)) return true;
    if ((left[index] ?? 0) > (right[index] ?? 0)) return false;
  }
  return false;
}

const CPA_TABLE_PAGE_SIZE_KEY = 'cpa-table-page-size';
const CPA_TABLE_PAGE_SIZES = [10, 20, 30, 40, 50] as const;

// Default page size is intentionally 50 (raised from 20): credential tables
// routinely hold hundreds of rows and quota capsules make rows tall, so fewer
// pages beats denser paging. Invalid stored values also clamp to 50.
function clampTablePageSize(value: number): number {
  return (CPA_TABLE_PAGE_SIZES as readonly number[]).includes(value) ? value : 50;
}

function readTablePageSize(): number {
  try {
    return clampTablePageSize(Number(localStorage.getItem(CPA_TABLE_PAGE_SIZE_KEY)));
  } catch {
    // Private mode and blocked storage throw here; fall back to the default.
    return 50;
  }
}

function writeTablePageSize(value: number) {
  try {
    localStorage.setItem(CPA_TABLE_PAGE_SIZE_KEY, String(clampTablePageSize(value)));
  } catch {
    // Best-effort persistence; failing to store the preference is harmless.
  }
}

// percent formats a backend-normalized percentage. CPA quota adapters always
// emit 0-100 percent values (see percentPointersFromUsed in quota.go), so no
// fraction-to-percent rescaling is applied here; applying one would double
// quotaStateText returns the static status copy for a credential whose quota
// cannot be rendered as windows (unsupported / insufficient / pending).
function quotaStateText(credential: CPACredential, t: TFunction): string | null {
  if (credential.quotaState === 'unsupported') return t('cpa.quota.unsupported');
  if (credential.quotaState === 'insufficient_data') return t('cpa.quota.insufficient');
  if (credential.quotaState === 'pending') return t('cpa.quota.pending');
  return null;
}

// Table quota column: status copy for unsupported/incomplete states, a compact
// capsule for the primary window otherwise (multi-window rows show a +N hint).
function QuotaSummaryCell({ credential }: { credential: CPACredential }) {
  const { t } = useTranslation();
  const stateText = quotaStateText(credential, t);
  if (stateText) return <span>{stateText}</span>;
  // Error state takes priority even when partial data exists.
  if (credential.quotaState === 'error') return <span>{t('cpa.quota.error')}</span>;
  if (credential.quotaData.items.length === 0) return <span>—</span>;
  return <QuotaSummaryCapsule items={credential.quotaData.items} fallback={<span>—</span>} />;
}

export default function CPAManagement() {
  const { t, i18n } = useTranslation();
  const { hasSystemScope } = usePermissions();
  const canWrite = hasSystemScope('write_settings');
  const instancesQuery = useCPAInstances();
  const instances = instancesQuery.data ?? [];
  const [selectedInstanceID, setSelectedInstanceID] = useState<number>();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingInstance, setEditingInstance] = useState<CPAInstance>();
  const [deletingInstance, setDeletingInstance] = useState<CPAInstance>();
  const [search, setSearch] = useState('');
  const [provider, setProvider] = useState('all');
  const [statuses, setStatuses] = useState<string[]>([]);
  const [planTypes, setPlanTypes] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState(readTablePageSize);
  const [after, setAfter] = useState<string>();
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([]);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const debouncedSearch = useDebounce(search, 300);

  useEffect(() => {
    if (!selectedInstanceID && instances.length > 0) setSelectedInstanceID(instances[0].id);
    if (selectedInstanceID && !instances.some((instance) => instance.id === selectedInstanceID)) {
      setSelectedInstanceID(instances[0]?.id);
    }
  }, [instances, selectedInstanceID]);

  const selectedInstance = instances.find((instance) => instance.id === selectedInstanceID);
  const overviewQuery = useCPAOverview(selectedInstanceID);
  const providerCounts = overviewQuery.data?.cpaProviderCounts ?? [];
  const providers = useMemo(() => {
    return providerCounts.filter((item) => item.count > 0).map((item) => item.provider).sort(compareCPAProviders);
  }, [providerCounts]);

  useEffect(() => {
    if (provider !== 'all' && !providers.includes(provider)) setProvider('all');
  }, [provider, providers]);

  const planTypesQuery = useCPAPlanTypes(selectedInstanceID, provider === 'all' ? undefined : provider);
  const credentialsQuery = useCPACredentials(
    selectedInstanceID
      ? {
          instanceID: selectedInstanceID,
          first: pageSize,
          after,
          search: debouncedSearch || undefined,
          provider: provider === 'all' ? undefined : provider,
          statuses,
          planTypes: provider === 'all' ? [] : planTypes,
          abnormalOnly: false,
        }
      : undefined
  );
  const refreshInstance = useRefreshCPAInstance();
  const refreshCredential = useRefreshCPACredential();
  const toggleCredential = useToggleCPACredential();
  const deleteInstance = useDeleteCPAInstance();
  const credentials = credentialsQuery.data?.edges.map((edge) => edge.node) ?? [];
  const stats = overviewQuery.data?.cpaCredentialStats;
  const [togglingCredential, setTogglingCredential] = useState<{ id: number; displayName: string; disable: boolean }>();

  const resetPagination = () => {
    setAfter(undefined);
    setCursorHistory([]);
  };

  const changeProvider = (value: string) => {
    setProvider(value);
    setPlanTypes([]);
    resetPagination();
  };

  const toggleExpanded = (id: number) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const refreshSelectedScope = () => {
    if (!selectedInstanceID) return;
    refreshInstance.mutate({ instanceID: selectedInstanceID, provider: provider === 'all' ? undefined : provider });
  };

  const confirmDeleteInstance = async () => {
    if (!deletingInstance) return;
    try {
      await deleteInstance.mutateAsync(deletingInstance.id);
      setDeletingInstance(undefined);
    } catch {
      // The mutation hook displays the error and keeps the confirmation available.
    }
  };

  const confirmToggleCredential = async () => {
    if (!togglingCredential) return;
    try {
      await toggleCredential.mutateAsync({ credentialID: togglingCredential.id, disabled: togglingCredential.disable });
      setTogglingCredential(undefined);
    } catch {
      // The mutation hook displays the error and keeps the confirmation available.
    }
  };

  return (
    <>
      <Header fixed>
        <div className='flex w-full flex-1 flex-col gap-2 md:flex-row md:items-center md:justify-between md:gap-0'>
          <div className='min-w-0'>
            <h2 className='text-xl font-bold tracking-tight'>{t('cpa.title')}</h2>
            <p className='text-muted-foreground text-sm'>{t('cpa.description')}</p>
          </div>
          {canWrite && (
            <Button
              className='space-x-1'
              onClick={() => {
                setEditingInstance(undefined);
                setDialogOpen(true);
              }}
            >
              <span>{t('cpa.instance.add')}</span>
              <IconPlus size={18} />
            </Button>
          )}
        </div>
      </Header>

      <Main fixed>
        <div className='flex h-full flex-col gap-4 overflow-hidden'>
          <div className='flex flex-wrap items-center gap-2'>
            <Select
              value={selectedInstanceID?.toString()}
              onValueChange={(value) => {
                setSelectedInstanceID(Number(value));
                resetPagination();
              }}
            >
              <SelectTrigger className='w-[260px]'>
                <Server className='mr-2 h-4 w-4' />
                <SelectValue placeholder={t('cpa.instance.select')} />
              </SelectTrigger>
              <SelectContent>
                {instances.map((instance) => (
                  <SelectItem key={instance.id} value={instance.id.toString()}>
                    {instance.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {selectedInstance && canWrite && (
              <>
                <Button
                  variant='outline'
                  size='icon'
                  title={t('common.buttons.edit')}
                  onClick={() => {
                    setEditingInstance(selectedInstance);
                    setDialogOpen(true);
                  }}
                >
                  <Pencil className='h-4 w-4' />
                </Button>
                <Button
                  variant='outline'
                  size='icon'
                  title={t('common.buttons.delete')}
                  onClick={() => setDeletingInstance(selectedInstance)}
                >
                  <Trash2 className='text-destructive h-4 w-4' />
                </Button>
              </>
            )}
            {selectedInstance && (
              <Badge variant={selectedInstance.enabled ? 'default' : 'secondary'}>
                {selectedInstance.enabled ? t('cpa.status.enabled') : t('cpa.status.disabled')}
              </Badge>
            )}
            {selectedInstance?.serverVersion && <Badge variant='outline'>CPA {selectedInstance.serverVersion}</Badge>}
            {selectedInstance && (
              <div className='text-muted-foreground ml-auto flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-sm'>
                <span className='shrink-0'>
                  {t('cpa.stats.available')}{' '}
                  <span className='text-foreground font-medium tabular-nums'>
                    {stats?.available ?? '—'} / {stats?.total ?? '—'}
                  </span>
                </span>
                <span className='shrink-0'>
                  {t('cpa.stats.abnormal')}{' '}
                  <span className={`font-medium tabular-nums ${(stats?.abnormal ?? 0) > 0 ? 'text-destructive' : 'text-foreground'}`}>
                    {stats?.abnormal ?? '—'}
                  </span>
                </span>
                <span className='min-w-0 truncate' title={formatTime(selectedInstance.lastSyncSuccessAt)}>
                  {t('cpa.stats.lastSync')}{' '}
                  <span className='text-foreground font-medium'>{formatTime(selectedInstance.lastSyncSuccessAt)}</span>
                </span>
              </div>
            )}
          </div>

          {instances.length === 0 && !instancesQuery.isLoading && (
            <div className='flex flex-1 flex-col items-center justify-center rounded-xl border border-dashed p-8 text-center'>
              <Server className='text-muted-foreground mb-4 h-10 w-10' />
              <h3 className='font-semibold'>{t('cpa.empty.title')}</h3>
              <p className='text-muted-foreground mt-1 max-w-lg text-sm'>{t('cpa.empty.description')}</p>
              {canWrite && (
                <Button className='mt-4 space-x-1' onClick={() => setDialogOpen(true)}>
                  <span>{t('cpa.instance.add')}</span>
                  <IconPlus size={18} />
                </Button>
              )}
            </div>
          )}

          {selectedInstance?.insecureSkipTLS && (
            <Alert variant='destructive'>
              <AlertTriangle className='h-4 w-4' />
              <AlertTitle>{t('cpa.warnings.insecureTLS.title')}</AlertTitle>
              <AlertDescription>{t('cpa.warnings.insecureTLS.active')}</AlertDescription>
            </Alert>
          )}
          {selectedInstance?.serverVersion && versionBelow(selectedInstance.serverVersion, '7.1.0') && (
            <Alert>
              <AlertTriangle className='h-4 w-4' />
              <AlertTitle>{t('cpa.warnings.oldVersion.title')}</AlertTitle>
              <AlertDescription>{t('cpa.warnings.oldVersion.description', { version: selectedInstance.serverVersion })}</AlertDescription>
            </Alert>
          )}
          {selectedInstance?.connectionStatus === 'error' && (
            <Alert variant='destructive'>
              <AlertTriangle className='h-4 w-4' />
              <AlertTitle>{t('cpa.warnings.connection.title')}</AlertTitle>
              <AlertDescription>{selectedInstance.lastError ?? t('cpa.warnings.connection.description')}</AlertDescription>
            </Alert>
          )}

          {selectedInstance && (
            <>
              <CPAProviderTabs
                providers={providers}
                providerCounts={providerCounts}
                totalCount={stats?.total ?? 0}
                selectedProvider={provider}
                onProviderChange={changeProvider}
              />

              <CPAToolbar
                search={search}
                onSearchChange={(value) => {
                  setSearch(value);
                  resetPagination();
                }}
                statuses={statuses}
                onStatusesChange={(values) => {
                  setStatuses(values);
                  resetPagination();
                }}
                provider={provider}
                selectedPlanTypes={planTypes}
                planTypes={planTypesQuery.data ?? []}
                onSelectedPlanTypesChange={(values) => {
                  setPlanTypes(values);
                  resetPagination();
                }}
                refresh={{
                  canRefresh:
                    canWrite &&
                    selectedInstance.enabled &&
                    (provider === 'all' || SUPPORTED_QUOTA_PROVIDERS.has(provider)),
                  pending: refreshInstance.isPending,
                  onRefresh: refreshSelectedScope,
                }}
              />

              <div className='shadow-soft relative min-h-0 flex-1 overflow-auto rounded-2xl border border-[var(--table-border)]'>
                <div className='min-w-max'>
                  <Table className='border-separate border-spacing-0 rounded-2xl bg-[var(--table-background)]'>
                    <TableHeader className='sticky top-0 z-20 bg-[var(--table-header)] shadow-sm'>
                      <TableRow className='group/row border-0'>
                        <TableHead className='text-muted-foreground w-10 border-0 text-xs font-semibold tracking-wider uppercase' />
                        <TableHead className='text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase'>
                          {t('cpa.columns.credential')}
                        </TableHead>
                        <TableHead className='text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase'>
                          {t('cpa.columns.provider')}
                        </TableHead>
                        <TableHead className='text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase'>
                          {t('cpa.columns.status')}
                        </TableHead>
                        <TableHead className='text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase'>
                          {t('cpa.columns.quota')}
                        </TableHead>
                        <TableHead className='text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase'>
                          {t('cpa.columns.refreshedAt')}
                        </TableHead>
                        <TableHead className='text-muted-foreground w-24 border-0 text-xs font-semibold tracking-wider uppercase' />
                      </TableRow>
                    </TableHeader>
                    <TableBody className='!bg-[var(--table-background)]'>
                      {credentialsQuery.isLoading ? (
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
                            <TableRow className='group/row table-row-hover rounded-xl border-0 !bg-[var(--table-background)]'>
                              <TableCell className='border-0 bg-inherit px-4 py-3'>
                                <Button variant='ghost' size='icon' onClick={() => toggleExpanded(credential.id)}>
                                  {expanded.has(credential.id) ? <ChevronDown className='h-4 w-4' /> : <ChevronRight className='h-4 w-4' />}
                                </Button>
                              </TableCell>
                              <TableCell className='border-0 bg-inherit px-4 py-3'>
                                <Tooltip>
                                  <TooltipTrigger asChild>
                                    <div className='font-medium max-w-[200px] cursor-default truncate'>
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
                                      <p className='text-destructive line-clamp-2 max-w-[240px] cursor-default break-all text-xs'>
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
                              <TableCell className='border-0 bg-inherit px-4 py-3'>
                                <div className='flex items-center gap-1'>
                                  <Button
                                    variant='ghost'
                                    size='icon'
                                    title={t('common.refresh')}
                                    disabled={
                                      !canWrite ||
                                      credential.quotaState === 'unsupported' ||
                                      !SUPPORTED_QUOTA_PROVIDERS.has(credential.provider) ||
                                      refreshCredential.isPending
                                    }
                                    onClick={() => refreshCredential.mutate(credential.id)}
                                  >
                                    <RefreshCw className='h-4 w-4' />
                                  </Button>
                                  {canWrite && (
                                    <Button
                                      variant='ghost'
                                      size='icon'
                                      title={credential.disabled ? t('cpa.credential.enable') : t('cpa.credential.disable')}
                                      disabled={toggleCredential.isPending}
                                      onClick={() =>
                                        setTogglingCredential({
                                          id: credential.id,
                                          displayName: credential.email || credential.displayName || credential.remoteName,
                                          disable: !credential.disabled,
                                        })
                                      }
                                    >
                                      {credential.disabled ? <Power className='h-4 w-4' /> : <PowerOff className='text-destructive h-4 w-4' />}
                                    </Button>
                                  )}
                                </div>
                              </TableCell>
                            </TableRow>
                            {expanded.has(credential.id) && (
                              <TableRow className='border-0'>
                                <TableCell colSpan={7} className='bg-muted/30 border-0 p-4'>
                                  <div className='grid gap-3'>
                                    {credential.quotaData.items.length > 0 ? (
                                      <QuotaWindowsBlock
                                        windows={cpaQuotaItemsToWindows(
                                          credential.quotaData.items,
                                          t,
                                          i18n.language === 'zh' ? 'zh-CN' : 'en-US'
                                        )}
                                      />
                                    ) : (
                                      <p className='text-muted-foreground text-sm'>
                                        {quotaStateText(credential, t) ??
                                          (credential.quotaState === 'error' ? t('cpa.quota.error') : '—')}
                                      </p>
                                    )}
                                  </div>
                                </TableCell>
                              </TableRow>
                            )}
                          </Fragment>
                        ))
                      )}
                    </TableBody>
                  </Table>
                </div>
              </div>

              <div className='flex-shrink-0'>
                <ServerSidePagination
                  pageInfo={credentialsQuery.data?.pageInfo}
                  pageSize={pageSize}
                  dataLength={credentials.length}
                  totalCount={credentialsQuery.data?.totalCount}
                  selectedRows={0}
                  onNextPage={() => {
                    if (!credentialsQuery.data?.pageInfo.hasNextPage || !credentialsQuery.data.pageInfo.endCursor) return;
                    setCursorHistory((history) => [...history, after]);
                    setAfter(credentialsQuery.data.pageInfo.endCursor ?? undefined);
                  }}
                  onPreviousPage={() => {
                    const history = [...cursorHistory];
                    setAfter(history.pop());
                    setCursorHistory(history);
                  }}
                  onPageSizeChange={(size) => {
                    const next = clampTablePageSize(size);
                    setPageSize(next);
                    writeTablePageSize(next);
                    resetPagination();
                  }}
                  selectedInfoLabel={t('cpa.pagination.total', { count: credentialsQuery.data?.totalCount ?? 0 })}
                  onResetCursor={resetPagination}
                />
              </div>
            </>
          )}
        </div>
      </Main>

      <CPAInstanceDialog
        open={dialogOpen}
        instance={editingInstance}
        onOpenChange={setDialogOpen}
        onSaved={(instance) => setSelectedInstanceID(instance.id)}
      />
      <AlertDialog
        open={Boolean(togglingCredential)}
        onOpenChange={(open) => {
          if (!open) setTogglingCredential(undefined);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {togglingCredential?.disable ? t('cpa.credential.disableTitle') : t('cpa.credential.enableTitle')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {togglingCredential?.disable
                ? t('cpa.credential.disableDescription', { name: togglingCredential?.displayName })
                : t('cpa.credential.enableDescription', { name: togglingCredential?.displayName })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.buttons.cancel')}</AlertDialogCancel>
            <Button variant={togglingCredential?.disable ? 'destructive' : 'default'} onClick={confirmToggleCredential} disabled={toggleCredential.isPending}>
              {togglingCredential?.disable ? t('cpa.credential.disable') : t('cpa.credential.enable')}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog
        open={Boolean(deletingInstance)}
        onOpenChange={(open) => {
          if (!open) setDeletingInstance(undefined);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('cpa.instance.deleteTitle')}</AlertDialogTitle>
            <AlertDialogDescription>{t('cpa.instance.deleteDescription', { name: deletingInstance?.name })}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.buttons.cancel')}</AlertDialogCancel>
            <Button variant='destructive' onClick={confirmDeleteInstance} disabled={deleteInstance.isPending}>
              {t('common.buttons.delete')}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
