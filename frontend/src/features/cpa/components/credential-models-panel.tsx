import { useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import { useCPACredentialModels } from '../api/queries';

export function CPACredentialModelsPanel({ credentialID }: { credentialID: number }) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState('');
  const modelsQuery = useCPACredentialModels(credentialID);
  const models = modelsQuery.data;

  const filtered = useMemo(() => {
    const keyword = filter.trim().toLowerCase();
    if (!models || !keyword) return models ?? [];
    return models.filter((model) =>
      [model.id, model.displayName, model.ownedBy].some((value) => value.toLowerCase().includes(keyword))
    );
  }, [models, filter]);

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-3'>
      <div className='relative'>
        <Search className='text-muted-foreground absolute top-1/2 left-2.5 h-4 w-4 -translate-y-1/2' />
        <Input
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder={t('cpa.models.search')}
          className='pl-8'
        />
      </div>
      <div data-testid='cpa-credential-models-list' className='min-h-0 flex-1 overflow-y-auto'>
        {modelsQuery.isLoading ? (
          <div className='space-y-2'>
            {Array.from({ length: 5 }, (_, index) => (
              <Skeleton key={index} className='h-9 w-full' />
            ))}
          </div>
        ) : modelsQuery.isError ? (
          <p className='text-destructive py-6 text-center text-sm'>{t('cpa.models.loadFailed')}</p>
        ) : filtered.length === 0 ? (
          <p className='text-muted-foreground py-6 text-center text-sm'>{t('cpa.models.empty')}</p>
        ) : (
          <ul className='divide-y'>
            {filtered.map((model) => (
              <li key={model.id} className='flex items-center justify-between gap-3 py-2'>
                <div className='min-w-0'>
                  <div className='truncate font-mono text-sm'>{model.id}</div>
                  {model.displayName && model.displayName !== model.id && (
                    <div className='text-muted-foreground truncate text-xs'>{model.displayName}</div>
                  )}
                </div>
                {model.ownedBy && (
                  <Badge variant='outline' className='shrink-0'>
                    {model.ownedBy}
                  </Badge>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
      {models && models.length > 0 && (
        <p className='text-muted-foreground text-xs'>
          {t('cpa.models.count', { shown: filtered.length, total: models.length })}
        </p>
      )}
    </div>
  );
}
