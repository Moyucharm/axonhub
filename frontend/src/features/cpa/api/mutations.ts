import { useMutation, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useErrorHandler } from '@/hooks/use-error-handler';
import {
  CREATE_INSTANCE,
  DELETE_INSTANCE,
  REFRESH_CREDENTIAL,
  REFRESH_INSTANCE,
  TOGGLE_CREDENTIAL,
  UPDATE_INSTANCE,
} from './operations';
import type {
  CPAInstance,
  CPAInstanceInput,
  CPACredential,
  CPARefreshResult,
} from '../types';

function useInvalidateCPA() {
  const queryClient = useQueryClient();
  return {
    instances: () => queryClient.invalidateQueries({ queryKey: ['cpa', 'instances'] }),
    overview: () => queryClient.invalidateQueries({ queryKey: ['cpa', 'overview'] }),
    credentials: () => queryClient.invalidateQueries({ queryKey: ['cpa', 'credentials'] }),
    all: () => queryClient.invalidateQueries({ queryKey: ['cpa'] }),
  };
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
      invalidate.instances();
      invalidate.overview();
      invalidate.credentials();
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
      invalidate.instances();
      invalidate.overview();
      invalidate.credentials();
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
      invalidate.all();
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
      invalidate.instances();
      invalidate.overview();
      invalidate.credentials();
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
      invalidate.credentials();
      invalidate.overview();
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
    onSuccess: (credential) => {
      invalidate.credentials();
      invalidate.overview();
      if (credential.cooling) {
        toast.warning(t('cpa.messages.credentialRefreshedCooling'));
      } else if (credential.disabled) {
        toast.info(t('cpa.messages.credentialRefreshedDisabled'));
      } else {
        toast.success(t('cpa.messages.credentialRefreshed'));
      }
    },
  });
}
