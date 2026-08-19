import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { mergeChannelSettingsForUpdate } from '../utils/merge.ts';

const strategy = {
  installationId: 'b832f8db-e28c-438c-bf64-e4f85b118ef7',
  threadId: 'thread_2f2ae53bf8084f46be2b6a7c9a18fb29',
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

const simulation = {
  enabled: true,
  preset: 'normal',
  options,
  version: '0.145.0',
  platform: 'ubuntu',
  standardUserAgent: 'custom-standard',
  liteUserAgent: 'custom-lite',
  strategy,
};

test('unrelated channel settings updates exclude a stale Codex strategy', () => {
  const merged = mergeChannelSettingsForUpdate({ codexSimulation: simulation }, {});

  assert.equal(merged.codexSimulation?.version, '0.145.0');
  assert.equal('strategy' in merged.codexSimulation, false);
});

test('an explicit Codex simulation patch includes the complete draft fingerprint', () => {
  const merged = mergeChannelSettingsForUpdate(
    { codexSimulation: simulation },
    { codexSimulation: simulation },
  );

  assert.equal(merged.codexSimulation?.version, '0.145.0');
  assert.equal(merged.codexSimulation?.platform, 'ubuntu');
  assert.equal(merged.codexSimulation?.standardUserAgent, 'custom-standard');
  assert.equal(merged.codexSimulation?.liteUserAgent, 'custom-lite');
  assert.deepEqual(merged.codexSimulation?.strategy, strategy);
});

test('saving a disabled Codex simulation clears its persisted settings', () => {
  const merged = mergeChannelSettingsForUpdate({ codexSimulation: simulation }, { codexSimulation: null });

  assert.equal(merged.codexSimulation, null);
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
    { codexSimulation: simulation },
    { codexSimulation: { enabled: true, preset: 'normal', options: allDisabled } },
  );

  assert.deepEqual(merged.codexSimulation?.options, allDisabled);
  assert.equal('strategy' in merged.codexSimulation, false);
});

test('Codex fingerprint controls remain local until the save mutation', () => {
  const srcRoot = join(import.meta.dirname, '..', '..', '..');
  const dialog = readFileSync(join(srcRoot, 'features/channels/components/channels-codex-simulation-dialog.tsx'), 'utf8');
  const channelData = readFileSync(join(srcRoot, 'features/channels/data/channels.ts'), 'utf8');

  assert.doesNotMatch(dialog, /useRandomizeChannelCodexSimulation|randomizeSimulation\.mutateAsync|draftDirty/);
  assert.doesNotMatch(channelData, /RandomizeChannelCodexSimulation|useRandomizeChannelCodexSimulation/);
  assert.match(dialog, /createRandomUUID/);
  assert.doesNotMatch(dialog, /crypto\.randomUUID\(\)/);
  assert.match(dialog, /onClick=\{applyRandomFingerprint\}/);
  assert.match(dialog, /if \(!sim\?\.enabled\)[\s\S]*version: ''[\s\S]*platform: null[\s\S]*strategy: null/);
  assert.match(dialog, /const codexSimulation = enabled[\s\S]*strategy: strategy![\s\S]*: null;/);
  assert.match(dialog, /updateChannel\.mutateAsync/);
  assert.match(dialog, /sm:max-w-3xl/);
});
