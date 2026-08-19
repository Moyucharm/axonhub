import assert from 'node:assert/strict';
import test from 'node:test';

import { formatLocalDateTime } from './format-date-time.ts';

process.env.TZ = 'Asia/Shanghai';

test('formats a cooldown crossing midnight in browser local time', () => {
  const now = new Date('2026-08-18T23:50:00+08:00');
  const formatted = formatLocalDateTime('2026-08-19T00:00:00+08:00', 'zh-CN', now);

  assert.match(formatted ?? '', /2026.*08.*19.*00:00/);
  assert.doesNotMatch(formatted ?? '', /16:00|24:00/);
});

test('omits the date for a local time on the same day', () => {
  const now = new Date('2026-08-18T10:00:00+08:00');

  assert.equal(formatLocalDateTime('2026-08-18T10:10:00+08:00', 'zh-CN', now), '10:10');
});

test('returns null for an invalid timestamp', () => {
  assert.equal(formatLocalDateTime('not-a-date', 'zh-CN'), null);
});
