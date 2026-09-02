import { useEffect, useState } from 'react';
import { usePermissions } from '@/hooks/usePermissions';
import {
  SUPPORTED_QUOTA_PROVIDERS,
  compareCPAProviders,
  useCPAInstances,
  useCPAOverview,
  useCPACredentials,
  useDeleteCPAInstance,
  useCPARefreshProgress,
  useRefreshCPACredential,
  useRefreshCPAInstance,
  useToggleCPACredential,
} from './data';
import type { CPAInstance } from './types';
import { useCPAFilters } from './use-cpa-filters';
import { useCPAPagination } from './use-cpa-pagination';

export { clampTablePageSize, CPA_TABLE_PAGE_SIZES } from './use-cpa-pagination';

export function useCPAController() {
  const { hasSystemScope } = usePermissions();
  const canWrite = hasSystemScope('write_settings');
  const instancesQuery = useCPAInstances();
  const instances = instancesQuery.data ?? [];
  const [selectedInstanceID, setSelectedInstanceID] = useState<number>();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingInstance, setEditingInstance] = useState<CPAInstance>();
  const [deletingInstance, setDeletingInstance] = useState<CPAInstance>();
  const { pageSize, after, setAfter, cursorHistory, setCursorHistory, resetPagination, setPageSize } = useCPAPagination();
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [togglingCredential, setTogglingCredential] = useState<{
    id: number;
    displayName: string;
    disable: boolean;
  }>();
  const [activeRefreshInstanceId, setActiveRefreshInstanceId] = useState<number | null>(null);

  useEffect(() => {
    if (!selectedInstanceID && instances.length > 0) setSelectedInstanceID(instances[0].id);
    if (selectedInstanceID && !instances.some((instance) => instance.id === selectedInstanceID)) {
      setSelectedInstanceID(instances[0]?.id);
    }
  }, [instances, selectedInstanceID]);

  const selectedInstance = instances.find((instance) => instance.id === selectedInstanceID);
  const overviewQuery = useCPAOverview(selectedInstanceID);
  const providerCounts = overviewQuery.data?.providers ?? [];
  const {
    search,
    setSearch,
    debouncedSearch,
    provider,
    statuses,
    setStatuses,
    planTypes,
    setPlanTypes,
    providers,
    availablePlanTypes,
    changeProvider: changeProviderFilter,
  } = useCPAFilters(providerCounts);
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
  const refreshProgress = useCPARefreshProgress(activeRefreshInstanceId);
  const credentials = credentialsQuery.data?.edges.map((edge) => edge.node) ?? [];
  const stats = overviewQuery.data?.stats;

  const selectInstance = (value: string) => {
    setSelectedInstanceID(Number(value));
    resetPagination();
  };

  const changeProvider = (value: string) => {
    changeProviderFilter(value);
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
    setActiveRefreshInstanceId(selectedInstanceID);
    refreshInstance.mutate(
      { instanceID: selectedInstanceID, provider: provider === 'all' ? undefined : provider },
      { onSettled: () => setActiveRefreshInstanceId(null) }
    );
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

  return {
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
    overviewQuery,
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
    showRefreshProgress: Boolean(refreshProgress.data?.running),
    resetPagination,
    selectInstance,
    changeProvider,
    toggleExpanded,
    refreshSelectedScope,
    confirmDeleteInstance,
    confirmToggleCredential,
    setPageSize,
    supportedQuotaProviders: SUPPORTED_QUOTA_PROVIDERS,
  };
}
