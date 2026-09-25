import assert from 'node:assert/strict';
import test from 'node:test';
import {
  getApiFormatsForProvider,
  getAvailableProtocolFormats,
  getChannelTypeForApiFormat,
  getConfigurableApiFormatsForChannelType,
  getInitialApiFormatForChannel,
  getModelProtocolsForApiFormat,
  getModelProtocolsForChannelApiFormat,
} from './protocol-options.ts';

const providerConfigs = {
  zenmux: { channelTypes: ['zenmux', 'zenmux_responses'] },
  openai: { channelTypes: ['openai', 'openai_responses'] },
  opencode_zen: { channelTypes: ['opencode_zen'] },
};

const channelConfigs = {
  zenmux: { apiFormat: 'openai/chat_completions' },
  zenmux_responses: { apiFormat: 'openai/responses' },
  zenmux_anthropic: { apiFormat: 'anthropic/messages' },
  zenmux_gemini: { apiFormat: 'gemini/contents' },
  openai: { apiFormat: 'openai/chat_completions' },
  openai_responses: { apiFormat: 'openai/responses' },
  opencode_zen: { apiFormat: 'openai/chat_completions' },
};

const configs = { providerConfigs, channelConfigs };

test('includes ZenMux native video in the add-channel provider formats', () => {
  assert.deepEqual(getApiFormatsForProvider('zenmux', configs), ['openai/chat_completions', 'openai/responses', 'zenmux/video']);
});

test('maps ZenMux native video back to the ZenMux channel type', () => {
  assert.equal(getChannelTypeForApiFormat('zenmux', 'zenmux/video', configs), 'zenmux');
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
      getInitialApiFormatForChannel('opencode_zen', 'openai/chat_completions', [
        { model: 'gpt-5.4', apiFormats: [apiFormat], enabled: true },
      ]),
      apiFormat
    );
  }
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

test('does not expose ZenMux native video to a provider lacking the ZenMux channel type', () => {
  const openaiOnly = {
    providerConfigs: { zenmux: { channelTypes: ['zenmux_responses', 'zenmux_anthropic', 'zenmux_gemini'] } },
    channelConfigs,
  };
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
    getInitialApiFormatForChannel('zenmux', 'openai/chat_completions', [{ model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true }]),
    'zenmux/video'
  );
});

test('keeps non-video protocols unchanged', () => {
  assert.deepEqual(getModelProtocolsForApiFormat('openai/chat_completions', ['gpt-5']), []);
  assert.equal(
    getInitialApiFormatForChannel('zenmux_responses', 'openai/responses', [
      { model: 'sora-2', apiFormats: ['zenmux/video'], enabled: true },
    ]),
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
