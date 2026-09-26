export const INSTANCE_FIELDS = `
  id name baseURL enabled insecureSkipTLS autoRefreshEnabled refreshIntervalMinutes
  autoManageEnabled autoResetEnabled usageStreamEnabled enabledPatrolIntervalMinutes disabledPatrolIntervalMinutes
  nextRefreshAt nextEnabledPatrolAt nextDisabledPatrolAt
  serverVersion serverCommit serverBuildDate lastSyncAttemptAt
  lastSyncSuccessAt lastErrorAt lastError hasSecret connectionStatus createdAt updatedAt
`;

export const CREDENTIAL_FIELDS = `
  id instanceID remoteName displayName provider email status statusMessage disabled unavailable
  runtimeOnly priority planType quotaState quotaLastAttemptAt quotaLastSuccessAt
  quotaLastFailureAt quotaLastError available abnormal stale expired cooling cooldownUntil createdAt updatedAt
  quotaData {
    items { id group label description usedPercent remainingPercent used limit remaining unit resetAt periodSeconds estimatedLimitUSD estimatedCostUSD estimateSource }
    resetCredits { id title resetType grantedAt expiresAt }
    resetCreditsFailed
  }
`;

export const INSTANCES_QUERY = `query CPAInstances { cpaInstances { ${INSTANCE_FIELDS} } }`;
export const CREDENTIALS_QUERY = `
  query CPACredentials($input: QueryCPACredentialsInput!) {
    queryCPACredentials(input: $input) {
      edges { cursor node { ${CREDENTIAL_FIELDS} } }
      pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
      totalCount
    }
  }
`;
export const OVERVIEW_QUERY = `
  query CPAOverview($instanceID: Int!) {
    cpaOverview(instanceID: $instanceID) {
      stats { available total abnormal }
      providers { provider count planTypes }
    }
  }
`;
export const CREATE_INSTANCE =
  `mutation CreateCPAInstance($input: CreateCPAInstanceInput!) { createCPAInstance(input: $input) { ${INSTANCE_FIELDS} } }`;
export const UPDATE_INSTANCE =
  `mutation UpdateCPAInstance($id: Int!, $input: UpdateCPAInstanceInput!) { updateCPAInstance(id: $id, input: $input) { ${INSTANCE_FIELDS} } }`;
export const DELETE_INSTANCE = `mutation DeleteCPAInstance($id: Int!) { deleteCPAInstance(id: $id) }`;
export const REFRESH_INSTANCE =
  `mutation RefreshCPAInstance($instanceID: Int!, $provider: String) { refreshCPAInstance(instanceID: $instanceID, provider: $provider) { requested succeeded failed skipped } }`;
export const REFRESH_PROGRESS =
  `query CPARefreshProgress($instanceID: Int!) { cpaRefreshProgress(instanceID: $instanceID) { requested completed succeeded failed skipped running } }`;
export const REFRESH_CREDENTIAL =
  `mutation RefreshCPACredential($credentialID: Int!) { refreshCPACredential(credentialID: $credentialID) { ${CREDENTIAL_FIELDS} } }`;
export const TOGGLE_CREDENTIAL =
  `mutation ToggleCPACredential($credentialID: Int!, $disabled: Boolean!) { toggleCPACredential(credentialID: $credentialID, disabled: $disabled) { ${CREDENTIAL_FIELDS} } }`;
export const RESET_CODEX_CREDENTIAL = `
  mutation ResetCPACodexCredential($credentialID: Int!, $creditID: String!) {
    resetCPACodexCredential(credentialID: $credentialID, creditID: $creditID)
  }
`;
