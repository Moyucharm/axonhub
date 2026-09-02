import { z } from 'zod';
import type { CPAInstance, CPAInstanceInput } from './types';

export const cpaInstanceFormSchema = z.object({
  name: z.string().trim().min(1),
  baseURL: z.string().trim().min(1),
  managementSecret: z.string(),
  enabled: z.boolean(),
  insecureSkipTLS: z.boolean(),
  autoRefreshEnabled: z.boolean(),
  refreshIntervalMinutes: z.number().int().min(5).max(1440),
  autoManageEnabled: z.boolean(),
  usageStreamEnabled: z.boolean(),
  enabledPatrolIntervalMinutes: z.number().int().min(1).max(1440),
  disabledPatrolIntervalMinutes: z.number().int().min(60).max(10080),
});

export type CPAInstanceFormValues = z.infer<typeof cpaInstanceFormSchema>;

export const DEFAULT_CPA_INSTANCE_FORM_VALUES: CPAInstanceFormValues = {
  name: '',
  baseURL: '',
  managementSecret: '',
  enabled: true,
  insecureSkipTLS: false,
  autoRefreshEnabled: true,
  refreshIntervalMinutes: 5,
  autoManageEnabled: false,
  usageStreamEnabled: false,
  enabledPatrolIntervalMinutes: 5,
  disabledPatrolIntervalMinutes: 480,
};

export function cpaInstanceFormValues(instance?: CPAInstance): CPAInstanceFormValues {
  return {
    ...DEFAULT_CPA_INSTANCE_FORM_VALUES,
    name: instance?.name ?? '',
    baseURL: instance?.baseURL ?? '',
    enabled: instance?.enabled ?? true,
    insecureSkipTLS: instance?.insecureSkipTLS ?? false,
    autoRefreshEnabled: instance?.autoRefreshEnabled ?? true,
    refreshIntervalMinutes: instance?.refreshIntervalMinutes ?? 5,
    autoManageEnabled: instance?.autoManageEnabled ?? false,
    usageStreamEnabled: instance?.usageStreamEnabled ?? false,
    enabledPatrolIntervalMinutes: instance?.enabledPatrolIntervalMinutes ?? 5,
    disabledPatrolIntervalMinutes: instance?.disabledPatrolIntervalMinutes ?? 480,
  };
}

export function cpaInstanceInput(values: CPAInstanceFormValues): CPAInstanceInput {
  return {
    ...values,
    name: values.name.trim(),
    baseURL: values.baseURL.trim(),
    managementSecret: values.managementSecret.trim() || undefined,
  };
}
