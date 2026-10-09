import { memo, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { useHorizontalScroll } from '@/hooks/use-horizontal-scroll';
import { CHANNEL_CONFIGS, type ChannelType } from '@/features/channels/data/config_channels';
import type { CPAProviderOverview } from '../types';

// Ensures all values are valid ChannelType keys at compile time.
const PROVIDER_CHANNEL_TYPES = {
  codex: 'codex',
  claude: 'claudecode',
  antigravity: 'antigravity',
  kimi: 'moonshot_coding',
  xai: 'xai',
} as const satisfies Record<string, ChannelType>;

interface CPAProviderTabsProps {
  providers: string[];
  providerCounts: CPAProviderOverview[];
  totalCount: number;
  selectedProvider: string;
  onProviderChange: (provider: string) => void;
}

export const CPAProviderTabs = memo(function CPAProviderTabs({
  providers,
  providerCounts,
  totalCount,
  selectedProvider,
  onProviderChange,
}: CPAProviderTabsProps) {
  const { t } = useTranslation();
  const scrollRef = useHorizontalScroll<HTMLDivElement>();
  const countByProvider = useMemo(() => new Map(providerCounts.map((item) => [item.provider, item.count])), [providerCounts]);

  const getIcon = (provider: string) => {
    const channelType = PROVIDER_CHANNEL_TYPES[provider];
    return channelType ? CHANNEL_CONFIGS[channelType]?.icon : undefined;
  };

  return (
    <div className='w-full shrink-0 overflow-hidden'>
      <div ref={scrollRef} className='hide-scroll flex flex-nowrap items-center gap-1.5 overflow-x-auto scroll-smooth'>
        {['all', ...providers].map((provider) => {
          const Icon = getIcon(provider);
          const selected = selectedProvider === provider;
          return (
            <button
              data-testid={`cpa-provider-${provider}`}
              key={provider}
              type='button'
              onClick={() => onProviderChange(provider)}
              className={cn(
                'flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-sm font-medium whitespace-nowrap transition-colors',
                selected
                  ? 'bg-primary border-primary text-primary-foreground'
                  : 'bg-card text-foreground hover:border-primary/60 hover:text-primary'
              )}
            >
              {Icon && <Icon size={15} />}
              {provider === 'all' ? t('cpa.tabs.all') : t(`cpa.providerLabels.${provider}`, { defaultValue: provider })}
              <span
                className={cn(
                  'rounded-full px-1.5 text-xs leading-5 tabular-nums',
                  selected ? 'bg-primary-foreground/20 text-primary-foreground' : 'bg-muted text-muted-foreground'
                )}
              >
                {provider === 'all' ? totalCount : (countByProvider.get(provider) ?? 0)}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
});
