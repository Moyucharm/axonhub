import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  getApiFormatsForProvider,
  getAvailableProtocolFormats,
  getChannelTypeForApiFormat,
  getConfigurableApiFormatsForChannelType,
  getEffectiveApiFormatForChannel,
  getModelProtocolsForApiFormat,
  getModelProtocolsForChannelApiFormat,
} from './protocol-options.ts';
import { getApiPath } from '../../requests/utils/curl-paths.ts';

const dataDir = import.meta.dirname;
const frontendRoot = join(dataDir, '..', '..', '..');

function read(relativePath) {
  return readFileSync(join(frontendRoot, relativePath), 'utf8');
}

function parseLocale(locale, file) {
  return JSON.parse(read(`locales/${locale}/${file}`));
}

const providerConfigs = {
  zenmux: { channelTypes: ['zenmux', 'zenmux_responses', 'zenmux_video'] },
  openai: { channelTypes: ['openai', 'openai_responses'] },
  opencode_zen: { channelTypes: ['opencode_zen'] },
};

const channelConfigs = {
  zenmux: { apiFormat: 'openai/chat_completions' },
  zenmux_responses: { apiFormat: 'openai/responses' },
  zenmux_anthropic: { apiFormat: 'anthropic/messages' },
  zenmux_gemini: { apiFormat: 'gemini/contents' },
  zenmux_video: { apiFormat: 'zenmux/video' },
  openai: { apiFormat: 'openai/chat_completions' },
  openai_responses: { apiFormat: 'openai/responses' },
  opencode_zen: { apiFormat: 'openai/chat_completions' },
};

const configs = { providerConfigs, channelConfigs };

test('includes ZenMux native video in the add-channel provider formats', () => {
  assert.deepEqual(getApiFormatsForProvider('zenmux', configs), ['openai/chat_completions', 'openai/responses', 'zenmux/video']);
});

test('maps ZenMux native video back to the dedicated ZenMux video channel type', () => {
  assert.equal(getChannelTypeForApiFormat('zenmux', 'zenmux/video', configs), 'zenmux_video');
});

test('exposes the native video default endpoint to model protocol editing', () => {
  assert.deepEqual(getAvailableProtocolFormats([{ apiFormat: 'zenmux/video' }], []), ['zenmux/video']);
});

test('exposes the three native OpenCode Zen endpoint formats', () => {
  const formats = ['openai/chat_completions', 'openai/responses', 'anthropic/messages'];
  assert.deepEqual(getApiFormatsForProvider('opencode_zen', configs), formats);
  assert.deepEqual(getConfigurableApiFormatsForChannelType('opencode_zen', formats), formats);
  for (const format of formats) {
    assert.equal(getChannelTypeForApiFormat('opencode_zen', format, configs), 'opencode_zen');
  }
});

test('persists the selected OpenCode Zen protocol for supported models', () => {
  assert.deepEqual(getModelProtocolsForChannelApiFormat('opencode_zen', 'openai/responses', ['gpt-5.4', 'qwen3-coder']), [
    { model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: true },
    { model: 'qwen3-coder', apiFormats: ['openai/responses'], enabled: true },
  ]);
  assert.deepEqual(
    getModelProtocolsForChannelApiFormat(
      'opencode_zen',
      'anthropic/messages',
      ['claude-sonnet-4-5'],
      [{ model: 'claude-sonnet-4-5', apiFormats: ['openai/responses'], enabled: true }]
    ),
    [{ model: 'claude-sonnet-4-5', apiFormats: ['anthropic/messages'], enabled: true }]
  );
  assert.deepEqual(
    getModelProtocolsForChannelApiFormat(
      'opencode_zen',
      'openai/chat_completions',
      ['gpt-5.4'],
      [{ model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: true }]
    ),
    [{ model: 'gpt-5.4', apiFormats: ['openai/chat_completions'], enabled: true }]
  );
  assert.deepEqual(getModelProtocolsForChannelApiFormat('opencode_zen', 'openai/chat_completions', ['space-bunny-free']), [
    { model: 'space-bunny-free', apiFormats: ['openai/chat_completions'], enabled: true },
  ]);
});

test('restores the persisted OpenCode Zen protocol in the editor', () => {
  for (const apiFormat of ['openai/chat_completions', 'openai/responses', 'anthropic/messages']) {
    assert.equal(
      getEffectiveApiFormatForChannel(
        'opencode_zen',
        'openai/chat_completions',
        [{ model: 'gpt-5.4', apiFormats: [apiFormat], enabled: true }],
        undefined
      ),
      apiFormat
    );
  }

  // Endpoints decide only when no model pins a protocol.
  assert.equal(
    getEffectiveApiFormatForChannel('opencode_zen', 'openai/chat_completions', [], [{ apiFormat: 'anthropic/messages' }]),
    'anthropic/messages'
  );
  // An explicit Chat selection outranks an Anthropic endpoint.
  assert.equal(
    getEffectiveApiFormatForChannel(
      'opencode_zen',
      'openai/chat_completions',
      [{ model: 'gpt-5.4', apiFormats: ['openai/chat_completions'], enabled: true }],
      [{ apiFormat: 'anthropic/messages' }]
    ),
    'openai/chat_completions'
  );
  // An explicit Responses selection outranks a Chat endpoint.
  assert.equal(
    getEffectiveApiFormatForChannel(
      'opencode_zen',
      'openai/chat_completions',
      [{ model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: true }],
      [{ apiFormat: 'openai/chat_completions' }]
    ),
    'openai/responses'
  );
  // Conflicting overrides keep the channel default, and an empty endpoint list
  // falls back to it as well.
  assert.equal(
    getEffectiveApiFormatForChannel(
      'opencode_zen',
      'openai/chat_completions',
      [
        { model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: true },
        { model: 'claude-sonnet-4-5', apiFormats: ['anthropic/messages'], enabled: true },
      ],
      [{ apiFormat: 'anthropic/messages' }]
    ),
    'openai/chat_completions'
  );
  assert.equal(getEffectiveApiFormatForChannel('opencode_zen', 'openai/chat_completions', [], []), 'openai/chat_completions');
  // A disabled override is not an explicit selection.
  assert.equal(
    getEffectiveApiFormatForChannel(
      'opencode_zen',
      'openai/chat_completions',
      [{ model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: false }],
      [{ apiFormat: 'anthropic/messages' }]
    ),
    'anthropic/messages'
  );
});

test('persists protocols for newly added models while preserving untouched models', () => {
  const existing = [
    { model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: true },
    { model: 'other-model', apiFormats: ['anthropic/messages'], enabled: true },
  ];
  const result = getModelProtocolsForChannelApiFormat('opencode_zen', 'openai/responses', ['gpt-5.4', 'new-model'], existing);
  assert.deepEqual(result, [
    { model: 'other-model', apiFormats: ['anthropic/messages'], enabled: true },
    { model: 'gpt-5.4', apiFormats: ['openai/responses'], enabled: true },
    { model: 'new-model', apiFormats: ['openai/responses'], enabled: true },
  ]);
});

test('keeps TypeSafe System One available as a custom endpoint format', () => {
  assert.deepEqual(
    getConfigurableApiFormatsForChannelType('openai', ['openai/chat_completions', 'typesafe/systemone', 'zenmux/video']),
    ['openai/chat_completions', 'typesafe/systemone']
  );
});

test('does not expose ZenMux native video to unrelated providers', () => {
  assert.deepEqual(getApiFormatsForProvider('openai', configs), ['openai/chat_completions', 'openai/responses']);
  assert.equal(getChannelTypeForApiFormat('openai', 'zenmux/video', configs), undefined);
});

test('does not expose ZenMux native video to a provider lacking the ZenMux video channel type', () => {
  const openaiOnly = { providerConfigs: { zenmux: { channelTypes: ['zenmux_responses', 'zenmux_anthropic', 'zenmux_gemini'] } }, channelConfigs };
  assert.deepEqual(getApiFormatsForProvider('zenmux', openaiOnly), ['openai/responses', 'anthropic/messages', 'gemini/contents']);
  assert.equal(getChannelTypeForApiFormat('zenmux', 'zenmux/video', openaiOnly), undefined);
});

test('preserves the existing reverse mapping for known formats', () => {
  assert.equal(getChannelTypeForApiFormat('zenmux', 'openai/responses', configs), 'zenmux_responses');
});

test('persists ZenMux native video for every selected supported model', () => {
  assert.deepEqual(getModelProtocolsForApiFormat('zenmux/video', ['sora-2', 'veo-3.1']), [
    { model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true },
    { model: 'veo-3.1', apiFormats: ['zenmux/video'], enabled: true },
  ]);
});

test('keeps per-model protocol overrides when selecting ZenMux native video in a mixed configuration', () => {
  const existing = [
    { model: 'gpt-5', apiFormats: ['openai/chat_completions'], enabled: true },
    { model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true },
  ];
  assert.deepEqual(getModelProtocolsForApiFormat('zenmux/video', ['gpt-5', 'sora-2', 'veo-3.1'], existing), [
    { model: 'gpt-5', apiFormats: ['openai/chat_completions'], enabled: true },
    { model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true },
    { model: 'veo-3.1', apiFormats: ['zenmux/video'], enabled: true },
  ]);
});

test('edit without format change keeps existing model protocol selections unchanged', () => {
  const existing = [
    { model: 'gpt-5', apiFormats: ['openai/chat_completions'], enabled: true },
    { model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true },
  ];
  const result = getModelProtocolsForApiFormat('zenmux/video', ['gpt-5', 'sora-2'], existing);
  assert.deepEqual(result, existing);
  assert.equal(result[1].apiFormats[0], 'zenmux/video');
});

test('selecting ZenMux native video never replaces non-video overrides', () => {
  const existing = [
    { model: 'sora-2', apiFormats: ['zenmux/video', 'openai/chat_completions'], enabled: true },
    { model: 'veo-3.1', apiFormats: ['openai/chat_completions'], enabled: true },
  ];
  assert.deepEqual(getModelProtocolsForApiFormat('zenmux/video', ['sora-2', 'veo-3.1', 'new-video'], existing), [
    { model: 'sora-2', apiFormats: ['zenmux/video', 'openai/chat_completions'], enabled: true },
    { model: 'veo-3.1', apiFormats: ['openai/chat_completions'], enabled: true },
    { model: 'new-video', apiFormats: ['zenmux/video'], enabled: true },
  ]);
});

test('restores ZenMux native video from persisted model protocols', () => {
  assert.equal(
    getEffectiveApiFormatForChannel(
      'zenmux',
      'openai/chat_completions',
      [{ model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true }],
      undefined
    ),
    'zenmux/video'
  );
});

test('keeps non-video protocols unchanged', () => {
  assert.deepEqual(getModelProtocolsForApiFormat('openai/chat_completions', ['gpt-5']), []);
  assert.equal(
    getEffectiveApiFormatForChannel(
      'zenmux_responses',
      'openai/responses',
      [{ model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true }],
      undefined
    ),
    'openai/responses'
  );
});

test('removes stale ZenMux video overrides when an edited channel leaves video format', () => {
  assert.deepEqual(
    getModelProtocolsForApiFormat(
      'openai/chat_completions',
      [],
      [
        { model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true },
        { model: 'gpt-5', apiFormats: ['zenmux/video', 'openai/chat_completions'], enabled: true },
      ]
    ),
    [{ model: 'gpt-5', apiFormats: ['openai/chat_completions'], enabled: true }]
  );
});

const configurableFormats = ['openai/chat_completions', 'openai/responses', 'typesafe/systemone', 'openai/decisions'];

test('shows the OpenAI Decisions endpoint only for the OpenAI create/edit channel type', () => {
  const openai = getConfigurableApiFormatsForChannelType('openai', configurableFormats);
  assert.ok(openai.includes('openai/decisions'), 'openai channel type should offer openai/decisions');

  for (const channelType of ['openai_responses', 'anthropic', 'gemini', 'zenmux', 'typesafe', 'codex', 'xai']) {
    const formats = getConfigurableApiFormatsForChannelType(channelType, configurableFormats);
    assert.ok(!formats.includes('openai/decisions'), `${channelType} must not offer openai/decisions in the UI`);
  }
});

test('keeps TypeSafe channels on System One while openai/decisions is offered', () => {
  assert.deepEqual(getConfigurableApiFormatsForChannelType('typesafe', configurableFormats), ['typesafe/systemone']);
  assert.ok(getConfigurableApiFormatsForChannelType('openai', configurableFormats).includes('typesafe/systemone'));
});

test('accepts openai/decisions in the API-format schema and configurable endpoint list', () => {
  const schema = read('features/channels/data/schema.ts');
  assert.match(schema, /apiFormatSchema[\s\S]*'openai\/decisions'/, 'apiFormatSchema should accept openai/decisions');
  assert.match(
    schema,
    /configurableChannelEndpointApiFormats[^;]*'openai\/decisions'/,
    'the endpoints dialog list should offer openai/decisions'
  );
});

test('routes the Decisions request to /v1/decisions for generated cURL', () => {
  assert.equal(getApiPath('openai/decisions'), '/v1/decisions');
});

test('localizes the openai/decisions endpoint label in channels and models locales', () => {
  for (const locale of ['en', 'zh-CN']) {
    const channels = parseLocale(locale, 'channels.json');
    const models = parseLocale(locale, 'models.json');
    assert.ok(channels['channels.dialogs.fields.apiFormat.formats.openai/decisions'], `${locale} channels label missing`);
    assert.ok(models['models.dialogs.association.conditions.formatOptions.openai/decisions'], `${locale} models label missing`);
  }
});

test('keeps the openai/decisions association format listed for model association conditions', () => {
  const dialog = read('features/models/components/models-association-dialog.tsx');
  assert.match(dialog, /requestFormatConditionOptions[\s\S]*'openai\/decisions'/, 'association format options should include openai/decisions');
});
