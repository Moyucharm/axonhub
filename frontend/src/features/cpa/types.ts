export interface CPAInstance {
  id: number;
  name: string;
  baseURL: string;
  enabled: boolean;
  insecureSkipTLS: boolean;
  autoRefreshEnabled: boolean;
  refreshIntervalMinutes: number;
  autoManageEnabled: boolean;
  usageStreamEnabled: boolean;
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
  estimatedLimitUSD?: number | null;
  estimatedCostUSD?: number | null;
  estimateSource?: string | null;
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

export interface CPAProviderOverview extends CPAProviderCount {
  planTypes: string[];
}

export interface CPAOverview {
  stats: CPAStats;
  providers: CPAProviderOverview[];
}

export interface CPARefreshResult {
  requested: number;
  succeeded: number;
  failed: number;
  skipped: number;
}

export interface CPARefreshProgress {
  requested: number;
  completed: number;
  succeeded: number;
  failed: number;
  skipped: number;
  running: boolean;
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
  usageStreamEnabled?: boolean;
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
