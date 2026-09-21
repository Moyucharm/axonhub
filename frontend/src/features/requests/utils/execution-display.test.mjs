import assert from 'node:assert/strict';
import test from 'node:test';
import { formatJsonValue, hasRecordedJsonValue, selectExecution } from './execution-display.ts';

test('keeps a selected execution by ID and falls back to the latest visible execution', () => {
  const executions = [{ id: 'execution-1' }, { id: 'execution-2' }, { id: 'execution-3' }];

  assert.deepEqual(selectExecution(executions, 'execution-2'), {
    execution: executions[1],
    index: 1,
  });
  assert.deepEqual(selectExecution(executions, 'execution-from-another-request'), {
    execution: executions[2],
    index: 2,
  });
  assert.deepEqual(selectExecution(executions, null), {
    execution: executions[2],
    index: 2,
  });
  assert.equal(selectExecution([], null), null);
});

test('recognizes recorded JSON values without treating empty objects as payloads', () => {
  assert.equal(hasRecordedJsonValue(null), false);
  assert.equal(hasRecordedJsonValue(undefined), false);
  assert.equal(hasRecordedJsonValue({}), false);
  assert.equal(hasRecordedJsonValue([]), true);
  assert.equal(hasRecordedJsonValue(''), true);
  assert.equal(hasRecordedJsonValue(false), true);
  assert.equal(hasRecordedJsonValue(0), true);
  assert.equal(hasRecordedJsonValue({ content: 'ok' }), true);
});

test('formats falsy JSON values instead of dropping them', () => {
  assert.equal(formatJsonValue(false), 'false');
  assert.equal(formatJsonValue(0), '0');
  assert.equal(formatJsonValue(''), '""');
  assert.equal(formatJsonValue(null), '');
});
