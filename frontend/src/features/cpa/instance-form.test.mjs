import assert from 'node:assert/strict';
import test from 'node:test';
import { cpaInstanceFormValues, cpaInstanceInput, DEFAULT_CPA_INSTANCE_FORM_VALUES } from './instance-form.ts';

test('CPA edit echo normalizes both automatic writes against refresh independently', () => {
  for (const refresh of [false, true]) {
    for (const manage of [false, true]) {
      for (const reset of [false, true]) {
        const values = cpaInstanceFormValues({
          autoRefreshEnabled: refresh,
          autoManageEnabled: manage,
          autoResetEnabled: reset,
        });
        assert.equal(values.autoManageEnabled, refresh && manage);
        assert.equal(values.autoResetEnabled, refresh && reset);
      }
    }
  }
});

test('CPA submission enforces refresh without tying reset use to auto-management', () => {
  const values = {
    ...DEFAULT_CPA_INSTANCE_FORM_VALUES,
    name: ' Primary ',
    baseURL: ' http://127.0.0.1:8317 ',
    autoManageEnabled: false,
    autoResetEnabled: true,
  };
  const input = cpaInstanceInput(values);
  assert.equal(input.autoManageEnabled, false);
  assert.equal(input.autoResetEnabled, true);
  assert.equal(input.name, 'Primary');
  assert.equal(input.baseURL, 'http://127.0.0.1:8317');
  const disabled = cpaInstanceInput({ ...values, autoRefreshEnabled: false, autoManageEnabled: true });
  assert.equal(disabled.autoManageEnabled, false);
  assert.equal(disabled.autoResetEnabled, false);
});

test('CPA refresh defaults on without granting automatic write permissions', () => {
  const values = cpaInstanceFormValues();
  assert.equal(values.autoRefreshEnabled, true);
  assert.equal(values.autoManageEnabled, false);
  assert.equal(values.autoResetEnabled, false);
  assert.equal(values.enabledPatrolIntervalMinutes, 5);
  assert.equal(values.disabledPatrolIntervalMinutes, 480);
});
