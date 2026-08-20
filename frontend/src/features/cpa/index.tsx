import { Fragment, useEffect, useMemo, useState } from 'react';
import type { TFunction } from 'i18next';
import { AlertTriangle, ChevronDown, ChevronRight, Pencil, Plus, RefreshCw, Server, Trash2 } from 'lucide-react';
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
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { CPAInstanceDialog } from './components/instance-dialog';
import {
  CPAInstance,
  CPACredential,
  CPAQuotaItem,
  useCPAInstances,
  useCPAOverview,
  useCPAPlanTypes,
  useCPACredentials,
  useDeleteCPAInstance,
  useRefreshCPACredential,
  useRefreshCPAInstance,
} from './data';

const KNOWN_PROVIDER_ORDER = ['codex', 'claude', 'antigravity', 'kimi', 'xai'];
const SUPPORTED_QUOTA_PROVIDERS = new Set(KNOWN_PROVIDER_ORDER);

function formatTime(value?: string | null) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function providerLabel(provider: string, t: TFunction) {
  return t(`cpa.providerLabels.${provider}`, { defaultValue: provider });
}

function planLabel(provider: string, plan: string, t: TFunction) {
  return t(`cpa.planLabels.${provider}.${plan.toLowerCase()}`, { defaultValue: plan });
}

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

function readTablePageSize(): number {
  try {
    return Number(localStorage.getItem(CPA_TABLE_PAGE_SIZE_KEY)) || 20;
  } catch {
    // Private mode and blocked storage throw here; fall back to the default.
    return 20;
  }
}

function writeTablePageSize(value: number) {
  try {
    localStorage.setItem(CPA_TABLE_PAGE_SIZE_KEY, String(value));
  } catch {
    // Best-effort persistence; failing to store the preference is harmless.
  }
}

// percent formats a backend-normalized percentage. CPA quota adapters always
// emit 0-100 percent values (see percentPointersFromUsed in quota.go), so no
// fraction-to-percent rescaling is applied here; applying one would double
// scale values below 1%.
function percent(value?: number | null) {
  if (value == null) return null;
  const normalized = Math.max(0, Math.min(100, value));
  return `${normalized.toFixed(normalized < 10 ? 1 : 0)}%`;
}

function quotaSummary(credential: CPACredential, t: TFunction) {
  const items = credential.quotaData.items;
  if (credential.quotaState === 'unsupported') return t('cpa.quota.unsupported');
  if (credential.quotaState === 'insufficient_data') return t('cpa.quota.insufficient');
  if (credential.quotaState === 'pending') return t('cpa.quota.pending');
  if (items.length === 0) return credential.quotaState === 'error' ? t('cpa.quota.error') : '—';
  const item = items[0];
  const remaining = percent(item.remainingPercent);
  if (remaining) return items.length > 1 ? `${remaining} · ${t('cpa.quota.windows', { count: items.length })}` : remaining;
  if (item.remaining != null && item.limit != null) return `${item.remaining}/${item.limit} ${item.unit}`.trim();
  if (item.remaining != null) return `${item.remaining} ${item.unit}`.trim();
  return items.length > 1 ? t('cpa.quota.windows', { count: items.length }) : item.label;
}

function QuotaDetail({ item }: { item: CPAQuotaItem }) {
  const { t } = useTranslation();
  return (
    <div className='bg-background rounded-lg border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div>
          <p className='font-medium'>{item.label}</p>
          {item.description && <p className='text-muted-foreground text-xs'>{item.description}</p>}
        </div>
        {item.group && <Badge variant='outline'>{item.group}</Badge>}
      </div>
      <div className='mt-3 grid gap-2 text-sm sm:grid-cols-2 lg:grid-cols-4'>
        <div>
          <span className='text-muted-foreground'>{t('cpa.quota.remaining')}:</span>{' '}
          {percent(item.remainingPercent) ?? item.remaining ?? '—'}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('cpa.quota.used')}:</span> {percent(item.usedPercent) ?? item.used ?? '—'}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('cpa.quota.limit')}:</span> {item.limit ?? '—'} {item.unit}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('cpa.quota.resetAt')}:</span> {formatTime(item.resetAt)}
        </div>
      </div>
    </div>
  );
}

export default function CPAManagement() {
  const { t } = useTranslation();
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
  const [status, setStatus] = useState('all');
  const [planType, setPlanType] = useState('all');
  const [abnormalOnly, setAbnormalOnly] = useState(false);
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
    const values = providerCounts.filter((item) => item.count > 0).map((item) => item.provider);
    return values.sort((left, right) => {
      const leftIndex = KNOWN_PROVIDER_ORDER.indexOf(left);
      const rightIndex = KNOWN_PROVIDER_ORDER.indexOf(right);
      if (leftIndex >= 0 && rightIndex >= 0) return leftIndex - rightIndex;
      if (leftIndex >= 0) return -1;
      if (rightIndex >= 0) return 1;
      return left.localeCompare(right);
    });
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
          statuses: status === 'all' ? [] : [status],
          planTypes: planType === 'all' || provider === 'all' ? [] : [planType],
          abnormalOnly,
        }
      : undefined
  );
  const refreshInstance = useRefreshCPAInstance();
  const refreshCredential = useRefreshCPACredential();
  const deleteInstance = useDeleteCPAInstance();
  const credentials = credentialsQuery.data?.edges.map((edge) => edge.node) ?? [];
  const stats = overviewQuery.data?.cpaCredentialStats;

  const resetPagination = () => {
    setAfter(undefined);
    setCursorHistory([]);
  };

  const changeProvider = (value: string) => {
    setProvider(value);
    setPlanType('all');
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

  return (
    <>
      <Header fixed>
        <div className='flex flex-1 flex-wrap items-center justify-between gap-4'>
          <div>
            <h2 className='text-xl font-bold tracking-tight'>{t('cpa.title')}</h2>
            <p className='text-muted-foreground text-sm'>{t('cpa.description')}</p>
          </div>
          {canWrite && (
            <Button
              onClick={() => {
                setEditingInstance(undefined);
                setDialogOpen(true);
              }}
            >
              <Plus className='mr-2 h-4 w-4' />
              {t('cpa.instance.add')}
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
          </div>

          {instances.length === 0 && !instancesQuery.isLoading && (
            <div className='flex flex-1 flex-col items-center justify-center rounded-xl border border-dashed p-8 text-center'>
              <Server className='text-muted-foreground mb-4 h-10 w-10' />
              <h3 className='font-semibold'>{t('cpa.empty.title')}</h3>
              <p className='text-muted-foreground mt-1 max-w-lg text-sm'>{t('cpa.empty.description')}</p>
              {canWrite && (
                <Button className='mt-4' onClick={() => setDialogOpen(true)}>
                  <Plus className='mr-2 h-4 w-4' />
                  {t('cpa.instance.add')}
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
              <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
                <div className='rounded-xl border p-4'>
                  <p className='text-muted-foreground text-sm'>{t('cpa.stats.available')}</p>
                  <p className='text-2xl font-semibold'>
                    {stats?.available ?? '—'} / {stats?.total ?? '—'}
                  </p>
                </div>
                <div className='rounded-xl border p-4'>
                  <p className='text-muted-foreground text-sm'>{t('cpa.stats.abnormal')}</p>
                  <p className='text-destructive text-2xl font-semibold'>{stats?.abnormal ?? '—'}</p>
                </div>
                <div className='rounded-xl border p-4'>
                  <p className='text-muted-foreground text-sm'>{t('cpa.stats.lastSync')}</p>
                  <p className='truncate text-sm font-medium'>{formatTime(selectedInstance.lastSyncSuccessAt)}</p>
                </div>
              </div>

              <div className='flex gap-1 overflow-x-auto border-b pb-2'>
                <Button size='sm' variant={provider === 'all' ? 'default' : 'ghost'} onClick={() => changeProvider('all')}>
                  {t('common.all')}{' '}
                  <Badge variant='secondary' className='ml-2'>
                    {stats?.total ?? 0}
                  </Badge>
                </Button>
                {providers.map((value) => (
                  <Button key={value} size='sm' variant={provider === value ? 'default' : 'ghost'} onClick={() => changeProvider(value)}>
                    {providerLabel(value, t)}{' '}
                    <Badge variant='secondary' className='ml-2'>
                      {providerCounts.find((item) => item.provider === value)?.count ?? 0}
                    </Badge>
                  </Button>
                ))}
              </div>

              <div className='flex flex-wrap items-center gap-2'>
                <Input
                  className='w-full sm:w-[280px]'
                  placeholder={t('cpa.filters.search')}
                  value={search}
                  onChange={(event) => {
                    setSearch(event.target.value);
                    resetPagination();
                  }}
                />
                <Select
                  value={status}
                  onValueChange={(value) => {
                    setStatus(value);
                    resetPagination();
                  }}
                >
                  <SelectTrigger className='w-[150px]'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='all'>{t('cpa.filters.allStatus')}</SelectItem>
                    <SelectItem value='enabled'>{t('cpa.status.enabled')}</SelectItem>
                    <SelectItem value='disabled'>{t('cpa.status.disabled')}</SelectItem>
                  </SelectContent>
                </Select>
                {provider !== 'all' && (
                  <Select
                    value={planType}
                    onValueChange={(value) => {
                      setPlanType(value);
                      resetPagination();
                    }}
                  >
                    <SelectTrigger className='w-[180px]'>
                      <SelectValue placeholder={t('cpa.filters.plan')} />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='all'>{t('cpa.filters.allPlans')}</SelectItem>
                      {(planTypesQuery.data ?? []).map((plan) => (
                        <SelectItem key={plan} value={plan}>
                          {planLabel(provider, plan, t)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
                <label className='flex items-center gap-2 text-sm'>
                  <Checkbox
                    checked={abnormalOnly}
                    onCheckedChange={(value) => {
                      setAbnormalOnly(value === true);
                      resetPagination();
                    }}
                  />
                  {t('cpa.filters.abnormalOnly')}
                </label>
                <div className='ml-auto'>
                  <Button
                    onClick={refreshSelectedScope}
                    disabled={
                      !canWrite ||
                      !selectedInstance.enabled ||
                      refreshInstance.isPending ||
                      (provider !== 'all' && !SUPPORTED_QUOTA_PROVIDERS.has(provider))
                    }
                  >
                    <RefreshCw className={`mr-2 h-4 w-4 ${refreshInstance.isPending ? 'animate-spin' : ''}`} />
                    {provider === 'all'
                      ? t('cpa.actions.refreshAll')
                      : t('cpa.actions.refreshProvider', { provider: providerLabel(provider, t) })}
                  </Button>
                </div>
              </div>

              <div className='min-h-0 flex-1 overflow-auto rounded-xl border'>
                <Table>
                  <TableHeader className='bg-background sticky top-0 z-10'>
                    <TableRow>
                      <TableHead className='w-10' />
                      <TableHead>{t('cpa.columns.credential')}</TableHead>
                      <TableHead>{t('cpa.columns.provider')}</TableHead>
                      <TableHead>{t('cpa.columns.status')}</TableHead>
                      <TableHead>{t('cpa.columns.quota')}</TableHead>
                      <TableHead>{t('cpa.columns.refreshedAt')}</TableHead>
                      <TableHead className='w-16' />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {credentialsQuery.isLoading ? (
                      <TableRow>
                        <TableCell colSpan={7} className='h-32 text-center'>
                          {t('common.loading')}
                        </TableCell>
                      </TableRow>
                    ) : credentials.length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={7} className='h-32 text-center'>
                          {t('common.noData')}
                        </TableCell>
                      </TableRow>
                    ) : (
                      credentials.map((credential) => (
                        <Fragment key={credential.id}>
                          <TableRow>
                            <TableCell>
                              <Button variant='ghost' size='icon' onClick={() => toggleExpanded(credential.id)}>
                                {expanded.has(credential.id) ? <ChevronDown className='h-4 w-4' /> : <ChevronRight className='h-4 w-4' />}
                              </Button>
                            </TableCell>
                            <TableCell>
                              <div className='font-medium'>{credential.displayName}</div>
                              <div className='text-muted-foreground text-xs'>
                                {credential.remoteName}
                                {credential.email ? ` · ${credential.email}` : ''}
                              </div>
                            </TableCell>
                            <TableCell>
                              <div>{providerLabel(credential.provider, t)}</div>
                              {credential.planType && (
                                <Badge variant='outline' className='mt-1'>
                                  {planLabel(credential.provider, credential.planType, t)}
                                </Badge>
                              )}
                            </TableCell>
                            <TableCell>
                              <div className='flex flex-wrap gap-1'>
                                {credential.disabled && <Badge variant='secondary'>{t('cpa.status.disabled')}</Badge>}
                                {credential.unavailable && <Badge variant='destructive'>{t('cpa.status.unavailable')}</Badge>}
                                {credential.abnormal && <Badge variant='destructive'>{t('cpa.status.abnormal')}</Badge>}
                                {credential.available && <Badge>{t('cpa.status.available')}</Badge>}
                                {credential.stale && <Badge variant='outline'>{t('cpa.status.stale')}</Badge>}
                              </div>
                              {credential.statusMessage && (
                                <p className='text-muted-foreground mt-1 max-w-[220px] truncate text-xs' title={credential.statusMessage}>
                                  {credential.statusMessage}
                                </p>
                              )}
                            </TableCell>
                            <TableCell>
                              <span className={credential.quotaState === 'error' ? 'text-destructive' : ''}>
                                {quotaSummary(credential, t)}
                              </span>
                              {credential.quotaLastError && (
                                <p className='text-destructive max-w-[240px] truncate text-xs' title={credential.quotaLastError}>
                                  {credential.quotaLastError}
                                </p>
                              )}
                            </TableCell>
                            <TableCell className='text-muted-foreground text-sm'>
                              {formatTime(credential.quotaLastSuccessAt ?? credential.quotaLastAttemptAt)}
                            </TableCell>
                            <TableCell>
                              <Button
                                variant='ghost'
                                size='icon'
                                title={t('common.refresh')}
                                disabled={
                                  !canWrite ||
                                  credential.disabled ||
                                  credential.quotaState === 'unsupported' ||
                                  !SUPPORTED_QUOTA_PROVIDERS.has(credential.provider) ||
                                  refreshCredential.isPending
                                }
                                onClick={() => refreshCredential.mutate(credential.id)}
                              >
                                <RefreshCw className='h-4 w-4' />
                              </Button>
                            </TableCell>
                          </TableRow>
                          {expanded.has(credential.id) && (
                            <TableRow>
                              <TableCell colSpan={7} className='bg-muted/30 p-4'>
                                <div className='grid gap-3'>
                                  {credential.quotaData.items.length > 0 ? (
                                    credential.quotaData.items.map((item) => <QuotaDetail key={item.id} item={item} />)
                                  ) : (
                                    <p className='text-muted-foreground text-sm'>{quotaSummary(credential, t)}</p>
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

              <div className='flex flex-wrap items-center justify-between gap-3 text-sm'>
                <span className='text-muted-foreground'>
                  {t('cpa.pagination.total', { count: credentialsQuery.data?.totalCount ?? 0 })}
                </span>
                <div className='flex items-center gap-2'>
                  <Select
                    value={pageSize.toString()}
                    onValueChange={(value) => {
                      const size = Number(value);
                      setPageSize(size);
                      writeTablePageSize(size);
                      resetPagination();
                    }}
                  >
                    <SelectTrigger className='w-[110px]'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {[10, 20, 30, 50].map((size) => (
                        <SelectItem key={size} value={size.toString()}>
                          {size}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button
                    variant='outline'
                    disabled={cursorHistory.length === 0}
                    onClick={() => {
                      const history = [...cursorHistory];
                      setAfter(history.pop());
                      setCursorHistory(history);
                    }}
                  >
                    {t('pagination.previousPage')}
                  </Button>
                  <Button
                    variant='outline'
                    disabled={!credentialsQuery.data?.pageInfo.hasNextPage || !credentialsQuery.data.pageInfo.endCursor}
                    onClick={() => {
                      setCursorHistory((history) => [...history, after]);
                      setAfter(credentialsQuery.data?.pageInfo.endCursor ?? undefined);
                    }}
                  >
                    {t('pagination.nextPage')}
                  </Button>
                </div>
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
