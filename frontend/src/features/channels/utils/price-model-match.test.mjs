import assert from 'node:assert/strict';
import test from 'node:test';
import { findCatalogPriceMatch, hasUsableCatalogCost } from './price-model-match.ts';

const PAID_COST = { input: 1, output: 2, cache_read: 0.25, cache_write: 0.5 };
const FREE_COST = { input: 0, output: 0, cache_read: 0, cache_write: 0 };

const paid = (overrides = {}) => ({ ...PAID_COST, ...overrides });

// Build a catalog fixture; a plain string is a priced model id, an object allows
// describing entries without cost or with an all-zero cost.
function catalog(providers) {
  const normalized = {};

  for (const [providerId, models] of Object.entries(providers)) {
    normalized[providerId] = {
      models: models.map((entry) => (typeof entry === 'string' ? { id: entry, cost: paid() } : entry)),
    };
  }

  return { providers: normalized };
}

test('a priced full id wins over the abbreviated entry in the same catalog', () => {
  const data = catalog({
    vendorx: [{ id: 'vendor/deepseek-flash', cost: paid({ input: 3 }) }, { id: 'deepseek-flash', cost: paid({ input: 9 }) }],
  });

  const match = findCatalogPriceMatch(data, 'vendor/deepseek-flash', 'vendorx');

  assert.equal(match.method, 'exact');
  assert.equal(match.model.id, 'vendor/deepseek-flash');
  assert.equal(match.model.cost.input, 3);
  assert.equal(match.providerId, 'vendorx');
});

test('the path prefix is dropped when the served name has no price of its own', () => {
  const data = catalog({ vendory: ['deepseek-flash'] });

  const match = findCatalogPriceMatch(data, 'vendor/deepseek-flash');

  assert.equal(match.method, 'prefix');
  assert.equal(match.model.id, 'deepseek-flash');
  assert.equal(match.providerId, 'vendory');
});

test('the reasoning effort suffix is dropped after the full name fails', () => {
  const data = catalog({ vendora: ['gemini-3.8-flash'] });

  const match = findCatalogPriceMatch(data, 'gemini-3.8-flash-high');

  assert.equal(match.method, 'suffix');
  assert.equal(match.model.id, 'gemini-3.8-flash');
});

test('path prefix and reasoning effort suffix are dropped together as the last resort', () => {
  const data = catalog({ vendora: ['gemini-3.1-pro'] });

  const match = findCatalogPriceMatch(data, 'vendor/gemini-3.1-pro-low');

  assert.equal(match.method, 'prefix_suffix');
  assert.equal(match.model.id, 'gemini-3.1-pro');
});

test('only one suffix is stripped and only for reasoning effort words', () => {
  const data = catalog({ vendora: ['glm-5.3', 'acme/model-3', 'gpt-5.6'] });

  // 型号词不是思考力度，必须保持不匹配
  assert.equal(findCatalogPriceMatch(data, 'glm-5.3-flash'), null);
  assert.equal(findCatalogPriceMatch(data, 'gpt-5.6-sol'), null);
  assert.equal(findCatalogPriceMatch(data, 'gpt-5.6-luna'), null);

  // 只剥一次：`-high-low` 不能退化成 `acme/model-3`
  assert.equal(findCatalogPriceMatch(data, 'acme/model-3-high-low'), null);

  const caseInsensitive = findCatalogPriceMatch(data, 'acme/model-3-HIGH');
  assert.equal(caseInsensitive.method, 'suffix');
  assert.equal(caseInsensitive.model.id, 'acme/model-3');
});

test('a trailing -max on qwen models is part of the name and is never stripped', () => {
  const data = catalog({ alibaba: ['qwen', 'qwen3.8'], vendora: ['grok-5'] });

  assert.equal(findCatalogPriceMatch(data, 'qwen-max'), null);
  assert.equal(findCatalogPriceMatch(data, 'vendor/qwen3.8-max'), null);

  // 对照：非 qwen 家族的 max 仍然可以作为思考力度后缀剥离
  const bareSuffix = findCatalogPriceMatch(data, 'grok-5-max');
  assert.equal(bareSuffix.method, 'suffix');
  assert.equal(bareSuffix.model.id, 'grok-5');

  const prefixed = findCatalogPriceMatch(data, 'vendor/grok-5-max');
  assert.equal(prefixed.method, 'prefix_suffix');
  assert.equal(prefixed.model.id, 'grok-5');
});

test('a served name matches the catalog regardless of letter case or padding', () => {
  const data = catalog({ siliconflow: ['glm-4.6'], alibaba: ['qwen3-coder-480b-a35b-instruct'] });

  // 渠道侧的默认写法带大写（config_channels.ts 的 `zai-org/GLM-4.6`），目录里是小写 id
  const prefixed = findCatalogPriceMatch(data, 'zai-org/GLM-4.6');
  assert.equal(prefixed.method, 'prefix');
  assert.equal(prefixed.model.id, 'glm-4.6');
  assert.equal(prefixed.providerId, 'siliconflow');

  const cased = findCatalogPriceMatch(data, 'Qwen/Qwen3-Coder-480B-A35B-Instruct');
  assert.equal(cased.method, 'prefix');
  assert.equal(cased.model.id, 'qwen3-coder-480b-a35b-instruct');

  // 首尾空格与大小写一起作用于查找，不影响命中
  const padded = findCatalogPriceMatch(data, ' GLM-4.6 ');
  assert.equal(padded.method, 'exact');
  assert.equal(padded.model.id, 'glm-4.6');

  // 判断思考力度后缀之后再拼接的候选同样按小写命中
  const suffixed = findCatalogPriceMatch(data, 'zai-org/GLM-4.6-HIGH');
  assert.equal(suffixed.method, 'prefix_suffix');
  assert.equal(suffixed.model.id, 'glm-4.6');
});

test('catalog entries without a usable cost never produce a match', () => {
  const data = catalog({
    poolside: [
      { id: 'poolside/laguna-s-2.1', cost: FREE_COST },
      { id: 'laguna-s-2.1', cost: FREE_COST },
      { id: 'laguna-m.1' },
    ],
  });

  assert.equal(findCatalogPriceMatch(data, 'poolside/laguna-s-2.1'), null);
  assert.equal(findCatalogPriceMatch(data, 'laguna-m.1'), null);
  assert.equal(findCatalogPriceMatch(data, 'vendor/poolside/laguna-s-2.1'), null);
});

test('hasUsableCatalogCost requires a positive finite token price', () => {
  assert.equal(hasUsableCatalogCost({ id: 'a', cost: FREE_COST }), false);
  assert.equal(hasUsableCatalogCost({ id: 'a', cost: {} }), false);
  assert.equal(hasUsableCatalogCost({ id: 'a' }), false);
  assert.equal(hasUsableCatalogCost({ id: 'a', cost: { input: Number.NaN, output: 1 } }), true);
  assert.equal(hasUsableCatalogCost({ id: 'a', cost: { input: 0, output: 0, cache_read: 0.0001 } }), true);
  assert.equal(hasUsableCatalogCost({ id: 'a', cost: { output: -1 } }), false);
});

test('duplicate catalog ids resolve by preferred provider, then by provider id', () => {
  const data = catalog({
    zai: [{ id: 'glm-5.2', cost: paid({ input: 2 }) }],
    alibaba: [{ id: 'glm-5.2', cost: paid({ input: 4 }) }],
  });

  const preferred = findCatalogPriceMatch(data, 'glm-5.2', 'zai');
  assert.equal(preferred.providerId, 'zai');
  assert.equal(preferred.model.cost.input, 2);

  const preferredPaidOnly = findCatalogPriceMatch(data, 'glm-5.2', 'missing-provider');
  assert.equal(preferredPaidOnly.providerId, 'alibaba');
  assert.equal(preferredPaidOnly.model.cost.input, 4);
});

test('a zero-priced duplicate is skipped in favour of the next provider', () => {
  const data = catalog({
    aaaprovider: [{ id: 'glm-5.2', cost: FREE_COST }],
    zzzprovider: [{ id: 'glm-5.2', cost: paid({ input: 5 }) }],
  });

  const match = findCatalogPriceMatch(data, 'glm-5.2');

  assert.equal(match.providerId, 'zzzprovider');
  assert.equal(match.model.cost.input, 5);
});

test('empty and path-only model ids resolve to nothing', () => {
  const data = catalog({ vendora: ['gemini-3.8-flash'] });

  assert.equal(findCatalogPriceMatch(data, ''), null);
  assert.equal(findCatalogPriceMatch(data, 'vendor/'), null);
  assert.equal(findCatalogPriceMatch(data, '-high'), null);
});
