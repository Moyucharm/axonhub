import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const srcRoot = join(import.meta.dirname, '..', '..', '..');
const read = (path) => readFileSync(join(srcRoot, path), 'utf8');
const locale = (name) => JSON.parse(read(`locales/${name}/channels.json`));

test('key pool schema and GraphQL hooks cover managed key behavior', () => {
  const schema = read('features/channels/data/schema.ts');
  const data = read('features/channels/data/channels.ts');
  assert.match(schema, /apiKeyModeSchema = z\.enum\(\['single', 'pool'\]\)/);
  assert.match(schema, /apiKeyPoolSettingsSchema[\s\S]*autoCheckIntervalHours/);
  assert.match(schema, /channelAPIKeyStateSchema[\s\S]*failureCount/);
  for (const operation of ['importChannelAPIKeys', 'exportChannelAPIKeys', 'removeChannelAPIKeys', 'checkChannelAPIKeys']) {
    assert.match(data, new RegExp(operation), `${operation} should be wired in the channel data layer`);
  }
});

test('key pool UI exposes mode selection and standalone management', () => {
  const dialog = read('features/channels/components/channels-action-dialog.tsx');
  const panel = read('features/channels/components/channel-api-key-pool-panel.tsx');
  const columns = read('features/channels/components/channels-columns.tsx');
  const dialogs = read('features/channels/components/channels-dialogs.tsx');
  const contextFile = read('features/channels/context/channels-context.tsx');
  assert.match(dialog, /name='credentials\.mode'/);
  assert.match(dialog, /name='settings\.apiKeyPool\.autoCheckEnabled'/);
  // Edit saves must not overwrite existing pool keys with the stale form snapshot.
  assert.match(dialog, /isPoolMode && !switchingToPool/);
  assert.match(dialog, /switchingToSingle/);
  // Edit mode pool shows a summary card plus a manage entry instead of the raw textarea.
  assert.match(dialog, /isEdit && apiKeyMode === 'pool'/);
  assert.match(dialog, /setKeyPoolOpen\(true\)/);
  assert.match(dialog, /getChannelAPIKeySummary\(managedChannel/);
  assert.match(panel, /useImportChannelAPIKeys/);
  assert.match(panel, /useExportChannelAPIKeys/);
  assert.match(panel, /useCheckChannelAPIKeys/);
  assert.match(panel, /onChannelChange/);
  assert.match(panel, /handleDisableSelected/);
  assert.match(panel, /handleEnableAll/);
  assert.match(panel, /handleSaveSettings/);
  assert.match(panel, /useTestChannelAPIKey/);
  // Selected-key check, pool summary line, disabled expiry and last auto-check display.
  assert.match(panel, /selected\.size > 0/);
  assert.match(panel, /enabledKeys/);
  assert.match(panel, /channels\.keyPool\.expiresAt/);
  assert.match(panel, /channels\.keyPool\.lastAutoCheck/);
  assert.match(panel, /state\.failureCount > 0/);
  assert.match(panel, /ChannelsAPIKeyRulesDialog/);
  assert.match(panel, /rulesOpen/);
  assert.match(panel, /isDefaultDisableRule/);
  assert.match(panel, /autoDisableEnabled/);
  assert.match(panel, /channels\.keyPool\.autoDisableThreshold/);
  assert.match(panel, /channels\.keyPool\.manageRules/);
  assert.match(panel, /channels\.keyPool\.rulesCount/);
  assert.match(panel, /channels\.keyPool\.selectAll/);
  assert.match(panel, /channels\.keyPool\.selectedCount/);
  assert.match(panel, /channels\.keyPool\.checkSelected/);
  assert.match(panel, /channels\.keyPool\.checkAll/);
  assert.match(panel, /channels\.keyPool\.exportSelected/);
  assert.match(panel, /channels\.keyPool\.exportAll/);
  assert.match(columns, /setOpen\('keyPool'\)/);
  // List name cell shows an enabled/total badge for pool channels.
  assert.match(columns, /getChannelAPIKeySummary\(channel\)/);
  assert.match(columns, /channels\.keyPool\.summary/);
  assert.match(columns, /isPool &&/);
  // The legacy dialogs are no longer entry points.
  assert.doesNotMatch(columns, /setOpen\('testAPIKeys'\)/);
  assert.doesNotMatch(columns, /setOpen\('disabledAPIKeys'\)/);
  assert.doesNotMatch(columns, /setOpen\('apiKeyRules'\)/);
  assert.doesNotMatch(dialogs, /ChannelsDisabledAPIKeysDialog/);
  assert.doesNotMatch(dialogs, /ChannelsTestAPIKeysDialog/);
  assert.doesNotMatch(dialogs, /ChannelsAPIKeyRulesDialog/);
  assert.doesNotMatch(contextFile, /'testAPIKeys'/);
  assert.doesNotMatch(contextFile, /'disabledAPIKeys'/);
  assert.doesNotMatch(contextFile, /'apiKeyRules'/);
  // Expanded row shows key mode and pool summary.
  const expandedRow = read('features/channels/components/channel-expanded-row.tsx');
  assert.match(expandedRow, /getChannelAPIKeySummary\(channel\)/);
  assert.match(expandedRow, /channels\.keyPool\.mode\.label/);
  assert.match(expandedRow, /channels\.keyPool\.keysLabel/);
});

test('key pool strings exist in English and Simplified Chinese', () => {
  for (const name of ['en', 'zh-CN']) {
    const messages = locale(name);
    for (const key of [
      'channels.keyPool.action',
      'channels.keyPool.mode.single',
      'channels.keyPool.mode.pool',
      'channels.keyPool.autoCheck',
      'channels.keyPool.import',
      'channels.keyPool.export',
      'channels.keyPool.check',
      'channels.keyPool.settings',
      'channels.keyPool.settingsTitle',
      'channels.keyPool.manage',
      'channels.keyPool.editSummary',
      'channels.keyPool.editHint',
      'channels.keyPool.enableSelected',
      'channels.keyPool.disableSelected',
      'channels.keyPool.confirmDisableSelected',
      'channels.keyPool.confirmRemoveSelected',
      'channels.keyPool.enableAll',
      'channels.keyPool.switchToSingleTitle',
      'channels.keyPool.switchToSingleDescription',
      'channels.keyPool.keepKey',
      'channels.keyPool.intervalTooSmall',
      'channels.keyPool.expiresAt',
      'channels.keyPool.keysLabel',
      'channels.keyPool.failures',
      'channels.keyPool.errorCode',
      'channels.keyPool.selectAll',
      'channels.keyPool.selectedCount',
      'channels.keyPool.exportSelected',
      'channels.keyPool.exportAll',
      'channels.keyPool.checkSelected',
      'channels.keyPool.checkAll',
      'channels.keyPool.exportSelectedResult',
      'channels.keyPool.rules',
      'channels.keyPool.manageRules',
      'channels.keyPool.rulesCount',
      'channels.keyPool.autoDisable',
      'channels.keyPool.autoDisableDescription',
      'channels.keyPool.autoDisableThreshold',
      'channels.keyPool.times',
      'channels.keyPool.advancedRulesHint',
      'channels.keyPool.thresholdTooSmall',
    ]) {
      assert.equal(typeof messages[key], 'string', `${name} should define ${key}`);
      assert.ok(messages[key].length > 0, `${name} ${key} should not be empty`);
    }
  }
});
