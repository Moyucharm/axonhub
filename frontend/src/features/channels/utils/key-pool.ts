import type { QueryClient } from '@tanstack/react-query';
import type { Channel } from '../data/schema';

export interface ChannelAPIKeySummary {
  total: number;
  enabled: number;
  disabled: number;
  isPool: boolean;
  hasDisabled: boolean;
}

/**
 * Summarize the API key state of a channel for list badges and edit summaries.
 * Pool mode is either an explicit `mode === 'pool'` or multiple keys.
 */
export function getChannelAPIKeySummary(channel?: Pick<Channel, 'credentials' | 'disabledAPIKeys'>): ChannelAPIKeySummary {
  const credentials = channel?.credentials;
  const allKeys = (credentials?.apiKeys ?? []).filter((key) => key.trim().length > 0);
  const total = allKeys.length;
  const disabled = channel?.disabledAPIKeys?.length ?? 0;
  const enabled = Math.max(0, total - disabled);
  return {
    total,
    enabled,
    disabled,
    isPool: credentials?.mode === 'pool' || total > 1,
    hasDisabled: disabled > 0,
  };
}

/** Mask an API key for display: `sk-1234****abcd`. */
export function maskAPIKey(key: string): string {
  if (key.length <= 8) return '****';
  return `${key.slice(0, 4)}****${key.slice(-4)}`;
}

/**
 * Find the freshest channel snapshot inside the channels list cache.
 * Returns undefined when the list has not been loaded or the channel is gone.
 */
export function findCachedChannel(queryClient: QueryClient, channelID: string): Channel | undefined {
  const cached = queryClient.getQueryData<Channel[]>(['channels']);
  return cached?.find((entry) => entry.id === channelID);
}

/**
 * Invalidate the channel queries, wait for the refetch, and return the latest
 * snapshot from the channels list cache so parents can update their state.
 */
export async function refreshCachedChannel(queryClient: QueryClient, channelID: string): Promise<Channel | undefined> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: ['channelDisabledAPIKeys', channelID] }),
    queryClient.invalidateQueries({ queryKey: ['channels'] }),
    queryClient.invalidateQueries({ queryKey: ['channel', channelID] }),
  ]);
  return findCachedChannel(queryClient, channelID);
}
