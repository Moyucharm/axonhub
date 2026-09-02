import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

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
  assert.match(channelsConfig, /cline:\s*{[\s\S]*baseURL:\s*'https:\/\/api\.cline\.bot\/api\/v1'/, 'Cline should use the documented API base URL');
  assert.match(channelsConfig, /cline:\s*{[\s\S]*apiFormat:\s*OPENAI_CHAT_COMPLETIONS/, 'Cline should use OpenAI Chat Completions in the UI');
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
  assert.equal(proxySelections.length, 5, 'all five channel proxy selections should be covered by this assertion');
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
