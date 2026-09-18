import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const dataDir = import.meta.dirname;
const srcRoot = join(dataDir, '..', '..', '..');

function read(relativePath) {
  return readFileSync(join(srcRoot, relativePath), 'utf8');
}

function parseLocale(locale) {
  return JSON.parse(read(`locales/${locale}/channels.json`));
}

test('Cline is available as a channel type in frontend schemas and configs', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsConfig = read('features/channels/data/config_channels.ts');
  const providersConfig = read('features/channels/data/config_providers.ts');

  assert.match(schema, /channelTypeSchema[\s\S]*'cline'/, 'channelTypeSchema should accept cline');
  assert.match(channelsConfig, /cline:\s*{[\s\S]*channelType:\s*'cline'/, 'CHANNEL_CONFIGS should define cline');
  assert.match(
    channelsConfig,
    /cline:\s*{[\s\S]*baseURL:\s*'https:\/\/api\.cline\.bot\/api\/v1'/,
    'Cline should use the documented API base URL'
  );
  assert.match(
    channelsConfig,
    /cline:\s*{[\s\S]*apiFormat:\s*OPENAI_CHAT_COMPLETIONS/,
    'Cline should use OpenAI Chat Completions in the UI'
  );
  assert.match(channelsConfig, /CHANNEL_TYPE_TO_PROVIDER[\s\S]*cline:\s*'cline'/, 'Cline should map to the Cline provider');
  assert.match(providersConfig, /cline:\s*{[\s\S]*channelTypes:\s*\[\s*'cline'\s*\]/, 'PROVIDER_CONFIGS should expose a Cline provider');
});

test('Qiniu and Fenno ad channel types are removed', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsConfig = read('features/channels/data/config_channels.ts');
  const providersConfig = read('features/channels/data/config_providers.ts');
  const en = parseLocale('en');
  const zh = parseLocale('zh-CN');

  for (const removedType of ["'qiniu'", "'qiniu_anthropic'", "'fenno'"]) {
    assert.ok(!schema.includes(removedType), `${removedType} must stay out of the frontend channel schema`);
  }
  assert.doesNotMatch(channelsConfig, /qiniu|fenno/);
  assert.doesNotMatch(providersConfig, /qiniu|fenno/);
  assert.doesNotMatch(en['channels.dialogs.bulkImport.supportedTypes'], /qiniu|fenno/);
  assert.doesNotMatch(zh['channels.dialogs.bulkImport.supportedTypes'], /qiniu|fenno/);
  for (const messages of [en, zh]) {
    assert.equal(messages['channels.types.qiniu'], undefined);
    assert.equal(messages['channels.types.qiniu_anthropic'], undefined);
    assert.equal(messages['channels.types.fenno'], undefined);
    assert.equal(messages['channels.providers.qiniu'], undefined);
    assert.equal(messages['channels.providers.fenno'], undefined);
  }

  assert.match(channelsConfig, /openai:\s*{[\s\S]*apiFormat:\s*OPENAI_CHAT_COMPLETIONS/);
  assert.match(channelsConfig, /openai_responses:\s*{[\s\S]*apiFormat:\s*OPENAI_RESPONSES/);
  assert.match(channelsConfig, /anthropic:\s*{[\s\S]*apiFormat:\s*ANTHROPIC_MESSAGES/);
  assert.match(providersConfig, /openai:\s*{[\s\S]*channelTypes:\s*\[\s*'openai',\s*'openai_responses'\s*\]/);
  assert.match(providersConfig, /anthropic:\s*{[\s\S]*channelTypes:\s*\[\s*'anthropic',/);

  // AtlasCloud remains removed from the selectable frontend types as well.
  assert.ok(!channelsConfig.includes('atlascloud:'), 'atlascloud channel must stay removed');
  assert.ok(!providersConfig.includes('atlascloud:'), 'atlascloud provider must stay removed');
  assert.ok(!schema.includes("'atlascloud'"), 'atlascloud channel type must stay removed');
});

test('Cline has localized channel and provider labels', () => {
  for (const locale of ['en', 'zh-CN']) {
    const messages = parseLocale(locale);

    assert.equal(messages['channels.types.cline'], 'Cline');
    assert.equal(messages['channels.providers.cline'], 'Cline');
  }
});

test('OpenCode Zen supports an optional user API key and dynamic models', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsConfig = read('features/channels/data/config_channels.ts');
  const providersConfig = read('features/channels/data/config_providers.ts');
  const dialog = read('features/channels/components/channels-action-dialog.tsx');
  const bulkDialog = read('features/channels/components/channels-bulk-import-dialog.tsx');
  const protocolOptions = read('features/channels/data/protocol-options.ts');

  assert.match(schema, /channelTypeSchema[\s\S]*'opencode_zen'/);
  assert.match(
    schema,
    /data\.type !== 'opencode_zen'[\s\S]*At least one API Key is required/,
    'create validation should exempt only OpenCode Zen from API key requirements'
  );
  assert.match(
    schema,
    /bulkImportChannelItemSchema[\s\S]*data\.type !== 'opencode_zen'[\s\S]*API Key is required/,
    'bulk import validation should exempt OpenCode Zen from API key requirements'
  );
  assert.match(
    channelsConfig,
    /opencode_zen:\s*{[\s\S]*?baseURL:\s*'https:\/\/opencode\.ai\/zen\/v1'[\s\S]*?defaultModels:\s*\[\][\s\S]*?apiFormat:\s*OPENAI_CHAT_COMPLETIONS/
  );
  assert.match(channelsConfig, /CHANNEL_TYPE_TO_PROVIDER[\s\S]*opencode_zen:\s*'opencode_zen'/);
  assert.match(providersConfig, /opencode_zen:\s*{[\s\S]*channelTypes:\s*\[\s*'opencode_zen'\s*\]/);
  assert.match(dialog, /const isOpenCodeZenType = activeChannelType === 'opencode_zen'/);
  assert.match(
    dialog,
    /\(isOpenCodeZenType \|\| !isEdit \|\| apiKeyMode !== 'pool'\)[\s\S]*name='credentials\.apiKeys'/,
    'the channel form should show the optional API key input for OpenCode Zen'
  );
  assert.match(dialog, /!isOpenCodeZenType\s*&&\s*\([\s\S]*name='credentials\.mode'/, 'OpenCode Zen should hide key pool mode controls');
  assert.match(dialog, /isOpenCodeZenSubmit[\s\S]*values\.credentials\.mode = 'single'[\s\S]*\.slice\(0, 1\)/);
  assert.match(dialog, /channels\.dialogs\.fields\.apiKey\.openCodeZenHint/);
  assert.match(
    dialog,
    /isXAISubscriptionType \|\| isOpenCodeZenType[\s\S]*return !!baseURL/,
    'static model fetching should not require an API key'
  );
  assert.match(bulkDialog, /typeResult\.data !== 'opencode_zen'[\s\S]*apiKeyRequired/);
  assert.match(
    protocolOptions,
    /OPEN_CODE_ZEN_API_FORMATS[\s\S]*'openai\/chat_completions'[\s\S]*'openai\/responses'[\s\S]*'anthropic\/messages'/,
    'OpenCode Zen should expose its three native protocols'
  );
  assert.match(
    dialog,
    /isOpenCodeZenSubmit[\s\S]*getModelProtocolsForChannelApiFormat/,
    'OpenCode Zen API format selection should be persisted as model protocol settings'
  );

  const en = parseLocale('en');
  const zh = parseLocale('zh-CN');
  assert.equal(en['channels.types.opencode_zen'], 'OpenCode Zen');
  assert.equal(en['channels.providers.opencode_zen'], 'OpenCode Zen');
  assert.equal(zh['channels.types.opencode_zen'], 'OpenCode Zen');
  assert.equal(zh['channels.providers.opencode_zen'], 'OpenCode Zen');
  assert.equal(
    en['channels.dialogs.fields.apiKey.openCodeZenHint'],
    'Optional. Leave blank to use Bearer public. Contributor-free models only accept requests that retain the OpenCode-compatible request structure.'
  );
  assert.equal(
    zh['channels.dialogs.fields.apiKey.openCodeZenHint'],
    '可选。留空时使用 Bearer public。Contributor Free 模型仅接受保留 OpenCode 兼容请求结构的调用。'
  );
  assert.match(en['channels.dialogs.bulkImport.supportedTypes'], /opencode_zen/);
  assert.match(zh['channels.dialogs.bulkImport.supportedTypes'], /opencode_zen/);
});

test('xAI subscription is exposed as an OAuth Responses channel', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsConfig = read('features/channels/data/config_channels.ts');
  const providersConfig = read('features/channels/data/config_providers.ts');
  const channelColumns = read('features/channels/components/channels-columns.tsx');

  assert.match(schema, /channelTypeSchema[\s\S]*'xai_subscription'/);
  assert.equal((schema.match(/data\.type === 'xai_subscription'/g) ?? []).length, 1, 'create schema should validate xAI OAuth credentials');
  assert.match(schema, /effectiveType === 'xai_subscription'/, 'update schema should validate xAI OAuth credentials');
  assert.match(
    schema,
    /requiresJSON\s*=\s*isCopilot\s*\|\|\s*type\s*===\s*'xai_subscription'[\s\S]*if\s*\(requiresJSON\s*&&\s*!apiKey\.trim\(\)\.startsWith\('\{'\)\)/,
    'xAI subscription should reject a plain API key before the generic JSON early return'
  );
  assert.match(
    channelsConfig,
    /xai_subscription:\s*{[\s\S]*baseURL:\s*'https:\/\/cli-chat-proxy\.grok\.com\/v1'[\s\S]*apiFormat:\s*OPENAI_RESPONSES/
  );
  assert.match(providersConfig, /xai_subscription:\s*{[\s\S]*channelTypes:\s*\[\s*'xai_subscription'\s*\]/);
  assert.match(
    channelColumns,
    /channel\.type !== 'xai_subscription'\s*&&\s*\([\s\S]*setOpen\('endpoints'\)/,
    'xAI subscription channels should not expose an endpoint editor that the server rejects'
  );
});

test('channel table shows provider quota only for OAuth channel types', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsData = read('features/channels/data/channels.ts');
  const channelColumns = read('features/channels/components/channels-columns.tsx');

  assert.match(schema, /providerQuotaStatus:\s*providerQuotaStatusSchema\.optional\(\)\.nullable\(\)/);
  assert.match(
    channelsData,
    /providerQuotaStatus\s*\{[\s\S]*status[\s\S]*quotaData[\s\S]*providerType[\s\S]*\}/,
    'channel list query should load the persisted provider quota status'
  );
  const oauthTypes = channelColumns.match(/const OAUTH_CHANNEL_TYPES\s*=\s*new Set<Channel\['type'\]>\(\[([\s\S]*?)\]\);/)?.[1];
  assert.ok(oauthTypes, 'OAuth channel type Set declaration should exist');
  for (const type of ['codex', 'claudecode', 'antigravity', 'github_copilot', 'xai_subscription']) {
    assert.match(oauthTypes, new RegExp(`'${type}'`));
  }
  assert.match(channelColumns, /100\s*-\s*usageRatio\s*\*\s*100/, 'the table should display remaining quota percentage');
  assert.match(channelColumns, /QUOTA_VISIBLE_LIMIT\s*=\s*5/, 'quota cells should initially show at most five rows');
  assert.match(channelColumns, /isExpanded\s*\?\s*limits\s*:\s*limits\.slice\(0,\s*QUOTA_VISIBLE_LIMIT\)/);
  assert.match(channelColumns, /channels\.quota\.expand/);
  assert.match(channelColumns, /channels\.quota\.collapse/);
  assert.doesNotMatch(channelColumns, /limit\.window\s*=\s*labels\[index\]/, 'xAI windows must not be labeled by array position');
  assert.match(channelColumns, /Math\.abs\(limit\.usageRatio\s*-\s*usageRatio\)/, 'legacy xAI limits should match raw billing usage');
  assert.match(
    channelColumns,
    /if\s*\(!OAUTH_CHANNEL_TYPES\.has\(channel\.type\)\)[\s\S]*?>-<\/span>/,
    'non-OAuth channels should display a dash'
  );
});

test('channel proxy connection reuse setting is submitted, echoed, and localized', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsData = read('features/channels/data/channels.ts');
  const proxyDialog = read('features/channels/components/channels-proxy-dialog.tsx');

  assert.match(
    schema,
    /proxyConfigSchema[\s\S]*disableConnectionReuse:\s*z\.boolean\(\)\.optional\(\)/,
    'ProxyConfig schema should accept disableConnectionReuse'
  );

  const proxySelections = channelsData.match(/proxy\s*\{[\s\S]*?\}/g) ?? [];
  assert.equal(proxySelections.length, 6, 'all channel proxy selections should be covered by this assertion');
  for (const selection of proxySelections) {
    assert.match(selection, /disableConnectionReuse/, 'channel proxy queries should echo disableConnectionReuse');
  }
  assert.match(channelsData, /proxy\?:\s*ProxyConfig;/, 'channel test input should use the shared ProxyConfig type');

  assert.match(proxyDialog, /name='disableConnectionReuse'/, 'proxy dialog should render the connection reuse switch');
  const submitSection = proxyDialog.slice(proxyDialog.indexOf('const onSubmit'), proxyDialog.indexOf('const handleTest'));
  const testSection = proxyDialog.slice(proxyDialog.indexOf('const handleTest'), proxyDialog.indexOf('return ('));
  assert.match(
    submitSection,
    /const proxyConfig[\s\S]*disableConnectionReuse:\s*values\.disableConnectionReuse/,
    'channel save payload should send disableConnectionReuse'
  );
  assert.match(
    testSection,
    /const proxyConfig[\s\S]*disableConnectionReuse:\s*values\.disableConnectionReuse/,
    'channel test payload should send disableConnectionReuse'
  );
  const presetPayload = submitSection.match(/saveProxyPreset\.mutate\(\{[\s\S]*?\}\);/)?.[0] ?? '';
  assert.doesNotMatch(presetPayload, /disableConnectionReuse/, 'proxy presets should remain address and credential only');
  assert.match(
    proxyDialog,
    /channels\.dialogs\.proxy\.fields\.disableConnectionReuse\.description/,
    'proxy dialog should render the explanatory text below the option'
  );

  const en = parseLocale('en');
  assert.equal(en['channels.dialogs.proxy.fields.disableConnectionReuse.label'], 'Use a new proxy connection for every request');
  assert.equal(
    en['channels.dialogs.proxy.fields.disableConnectionReuse.description'],
    'Enable this for proxy pools such as Resin that rotate nodes per connection. Each request will create a new proxy connection, increasing CONNECT and TLS handshake overhead.'
  );

  const zh = parseLocale('zh-CN');
  assert.equal(zh['channels.dialogs.proxy.fields.disableConnectionReuse.label'], '每次请求使用新的代理连接');
  assert.equal(
    zh['channels.dialogs.proxy.fields.disableConnectionReuse.description'],
    '适用于 Resin 等按连接切换节点的代理池。开启后每个请求都会重新建立代理连接，并增加 CONNECT 与 TLS 握手开销。'
  );
});
test('Command Code exposes OpenAI and Anthropic channel variants with shared base URL', () => {
  const schema = read('features/channels/data/schema.ts');
  const channelsConfig = read('features/channels/data/config_channels.ts');
  const providersConfig = read('features/channels/data/config_providers.ts');
  const systemQuotas = read('features/system/data/quotas.ts');
  const dialog = read('features/channels/components/channels-action-dialog.tsx');

  assert.match(schema, /channelTypeSchema[\s\S]*'commandcode'[\s\S]*'commandcode_anthropic'/);
  assert.match(
    schema,
    /commandCodeQuotaSettingsSchema[\s\S]*authCookie:[\s\S]*z\.string\(\)\.optional\(\)\.nullable\(\)/,
    'schema should model the Command Code quota cookie'
  );
  assert.match(schema, /channelSettingsSchema[\s\S]*providerQuota:[\s\S]*channelProviderQuotaSettingsSchema/);
  assert.match(
    channelsConfig,
    /commandcode:\s*{[\s\S]*?channelType:\s*'commandcode'[\s\S]*?baseURL:\s*'https:\/\/api\.commandcode\.ai\/provider\/v1'[\s\S]*?defaultModels:\s*\[\][\s\S]*?apiFormat:\s*OPENAI_CHAT_COMPLETIONS,/,
    'commandcode should use the shared base URL with OpenAI chat completions and no static models'
  );
  assert.match(
    channelsConfig,
    /commandcode_anthropic:\s*{[\s\S]*?channelType:\s*'commandcode_anthropic'[\s\S]*?baseURL:\s*'https:\/\/api\.commandcode\.ai\/provider\/v1'[\s\S]*?defaultModels:\s*\[\][\s\S]*?apiFormat:\s*ANTHROPIC_MESSAGES,/,
    'commandcode_anthropic should use the shared base URL with Anthropic messages and no static models'
  );
  assert.match(channelsConfig, /CHANNEL_TYPE_TO_PROVIDER[\s\S]*commandcode_anthropic:\s*'commandcode'/);
  assert.match(
    providersConfig,
    /commandcode:\s*{[\s\S]*channelTypes:\s*\[\s*'commandcode',\s*'commandcode_anthropic'\s*\]/,
    'PROVIDER_CONFIGS should group both Command Code channel types'
  );
  assert.match(
    systemQuotas,
    /type:\s*'commandcode'\s*\|\s*'commandcode_anthropic';[\s\S]*quotaData: ProviderCommandCodeQuotaData/,
    'quota parsing should type Command Code channels with ProviderCommandCodeQuotaData'
  );
  assert.match(
    dialog,
    /settings\.providerQuota\.commandCode\.authCookie/,
    'the channel dialog should bind the quota cookie input to settings.providerQuota.commandCode.authCookie'
  );
});

test('Command Code has localized channel, provider, cookie field, and quota labels', () => {
  for (const locale of ['en', 'zh-CN']) {
    const channels = parseLocale(locale);
    const system = JSON.parse(read(`locales/${locale}/system.json`));

    assert.equal(channels['channels.types.commandcode'], 'Command Code');
    assert.equal(channels['channels.providers.commandcode'], 'Command Code');
    assert.ok(channels['channels.types.commandcode_anthropic']);
    assert.ok(
      channels['channels.dialogs.fields.commandCodeQuota.authCookie.placeholder'].includes('__Secure-commandcode_prod_.session_token')
    );
    assert.ok(channels['channels.dialogs.fields.commandCodeQuota.authCookie.description']);
    assert.ok(system['quota.label.commandcode.top_up']);
    assert.ok(system['quota.label.commandcode.no_windows']);
    assert.ok(system['system.quota.collection.providers.commandcode']);
  }
});
