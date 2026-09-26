import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  cpaWindowKind,
  cpaQuotaItemsToWindows,
  formatTime,
  shortGroupLabel,
  summarizeCredentialQuotaGroups,
  summarizeQuotaGroups,
} from './quota-windows.ts';

const srcRoot = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

function read(relative) {
  return readFileSync(path.join(srcRoot, relative), 'utf8');
}

// Identity t() stub: assert against the key text itself.
const t = (key) => key;

test('cpaWindowKind classifies periodSeconds windows', () => {
  const item = (periodSeconds) => ({ periodSeconds, label: 'x', group: '' });
  assert.equal(cpaWindowKind(item(5 * 3600)), 'hourly');
  assert.equal(cpaWindowKind(item(86400)), 'daily');
  assert.equal(cpaWindowKind(item(7 * 86400)), 'weekly');
  assert.equal(cpaWindowKind(item(30 * 86400)), 'monthly');
  // Codex-style boundary values.
  assert.equal(cpaWindowKind(item(2 * 3600)), 'hourly');
  assert.equal(cpaWindowKind(item(6 * 86400)), 'weekly');
  assert.equal(cpaWindowKind(item(20 * 86400)), 'monthly');
});

test('cpaWindowKind falls back to label text without periodSeconds', () => {
  const item = (label, group = '') => ({ label, group });
  assert.equal(cpaWindowKind(item('5 hour')), 'hourly');
  assert.equal(cpaWindowKind(item('7 day')), 'weekly');
  assert.equal(cpaWindowKind(item('7 day Opus')), 'weekly');
  assert.equal(cpaWindowKind(item('7 day Fable')), 'weekly');
  assert.equal(cpaWindowKind(item('Monthly balance')), 'monthly');
  assert.equal(cpaWindowKind(item('Extra usage')), 'other');
  assert.equal(cpaWindowKind(item('Daily tokens', 'Products')), 'daily');
  // periodSeconds wins over an ambiguous label.
  assert.equal(cpaWindowKind({ periodSeconds: 18000, label: 'Something', group: '' }), 'hourly');
});

test('cpaQuotaItemsToWindows uses usedPercent and builds tooltip extras', () => {
  const windows = cpaQuotaItemsToWindows(
    [
      {
        id: 'a',
        group: 'Code',
        label: '7 day',
        description: 'Weekly coding allowance',
        usedPercent: 42,
        remainingPercent: 58,
        used: 420,
        limit: 1000,
        unit: 'credits',
        resetAt: '2026-08-21T00:00:00Z',
      },
      { id: 'b', group: '', label: '5 hour', description: '', usedPercent: null, remainingPercent: 25 },
    ],
    t
  );

  assert.equal(windows.length, 2);
  assert.equal(windows[0].id, 'b');
  assert.equal(windows[0].kind, 'hourly');
  assert.equal(windows[0].percent, 75);
  assert.equal(windows[0].group, undefined);
  assert.equal(windows[0].shortLabelKey, 'quota.capsule.period.5h');

  assert.equal(windows[1].id, 'a');
  assert.equal(windows[1].kind, 'weekly');
  // Group prefix keeps windows distinguishable outside their pool context.
  assert.equal(windows[1].fullLabel, 'Code · 7 day');
  assert.equal(windows[1].group, 'Code');
  assert.equal(windows[1].shortLabelKey, 'quota.capsule.period.7d');
  assert.equal(windows[1].percent, 42);
  assert.equal(windows[1].tooltipExtras.length, 3);
  assert.match(windows[1].tooltipExtras[0], /cpa\.quota\.used: 420 credits \/ cpa\.quota\.limit: [\s\S]*1[,.]?000 credits/);
  assert.match(windows[1].tooltipExtras[1], /^cpa\.quota\.resetAt: /);
  assert.equal(windows[1].tooltipExtras[2], 'Weekly coding allowance');
});

test('cpaQuotaItemsToWindows clamps percentages and falls back to remainingPercent', () => {
  const windows = cpaQuotaItemsToWindows(
    [
      { id: 'c', group: '', label: 'monthly', description: '', usedPercent: 150, remainingPercent: -20 },
      { id: 'd', group: '', label: 'credits', description: '', usedPercent: null, remainingPercent: 10 },
    ],
    t
  );
  assert.equal(windows[0].percent, 100);
  assert.equal(windows[1].percent, 90);
});

test('cpaQuotaItemsToWindows skips items without any computable percentage', () => {
  // A 0/0 "Monthly balance" (both percents null) must not render as a full
  // remaining bar; only the usable sibling item survives.
  const windows = cpaQuotaItemsToWindows(
    [
      {
        id: 'z',
        group: '',
        label: 'Monthly balance',
        description: '',
        usedPercent: null,
        remainingPercent: null,
        used: 0,
        limit: 0,
        unit: 'cents',
      },
      { id: 'ok', group: '', label: 'Weekly credits', description: '', usedPercent: 30, remainingPercent: 70 },
      { id: 'no-pct-no-vals', group: '', label: 'Mystery', description: '' },
    ],
    t
  );
  assert.equal(windows.length, 1);
  assert.equal(windows[0].id, 'ok');
  assert.equal(windows[0].percent, 30);
});

test('summarizeQuotaGroups picks the tightest representative per pool in first-appearance order', () => {
  const win = (id, group, percent, kind) => ({ id, group, percent, kind });
  // Antigravity pro shape: two pools x two periods.
  const windows = [
    win('gemini-weekly', 'Gemini Models', 1, 'weekly'),
    win('gemini-5h', 'Gemini Models', 90, 'hourly'),
    win('3p-weekly', 'Claude and GPT models', 0, 'weekly'),
    win('3p-5h', 'Claude and GPT models', 40, 'hourly'),
  ];
  const groups = summarizeQuotaGroups(windows);

  assert.equal(groups.length, 2);
  assert.equal(groups[0].group, 'Gemini Models');
  assert.equal(groups[0].rep.id, 'gemini-5h');
  assert.deepEqual(
    groups[0].rest.map((w) => w.id),
    ['gemini-weekly']
  );
  assert.equal(groups[1].group, 'Claude and GPT models');
  assert.equal(groups[1].rep.id, '3p-5h');
});

test('summarizeCredentialQuotaGroups orders windows by duration ascending and alphabet', () => {
  const win = (id, group, percent, kind, periodSeconds) => ({ id, group, percent, kind, periodSeconds });
  const fiveHour = win('code-primary', 'Code', 10, 'hourly', 5 * 60 * 60);
  const weekly = win('code-secondary', 'Code', 20, 'weekly', 7 * 24 * 60 * 60);

  const codex = summarizeCredentialQuotaGroups([fiveHour, weekly], 'codex');
  // 小区间（5h）排在大区间（7d）前面
  assert.deepEqual(
    codex.map(({ rep }) => rep.id),
    ['code-primary', 'code-secondary']
  );

  const antigravity = summarizeCredentialQuotaGroups([weekly, fiveHour], 'antigravity');
  assert.deepEqual(antigravity.map(({ rep }) => rep.id), ['code-primary', 'code-secondary']);

  const monthly = win('code-monthly', 'Code', 5, 'monthly', 30 * 24 * 60 * 60);
  const triple = summarizeCredentialQuotaGroups([weekly, monthly, fiveHour], 'codex');
  // 超过两个额度区间时，小区间在前，大区间在后（供 +x 放入气泡）
  assert.deepEqual(
    triple.map(({ rep }) => rep.id),
    ['code-primary', 'code-secondary', 'code-monthly']
  );
});

test('summarizeCredentialQuotaGroups orders multi-pool windows by duration ascending then alphabet', () => {
  const win = (id, group, percent, kind, periodSeconds) => ({ id, group, percent, kind, periodSeconds });
  const groups = summarizeCredentialQuotaGroups(
    [
      win('g-5h', 'Gemini Models', 62, 'hourly', 5 * 60 * 60),
      win('g-7d', 'Gemini Models', 12, 'weekly', 7 * 24 * 60 * 60),
      win('c-5h', 'Claude and GPT models', 96, 'hourly', 5 * 60 * 60),
      win('c-7d', 'Claude and GPT models', 45, 'weekly', 7 * 24 * 60 * 60),
    ],
    'antigravity'
  );
  // 5h 优先于 7d；时间相同时 Claude (C) 在 Gemini (G) 前
  assert.deepEqual(
    groups.map(({ rep }) => rep.id),
    ['c-5h', 'g-5h', 'c-7d', 'g-7d']
  );
});

test('summarizeQuotaGroups breaks percent ties by kind priority and keeps ungrouped singletons', () => {
  const win = (id, group, percent, kind) => ({ id, group, percent, kind });
  // All-zero pools surface the long-term ceiling (weekly wins the tie).
  const antigravityFree = summarizeQuotaGroups([
    win('gemini-weekly', 'Gemini Models', 0, 'weekly'),
    win('3p-weekly', 'Claude and GPT models', 0, 'weekly'),
  ]);
  assert.equal(antigravityFree.length, 2);
  assert.equal(antigravityFree[0].rep.id, 'gemini-weekly');
  assert.deepEqual(antigravityFree[0].rest, []);

  // Ungrouped windows never merge into one pool.
  const ungrouped = summarizeQuotaGroups([win('x', undefined, 10, 'other'), win('y', undefined, 20, 'other')]);
  assert.equal(ungrouped.length, 2);
  assert.equal(ungrouped[0].group, undefined);
  assert.equal(ungrouped[1].rep.id, 'y');
});

test('shortGroupLabel maps known pools to i18n keys and truncates unknown ones', () => {
  assert.equal(shortGroupLabel('Gemini Models', t), 'cpa.quota.group.gemini');
  assert.equal(shortGroupLabel('claude and gpt models', t), 'cpa.quota.group.claude_gpt');
  assert.equal(shortGroupLabel(undefined, t), undefined);
  assert.equal(shortGroupLabel('Products', t), 'Products');
  assert.equal(shortGroupLabel('A very long pool name here', t), 'A very long…');
});

test('formatTime renders a fallback dash for missing timestamps', () => {
  assert.equal(formatTime(undefined), '—');
  const rendered = formatTime('2026-08-21T00:00:00Z');
  assert.ok(rendered.includes('2026'));
  assert.equal(formatTime('not-a-date'), 'not-a-date');
});

test('CPA page renders quota capsules via QuotaWindowsBlock and QuotaSummaryCapsule', () => {
  const index = read('features/cpa/index.tsx');
  const table = read('features/cpa/components/credential-table.tsx');
  const summary = read('features/cpa/components/quota-summary-capsule.tsx');
  const capsule = read('components/quota-capsule.tsx');
  assert.match(index, /CPACredentialTable/);
  assert.match(table, /QuotaWindowsBlock/);
  assert.match(table, /QuotaSummaryCapsule/);
  assert.match(summary, /QuotaCapsule/);
  assert.match(summary, /size='sm'/);
  assert.match(capsule, /type CapsuleSize = 'md' \| 'sm'/);
});


test('overflow popover reuses the external capsule and wraps full window names', () => {
  const capsule = read('components/quota-capsule.tsx');
  const summary = read('features/cpa/components/quota-summary-capsule.tsx');
  assert.match(capsule, /w-\[min\(28rem,calc\(100vw-2rem\)\)\]/);
  assert.match(capsule, /grid-cols-\[minmax\(0,1fr\)_max-content\]/);
  // The popover hands the capsule a fixed 13rem rail to fill.
  assert.match(capsule, /<span className='w-52'>/);
  assert.match(capsule, /min-w-0 .*break-words/);
  assert.match(capsule, /<QuotaCapsule window=\{window\} size=\{size\} \/>/);
  assert.doesNotMatch(capsule, /w-36 shrink-0 truncate text-right/);
  assert.match(summary, /<QuotaMorePopover key='more' windows=\{hidden\} size='sm'>/);
});


test('expanded quota block lists every window as a width-capped detail block', () => {
  const capsule = read('components/quota-capsule.tsx');
  // Auto-filled columns capped at 20rem: a colSpan table row lays blocks side
  // by side instead of stretching one bar across the whole row, on the same
  // 24px gutter the other expanded rows use.
  assert.match(capsule, /grid-cols-\[repeat\(auto-fill,minmax\(16rem,20rem\)\)\] gap-6/);
  assert.match(capsule, /windows\.map\(\(window\) => \(\s*<QuotaWindowCard key=\{window\.id\} window=\{window\} \/>/);
  // Every window gets its own block with inline details and the estimate badge,
  // instead of one primary bar plus a +N popover.
  assert.match(capsule, /data-testid='quota-window-card'/);
  // Table cells force whitespace-nowrap; blocks must re-enable wrapping so long
  // detail lines stay inside their block. Blocks stay flat — no surface, border
  // or radius — like the channel/model expanded rows on the same band.
  assert.match(capsule, /data-testid='quota-window-card' className='space-y-2 whitespace-normal'/);
  assert.doesNotMatch(capsule, /data-testid='quota-window-card' className='[^']*(rounded|border|bg-)/);
  assert.match(capsule, /window\.tooltipExtras\.map/);
  assert.match(capsule, /<EstimateBadge window=\{window\} \/>/);
  assert.doesNotMatch(capsule, /quota-capsule-more/);
});

test('expanded quota row animates like the channel expanded row', () => {
  const table = read('features/cpa/components/credential-table.tsx');
  assert.match(table, /<AnimatePresence initial=\{false\}>/);
  assert.match(table, /initial=\{\{ height: 0, opacity: 0 \}\}/);
  assert.match(table, /animate=\{\{ height: 'auto', opacity: 1 \}\}/);
  assert.match(table, /exit=\{\{ height: 0, opacity: 0 \}\}/);
  assert.match(table, /duration: 0\.2, ease: 'easeInOut'/);
  // Band and padding sit on an inner element: the animated wrapper must stay
  // padding-free or a collapsed row keeps the padding as an empty gray strip.
  assert.match(table, /overflow-hidden'\s*>\s*\{\/\*\s*Band and padding/);
  assert.match(table, /<div className='bg-muted\/30 hover:bg-muted\/50 p-6'>/);
});

test('quota capsule renders remaining semantics with stepped severity tones', () => {
  const capsule = read('components/quota-capsule.tsx');
  // Remaining bar: track width comes from 100 - used, tooltip shows both.
  assert.match(capsule, /const remaining = 100 - clampedUsed/);
  assert.match(capsule, /quota\.capsule\.remaining/);
  assert.match(capsule, /quota\.label\.percent_used/);
  // Stepped green/amber/red tones instead of an interpolated hue ramp: the fill
  // and the percentage share one accent, so no window renders chartreuse.
  assert.match(capsule, /function remainingTone/);
  assert.match(capsule, /remaining >= 50/);
  assert.match(capsule, /remaining >= 20/);
  assert.match(capsule, /TONE_CLASSES\[remainingTone\(used\)\]/);
  assert.match(capsule, /CHIP_TONES/);
  assert.doesNotMatch(capsule, /severityGradient|severityColor\(/);
  // Data layer keeps used-percent semantics for primary-window picking.
  const cpaWindows = read('features/cpa/quota-windows.ts');
  assert.match(cpaWindows, /const percent = Math\.max\(0, Math\.min\(100, usedPct\)\)/);
});

test('quota capsule remaining i18n keys exist in both locales', () => {
  const en = JSON.parse(read('locales/en/system.json'));
  const zh = JSON.parse(read('locales/zh-CN/system.json'));
  assert.equal(en['quota.capsule.remaining'], '{{percent}}% remaining');
  assert.equal(zh['quota.capsule.remaining'], '剩余 {{percent}}%');
  assert.equal(en['quota.capsule.percent'], '{{percent}}%');
  // CPA pool chip labels resolve through shortGroupLabel's known-pool map.
  const enCpa = JSON.parse(read('locales/en/cpa.json'));
  const zhCpa = JSON.parse(read('locales/zh-CN/cpa.json'));
  assert.equal(enCpa['cpa.quota.group.gemini'], 'Gemini');
  assert.equal(zhCpa['cpa.quota.group.gemini'], 'Gemini');
  assert.equal(enCpa['cpa.quota.group.claude_gpt'], 'Claude/GPT');
  assert.equal(zhCpa['cpa.quota.group.claude_gpt'], 'Claude/GPT');
  assert.match(enCpa['cpa.quota.estimateDetail'], /estimation interval/);
  assert.match(zhCpa['cpa.quota.estimateDetail'], /观测区间成本/);
});

test('CPA credential column shows email with filename on hover and defaults to 50 rows', () => {
  const table = read('features/cpa/components/credential-table.tsx');
  const pagination = read('features/cpa/use-cpa-pagination.ts');
  // Email-only main line with displayName fallback, filename in the hover tooltip.
  assert.match(table, /credential\.email \|\| credential\.displayName/);
  assert.match(table, /credential\.remoteName/);
  assert.match(table, /TooltipContent side='top'/);
  // Page size defaults to 50 (keeps the selectable sizes list).
  assert.match(pagination, /CPA_TABLE_PAGE_SIZES = \[10, 20, 30, 40, 50\]/);
  assert.match(pagination, /includes\(value\) \? value : 50/);
  assert.match(pagination, /return 50;/);
});

test('cpaQuotaItemsToWindows passes through quota value estimates', () => {
  const windows = cpaQuotaItemsToWindows(
    [
      {
        id: 'codex-secondary',
        group: 'Codex',
        label: '7 day',
        usedPercent: 3.42,
        periodSeconds: 604800,
        estimatedLimitUSD: 100.25,
        estimatedCostUSD: 3.42,
        estimateSource: 'precise-header-delta',
      },
      {
        id: 'codex-primary',
        group: 'Codex',
        label: '5 hour',
        usedPercent: 10,
        periodSeconds: 18000,
        estimatedLimitUSD: 42.5,
        estimatedCostUSD: 2.5,
        estimateSource: 'precise-header-delta',
      },
    ],
    (key) => key
  );
  assert.equal(windows.length, 2);
  const weekly = windows.find((w) => w.id === 'codex-secondary');
  assert.equal(weekly.estimatedLimitUSD, 100.25);
  assert.equal(weekly.estimatedCostUSD, 3.42);
  assert.equal(weekly.periodSeconds, 604800);
  assert.ok(weekly.tooltipExtras.some((line) => line.includes('cpa.quota.estimateDetail')));
  const primary = windows.find((w) => w.id === 'codex-primary');
  assert.equal(primary.estimatedLimitUSD, 42.5);
  assert.ok(primary.tooltipExtras.some((line) => line.includes('cpa.quota.estimateDetail')));
  const providerWindow = cpaQuotaItemsToWindows([
    { id: 'antigravity-5h', label: '5 hour', usedPercent: 20, periodSeconds: 18000,
      estimatedLimitUSD: 38, estimatedCostUSD: 7.6, estimateSource: 'refresh-delta' },
    { id: 'balance', label: 'Balance', estimatedLimitUSD: 10 },
  ], (key) => key);
  assert.equal(providerWindow.length, 1);
  assert.equal(providerWindow[0].estimatedLimitUSD, 38);
  assert.ok(providerWindow[0].tooltipExtras.some((line) => line.includes('cpa.quota.estimateDetail')));
});
