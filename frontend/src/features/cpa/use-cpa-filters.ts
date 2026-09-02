import { useEffect, useMemo, useState } from 'react';
import { useDebounce } from '@/hooks/use-debounce';
import type { CPAProviderOverview, CPACredentialFilterStatus } from './types';
import { compareCPAProviders } from './types';

export function useCPAFilters(providerCounts: CPAProviderOverview[]) {
  const [search, setSearch] = useState('');
  const [provider, setProvider] = useState('all');
  const [statuses, setStatuses] = useState<CPACredentialFilterStatus[]>([]);
  const [planTypes, setPlanTypes] = useState<string[]>([]);
  const debouncedSearch = useDebounce(search, 300);
  const providers = useMemo(
    () =>
      providerCounts
        .filter((item) => item.count > 0)
        .map((item) => item.provider)
        .sort(compareCPAProviders),
    [providerCounts]
  );

  useEffect(() => {
    if (provider !== 'all' && !providers.includes(provider)) setProvider('all');
  }, [provider, providers]);

  const availablePlanTypes = useMemo(
    () => providerCounts.find((item) => item.provider === provider)?.planTypes ?? [],
    [providerCounts, provider]
  );

  const changeProvider = (value: string) => {
    setProvider(value);
    setPlanTypes([]);
  };

  return {
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
    changeProvider,
  };
}
