import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useErrorHandler } from '@/hooks/use-error-handler';

export interface CPAInstance {
  id: number;
  name: string;
  baseURL: string;
  enabled: boolean;
  insecureSkipTLS: boolean;
  autoRefreshEnabled: boolean;
  refreshIntervalMinutes: number;
  autoManageEnabled: boolean;
  enabledPatrolIntervalMinutes: number;
  disabledPatrolIntervalMinutes: number;
  nextRefreshAt?: string | null;
  nextEnabledPatrolAt?: string | null;
  nextDisabledPatrolAt?: string | null;
  serverVersion: string;
  serverCommit: string;
  serverBuildDate: string;
  lastSyncAttemptAt?: string | null;
  lastSyncSuccessAt?: string | null;
  lastErrorAt?: string | null;
  lastError?: string | null;
  hasSecret: boolean;
  connectionStatus: string;
  createdAt: string;
  updatedAt: string;
}

export interface CPAQuotaItem {
  id: string;
  group: string;
  label: string;
  description: string;
  usedPercent?: number | null;
  remainingPercent?: number | null;
  used?: number | null;
  limit?: number | null;
  remaining?: number | null;
  unit: string;
  resetAt?: string | null;
  periodSeconds?: number | null;
}

export interface CPACredential {
  id: number;
  instanceID: number;
  remoteName: string;
  displayName: string;
  provider: string;
  email: string;
  status: string;
  statusMessage: string;
  disabled: boolean;
  unavailable: boolean;
  runtimeOnly: boolean;
  priority: number;
  planType: string;
  quotaState: string;
  quotaData: { items: CPAQuotaItem[] };
  quotaLastAttemptAt?: string | null;
  quotaLastSuccessAt?: string | null;
  quotaLastFailureAt?: string | null;
  quotaLastError: string;
  available: boolean;
  abnormal: boolean;
  stale: boolean;
  expired: boolean;
  cooling: boolean;
  cooldownUntil?: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface CPACredentialConnection {
  edges: Array<{ cursor: string; node: CPACredential }>;
  pageInfo: {
    hasNextPage: boolean;
    hasPreviousPage: boolean;
    startCursor?: string | null;
    endCursor?: string | null;
  };
  totalCount: number;
}

export interface CPAStats {
  available: number;
  total: number;
  abnormal: number;
}

export const KNOWN_PROVIDER_ORDER = ['codex', 'claude', 'antigravity', 'kimi', 'xai'];
export const SUPPORTED_QUOTA_PROVIDERS = new Set(KNOWN_PROVIDER_ORDER);

export function compareCPAProviders(left: string, right: string): number {
  const leftIndex = KNOWN_PROVIDER_ORDER.indexOf(left);
  const rightIndex = KNOWN_PROVIDER_ORDER.indexOf(right);
  if (leftIndex >= 0 && rightIndex >= 0) return leftIndex - rightIndex;
  if (leftIndex >= 0) return -1;
  if (rightIndex >= 0) return 1;
  return left.localeCompare(right);
}

export interface CPAProviderCount {
  provider: string;
  count: number;
}

export interface CPARefreshResult {
  requested: number;
  succeeded: number;
  failed: number;
  skipped: number;
}

export interface CPAInstanceInput {
  name: string;
  baseURL: string;
  managementSecret?: string;
  enabled?: boolean;
  insecureSkipTLS: boolean;
  autoRefreshEnabled?: boolean;
  refreshIntervalMinutes?: number;
  autoManageEnabled?: boolean;
  enabledPatrolIntervalMinutes?: number;
  disabledPatrolIntervalMinutes?: number;
}

export interface CPACredentialQueryInput {
  instanceID: number;
  first: number;
  after?: string;
  search?: string;
  provider?: string;
  statuses: string[];
  planTypes: string[];
  abnormalOnly: boolean;
}

const INSTANCE_FIELDS = `
  id name baseURL enabled insecureSkipTLS autoRefreshEnabled refreshIntervalMinutes
  autoManageEnabled enabledPatrolIntervalMinutes disabledPatrolIntervalMinutes
  nextRefreshAt nextEnabledPatrolAt nextDisabledPatrolAt
  serverVersion serverCommit serverBuildDate lastSyncAttemptAt
  lastSyncSuccessAt lastErrorAt lastError hasSecret connectionStatus createdAt updatedAt
`;

const CREDENTIAL_FIELDS = `
  id instanceID remoteName displayName provider email status statusMessage disabled unavailable
  runtimeOnly priority planType quotaState quotaLastAttemptAt quotaLastSuccessAt
  quotaLastFailureAt quotaLastError available abnormal stale expired cooling cooldownUntil createdAt updatedAt
  quotaData { items { id group label description usedPercent remainingPercent used limit remaining unit resetAt periodSeconds } }
`;

const INSTANCES_QUERY = `query CPAInstances { cpaInstances { ${INSTANCE_FIELDS} } }`;
const CREDENTIALS_QUERY = `
  query CPACredentials($input: QueryCPACredentialsInput!) {
    queryCPACredentials(input: $input) {
      edges { cursor node { ${CREDENTIAL_FIELDS} } }
      pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
      totalCount
    }
  }
`;
const OVERVIEW_QUERY = `
  query CPAOverview($instanceID: Int!) {
    cpaCredentialStats(instanceID: $instanceID) { available total abnormal }
    cpaProviderCounts(instanceID: $instanceID) { provider count }
  }
`;
const PLAN_TYPES_QUERY = `query CPAPlanTypes($instanceID: Int!, $provider: String!) { cpaPlanTypes(instanceID: $instanceID, provider: $provider) }`;
const CREATE_INSTANCE = `mutation CreateCPAInstance($input: CreateCPAInstanceInput!) { createCPAInstance(input: $input) { ${INSTANCE_FIELDS} } }`;
const UPDATE_INSTANCE = `mutation UpdateCPAInstance($id: Int!, $input: UpdateCPAInstanceInput!) { updateCPAInstance(id: $id, input: $input) { ${INSTANCE_FIELDS} } }`;
const DELETE_INSTANCE = `mutation DeleteCPAInstance($id: Int!) { deleteCPAInstance(id: $id) }`;
const REFRESH_INSTANCE = `mutation RefreshCPAInstance($instanceID: Int!, $provider: String) { refreshCPAInstance(instanceID: $instanceID, provider: $provider) { requested succeeded failed skipped } }`;
const REFRESH_CREDENTIAL = `mutation RefreshCPACredential($credentialID: Int!) { refreshCPACredential(credentialID: $credentialID) { ${CREDENTIAL_FIELDS} } }`;
const TOGGLE_CREDENTIAL = `mutation ToggleCPACredential($credentialID: Int!, $disabled: Boolean!) { toggleCPACredential(credentialID: $credentialID, disabled: $disabled) { ${CREDENTIAL_FIELDS} } }`;

export function useCPAInstances() {
  const { t } = useTranslation();
  const { handleError } = useErrorHandler();
  return useQuery({
    queryKey: ['cpa', 'instances'],
    queryFn: async () => {
      try {
        const data = await graphqlRequest<{ cpaInstances: CPAInstance[] }>(INSTANCES_QUERY);
        return data.cpaInstances;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.loadInstances') });
        throw error;
      }
    },
  });
}

export function useCPACredentials(input?: CPACredentialQueryInput) {
  const { t } = useTranslation();
  const { handleError } = useErrorHandler();
  return useQuery({
    queryKey: ['cpa', 'credentials', input],
    enabled: Boolean(input?.instanceID),
    queryFn: async () => {
      try {
        const data = await graphqlRequest<{ queryCPACredentials: CPACredentialConnection }>(CREDENTIALS_QUERY, { input });
        return data.queryCPACredentials;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.loadCredentials') });
        throw error;
      }
    },
  });
}

export function useCPAOverview(instanceID?: number) {
  const { t } = useTranslation();
  const { handleError } = useErrorHandler();
  return useQuery({
    queryKey: ['cpa', 'overview', instanceID],
    enabled: Boolean(instanceID),
    queryFn: async () => {
      try {
        return await graphqlRequest<{ cpaCredentialStats: CPAStats; cpaProviderCounts: CPAProviderCount[] }>(OVERVIEW_QUERY, {
          instanceID,
        });
      } catch (error) {
        handleError(error, { context: t('cpa.errors.loadOverview') });
        throw error;
      }
    },
  });
}

export function useCPAPlanTypes(instanceID?: number, provider?: string) {
  const { t } = useTranslation();
  const { handleError } = useErrorHandler();
  return useQuery({
    queryKey: ['cpa', 'plans', instanceID, provider],
    enabled: Boolean(instanceID && provider),
    queryFn: async () => {
      try {
        const data = await graphqlRequest<{ cpaPlanTypes: string[] }>(PLAN_TYPES_QUERY, { instanceID, provider });
        return data.cpaPlanTypes;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.loadPlans') });
        throw error;
      }
    },
  });
}

function useInvalidateCPA() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: ['cpa'] });
}

export function useCreateCPAInstance() {
  const { t } = useTranslation();
  const invalidate = useInvalidateCPA();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: async (input: CPAInstanceInput) => {
      try {
        const data = await graphqlRequest<{ createCPAInstance: CPAInstance }>(CREATE_INSTANCE, { input });
        return data.createCPAInstance;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.create') });
        throw error;
      }
    },
    onSuccess: () => {
      invalidate();
      toast.success(t('cpa.messages.created'));
    },
  });
}

export function useUpdateCPAInstance() {
  const { t } = useTranslation();
  const invalidate = useInvalidateCPA();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: async ({ id, input }: { id: number; input: Partial<CPAInstanceInput> }) => {
      try {
        const data = await graphqlRequest<{ updateCPAInstance: CPAInstance }>(UPDATE_INSTANCE, { id, input });
        return data.updateCPAInstance;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.update') });
        throw error;
      }
    },
    onSuccess: () => {
      invalidate();
      toast.success(t('cpa.messages.updated'));
    },
  });
}

export function useDeleteCPAInstance() {
  const { t } = useTranslation();
  const invalidate = useInvalidateCPA();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: async (id: number) => {
      try {
        const data = await graphqlRequest<{ deleteCPAInstance: boolean }>(DELETE_INSTANCE, { id });
        return data.deleteCPAInstance;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.delete') });
        throw error;
      }
    },
    onSuccess: () => {
      invalidate();
      toast.success(t('cpa.messages.deleted'));
    },
  });
}

export function useRefreshCPAInstance() {
  const { t } = useTranslation();
  const invalidate = useInvalidateCPA();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: async ({ instanceID, provider }: { instanceID: number; provider?: string }) => {
      try {
        const data = await graphqlRequest<{ refreshCPAInstance: CPARefreshResult }>(REFRESH_INSTANCE, { instanceID, provider });
        return data.refreshCPAInstance;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.refresh') });
        throw error;
      }
    },
    onSuccess: (result) => {
      invalidate();
      if (result.failed > 0) toast.warning(t('cpa.messages.refreshPartial', { count: result.failed }));
      else toast.success(t('cpa.messages.refreshSuccess', { count: result.succeeded }));
    },
  });
}

export function useToggleCPACredential() {
  const { t } = useTranslation();
  const invalidate = useInvalidateCPA();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: async ({ credentialID, disabled }: { credentialID: number; disabled: boolean }) => {
      try {
        const data = await graphqlRequest<{ toggleCPACredential: CPACredential }>(TOGGLE_CREDENTIAL, { credentialID, disabled });
        return data.toggleCPACredential;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.toggleCredential') });
        throw error;
      }
    },
    onSuccess: () => {
      invalidate();
      toast.success(t('cpa.messages.credentialToggled'));
    },
  });
}

export function useRefreshCPACredential() {
  const { t } = useTranslation();
  const invalidate = useInvalidateCPA();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: async (credentialID: number) => {
      try {
        const data = await graphqlRequest<{ refreshCPACredential: CPACredential }>(REFRESH_CREDENTIAL, { credentialID });
        return data.refreshCPACredential;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.refreshCredential') });
        throw error;
      }
    },
    onSuccess: () => {
      invalidate();
      toast.success(t('cpa.messages.credentialRefreshed'));
    },
  });
}
