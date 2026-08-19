import assert from 'node:assert/strict';
import test from 'node:test';

import { createRandomUUID } from './random-uuid.ts';

const UUID_V4_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

test('creates canonical UUID v4 values without randomUUID', () => {
  const values = Array.from({ length: 20 }, () => createRandomUUID());

  for (const value of values) {
    assert.match(value, UUID_V4_PATTERN);
  }
  assert.equal(new Set(values).size, values.length);
});
