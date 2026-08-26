import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import ts from 'typescript';

const srcRoot = join(import.meta.dirname, '..', '..', '..');

const source = readFileSync(join(srcRoot, 'features/channels/data/base-url-normalize.ts'), 'utf8');
const transpiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2023,
  },
}).outputText;
const moduleUrl = `data:text/javascript;base64,${Buffer.from(transpiled).toString('base64')}`;
const { normalizeBaseURLCandidates } = await import(moduleUrl);

const OPENAI = 'openai/chat_completions';
const RESPONSES = 'openai/responses';
const ANTHROPIC = 'anthropic/messages';

// 使用随机虚构的主机名，确保测试与实现都不依赖具体服务商域名
const HOSTS = ['gw-alpha.example', 'gateway-beta.test', 'relay-gamma.example.org'];
const hostOf = (i) => HOSTS[i % HOSTS.length];
const originOf = (i) => `https://${hostOf(i)}`;

test('补全缺失协议、去末尾斜杠并追加版本段', () => {
  const candidates = normalizeBaseURLCandidates(` ${hostOf(0)} `, OPENAI);
  assert.equal(candidates[0].url, `${originOf(0)}/v1`);
});

test('剥离协议端点尾巴（含 responses / chat/completions / messages）', () => {
  const endpointCases = [
    { hostIdx: 0, suffix: '/v1/chat/completions' },
    { hostIdx: 1, suffix: '/v1/responses' },
    { hostIdx: 2, suffix: '/v1/messages' },
  ];
  for (const { hostIdx, suffix } of endpointCases) {
    const candidates = normalizeBaseURLCandidates(`${originOf(hostIdx)}${suffix}`, OPENAI);
    assert.equal(candidates[0].url, `${originOf(hostIdx)}/v1`, `应剥掉端点尾巴 ${suffix}`);
    assert.ok(candidates.every((c) => !c.url.endsWith(suffix)), `候选不应保留尾巴 ${suffix}`);
  }
});

test('丢弃 query 与 hash、合并重复斜杠', () => {
  const candidates = normalizeBaseURLCandidates(`${originOf(0)}//v1///?foo=bar#frag`, OPENAI);
  assert.equal(candidates[0].url, `${originOf(0)}/v1`);
});

test('已有版本段时保持原样并提供备选', () => {
  for (const version of ['v1', 'v3', 'v14', 'v1beta']) {
    const candidates = normalizeBaseURLCandidates(`${originOf(0)}/api/prefix/${version}`, OPENAI);
    assert.equal(candidates[0].url, `${originOf(0)}/api/prefix/${version}`, `版本段 ${version} 应被识别并保留`);
    assert.notEqual(candidates[0].reasonKey, 'prefixWithV1');
  }
});

test('无版本段的路径会给出保留前缀 + 补版本的推荐候选', () => {
  const candidates = normalizeBaseURLCandidates(`${originOf(1)}/relay`, OPENAI);
  assert.deepEqual(
    candidates.map((c) => c.url),
    [`${originOf(1)}/relay/v1`, `${originOf(1)}/v1`, `${originOf(1)}/relay`],
  );
});

test('responses 格式同样按版本段惯例处理', () => {
  const candidates = normalizeBaseURLCandidates(originOf(2), RESPONSES);
  assert.equal(candidates[0].url, `${originOf(2)}/v1`);
});

test('anthropic 格式移除尾部版本段', () => {
  const candidates = normalizeBaseURLCandidates(`${originOf(0)}/v1`, ANTHROPIC);
  assert.equal(candidates[0].url, originOf(0));
});

test('anthropic 格式保留非版本的网关前缀', () => {
  const candidates = normalizeBaseURLCandidates(`${originOf(1)}/anthropic`, ANTHROPIC);
  assert.equal(candidates[0].url, `${originOf(1)}/anthropic`);
  assert.ok(candidates.some((c) => c.url === originOf(1)));
});

test('anthropic 格式剥离端点尾巴后不再误加版本段', () => {
  const candidates = normalizeBaseURLCandidates(`${originOf(2)}/v1/messages`, ANTHROPIC);
  assert.equal(candidates[0].url, originOf(2));
  assert.ok(candidates.every((c) => !/\/v\d+([a-z].*)?$/i.test(new URL(c.url).pathname)));
});

test('非法输入返回空数组', () => {
  assert.deepEqual(normalizeBaseURLCandidates('', OPENAI), []);
  assert.deepEqual(normalizeBaseURLCandidates('   ', ANTHROPIC), []);
  assert.deepEqual(normalizeBaseURLCandidates('ht tp://bad url', OPENAI), []);
  assert.deepEqual(normalizeBaseURLCandidates('ftp://example.invalid', OPENAI), []);
});

test('候选列表去重且不超过上限', () => {
  const candidates = normalizeBaseURLCandidates(originOf(0), OPENAI);
  const urls = candidates.map((c) => c.url);
  assert.equal(new Set(urls).size, urls.length, '候选不应重复');
  assert.ok(candidates.length <= 4);
});
