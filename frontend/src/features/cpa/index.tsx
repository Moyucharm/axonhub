import { IconPlus } from '@tabler/icons-react';
import { Server } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { CPACredentialToggleDialog, CPAInstanceDeleteDialog } from './components/confirmation-dialogs';
import { CPAToolbar } from './components/cpa-toolbar';
import { CPACredentialTable } from './components/credential-table';
import { CPAInstanceAlerts } from './components/instance-alerts';
import { CPAInstanceDialog } from './components/instance-dialog';
import { CPAInstanceToolbar } from './components/instance-toolbar';
import { CPAProviderTabs } from './components/provider-tabs';
import { CPARefreshProgress } from './components/refresh-progress';
import { SUPPORTED_QUOTA_PROVIDERS } from './types';
import { useCPAController } from './use-cpa-controller';

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

  const openCreateDialog = () => {
    setEditingInstance(undefined);
    setDialogOpen(true);
  };

  return (
    <>
      <Header fixed>
        <div className='flex w-full flex-1 flex-col gap-2 md:flex-row md:items-center md:justify-between md:gap-0'>
          <div className='min-w-0'>
            <h2 data-testid='cpa-page-title' className='text-xl font-bold tracking-tight'>
              {t('cpa.title')}
            </h2>
            <p className='text-muted-foreground text-sm'>{t('cpa.description')}</p>
          </div>
          {canWrite && (
            <Button data-testid='cpa-add-instance' className='space-x-1' onClick={openCreateDialog}>
              <span>{t('cpa.instance.add')}</span>
              <IconPlus size={18} />
            </Button>
          )}
        </div>
      </Header>

      <Main fixed>
        <div className='flex h-full flex-col gap-4 overflow-hidden'>
          <CPAInstanceToolbar
            instances={instances}
            selectedInstanceID={selectedInstanceID}
            selectedInstance={selectedInstance}
            stats={stats}
            canWrite={canWrite}
            onSelect={selectInstance}
            onEdit={(instance) => {
              setEditingInstance(instance);
              setDialogOpen(true);
            }}
            onDelete={setDeletingInstance}
          />

          {instances.length === 0 && !instancesQuery.isLoading && (
            <div className='flex flex-1 flex-col items-center justify-center rounded-xl border border-dashed p-8 text-center'>
              <Server className='text-muted-foreground mb-4 h-10 w-10' />
              <h3 className='font-semibold'>{t('cpa.empty.title')}</h3>
              <p className='text-muted-foreground mt-1 max-w-lg text-sm'>{t('cpa.empty.description')}</p>
              {canWrite && (
                <Button className='mt-4 space-x-1' onClick={openCreateDialog}>
                  <span>{t('cpa.instance.add')}</span>
                  <IconPlus size={18} />
                </Button>
              )}
            </div>
          )}

          {selectedInstance && <CPAInstanceAlerts instance={selectedInstance} />}

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
                  canRefresh: canWrite && selectedInstance.enabled && (provider === 'all' || SUPPORTED_QUOTA_PROVIDERS.has(provider)),
                  pending: refreshInstance.isPending,
                  onRefresh: refreshSelectedScope,
                }}
              />

              {showRefreshProgress && refreshProgress.data && <CPARefreshProgress progress={refreshProgress.data} />}

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
      <CPACredentialToggleDialog
        confirmation={togglingCredential}
        pending={toggleCredential.isPending}
        onOpenChange={(open) => {
          if (!open) setTogglingCredential(undefined);
        }}
        onConfirm={confirmToggleCredential}
      />
      <CPAInstanceDeleteDialog
        instance={deletingInstance}
        pending={deleteInstance.isPending}
        onOpenChange={(open) => {
          if (!open) setDeletingInstance(undefined);
        }}
        onConfirm={confirmDeleteInstance}
      />
    </>
  );
}
