import { useMemo } from 'react';
import { Cross2Icon } from '@radix-ui/react-icons';
import { IconRefresh, IconSearch } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { DataTableFacetedFilter } from '@/components/data-table-faceted-filter';
import { useHorizontalScroll } from '@/hooks/use-horizontal-scroll';
import { planLabel } from '../labels';
import { cn } from '@/lib/utils';

interface RefreshAction {
  canRefresh: boolean;
  pending: boolean;
  onRefresh: () => void;
}

interface CPAToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  statuses: string[];
  onStatusesChange: (values: string[]) => void;
  provider: string;
  planType: string;
  planTypes: string[];
  onPlanTypeChange: (value: string) => void;
  refresh: RefreshAction;
}

export function CPAToolbar({
  search,
  onSearchChange,
  statuses,
  onStatusesChange,
  provider,
  planType,
  planTypes,
  onPlanTypeChange,
  refresh,
}: CPAToolbarProps) {
  const { t } = useTranslation();
  const scrollRef = useHorizontalScroll<HTMLDivElement>();
  const isFiltered = Boolean(search) || statuses.length > 0 || planType !== 'all';

  const statusOptions = useMemo(
    () => [
      { value: 'enabled', label: t('cpa.status.enabled') },
      { value: 'disabled', label: t('cpa.status.disabled') },
      { value: 'abnormal', label: t('cpa.status.abnormal') },
      { value: 'cooldown', label: t('cpa.status.cooldown') },
    ],
    [t]
  );
  const planOptions = useMemo(
    () =>
      planTypes.map((plan) => ({
        value: plan,
        label: planLabel(provider, plan, t),
      })),
    [planTypes, provider, t]
  );

  return (
    <div ref={scrollRef} className='flex shrink-0 items-center gap-4 overflow-x-auto pb-2 md:overflow-x-visible md:pb-0'>
      <div className='relative w-[150px] shrink-0 lg:w-auto lg:flex-1'>
        <IconSearch className='text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2' />
        <Input
          placeholder={t('cpa.filters.search')}
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          className='h-8 pl-8'
        />
      </div>
      <DataTableFacetedFilter
        title={t('cpa.filters.status')}
        options={statusOptions}
        selectedValues={statuses}
        onSelectedValuesChange={onStatusesChange}
      />
      {provider !== 'all' && planOptions.length > 0 && (
        <DataTableFacetedFilter
          title={t('cpa.filters.plan')}
          options={planOptions}
          singleSelect
          selectedValues={planType === 'all' ? [] : [planType]}
          onSelectedValuesChange={(values) => onPlanTypeChange(values[0] ?? 'all')}
        />
      )}
      {isFiltered && (
        <Button
          variant='ghost'
          className='h-8 px-2 lg:px-3'
          onClick={() => {
            onSearchChange('');
            onStatusesChange([]);
            onPlanTypeChange('all');
          }}
        >
          {t('common.filters.reset')}
          <Cross2Icon className='ml-2 h-4 w-4' />
        </Button>
      )}
      <div className='ml-auto shrink-0'>
        <Button
          className='h-8 shrink-0 space-x-1'
          onClick={refresh.onRefresh}
          disabled={!refresh.canRefresh || refresh.pending}
        >
          <span>
            {provider === 'all'
              ? t('cpa.actions.refreshAll')
              : t('cpa.actions.refreshProvider', {
                  provider: t(`cpa.providerLabels.${provider}`, { defaultValue: provider }),
                })}
          </span>
          <IconRefresh className={cn('h-4 w-4', refresh.pending && 'animate-spin')} />
        </Button>
      </div>
    </div>
  );
}
