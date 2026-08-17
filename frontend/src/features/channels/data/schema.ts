import { z } from 'zod';
import { pageInfoSchema } from '@/gql/pagination';

export const apiFormatSchema = z.enum([
  'openai/chat_completions',
  'openai/responses',
  'openai/image_generation',
  'openai/image_edit',
  'openai/image_variation',
  'openai/embeddings',
  'openai/video',
  'openai/moderations',
  'openai/audio_speech',
  'openai/audio_transcriptions',
  'openai/audio_translations',
  'anthropic/messages',
  'gemini/contents',
  'gemini/embeddings',
  'aisdk/text',
  'aisdk/datastream',
  'jina/rerank',
  'jina/embeddings',
  'ollama/chat',
]);

export type ApiFormat = z.infer<typeof apiFormatSchema>;

export const configurableChannelEndpointApiFormats = [
  'openai/chat_completions',
  'openai/responses',
  'openai/image_generation',
  'openai/image_edit',
  'openai/image_variation',
  'openai/embeddings',
  'openai/moderations',
  'openai/audio_speech',
  'openai/audio_transcriptions',
  'openai/audio_translations',
  'anthropic/messages',
  'gemini/contents',
  'gemini/embeddings',
  'jina/rerank',
  'jina/embeddings',
] as const;

export const configurableChannelEndpointApiFormatSchema = z.enum(configurableChannelEndpointApiFormats);

// Channel Endpoint
export const channelEndpointSchema = z.object({
  apiFormat: z.string().min(1),
  path: z.string().optional(),
  baseURL: z.url('Invalid URL').optional().or(z.literal('')),
  transport: z.enum(['http', 'websocket']).optional().or(z.literal('')),
});
export type ChannelEndpoint = z.infer<typeof channelEndpointSchema>;

// Channel Types
export const channelTypeSchema = z.enum([
  'openai',
  'openai_responses',
  'atlascloud',
  'cline',
  'codex',
  'anthropic',
  'anthropic_aws',
  'anthropic_gcp',
  'gemini_openai',
  'gemini',
  'gemini_vertex',
  'deepseek',
  'deepseek_anthropic',
  'deepinfra',
  'qiniu',
  'doubao',
  'doubao_anthropic',
  'moonshot',
  'moonshot_anthropic',
  'zhipu',
  'zai',
  'zhipu_anthropic',
  'zai_anthropic',
  'vercel',
  'anthropic_fake',
  'openai_fake',
  'openrouter',
  'xiaomi',
  'xiaomi_anthropic',
  'xai',
  'ppio',
  'siliconflow',
  'volcengine',
  'volcengine_anthropic',
  'longcat',
  'longcat_anthropic',
  'minimax',
  'minimax_anthropic',
  'aihubmix',
  'aihubmix_anthropic',
  'burncloud',
  'modelscope',
  'bailian',
  'bailian_anthropic',
  'moonshot_coding',
  'jina',
  'github',
  'github_copilot',
  'claudecode',
  'antigravity',
  'cerebras',
  'nanogpt',
  'nanogpt_responses',
  'fireworks',
  'opencode_go',
  'opencode_go_anthropic',
  'ollama',
  'ollama_anthropic',
  'evolink',
  'evolink_anthropic',
  'groq',
]);
export type ChannelType = z.infer<typeof channelTypeSchema>;

// Channel Status
export const channelStatusSchema = z.enum(['enabled', 'disabled', 'archived']);
export type ChannelStatus = z.infer<typeof channelStatusSchema>;

export const capabilityPolicySchema = z.enum(['unlimited', 'require', 'forbid']);
export type CapabilityPolicy = z.infer<typeof capabilityPolicySchema>;

export const apiKeyAutoDisableActionSchema = z.enum(['temporary_disable', 'permanent_disable_delete']);
export type APIKeyAutoDisableAction = z.infer<typeof apiKeyAutoDisableActionSchema>;

export const apiKeyAutoDisableRuleSchema = z.object({
  statusCodes: z.array(z.number().int().min(100).max(599)).optional().nullable(),
  keywordPatterns: z.array(z.string()).optional().nullable(),
  times: z.number().int().min(1),
  action: apiKeyAutoDisableActionSchema,
  disableDurationMinutes: z.number().int().positive().optional().nullable(),
});
export type APIKeyAutoDisableRule = z.infer<typeof apiKeyAutoDisableRuleSchema>;

// Empty conditions (no status codes and no keyword patterns) mean "any error" ->
// the rule matches every failed request, mirroring the global API key setting's
// any-error mode. Only the duration rule stays enforced.
export const apiKeyAutoDisableRuleFormSchema = apiKeyAutoDisableRuleSchema.refine(
  (rule) => rule.action !== 'temporary_disable' || (rule.disableDurationMinutes ?? 0) > 0,
  {
    message: 'Temporary disable requires a duration',
    path: ['disableDurationMinutes'],
  }
);

export const channelPoliciesSchema = z.object({
  stream: capabilityPolicySchema.optional(),
  apiKeyAutoDisableRules: z.array(apiKeyAutoDisableRuleSchema).optional().nullable(),
});
export type ChannelPolicies = z.infer<typeof channelPoliciesSchema>;

// Model Mapping
export const modelMappingSchema = z.object({
  from: z.string(),
  to: z.string(),
});
export type ModelMapping = z.infer<typeof modelMappingSchema>;

// Header Entry
export const headerEntrySchema = z.object({
  key: z.string().min(1, 'Header key is required'),
  value: z.string(),
});
export type HeaderEntry = z.infer<typeof headerEntrySchema>;

// Override Operation
export const overrideMatchSchema = z.object({
  path: z.string().trim().min(1),
  eq: z.string().trim().min(1),
});
export type OverrideMatch = z.infer<typeof overrideMatchSchema>;

export const overrideOperationSchema = z.object({
  op: z.enum(['set', 'set_if_absent', 'delete', 'rename', 'copy', 'array_append', 'array_prepend', 'array_insert', 'array_remove']),
  path: z.string().optional(),
  from: z.string().optional(),
  to: z.string().optional(),
  value: z.any().optional(),
  condition: z.string().optional(),
  match: overrideMatchSchema.nullish(),
  index: z.number().int().nullish(),
  splat: z.boolean().nullish(),
});
export type OverrideOperation = z.infer<typeof overrideOperationSchema>;

// Proxy Type
export const proxyTypeSchema = z.enum(['disabled', 'environment', 'url']);
export type ProxyType = z.infer<typeof proxyTypeSchema>;

// Proxy Config
export const proxyConfigSchema = z.object({
  type: proxyTypeSchema,
  url: z.string().optional(),
  username: z.string().optional(),
  password: z.string().optional(),
  disableConnectionReuse: z.boolean().optional(),
});
export type ProxyConfig = z.infer<typeof proxyConfigSchema>;

// Transform Options
export const reasoningEffortMappingSchema = z.object({
  from: z.string().min(1),
  to: z.string().min(1),
});
export type ReasoningEffortMapping = z.infer<typeof reasoningEffortMappingSchema>;

export const transformOptionsSchema = z.object({
  forceArrayInstructions: z.boolean().optional(),
  forceArrayInputs: z.boolean().optional(),
  replaceDeveloperRoleWithSystem: z.boolean().optional(),
  reasoningEffortMapping: z.array(reasoningEffortMappingSchema).nullish(),
});
export type TransformOptions = z.infer<typeof transformOptionsSchema>;

// Channel Probe
export const channelProbePointSchema = z.object({
  timestamp: z.number(),
  totalRequestCount: z.number(),
  successRequestCount: z.number(),
  avgTokensPerSecond: z.number().optional().nullable(),
  avgTimeToFirstTokenMs: z.number().optional().nullable(),
});
export type ChannelProbePoint = z.infer<typeof channelProbePointSchema>;

export const channelProbeDataSchema = z.object({
  channelID: z.string(),
  points: z.array(channelProbePointSchema),
});
export type ChannelProbeData = z.infer<typeof channelProbeDataSchema>;

// Channel Rate Limit
export const channelRateLimitSchema = z.object({
  rpm: z.number().int().nonnegative().optional().nullable(),
  tpm: z.number().int().nonnegative().optional().nullable(),
  maxConcurrent: z.number().int().nonnegative().optional().nullable(),
  queueSize: z.number().int().nonnegative().optional().nullable(),
  queueTimeoutMs: z.number().int().nonnegative().optional().nullable(),
});
export type ChannelRateLimit = z.infer<typeof channelRateLimitSchema>;

// Live snapshot of the per-channel concurrency limiter.
// Returned from the backend only when MaxConcurrent is configured.
export const channelLimiterStatsSchema = z.object({
  inFlight: z.number().int().nonnegative(),
  waiting: z.number().int().nonnegative(),
  capacity: z.number().int().nonnegative(),
  queueSize: z.number().int().nonnegative(),
});
export type ChannelLimiterStats = z.infer<typeof channelLimiterStatsSchema>;

export const retryableErrorPatternSchema = z.object({
  pattern: z.string().min(1),
  regex: z.boolean().optional().nullable(),
});
export type RetryableErrorPattern = z.infer<typeof retryableErrorPatternSchema>;

export const openCodeGoQuotaSettingsSchema = z.object({
  workspaceId: z.string().optional().nullable(),
  authCookie: z.string().optional().nullable(),
});
export type OpenCodeGoQuotaSettings = z.infer<typeof openCodeGoQuotaSettingsSchema>;

export const channelProviderQuotaSettingsSchema = z.object({
  opencodeGo: openCodeGoQuotaSettingsSchema.optional().nullable(),
});
export type ChannelProviderQuotaSettings = z.infer<typeof channelProviderQuotaSettingsSchema>;

// Channel Settings
export const apiKeyPoolSettingsSchema = z.object({
  retryCount: z.number().int().min(1).optional().nullable(),
  autoCheckEnabled: z.boolean().optional().default(false),
  autoCheckIntervalHours: z.number().int().min(1).optional().nullable(),
  autoCheckConcurrency: z.number().int().min(1).max(32).optional().nullable(),
  autoCheckTimeoutSeconds: z.number().int().min(1).max(600).optional().nullable(),
  lastAutoCheckAt: z.string().optional().nullable(),
});
export type APIKeyPoolSettings = z.infer<typeof apiKeyPoolSettingsSchema>;

// Codex Simulation
// 渠道级 Codex 指纹模拟：仅 openai_responses 渠道可启用。
// preset 是最近一次选中的预设等级标签；options 为实际生效的子选项。
export const codexSimulationPresetSchema = z.enum(['ua', 'normal', 'enhanced']);
export type CodexSimulationPreset = z.infer<typeof codexSimulationPresetSchema>;

export const codexSimulationOptionsSchema = z.object({
  prompt: z.boolean(),
  userAgent: z.boolean(),
  codexHeaders: z.boolean(),
  clientMetadata: z.boolean(),
  responsesShape: z.boolean(),
  additionalTool: z.boolean(),
});
export type CodexSimulationOptions = z.infer<typeof codexSimulationOptionsSchema>;

export const codexSimulationStrategySchema = z.object({
  installationId: z.string(),
  threadId: z.string(),
  windowGeneration: z.number().int().nonnegative(),
});
export type CodexSimulationStrategy = z.infer<typeof codexSimulationStrategySchema>;

export const codexSimulationPlatformSchema = z.enum(['windows10', 'windows11', 'debian', 'ubuntu', 'macos', 'custom']);
export type CodexSimulationPlatform = z.infer<typeof codexSimulationPlatformSchema>;
export const DEFAULT_CODEX_SIMULATION_PLATFORM: CodexSimulationPlatform = 'windows10';

export const DEFAULT_CODEX_SIMULATION_VERSION = '0.144.2';

export function codexSimulationPlatformLabel(platform: CodexSimulationPlatform): string {
  return {
    windows10: 'Windows 10',
    windows11: 'Windows 11',
    debian: 'Debian 13.0.0',
    ubuntu: 'Ubuntu 24.04',
    macos: 'macOS 15.0',
    custom: 'Custom',
  }[platform];
}

export function inferCodexSimulationPlatform(
  standardUserAgent?: string,
  liteUserAgent?: string
): CodexSimulationPlatform {
  const candidates: Array<[CodexSimulationPlatform, string]> = [
    ['windows10', 'Windows 10'],
    ['windows11', 'Windows 11'],
    ['debian', 'Debian 13.0.0'],
    ['ubuntu', 'Ubuntu 24.04'],
    ['macos', 'macOS 15.0'],
  ];
  for (const [platform, name] of candidates) {
    if (standardUserAgent?.includes(`(${name};`) && liteUserAgent?.includes(`(${name};`)) {
      return platform;
    }
  }

  return standardUserAgent || liteUserAgent ? 'custom' : DEFAULT_CODEX_SIMULATION_PLATFORM;
}

export function buildCodexSimulationUserAgents(
  version: string,
  platform: CodexSimulationPlatform = DEFAULT_CODEX_SIMULATION_PLATFORM
): { standard: string; lite: string } {
  const effectiveVersion = version.trim() || DEFAULT_CODEX_SIMULATION_VERSION;
  const platformName = codexSimulationPlatformLabel(platform === 'custom' ? DEFAULT_CODEX_SIMULATION_PLATFORM : platform);
  return {
    standard: `codex_cli_rs/${effectiveVersion} (${platformName}; x86_64)`,
    lite: `codex_exec/${effectiveVersion} (${platformName}; x86_64) tmux/3.5a (codex_exec; ${effectiveVersion})`,
  };
}

export const codexSimulationSettingsSchema = z.object({
  enabled: z.boolean().optional().default(false),
  preset: codexSimulationPresetSchema.optional(),
  options: codexSimulationOptionsSchema.optional(),
  version: z.string().optional(),
  platform: z.preprocess(
    (value) => (value === '' || value === null ? undefined : value),
    codexSimulationPlatformSchema.optional()
  ),
  standardUserAgent: z.string().optional(),
  liteUserAgent: z.string().optional(),
  strategy: codexSimulationStrategySchema.optional().nullable(),
});
export type CodexSimulationSettings = z.infer<typeof codexSimulationSettingsSchema>;
export type CodexSimulationSettingsInput = Pick<
  CodexSimulationSettings,
  'enabled' | 'preset' | 'options' | 'version' | 'platform' | 'standardUserAgent' | 'liteUserAgent'
>;

// Preset-default option sets for the three levels.
export const CODEX_SIMULATION_PRESET_DEFAULTS: Record<CodexSimulationPreset, CodexSimulationOptions> = {
  ua: {
    prompt: false,
    userAgent: true,
    codexHeaders: false,
    clientMetadata: false,
    responsesShape: false,
    additionalTool: false,
  },
  normal: {
    prompt: true,
    userAgent: true,
    codexHeaders: true,
    clientMetadata: true,
    responsesShape: true,
    additionalTool: false,
  },
  enhanced: {
    prompt: true,
    userAgent: true,
    codexHeaders: true,
    clientMetadata: true,
    responsesShape: true,
    additionalTool: true,
  },
};

// Reports whether the given option set differs from the preset defaults (customized state).
export function isCodexSimulationCustomized(preset: CodexSimulationPreset | undefined, options?: CodexSimulationOptions): boolean {
  if (!options || !preset) {
    return false;
  }
  const defaults = CODEX_SIMULATION_PRESET_DEFAULTS[preset];
  return (
    options.prompt !== defaults.prompt ||
    options.userAgent !== defaults.userAgent ||
    options.codexHeaders !== defaults.codexHeaders ||
    options.clientMetadata !== defaults.clientMetadata ||
    options.responsesShape !== defaults.responsesShape ||
    options.additionalTool !== defaults.additionalTool
  );
}

export const channelSettingsSchema = z.object({
  extraModelPrefix: z.string().optional(),
  modelMappings: z.array(modelMappingSchema).optional().nullable(),
  autoTrimedModelPrefixes: z.array(z.string()).optional().nullable(),
  hideOriginalModels: z.boolean().optional(),
  hideMappedModels: z.boolean().optional(),
  lowercaseModelId: z.boolean().optional(),
  bodyOverrideOperations: z.array(overrideOperationSchema).optional(),
  headerOverrideOperations: z.array(overrideOperationSchema).optional(),
  proxy: proxyConfigSchema.optional().nullable(),
  transformOptions: transformOptionsSchema.optional(),
  passThroughUserAgent: z.boolean().optional().nullable(),
  passThroughBody: z.boolean().optional().nullable(),
  rateLimit: channelRateLimitSchema.optional().nullable(),
  retryableStatusCodes: z.array(z.number().int().min(400).max(599)).optional().nullable(),
  retryableErrorPatterns: z.array(retryableErrorPatternSchema).optional().nullable(),
  providerQuota: channelProviderQuotaSettingsSchema.optional().nullable(),
  apiKeyPool: apiKeyPoolSettingsSchema.optional().nullable(),
  codexSimulation: codexSimulationSettingsSchema.optional().nullable(),
});

export type ChannelSettings = z.infer<typeof channelSettingsSchema>;

// Channel Model Entry
export const channelModelEntrySchema = z.object({
  requestModel: z.string(),
  actualModel: z.string(),
  source: z.string(),
});
export type ChannelModelEntry = z.infer<typeof channelModelEntrySchema>;

// Channel Credentials
export const apiKeyModeSchema = z.enum(['single', 'pool']);
export type APIKeyMode = z.infer<typeof apiKeyModeSchema>;

export const channelAPIKeyStateSchema = z.object({
  key: z.string(),
  failureCount: z.number().int().nonnegative(),
  lastFailedAt: z.string().optional().nullable(),
  lastErrorCode: z.number().int().optional().nullable(),
  lastError: z.string().optional().nullable(),
});
export type ChannelAPIKeyState = z.infer<typeof channelAPIKeyStateSchema>;

export const channelCredentialsSchema = z.object({
  mode: apiKeyModeSchema.optional().nullable(),
  apiKey: z.string().optional().nullable(),
  apiKeys: z.array(z.string()).optional().nullable(),
  apiKeyStates: z.array(channelAPIKeyStateSchema).optional().nullable(),
  oauth: z
    .object({
      accessToken: z.string().optional().nullable(),
      refreshToken: z.string().optional().nullable(),
      clientID: z.string().optional().nullable(),
      accountID: z.string().optional().nullable(),
      expiresAt: z.string().optional().nullable(),
      tokenType: z.string().optional().nullable(),
      scopes: z.array(z.string()).optional().nullable(),
    })
    .optional()
    .nullable(),
  gcp: z
    .object({
      region: z.string(),
      projectID: z.string(),
      jsonData: z.string(),
    })
    .optional()
    .nullable(),
});
export type ChannelCredentials = z.infer<typeof channelCredentialsSchema>;

// Disabled API Key
export const disabledAPIKeySchema = z.object({
  key: z.string(),
  disabledAt: z.string(),
  errorCode: z.number(),
  reason: z.string().optional().nullable(),
  expiresAt: z.string().optional().nullable(),
  failureCount: z.number().int().nonnegative().optional().default(0),
  lastFailedAt: z.string().optional().nullable(),
});
export type DisabledAPIKey = z.infer<typeof disabledAPIKeySchema>;

// Channel
export const channelSchema = z.object({
  id: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  type: channelTypeSchema,
  baseURL: z.string(),
  name: z.string(),
  status: channelStatusSchema,
  policies: channelPoliciesSchema.optional().nullable(),
  credentials: channelCredentialsSchema.optional().nullable(),
  disabledAPIKeys: z.array(disabledAPIKeySchema).optional().nullable(),
  supportedModels: z.array(z.string()),
  autoSyncSupportedModels: z.boolean().default(false),
  autoSyncModelPattern: z.string().optional().default(''),
  manualModels: z.array(z.string()).optional().default([]).nullable(),
  tags: z.array(z.string()).optional().default([]).nullable(),
  defaultTestModel: z.string(),
  settings: channelSettingsSchema.optional().nullable(),
  orderingWeight: z.number().optional().default(0),
  errorMessage: z.string().optional().nullable(),
  remark: z.string().optional().nullable(),
  allModelEntries: z.array(channelModelEntrySchema).optional(),
  liveLimiterStats: channelLimiterStatsSchema.optional().nullable(),
  endpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
  defaultEndpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
});
export type Channel = z.infer<typeof channelSchema>;

// Simplified schema for saveChannelEndpoints mutation response
export const channelEndpointsResponseSchema = z.object({
  id: z.string(),
  type: channelTypeSchema,
  name: z.string(),
  defaultEndpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
  endpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
});
export type ChannelEndpointsResponse = z.infer<typeof channelEndpointsResponseSchema>;

export const testAPIKeyResultSchema = z.object({
  keyPrefix: z.string(),
  success: z.boolean(),
  latency: z.number(),
  error: z.string().optional().nullable(),
  disabled: z.boolean(),
});
export type TestAPIKeyResult = z.infer<typeof testAPIKeyResultSchema>;

export const testChannelAPIKeysPayloadSchema = z.object({
  channelID: z.string(),
  total: z.number(),
  successCount: z.number(),
  failedCount: z.number(),
  results: z.array(testAPIKeyResultSchema),
});
export type TestChannelAPIKeysPayload = z.infer<typeof testChannelAPIKeysPayloadSchema>;

// Pricing Schemas
export const pricingModeSchema = z.enum(['flat_fee', 'usage_per_unit', 'usage_tiered', 'usage_volume']);
export type PricingMode = z.infer<typeof pricingModeSchema>;

export const priceItemCodeSchema = z.enum(['prompt_tokens', 'completion_tokens', 'prompt_cached_tokens', 'prompt_write_cached_tokens']);
export type PriceItemCode = z.infer<typeof priceItemCodeSchema>;

export const priceTierSchema = z.object({
  upTo: z.number().nullable().optional(),
  pricePerUnit: z.union([z.string(), z.number()]),
});
export type PriceTier = z.infer<typeof priceTierSchema>;

export const tieredPricingSchema = z.object({
  tiers: z.array(priceTierSchema),
});
export type TieredPricing = z.infer<typeof tieredPricingSchema>;

export const pricingSchema = z.object({
  mode: pricingModeSchema,
  flatFee: z.union([z.string(), z.number()]).nullable().optional(),
  usagePerUnit: z.union([z.string(), z.number()]).nullable().optional(),
  usageTiered: tieredPricingSchema.nullable().optional(),
});
export type Pricing = z.infer<typeof pricingSchema>;

export const promptWriteCacheVariantSchema = z.object({
  variantCode: z.enum(['five_min', 'one_hour']),
  pricing: pricingSchema,
});
export type PromptWriteCacheVariant = z.infer<typeof promptWriteCacheVariantSchema>;

export const modelPriceItemSchema = z.object({
  itemCode: priceItemCodeSchema,
  pricing: pricingSchema,
  promptWriteCacheVariants: z.array(promptWriteCacheVariantSchema).nullable().optional(),
});
export type ModelPriceItem = z.infer<typeof modelPriceItemSchema>;

// Time-based price schedule schemas
// DailyTimeRange uses "HH:mm" format strings (e.g. "03:00", "18:30")
export const dailyTimeRangeSchema = z.object({
  start: z.string(),
  end: z.string(),
});
export type DailyTimeRange = z.infer<typeof dailyTimeRangeSchema>;

export const dateRangeSchema = z.object({
  start: z.string(),
  end: z.string(),
});
export type DateRange = z.infer<typeof dateRangeSchema>;

export const overrideWhenSchema = z.object({
  dailyTime: dailyTimeRangeSchema.optional().nullable(),
  weekdays: z.array(z.number().int().min(1).max(7)).optional().nullable(),
  dateRange: dateRangeSchema.optional().nullable(),
});
export type OverrideWhen = z.infer<typeof overrideWhenSchema>;

export const priceOverrideSchema = z.object({
  name: z.string(),
  priority: z.number().int(),
  when: overrideWhenSchema,
  items: z.array(modelPriceItemSchema),
});
export type PriceOverride = z.infer<typeof priceOverrideSchema>;

export const priceScheduleSchema = z.object({
  timezone: z.string(),
  overrides: z.array(priceOverrideSchema),
});
export type PriceSchedule = z.infer<typeof priceScheduleSchema>;

export const modelPriceSchema = z.object({
  items: z.array(modelPriceItemSchema),
  schedule: priceScheduleSchema.optional().nullable(),
});
export type ModelPrice = z.infer<typeof modelPriceSchema>;

export const channelModelPriceSchema = z.object({
  id: z.string(),
  modelID: z.string(),
  price: modelPriceSchema,
});
export type ChannelModelPrice = z.infer<typeof channelModelPriceSchema>;

export const saveChannelModelPriceInputSchema = z.object({
  modelId: z.string(),
  price: modelPriceSchema,
});
export type SaveChannelModelPriceInput = z.infer<typeof saveChannelModelPriceInputSchema>;
// Helper function to validate OAuth credentials
function validateOAuthCredentials(type: string, apiKey: string | undefined, ctx: z.RefinementCtx) {
  if (!apiKey) return;

  // For GitHub Copilot, enforce JSON format
  const isCopilot = type === 'github_copilot';
  if (isCopilot && !apiKey.trim().startsWith('{')) {
    ctx.addIssue({
      code: 'custom' as const,
      message: 'channels.dialogs.oauth.errors.copilotCredentialsInvalid',
      path: ['credentials', 'apiKey'],
    });
    return;
  }

  // Only enforce JSON validation if it looks like JSON (starts with '{')
  if (!apiKey.trim().startsWith('{')) return;

  const issue = {
    code: 'custom' as const,
    message: 'channels.dialogs.oauth.errors.credentialsInvalid',
    path: ['credentials', 'apiKey'],
  };

  let json: unknown;
  try {
    json = JSON.parse(apiKey);
  } catch {
    ctx.addIssue(issue);
    return;
  }

  // GitHub Copilot only requires access_token, others may require refresh_token
  const parsed = z
    .object({
      access_token: z.string().min(1),
      refresh_token: isCopilot ? z.string().optional() : z.string().min(1),
    })
    .safeParse(json);

  if (!parsed.success) {
    ctx.addIssue(issue);
  }
}

// Create Channel Input

export const createChannelInputSchema = z
  .object({
    type: channelTypeSchema,
    baseURL: z.url('Please enter a valid URL'),
    name: z.string().min(1, 'Name is required'),
    policies: channelPoliciesSchema.optional(),
    supportedModels: z.array(z.string()).min(0, 'At least one supported model is required'),
    autoSyncSupportedModels: z.boolean().optional().default(false),
    autoSyncModelPattern: z.string().optional().default(''),
    manualModels: z.array(z.string()).optional().nullable(),
    tags: z.array(z.string()).optional().default([]),
    defaultTestModel: z.string().min(1, 'Please select a default test model'),
    remark: z.string().optional(),
    orderingWeight: z.number().int().optional(),
    settings: channelSettingsSchema.optional(),
    endpoints: z.array(channelEndpointSchema).optional(),
    credentials: z.object({
      mode: apiKeyModeSchema.optional().default('single'),
      // apiKey is used for OAuth credentials (JSON string with access_token, refresh_token)
      apiKey: z.string().optional(),
      // apiKeys is used for regular API keys (multiple keys for load balancing)
      apiKeys: z.array(z.string()).optional().default([]),
      gcp: z
        .object({
          region: z.string().optional(),
          projectID: z.string().optional(),
          jsonData: z.string().optional(),
        })
        .optional(),
    }),
  })
  .superRefine((data, ctx) => {
    const isOAuthType =
      data.type === 'codex' || data.type === 'claudecode' || data.type === 'antigravity' || data.type === 'github_copilot';
    const hasApiKey = data.credentials.apiKey && data.credentials.apiKey.trim().length > 0;
    const hasApiKeys = data.credentials.apiKeys && data.credentials.apiKeys.some((k) => k.trim().length > 0);

    // github_copilot requires credentials.apiKey (OAuth JSON with access_token)
    if (data.type === 'github_copilot' && !hasApiKey) {
      ctx.addIssue({
        code: 'custom' as const,
        message: 'channels.dialogs.oauth.errors.copilotCredentialsRequired',
        path: ['credentials', 'apiKey'],
      });
    }

    // Validate that at least one credential type is provided
    if (!hasApiKey && !hasApiKeys && data.type !== 'anthropic_aws' && data.type !== 'anthropic_gcp') {
      ctx.addIssue({
        code: 'custom' as const,
        message: 'At least one API Key is required',
        path: ['credentials', 'apiKeys'],
      });
    }

    // For OAuth types, validate the OAuth JSON format if apiKey is provided
    if (isOAuthType && hasApiKey) {
      validateOAuthCredentials(data.type, data.credentials.apiKey, ctx);
    }
    // 如果是 anthropic_gcp 类型，GCP 字段必填（精确到字段级报错）
    if (data.type === 'anthropic_gcp') {
      const gcp = data.credentials?.gcp;
      if (!gcp?.region) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Region is required',
          path: ['credentials', 'gcp', 'region'],
        });
      }
      if (!gcp?.projectID) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Project ID is required',
          path: ['credentials', 'gcp', 'projectID'],
        });
      }
      if (!gcp?.jsonData) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Service Account JSON is required',
          path: ['credentials', 'gcp', 'jsonData'],
        });
      }
    }
  });
export type CreateChannelInput = z.infer<typeof createChannelInputSchema>;

// Update Channel Input
export const updateChannelInputSchema = z
  .object({
    type: channelTypeSchema.optional(),
    baseURL: z.string().url('Please enter a valid URL').optional(),
    name: z.string().min(1, 'Name is required').optional(),
    policies: channelPoliciesSchema.optional(),
    supportedModels: z.array(z.string()).min(1, 'At least one supported model is required').optional(),
    autoSyncSupportedModels: z.boolean().optional(),
    autoSyncModelPattern: z.string().optional(),
    manualModels: z.array(z.string()).optional().nullable(),
    tags: z.array(z.string()).optional(),
    defaultTestModel: z.string().min(1, 'Please select a default test model').optional(),
    settings: channelSettingsSchema.optional(),
    errorMessage: z.string().optional().nullable(),
    remark: z.string().optional().nullable(),
    endpoints: z.array(channelEndpointSchema).optional(),
    credentials: z
      .object({
        mode: apiKeyModeSchema.optional(),
        // apiKey 用于 OAuth 凭据 (codex/claudecode/antigravity)，存储 JSON 字符串（含 access_token, refresh_token）
        apiKey: z.string().optional(),
        // apiKeys 用于普通 API Key（支持多 key 负载均衡），OAuth 类型不使用此字段
        apiKeys: z.array(z.string()).optional(),
        gcp: z
          .object({
            region: z.string().optional(),
            projectID: z.string().optional(),
            jsonData: z.string().optional(),
          })
          .optional(),
      })
      .optional(),
    orderingWeight: z.number().optional(),
  })
  .superRefine((data, ctx) => {
    const effectiveType = data.type;
    const hasApiKey = data.credentials?.apiKey && data.credentials.apiKey.trim().length > 0;

    // For OAuth validation on updates: validate if type is OAuth, or if credentials.apiKey is provided
    // (which indicates OAuth credentials are being set)
    const isOAuthType =
      effectiveType === 'codex' || effectiveType === 'claudecode' || effectiveType === 'antigravity' || effectiveType === 'github_copilot';

    // Derive type from parent context if not available
    let derivedType = effectiveType;
    if (!derivedType && hasApiKey) {
      // Try to get type from parent context
      const parent = ctx.parent;
      if (parent && typeof parent === 'object' && 'type' in parent) {
        derivedType = (parent as { type?: string }).type;
      }
    }

    // If we have an OAuth key but no type, check if it looks like Copilot credentials
    const isCopilotKey = hasApiKey && data.credentials?.apiKey?.trim().startsWith('{');

    if (isOAuthType || derivedType === 'github_copilot' || isCopilotKey) {
      if (isCopilotKey && !derivedType) {
        try {
          const parsed = JSON.parse(data.credentials.apiKey);
          if (!parsed.access_token) {
            ctx.addIssue({
              code: 'custom',
              message: 'channels.dialogs.oauth.errors.copilotCredentialsInvalid',
              path: ['credentials', 'apiKey'],
            });
          }
        } catch {
          ctx.addIssue({
            code: 'custom',
            message: 'channels.dialogs.oauth.errors.copilotCredentialsInvalid',
            path: ['credentials', 'apiKey'],
          });
        }
        return;
      }
      validateOAuthCredentials(derivedType, data.credentials?.apiKey, ctx);
    }

    // 如果是 anthropic_gcp 类型且提供了 credentials，GCP 字段必填（字段级报错）
    if (data.type === 'anthropic_gcp' && data.credentials) {
      const gcp = data.credentials.gcp;
      if (!gcp?.region) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Region is required',
          path: ['credentials', 'gcp', 'region'],
        });
      }
      if (!gcp?.projectID) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Project ID is required',
          path: ['credentials', 'gcp', 'projectID'],
        });
      }
      if (!gcp?.jsonData) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Service Account JSON is required',
          path: ['credentials', 'gcp', 'jsonData'],
        });
      }
    }
  });

export type UpdateChannelInput = z.infer<typeof updateChannelInputSchema>;

// Channel Connection (for pagination)
export const channelConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: channelSchema,
      cursor: z.string(),
    })
  ),
  pageInfo: pageInfoSchema,
  totalCount: z.number(),
});
export type ChannelConnection = z.infer<typeof channelConnectionSchema>;

// Bulk Import Schemas
export const bulkImportChannelItemSchema = z.object({
  type: channelTypeSchema,
  name: z.string().min(1, 'Name is required'),
  baseURL: z.string().url('Please enter a valid URL').min(1, 'Base URL is required'),
  apiKey: z.string().min(1, 'API Key is required'),
  supportedModels: z.array(z.string()).min(1, 'At least one supported model is required'),
  defaultTestModel: z.string().min(1, 'Please select a default test model'),
});
export type BulkImportChannelItem = z.infer<typeof bulkImportChannelItemSchema>;

export const bulkImportChannelsInputSchema = z.object({
  channels: z.array(bulkImportChannelItemSchema).min(1, 'At least one channel is required'),
});
export type BulkImportChannelsInput = z.infer<typeof bulkImportChannelsInputSchema>;

export const bulkImportChannelsResultSchema = z.object({
  success: z.boolean(),
  created: z.number(),
  failed: z.number(),
  errors: z.array(z.string()).optional().nullable(),
  channels: z.array(channelSchema).nullable(),
});
export type BulkImportChannelsResult = z.infer<typeof bulkImportChannelsResultSchema>;

// Raw text input for bulk import
export const bulkImportTextSchema = z.object({
  text: z.string().min(1, 'Please enter data to import'),
});
export type BulkImportText = z.infer<typeof bulkImportTextSchema>;

// Bulk Ordering Schemas
export const channelOrderingItemSchema = z.object({
  id: z.string(),
  name: z.string(),
  type: channelTypeSchema,
  status: channelStatusSchema,
  baseURL: z.string(),
  orderingWeight: z.number(),
  tags: z.array(z.string()).optional().default([]).nullable(),
  supportedModels: z.array(z.string()).optional().default([]).nullable(),
  allModelEntries: z.array(channelModelEntrySchema).optional(),
});
export type ChannelOrderingItem = z.infer<typeof channelOrderingItemSchema>;

export const channelOrderingConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: channelOrderingItemSchema,
    })
  ),
  totalCount: z.number(),
});
export type ChannelOrderingConnection = z.infer<typeof channelOrderingConnectionSchema>;

export const channelSummarySchema = z.object({
  id: z.string(),
  name: z.string(),
  type: channelTypeSchema,
  status: channelStatusSchema,
  baseURL: z.string(),
  orderingWeight: z.number(),
  tags: z.array(z.string()).optional().default([]).nullable(),
  endpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
  allModelEntries: z.array(channelModelEntrySchema).optional().default([]),
});
export type ChannelSummary = z.infer<typeof channelSummarySchema>;

export const channelSummaryConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: channelSummarySchema,
    })
  ),
  totalCount: z.number(),
});
export type ChannelSummaryConnection = z.infer<typeof channelSummaryConnectionSchema>;

export const bulkUpdateChannelOrderingInputSchema = z.object({
  channels: z
    .array(
      z.object({
        id: z.string(),
        orderingWeight: z.number(),
      })
    )
    .min(1, 'At least one channel is required'),
});
export type BulkUpdateChannelOrderingInput = z.infer<typeof bulkUpdateChannelOrderingInputSchema>;

export const bulkUpdateChannelOrderingResultSchema = z.object({
  success: z.boolean(),
  updated: z.number(),
  channels: z.array(channelSchema),
});
export type BulkUpdateChannelOrderingResult = z.infer<typeof bulkUpdateChannelOrderingResultSchema>;

// Re-export template types from templates.ts
export type {
  ChannelOverrideTemplate,
  ChannelOverrideTemplateConnection,
  CreateChannelOverrideTemplateInput,
  UpdateChannelOverrideTemplateInput,
  ApplyChannelOverrideTemplateInput,
  ApplyChannelOverrideTemplatePayload,
  ClearChannelOverrideTemplatesInput,
  ClearChannelOverrideTemplatesPayload,
} from './templates';
