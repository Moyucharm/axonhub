import { IconPlus } from '@tabler/icons-react';
import { AlertTriangle, Pencil, Server, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
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
import { Progress } from '@/components/ui/progress';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { CPAInstanceDialog } from './components/instance-dialog';
import { CPACredentialTable } from './components/credential-table';
import { CPAProviderTabs } from './components/provider-tabs';
import { CPAToolbar } from './components/cpa-toolbar';
import { formatTime } from './quota-windows';
import { SUPPORTED_QUOTA_PROVIDERS } from './types';
import { useCPAController, versionBelow } from './use-cpa-controller';

export default function CPAManagement() {
  const { t, i18n } = useTranslation();
  const controller = useCPAController();
  const {
    canWrite,
    instancesQuery,
    instances,
    selectedInstanceID,
    setSelectedInstanceID,
    selectedInstance,
    dialogOpen,
    setDialogOpen,
    editingInstance,
    setEditingInstance,
    deletingInstance,
    setDeletingInstance,
    search,
    setSearch,
    provider,
    statuses,
    setStatuses,
    planTypes,
    setPlanTypes,
    pageSize,
    after,
    cursorHistory,
    setCursorHistory,
    setAfter,
    expanded,
    togglingCredential,
    setTogglingCredential,
    providerCounts,
    providers,
    availablePlanTypes,
    credentialsQuery,
    credentials,
    stats,
    refreshInstance,
    refreshCredential,
    toggleCredential,
    deleteInstance,
    refreshProgress,
    showRefreshProgress,
    resetPagination,
    selectInstance,
    changeProvider,
    toggleExpanded,
    refreshSelectedScope,
    confirmDeleteInstance,
    confirmToggleCredential,
    setPageSize,
  } = controller;

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
              onValueChange={selectInstance}
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
                planTypes={availablePlanTypes}
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

              {showRefreshProgress && refreshProgress.data && (
                <div className='flex items-center gap-3 pb-2'>
                  <Progress
                    value={refreshProgress.data.requested > 0 ? (refreshProgress.data.completed / refreshProgress.data.requested) * 100 : 0}
                    className='h-2 flex-1'
                  />
                  <span className='text-muted-foreground shrink-0 text-xs'>
                    {t('cpa.messages.refreshProgress', {
                      completed: refreshProgress.data.completed,
                      requested: refreshProgress.data.requested,
                    })}
                  </span>
                  {refreshProgress.data.failed > 0 && (
                    <Badge variant='destructive' className='shrink-0'>
                      {t('cpa.messages.refreshFailedCount', { count: refreshProgress.data.failed })}
                    </Badge>
                  )}
                </div>
              )}

              <CPACredentialTable
                credentials={credentials}
                pageInfo={credentialsQuery.data?.pageInfo}
                totalCount={credentialsQuery.data?.totalCount}
                isLoading={credentialsQuery.isLoading}
                pageSize={pageSize}
                expanded={expanded}
                canWrite={canWrite}
                refreshPending={refreshCredential.isPending}
                togglePending={toggleCredential.isPending}
                locale={i18n.language === 'zh' ? 'zh-CN' : 'en-US'}
                onToggleExpanded={toggleExpanded}
                onRefreshCredential={(id) => refreshCredential.mutate(id)}
                onRequestToggle={(credential) =>
                  setTogglingCredential({
                    id: credential.id,
                    displayName: credential.email || credential.displayName || credential.remoteName,
                    disable: !credential.disabled,
                  })
                }
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
                onPageSizeChange={setPageSize}
                onResetCursor={resetPagination}
              />
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
