import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const cpaDir = import.meta.dirname;
const operations = readFileSync(join(cpaDir, 'api/operations.ts'), 'utf8');
const queries = readFileSync(join(cpaDir, 'api/queries.ts'), 'utf8');
const filters = readFileSync(join(cpaDir, 'use-cpa-filters.ts'), 'utf8');
const schema = readFileSync(join(cpaDir, '../../../../internal/server/gql/cpa.graphql'), 'utf8');

test('CPA overview combines stats, provider counts, and plan types', () => {
  assert.match(operations, /cpaOverview\(instanceID: \$instanceID\)/);
  assert.match(operations, /stats \{ available total abnormal \}/);
  assert.match(operations, /providers \{ provider count planTypes \}/);
  assert.doesNotMatch(operations, /cpaCredentialStats|cpaProviderCounts|cpaPlanTypes/);
  assert.doesNotMatch(queries, /PLAN_TYPES_QUERY|useCPAPlanTypes|\['cpa', 'plans'/);
  assert.match(schema, /enum CPAConnectionStatus/);
  assert.match(schema, /enum CPAQuotaState/);
  assert.match(schema, /statuses: \[CPACredentialFilterStatus!\]!/);
  assert.doesNotMatch(schema, /cpaCredentialStats|cpaProviderCounts|cpaPlanTypes/);
});

test('CPA controller derives plan options from the selected provider overview', () => {
  assert.match(filters, /providerCounts/);
  assert.match(filters, /find\(\(item\) => item\.provider === provider\)\?\.planTypes \?\? \[\]/);
  assert.match(filters, /availablePlanTypes/);
});
