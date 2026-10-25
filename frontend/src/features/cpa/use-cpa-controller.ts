import { useEffect, useRef, useState } from 'react';
import { usePermissions } from '@/hooks/usePermissions';
import { useErrorHandler } from '@/hooks/use-error-handler';
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
  useResetCPACodexCredential,
  useResetCPAClaudeCredential,
  useResetCPACredentialCooldown,
} from './data';
import type { CPAInstance } from './types';
import type {
  CPAClaudeResetConfirmation,
  CPACodexResetConfirmation,
  CPACredentialCooldownResetConfirmation,
} from './components/confirmation-dialogs';
import { useCPAFilters } from './use-cpa-filters';
import { useCPAPagination } from './use-cpa-pagination';

export { clampTablePageSize, CPA_TABLE_PAGE_SIZES } from './use-cpa-pagination';

export function useCPAController() {
  const { hasSystemScope } = usePermissions();
  const canWrite = hasSystemScope('write_settings');
  const { handleError } = useErrorHandler();
  const instancesQuery = useCPAInstances();
  const instances = instancesQuery.data ?? [];
  const [selectedInstanceID, setSelectedInstanceID] = useState<number>();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingInstance, setEditingInstance] = useState<CPAInstance>();
  const [deletingInstance, setDeletingInstance] = useState<CPAInstance>();
  const { pageSize, after, setAfter, cursorHistory, setCursorHistory, resetPagination, setPageSize } = useCPAPagination();
  const [detailCredentialID, setDetailCredentialID] = useState<number | null>(null);
  const [togglingCredential, setTogglingCredential] = useState<{
    id: number;
    displayName: string;
    disable: boolean;
  }>();
  const [resettingCredential, setResettingCredential] = useState<CPACodexResetConfirmation>();
  const resetSubmissionInFlight = useRef(false);
  const [claudeResettingCredential, setClaudeResettingCredential] = useState<CPAClaudeResetConfirmation>();
  const claudeResetSubmissionInFlight = useRef(false);
  const [cooldownResettingCredential, setCooldownResettingCredential] = useState<CPACredentialCooldownResetConfirmation>();
  const cooldownResetSubmissionInFlight = useRef(false);
  const cooldownResetPreparing = useRef(false);
  const cooldownResetNeedsRefresh = useRef(false);
  const cooldownResetContextVersion = useRef(0);

  const clearCooldownResetConfirmation = () => {
    cooldownResetContextVersion.current += 1;
    setCooldownResettingCredential(undefined);
  };
  const [activeRefreshInstanceId, setActiveRefreshInstanceId] = useState<number | null>(null);

  useEffect(() => {
    if (!selectedInstanceID && instances.length > 0) setSelectedInstanceID(instances[0].id);
    if (selectedInstanceID && !instances.some((instance) => instance.id === selectedInstanceID)) {
      setDetailCredentialID(null);
      setResettingCredential(undefined);
      setClaudeResettingCredential(undefined);
      clearCooldownResetConfirmation();
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
  const resetCredential = useResetCPACodexCredential();
  const claudeResetCredential = useResetCPAClaudeCredential();
  const cooldownResetCredential = useResetCPACredentialCooldown();
  const refreshProgress = useCPARefreshProgress(activeRefreshInstanceId);
  const credentials = credentialsQuery.data?.edges.map((edge) => edge.node) ?? [];
  const stats = overviewQuery.data?.stats;

  const changeSelectedInstance = (id: number) => {
    if (id === selectedInstanceID) return;
    setDetailCredentialID(null);
    setResettingCredential(undefined);
    setClaudeResettingCredential(undefined);
    clearCooldownResetConfirmation();
    setSelectedInstanceID(id);
  };

  const selectInstance = (value: string) => {
    changeSelectedInstance(Number(value));
    resetPagination();
  };

  const changeProvider = (value: string) => {
    changeProviderFilter(value);
    resetPagination();
  };


  const requestResetCredential = (confirmation: CPACodexResetConfirmation) => {
    if (resetSubmissionInFlight.current) return;
    resetCredential.reset();
    setResettingCredential(confirmation);
  };

  const requestClaudeResetCredential = (confirmation: CPAClaudeResetConfirmation) => {
    if (claudeResetSubmissionInFlight.current) return;
    claudeResetCredential.reset();
    setClaudeResettingCredential(confirmation);
  };

  const requestCooldownResetCredential = async (confirmation: CPACredentialCooldownResetConfirmation) => {
    if (!canWrite || cooldownResetSubmissionInFlight.current || cooldownResetPreparing.current || cooldownResettingCredential) return;
    const contextVersion = cooldownResetContextVersion.current;
    cooldownResetPreparing.current = true;
    try {
      let currentCredentials = credentials;
      if (cooldownResetNeedsRefresh.current) {
        const result = await credentialsQuery.refetch({ throwOnError: true });
        currentCredentials = result.data?.edges.map((edge) => edge.node) ?? [];
      }
      if (contextVersion !== cooldownResetContextVersion.current) return;
      const credential = currentCredentials.find((item) => item.id === confirmation.credentialID);
      if (!credential?.canResetCooldown || !credential.routingCooldown.items.some((item) => Date.parse(item.retryAt) > Date.now())) return;
      cooldownResetNeedsRefresh.current = false;
      cooldownResetCredential.reset();
      setCooldownResettingCredential(confirmation);
    } catch (error) {
      handleError(error);
    } finally {
      cooldownResetPreparing.current = false;
    }
  };

  const closeCooldownResetDialog = (open: boolean) => {
    if (!open && !cooldownResetSubmissionInFlight.current) clearCooldownResetConfirmation();
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
      if (deletingInstance.id === selectedInstanceID) clearCooldownResetConfirmation();
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

  const confirmResetCredential = async () => {
    if (!resettingCredential || resetSubmissionInFlight.current || resetCredential.isError) return;
    const confirmation = resettingCredential;
    resetSubmissionInFlight.current = true;
    try {
      await resetCredential.mutateAsync({ credentialID: confirmation.credentialID, creditID: confirmation.creditID });
      setResettingCredential((current) => (current === confirmation ? undefined : current));
    } catch {
      // Leave the frozen credit in the dialog; the hook reports the error.
    } finally {
      resetSubmissionInFlight.current = false;
    }
  };

  const confirmClaudeResetCredential = async () => {
    if (!claudeResettingCredential || claudeResetSubmissionInFlight.current || claudeResetCredential.isError) return;
    const confirmation = claudeResettingCredential;
    claudeResetSubmissionInFlight.current = true;
    try {
      await claudeResetCredential.mutateAsync({ credentialID: confirmation.credentialID, grantID: confirmation.grantID });
      setClaudeResettingCredential((current) => (current === confirmation ? undefined : current));
    } catch {
      // Leave the frozen grant in the dialog; the hook reports the error.
    } finally {
      claudeResetSubmissionInFlight.current = false;
    }
  };

  const confirmCooldownResetCredential = async () => {
    if (!canWrite || !cooldownResettingCredential || cooldownResetSubmissionInFlight.current || cooldownResetCredential.isError || cooldownResetNeedsRefresh.current) return;
    const confirmation = cooldownResettingCredential;
    cooldownResetSubmissionInFlight.current = true;
    try {
      await cooldownResetCredential.mutateAsync(confirmation.credentialID);
      setCooldownResettingCredential((current) => (current === confirmation ? undefined : current));
    } catch {
      cooldownResetNeedsRefresh.current = true;
      // Keep the error visible; closing and reading state is required before another attempt.
    } finally {
      cooldownResetSubmissionInFlight.current = false;
    }
  };

  return {
    canWrite,
    instancesQuery,
    instances,
    selectedInstanceID,
    setSelectedInstanceID: changeSelectedInstance,
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
    detailCredentialID,
    setDetailCredentialID,
    togglingCredential,
    setTogglingCredential,
    resettingCredential,
    setResettingCredential,
    claudeResettingCredential,
    setClaudeResettingCredential,
    cooldownResettingCredential,
    closeCooldownResetDialog,
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
    resetCredential,
    claudeResetCredential,
    cooldownResetCredential,
    showRefreshProgress: Boolean(refreshProgress.data?.running),
    resetPagination,
    selectInstance,
    changeProvider,
    refreshSelectedScope,
    confirmDeleteInstance,
    confirmToggleCredential,
    confirmResetCredential,
    requestResetCredential,
    confirmClaudeResetCredential,
    requestClaudeResetCredential,
    confirmCooldownResetCredential,
    requestCooldownResetCredential,
    setPageSize,
    supportedQuotaProviders: SUPPORTED_QUOTA_PROVIDERS,
  };
}
