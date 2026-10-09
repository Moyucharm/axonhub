import type { ApiFormat, ChannelType, ModelProtocol } from './schema';

type ProtocolEndpoint = {
  readonly apiFormat?: string | null;
};

export function getAvailableProtocolFormats(
  defaultEndpoints: readonly ProtocolEndpoint[],
  endpoints: readonly ProtocolEndpoint[]
): string[] {
  const formats = new Set<string>();

  for (const endpoint of [...defaultEndpoints, ...endpoints]) {
    if (typeof endpoint.apiFormat === 'string' && endpoint.apiFormat.length > 0) {
      formats.add(endpoint.apiFormat);
    }
  }

  return Array.from(formats);
}

type ProviderConfig = {
  readonly channelTypes: readonly ChannelType[];
};

type ChannelConfig = {
  readonly apiFormat?: ApiFormat | null;
};

export type ProtocolConfigs = {
  readonly providerConfigs: Readonly<Record<string, ProviderConfig>>;
  readonly channelConfigs: Readonly<Record<string, ChannelConfig>>;
};

const OPEN_CODE_ZEN_API_FORMATS: readonly ApiFormat[] = ['openai/chat_completions', 'openai/responses', 'anthropic/messages'];

// Channel types whose model overrides and endpoints pick between several
// protocol formats. Everything else keeps its configured format.
const SUPPORTED_PROTOCOL_FORMATS: Partial<Record<ChannelType, readonly ApiFormat[]>> = {
  opencode_zen: OPEN_CODE_ZEN_API_FORMATS,
  zenmux: ['zenmux/video'],
};

export function getApiFormatsForProvider(provider: string, configs: ProtocolConfigs): ApiFormat[] {
  const providerConfig = configs.providerConfigs[provider];
  if (!providerConfig) return [];

  const formats: ApiFormat[] = [];
  for (const channelType of providerConfig.channelTypes) {
    const apiFormat = configs.channelConfigs[channelType]?.apiFormat;
    if (apiFormat && !formats.includes(apiFormat)) {
      formats.push(apiFormat);
    }
  }

  if (providerConfig.channelTypes.includes('opencode_zen')) {
    for (const format of OPEN_CODE_ZEN_API_FORMATS) {
      if (!formats.includes(format)) formats.push(format);
    }
  }
  return formats;
}

/**
 * Custom endpoint formats the endpoints dialog may offer for a channel.
 * Native video is available as a custom endpoint on every ZenMux channel type.
 * The OpenAI Decisions endpoint is opt-in for the OpenAI channel only; the
 * backend still accepts manual configuration for other channel types.
 * TypeSafe channels only speak System One, while any other channel may expose
 * a Jev-compatible typesafe/systemone endpoint.
 * Other custom endpoint formats remain available everywhere.
 */
export function getConfigurableApiFormatsForChannelType(
  channelType: ChannelType,
  configurableFormats: readonly string[]
): string[] {
  if (channelType === 'typesafe') {
    return ['typesafe/systemone'];
  }
  if (channelType === 'opencode_zen') {
    return configurableFormats.filter((format) => OPEN_CODE_ZEN_API_FORMATS.includes(format as ApiFormat));
  }
  const filtered = configurableFormats.filter((format) => channelType === 'openai' || format !== 'openai/decisions');
  if (['zenmux', 'zenmux_responses', 'zenmux_anthropic', 'zenmux_gemini', 'zenmux_video'].includes(channelType)) {
    return [...filtered];
  }
  return filtered.filter((format) => format !== 'zenmux/video');
}

export function getChannelTypeForApiFormat(provider: string, apiFormat: ApiFormat, configs: ProtocolConfigs): ChannelType | undefined {
  const providerConfig = configs.providerConfigs[provider];
  if (!providerConfig) return undefined;

  // Native video is a ZenMux channel type, so it is only mappable back to a
  // channel type when the provider config actually includes the ZenMux
  // channel type.
  if (apiFormat === 'zenmux/video') {
    return providerConfig.channelTypes.includes('zenmux_video') ? 'zenmux_video' : undefined;
  }

  if (OPEN_CODE_ZEN_API_FORMATS.includes(apiFormat) && providerConfig.channelTypes.includes('opencode_zen')) {
    return 'opencode_zen';
  }

  for (const channelType of providerConfig.channelTypes) {
    if (configs.channelConfigs[channelType]?.apiFormat === apiFormat) {
      return channelType;
    }
  }

  return undefined;
}

export function getModelProtocolsForApiFormat(
  apiFormat: ApiFormat,
  models: readonly string[],
  existingProtocols: readonly ModelProtocol[] | null | undefined = []
): ModelProtocol[] {
  if (apiFormat !== 'zenmux/video') {
    return (existingProtocols ?? [])
      .map((protocol) => ({
        ...protocol,
        apiFormats: protocol.apiFormats.filter((format) => format !== 'zenmux/video'),
      }))
      .filter((protocol) => protocol.apiFormats.length > 0);
  }

  // Native video selection must not clobber per-model protocol overrides:
  // preserve any existing explicit selection for a model and only assign the
  // video format to models the user has not pinned to a protocol yet.
  const existingByModel = new Map((existingProtocols ?? []).map((protocol) => [protocol.model, protocol]));
  return models.map((model) => {
    const existing = existingByModel.get(model);
    if (existing) {
      return { ...existing };
    }
    return { model, apiFormats: [apiFormat], enabled: true };
  });
}

export function getModelProtocolsForChannelApiFormat(
  channelType: ChannelType,
  apiFormat: ApiFormat,
  models: readonly string[],
  existingProtocols: readonly ModelProtocol[] | null | undefined = []
): ModelProtocol[] {
  if (channelType !== 'opencode_zen') {
    return getModelProtocolsForApiFormat(apiFormat, models, existingProtocols);
  }

  const selectedModels = new Set(models);
  const untouchedProtocols = (existingProtocols ?? []).filter((protocol) => !selectedModels.has(protocol.model));

  return [...untouchedProtocols, ...models.map((model) => ({ model, apiFormats: [apiFormat], enabled: true }))];
}

/**
 * Effective protocol of a stored channel. Explicit model overrides win even
 * when they select the default format; the endpoint list is only consulted
 * when no model pins a format, and several distinct overrides keep the
 * channel default because a row badge cannot show two choices.
 */
export function getEffectiveApiFormatForChannel(
  channelType: ChannelType,
  defaultApiFormat: ApiFormat,
  modelProtocols: readonly ModelProtocol[] | null | undefined,
  endpoints: readonly ProtocolEndpoint[] | null | undefined
): ApiFormat {
  const supportedFormats = SUPPORTED_PROTOCOL_FORMATS[channelType];
  if (!supportedFormats) return defaultApiFormat;

  const selectedFormats = new Set<ApiFormat>(
    (modelProtocols ?? [])
      .filter((protocol) => protocol.enabled !== false)
      .flatMap((protocol) => protocol.apiFormats)
      .filter((format): format is ApiFormat => supportedFormats.includes(format as ApiFormat))
  );
  if (selectedFormats.size === 1) {
    for (const selectedFormat of selectedFormats) return selectedFormat;
  }
  if (selectedFormats.size > 1) return defaultApiFormat;

  const endpointFormat = endpoints?.find(
    (endpoint) => endpoint.apiFormat && supportedFormats.includes(endpoint.apiFormat as ApiFormat)
  )?.apiFormat;
  return (endpointFormat as ApiFormat | undefined) ?? defaultApiFormat;
}
