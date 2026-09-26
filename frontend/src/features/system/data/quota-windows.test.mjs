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
  return JSON.parse(read(`locales/${locale}/system.json`));
}

test('quota-windows.ts classifies per-channel windows by kind', () => {
  const source = read('features/system/data/quota-windows.ts');
  // Claude: 7d is weekly, 5h is hourly
  assert.match(source, /push\('7d', 'weekly'/);
  assert.match(source, /push\('5h', 'hourly'/);
  // Codex: window seconds decide weekly (6d-8d) / monthly (>=20d) / daily (~1d) / hourly (~5h)
  assert.match(source, /seconds >= 6 \* DAY && seconds <= 8 \* DAY/);
  assert.match(source, /seconds >= 20 \* DAY/);
  // Cline last7d is weekly, last30d monthly
  assert.match(source, /\['last7d', 'weekly'/);
  assert.match(source, /\['last30d', 'monthly'/);
  // OpenCode Go weekly / monthly / rolling
  assert.match(source, /\['weekly', 'weekly'/);
  assert.match(source, /\['monthly', 'monthly'/);
  // Zhipu weekly_limit is weekly, five_hour hourly
  assert.match(source, /weekly_limit: \{ kind: 'weekly'/);
  // Kimi label-based kind detection
  assert.match(source, /function kimiWindowKind/);
  // Copilot has no weekly/monthly period (other)
  assert.match(source, /kind: 'other'/);
});

test('quota capsule renders with testids and keeps header percentage semantics', () => {
  const badges = read('components/quota-badges.tsx');
  const capsule = read('components/quota-capsule.tsx');
  assert.match(capsule, /data-testid=['"]quota-capsule['"]/);
  assert.match(capsule, /data-testid=['"]quota-window-card['"]/);
  // The header battery still uses the tightest-window percentage.
  assert.match(badges, /function getChannelPercentage/);
});

test('quota capsule i18n keys exist in both locales', () => {
  const required = [
    'quota.capsule.empty',
    'quota.capsule.more',
    'quota.capsule.period.5h',
    'quota.capsule.period.1d',
    'quota.capsule.period.7d',
    'quota.capsule.period.30d',
    'quota.capsule.percent',
  ];
  for (const locale of ['en', 'zh-CN']) {
    const messages = parseLocale(locale);
    for (const key of required) {
      assert.ok(messages[key], `${locale} system.json is missing ${key}`);
    }
  }
});
