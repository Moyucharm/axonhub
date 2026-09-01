import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const cpaDir = import.meta.dirname;
const operations = readFileSync(join(cpaDir, 'api/operations.ts'), 'utf8');
const queries = readFileSync(join(cpaDir, 'api/queries.ts'), 'utf8');
const controller = readFileSync(join(cpaDir, 'use-cpa-controller.ts'), 'utf8');

test('CPA overview combines stats, provider counts, and plan types', () => {
  assert.match(operations, /cpaOverview\(instanceID: \$instanceID\)/);
  assert.match(operations, /stats \{ available total abnormal \}/);
  assert.match(operations, /providers \{ provider count planTypes \}/);
  assert.doesNotMatch(operations, /cpaCredentialStats|cpaProviderCounts|cpaPlanTypes/);
  assert.doesNotMatch(queries, /PLAN_TYPES_QUERY|useCPAPlanTypes|\['cpa', 'plans'/);
});

test('CPA controller derives plan options from the selected provider overview', () => {
  assert.match(controller, /overviewQuery\.data\?\.providers \?\? \[\]/);
  assert.match(controller, /find\(\(item\) => item\.provider === provider\)\?\.planTypes \?\? \[\]/);
  assert.match(controller, /availablePlanTypes/);
});
