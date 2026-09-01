import { useEffect, useMemo, useState } from 'react';
import { useDebounce } from '@/hooks/use-debounce';
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

const CPA_TABLE_PAGE_SIZE_KEY = 'cpa-table-page-size';
export const CPA_TABLE_PAGE_SIZES = [10, 20, 30, 40, 50] as const;

export function clampTablePageSize(value: number): number {
  return (CPA_TABLE_PAGE_SIZES as readonly number[]).includes(value) ? value : 50;
}

function readTablePageSize(): number {
  try {
    return clampTablePageSize(Number(localStorage.getItem(CPA_TABLE_PAGE_SIZE_KEY)));
  } catch {
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

export function useCPAController() {
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
  const [togglingCredential, setTogglingCredential] = useState<{
    id: number;
    displayName: string;
    disable: boolean;
  }>();
  const [activeRefreshInstanceId, setActiveRefreshInstanceId] = useState<number | null>(null);
  const debouncedSearch = useDebounce(search, 300);

  useEffect(() => {
    if (!selectedInstanceID && instances.length > 0) setSelectedInstanceID(instances[0].id);
    if (selectedInstanceID && !instances.some((instance) => instance.id === selectedInstanceID)) {
      setSelectedInstanceID(instances[0]?.id);
    }
  }, [instances, selectedInstanceID]);

  const selectedInstance = instances.find((instance) => instance.id === selectedInstanceID);
  const overviewQuery = useCPAOverview(selectedInstanceID);
  const providerCounts = overviewQuery.data?.providers ?? [];
  const providers = useMemo(
    () => providerCounts.filter((item) => item.count > 0).map((item) => item.provider).sort(compareCPAProviders),
    [providerCounts]
  );

  useEffect(() => {
    if (provider !== 'all' && !providers.includes(provider)) setProvider('all');
  }, [provider, providers]);

  const availablePlanTypes = useMemo(
    () => providerCounts.find((item) => item.provider === provider)?.planTypes ?? [],
    [providerCounts, provider]
  );
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

  const resetPagination = () => {
    setAfter(undefined);
    setCursorHistory([]);
  };

  const selectInstance = (value: string) => {
    setSelectedInstanceID(Number(value));
    resetPagination();
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
    setPageSize: (value: number) => {
      const next = clampTablePageSize(value);
      setPageSize(next);
      writeTablePageSize(next);
      resetPagination();
    },
    supportedQuotaProviders: SUPPORTED_QUOTA_PROVIDERS,
  };
}

export function versionBelow(version: string, minimum: string) {
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
