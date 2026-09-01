import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useErrorHandler } from '@/hooks/use-error-handler';
import {
  CREDENTIALS_QUERY,
  INSTANCES_QUERY,
  OVERVIEW_QUERY,
  REFRESH_PROGRESS,
} from './operations';
import type {
  CPAInstance,
  CPACredentialConnection,
  CPAOverview,
  CPARefreshProgress,
  CPACredentialQueryInput,
} from '../types';

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
        const data = await graphqlRequest<{ cpaOverview: CPAOverview }>(OVERVIEW_QUERY, { instanceID });
        return data.cpaOverview;
      } catch (error) {
        handleError(error, { context: t('cpa.errors.loadOverview') });
        throw error;
      }
    },
  });
}

export function useCPARefreshProgress(instanceID: number | null) {
  return useQuery({
    queryKey: ['cpa', 'refresh-progress', instanceID],
    enabled: instanceID != null,
    queryFn: async () => {
      const data = await graphqlRequest<{ cpaRefreshProgress: CPARefreshProgress | null }>(REFRESH_PROGRESS, { instanceID });
      return data.cpaRefreshProgress;
    },
    refetchInterval: instanceID != null ? 500 : false,
  });
}
