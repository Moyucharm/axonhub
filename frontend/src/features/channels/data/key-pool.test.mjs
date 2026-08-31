import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { partitionSelectedAPIKeys, reconcileRemovedAPIKeys } from '../utils/key-pool.ts';

const srcRoot = join(import.meta.dirname, '..', '..', '..');
const read = (path) => readFileSync(join(srcRoot, path), 'utf8');
const locale = (name) => JSON.parse(read(`locales/${name}/channels.json`));

test('key pool schema and GraphQL hooks cover managed key behavior', () => {
  const schema = read('features/channels/data/schema.ts');
  const data = read('features/channels/data/channels.ts');
  assert.match(schema, /apiKeyModeSchema = z\.enum\(\['single', 'pool'\]\)/);
  assert.match(schema, /apiKeyPoolSettingsSchema[\s\S]*retryCount: z\.number\(\)\.int\(\)\.min\(1\)/);
  assert.match(schema, /apiKeyPoolSettingsSchema[\s\S]*autoCheckIntervalHours/);
  assert.match(schema, /channelAPIKeyStateSchema[\s\S]*failureCount/);
  const keyPoolUtils = read('features/channels/utils/key-pool.ts');
  assert.match(keyPoolUtils, /DEFAULT_API_KEY_POOL_REQUEST_COUNT = 3/);
  for (const operation of ['importChannelAPIKeys', 'exportChannelAPIKeys', 'removeChannelAPIKeys', 'checkChannelAPIKeys', 'disableSelectedChannelAPIKeys']) {
    assert.match(data, new RegExp(operation), `${operation} should be wired in the channel data layer`);
  }

  const updateMutation = data.match(/const UPDATE_CHANNEL_MUTATION = `([\s\S]*?)`;/)?.[1] ?? '';
  for (const field of ['credentials', 'apiKeyPool', 'bodyOverrideOperations', 'headerOverrideOperations', 'rateLimit']) {
    assert.match(updateMutation, new RegExp(`\\b${field}\\b`), `updateChannel must return ${field} for snapshot replacement`);
  }
});

test('key pool UI exposes mode selection and standalone management', () => {
  const dialog = read('features/channels/components/channels-action-dialog.tsx');
  const panel = read('features/channels/components/channel-api-key-pool-panel.tsx');
  const managementDialog = read('features/channels/components/channels-api-key-management-dialog.tsx');
  const availabilityDialog = read('features/channels/components/channels-availability-dialog.tsx');
  const columns = read('features/channels/components/channels-columns.tsx');
  const dialogs = read('features/channels/components/channels-dialogs.tsx');
  const contextFile = read('features/channels/context/channels-context.tsx');
  assert.match(dialog, /name='credentials\.mode'/);
  assert.match(dialog, /name='settings\.apiKeyPool\.autoCheckEnabled'/);
  assert.match(dialog, /DEFAULT_API_KEY_POOL_REQUEST_COUNT/);
  assert.match(dialog, /channels\.keyPool\.retryCountDescription/);
  // Edit saves must use the latest panel snapshot and never overwrite managed pool keys.
  assert.match(dialog, /const persistedChannel = managedChannel \?\? currentRow/);
  assert.match(dialog, /persistedChannel\.credentials\?\.mode === 'pool'/);
  assert.match(dialog, /isPoolMode && !switchingToPool/);
  assert.match(dialog, /handleManagedChannelChange/);
  assert.match(dialog, /form\.setValue\('credentials\.apiKeys'/);
  assert.match(dialog, /switchingToSingle/);
  // Edit mode pool shows a summary card plus a manage entry instead of the raw textarea.
  assert.match(dialog, /isEdit && apiKeyMode === 'pool'/);
  assert.match(dialog, /setKeyPoolOpen\(true\)/);
  assert.match(dialog, /getChannelAPIKeySummary\(managedChannel/);
  assert.match(panel, /useImportChannelAPIKeys/);
  assert.match(panel, /useExportChannelAPIKeys/);
  assert.match(panel, /useCheckChannelAPIKeys/);
  assert.match(panel, /onChannelChange/);
  assert.match(panel, /useDisableSelectedChannelAPIKeys/);
  assert.match(panel, /partitionSelectedAPIKeys\(selectedKeys, disabledSet\)/);
  assert.match(panel, /selectedEnabledKeys/);
  assert.match(panel, /selectedDisabledKeys/);
  assert.doesNotMatch(panel, /Promise\.all\(Array\.from\(selected\)\.map\(\(key\) => disableKey/);
  assert.match(panel, /reconcileRemovedAPIKeys\(allKeys, selected, result\.message\)/);
  assert.match(panel, /handleDisableSelected/);
  assert.match(panel, /handleEnableAll/);
  assert.match(panel, /handleSaveSettings/);
  assert.match(panel, /useTestChannelAPIKey/);
  assert.match(panel, /DEFAULT_API_KEY_POOL_REQUEST_COUNT/);
  // Selected-key check, pool summary line, disabled expiry and last auto-check display.
  assert.match(panel, /selected\.size > 0/);
  assert.match(panel, /enabledKeys/);
  assert.match(panel, /channels\.keyPool\.expiresAt/);
  assert.match(panel, /channels\.keyPool\.lastAutoCheck/);
  assert.match(panel, /state\.failureCount > 0/);
  assert.match(panel, /ChannelsAvailabilityDialog/);
  assert.match(panel, /rulesOpen/);
  assert.match(panel, /mode: 'pool'/);
  assert.match(panel, /channels\.keyPool\.rulesActiveDescription/);
  assert.match(panel, /channels\.keyPool\.rulesFallbackDescription/);
  assert.doesNotMatch(panel, /isDefaultDisableRule/);
  assert.doesNotMatch(panel, /autoDisableThreshold/);
  assert.match(panel, /channels\.keyPool\.manageRules/);
  assert.match(availabilityDialog, /disableUntilCron/);
  assert.match(availabilityDialog, /permanent_disable_delete/);
  assert.match(managementDialog, /ChannelAPIKeyPoolPanel/);
  assert.match(managementDialog, /isPool/);
  assert.match(managementDialog, /max-h-\[85vh\]/);
  assert.match(managementDialog, /overflow-hidden/);
  assert.match(dialogs, /onChannelChange=\{setCurrentRow\}/);
  assert.match(panel, /channels\.keyPool\.selectAll/);
  assert.match(panel, /channels\.keyPool\.selectedCount/);
  assert.match(panel, /channels\.keyPool\.checkSelected/);
  assert.match(panel, /channels\.keyPool\.checkAll/);
  assert.match(panel, /channels\.keyPool\.exportSelected/);
  assert.match(panel, /channels\.keyPool\.exportAll/);
  assert.match(columns, /setOpen\('keyManagement'\)/);
  // List name cell shows an enabled/total badge for pool channels.
  assert.match(columns, /getChannelAPIKeySummary\(channel\)/);
  assert.match(columns, /channels\.keyPool\.summary/);
  assert.match(columns, /isPool &&/);
  assert.match(columns, /channelPermissions\.canWrite \?/);
  assert.match(columns, /aria-label=\{t\('channels\.keyPool\.action'\)\}/);
  assert.match(columns, /event\.stopPropagation\(\)/);
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

test('key pool selection partitions enabled and disabled keys', () => {
  assert.deepEqual(
    partitionSelectedAPIKeys(['key1', 'key2', 'key3'], new Set(['key2'])),
    { enabled: ['key1', 'key3'], disabled: ['key2'] }
  );
  assert.deepEqual(partitionSelectedAPIKeys([], new Set(['key1'])), { enabled: [], disabled: [] });
});

test('key pool removal snapshot follows final-key preservation contract', () => {
  assert.deepEqual(
    reconcileRemovedAPIKeys(['key1', 'key2', 'key3'], new Set(['key1', 'key3'])),
    ['key2']
  );
  assert.deepEqual(
    reconcileRemovedAPIKeys(['key1', 'key2'], new Set(['key1', 'key2']), 'ONE_KEY_PRESERVED'),
    ['key1']
  );
  assert.deepEqual(
    reconcileRemovedAPIKeys(['key1', 'key2'], new Set(['unknown']), 'ONE_KEY_PRESERVED'),
    ['key1', 'key2']
  );
  assert.deepEqual(reconcileRemovedAPIKeys([], new Set(), 'ONE_KEY_PRESERVED'), []);
});

test('key pool strings exist in English and Simplified Chinese', () => {
  for (const name of ['en', 'zh-CN']) {
    const messages = locale(name);
    for (const key of [
      'channels.messages.disableSelectedAPIKeysSuccess',
      'channels.keyPool.action',
      'channels.keyPool.mode.single',
      'channels.keyPool.mode.pool',
      'channels.keyPool.autoCheck',
      'channels.keyPool.autoCheckDescription',
      'channels.keyPool.requestStrategyTitle',
      'channels.keyPool.retryCountDescription',
      'channels.keyPool.requestCountTooSmall',
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
      'channels.keyPool.rulesActiveDescription',
      'channels.keyPool.rulesFallbackDescription',
      'channels.keyPool.times',
    ]) {
      assert.equal(typeof messages[key], 'string', `${name} should define ${key}`);
      assert.ok(messages[key].length > 0, `${name} ${key} should not be empty`);
    }
  }
});
