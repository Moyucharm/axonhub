import assert from 'node:assert/strict';
import test from 'node:test';
import { isModelMismatch } from './model-mismatch.ts';

test('date snapshots, provider prefixes, case and -latest are not mismatches', () => {
  assert.equal(isModelMismatch('gpt-6-astra', 'gpt-6-astra-2026-09-01'), false);
  assert.equal(isModelMismatch('openai/gpt-5', 'GPT-5'), false);
  assert.equal(isModelMismatch('claude-x', 'claude-x-latest'), false);
});

test('variant suffixes are mismatches', () => {
  assert.equal(isModelMismatch('gemini-3.8-flash-high', 'gemini-3.8-flash'), true);
});

test('missing model is unknown, not a mismatch', () => {
  assert.equal(isModelMismatch('a', ''), false);
});
