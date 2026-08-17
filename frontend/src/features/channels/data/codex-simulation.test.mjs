import assert from 'node:assert/strict';
import test from 'node:test';

import { mergeChannelSettingsForUpdate } from '../utils/merge.ts';

const strategy = {
  installationId: 'installation-id',
  threadId: 'thread_id',
  windowGeneration: 0,
};

const options = {
  prompt: true,
  userAgent: true,
  codexHeaders: true,
  clientMetadata: true,
  responsesShape: true,
  additionalTool: false,
};

test('channel settings input excludes server-managed Codex strategy', () => {
  const merged = mergeChannelSettingsForUpdate(
    {
      codexSimulation: {
        enabled: true,
        preset: 'normal',
        options,
        strategy,
      },
    },
    {},
  );

  assert.deepEqual(merged.codexSimulation, {
    enabled: true,
    preset: 'normal',
    options,
  });
  assert.equal('strategy' in merged.codexSimulation, false);
});

test('channel settings input preserves editable version and User-Agent profiles', () => {
  const merged = mergeChannelSettingsForUpdate(
    {
      codexSimulation: {
        enabled: true,
        preset: 'normal',
        options,
        version: '0.145.0',
        platform: 'ubuntu',
        standardUserAgent: 'custom-standard',
        liteUserAgent: 'custom-lite',
        strategy,
      },
    },
    {},
  );

  assert.equal(merged.codexSimulation.version, '0.145.0');
  assert.equal(merged.codexSimulation.platform, 'ubuntu');
  assert.equal(merged.codexSimulation.standardUserAgent, 'custom-standard');
  assert.equal(merged.codexSimulation.liteUserAgent, 'custom-lite');
  assert.equal('strategy' in merged.codexSimulation, false);
});

test('explicit all-false Codex options remain false in the input', () => {
  const allDisabled = {
    prompt: false,
    userAgent: false,
    codexHeaders: false,
    clientMetadata: false,
    responsesShape: false,
    additionalTool: false,
  };

  const merged = mergeChannelSettingsForUpdate(
    {
      codexSimulation: {
        enabled: true,
        preset: 'normal',
        options: allDisabled,
        strategy,
      },
    },
    { codexSimulation: { enabled: true, preset: 'normal', options: allDisabled } },
  );

  assert.deepEqual(merged.codexSimulation?.options, allDisabled);
  assert.equal('strategy' in merged.codexSimulation, false);
});
