import type { ApiFormat } from './config_channels';

/**
 * Base URL 规范化候选结果
 */
export interface NormalizedCandidate {
  /** 规范化后的完整 URL */
  url: string;
  /**
   * 变更原因的 i18n key（channels.dialogs.fields.baseURL.normalize.reasons.*），
   * 用于在候选列表中向用户说明这条候选做了什么
   */
  reasonKey: string;
}

/**
 * 协议级 API 端点尾巴：粘贴整条接口 URL 时应逐级剥掉，
 * 只保留 Base URL。这些是各家公开协议约定的端点名，与具体服务商无关。
 */
const ENDPOINT_TAILS = [
  'messages/count_tokens',
  'chat/completions',
  'completions',
  'responses',
  'embeddings',
  'moderations',
  'generations',
  'generateContent',
  'streamGenerateContent',
  'countTokens',
  'messages',
  'complete',
  'models',
];

/**
 * 版本段正则：v1 / v2 / v14 / v1beta / v3alpha 等。
 * 用模式匹配而非字符串白名单，避免硬编码具体版本号。
 */
const VERSION_SEGMENT_RE = /^v\d+([a-z][a-z0-9]*)?$/i;

/** 候选列表上限，避免选项过多干扰用户 */
const MAX_CANDIDATES = 4;

/** Anthropic Messages 协议格式字面量（保持模块零依赖，便于独立测试） */
const ANTHROPIC_FORMAT = 'anthropic/messages';

/**
 * 对原始输入做基础清洗：
 * 去空白与包裹引号、补全协议、丢弃 query/hash、合并重复斜杠、去末尾斜杠。
 * 返回 null 表示无法解析为合法 URL。
 */
function cleanRawURL(raw: string): URL | null {
  let text = raw.trim().replace(/^["'`]+|["'`]+$/g, '');
  if (!text) {
    return null;
  }
  // 缺少协议时默认按 https 处理
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) {
    text = `https://${text}`;
  }

  let parsed: URL;
  try {
    parsed = new URL(text);
  } catch {
    return null;
  }

  if (!['http:', 'https:', 'ws:', 'wss:'].includes(parsed.protocol)) {
    return null;
  }

  parsed.search = '';
  parsed.hash = '';

  // 合并路径中的重复斜杠
  if (parsed.pathname.includes('//')) {
    parsed.pathname = parsed.pathname.replace(/\/{2,}/g, '/');
  }

  return parsed;
}

/**
 * 迭代剥离路径尾部的协议端点段（如 .../v1/chat/completions -> .../v1）
 */
function stripEndpointTails(url: URL): void {
  let changed = true;
  while (changed) {
    changed = false;
    for (const tail of ENDPOINT_TAILS) {
      const suffix = `/${tail}`;
      if (url.pathname.toLowerCase().endsWith(suffix.toLowerCase())) {
        url.pathname = url.pathname.slice(0, -suffix.length);
        changed = true;
      }
    }
  }
}

/** 判断单个路径段是否为版本段 */
const isVersionSegment = (segment: string): boolean => VERSION_SEGMENT_RE.test(segment);

/** 获取去除空段后的路径段列表 */
const pathSegments = (url: URL): string[] => url.pathname.split('/').filter(Boolean);

/** 拼接 origin 与路径段为规范 URL 字符串（无末尾斜杠） */
function joinURL(origin: string, segments: string[]): string {
  if (segments.length === 0) {
    return origin;
  }
  return `${origin}/${segments.join('/')}`;
}

/**
 * 针对默认家族（除 Anthropic 外的格式）生成候选。
 * 目标形态：<origin>[前缀]/<版本段>；缺失版本段时给出补全候选。
 */
function buildVersionedCandidates(origin: string, segments: string[]): NormalizedCandidate[] {
  const candidates: NormalizedCandidate[] = [];
  const lastSegment = segments.at(-1);

  if (lastSegment && isVersionSegment(lastSegment)) {
    // 已符合惯例：保留清洗后的完整地址，另给仅域名 + 版本段的备选
    candidates.push({ url: joinURL(origin, segments), reasonKey: 'keptAsIs' });
    candidates.push({ url: joinURL(origin, ['v1']), reasonKey: 'rootWithV1' });
    return dedupe(candidates);
  }

  // 无版本段：优先保留原前缀并补全版本段，其次仅域名 + 版本段
  if (segments.length > 0) {
    candidates.push({ url: joinURL(origin, [...segments, 'v1']), reasonKey: 'prefixWithV1' });
  }
  candidates.push({ url: joinURL(origin, ['v1']), reasonKey: 'rootWithV1' });
  // 保留原样作为兜底（有些网关确实使用非版本前缀）
  if (segments.length > 0) {
    candidates.push({ url: joinURL(origin, segments), reasonKey: 'keepPrefixOnly' });
  } else {
    candidates.push({ url: origin, reasonKey: 'originOnly' });
  }
  return dedupe(candidates);
}

/**
 * 针对 Anthropic Messages 家族生成候选。
 * 目标形态：不带尾部版本段；允许保留业务网关前缀。
 */
function buildAnthropicCandidates(origin: string, segments: string[]): NormalizedCandidate[] {
  const candidates: NormalizedCandidate[] = [];
  const trimmed = [...segments];
  let removedVersion = false;

  while (trimmed.length > 0 && isVersionSegment(trimmed.at(-1)!)) {
    trimmed.pop();
    removedVersion = true;
  }

  if (removedVersion) {
    candidates.push({ url: joinURL(origin, trimmed), reasonKey: 'stripVersionSuffix' });
    candidates.push({ url: origin, reasonKey: 'originOnly' });
  } else if (trimmed.length > 0) {
    candidates.push({ url: joinURL(origin, trimmed), reasonKey: 'keepPrefixOnly' });
    candidates.push({ url: origin, reasonKey: 'originOnly' });
  } else {
    candidates.push({ url: origin, reasonKey: 'keptAsIs' });
  }
  return dedupe(candidates);
}

/** 按 url 去重并截断到上限 */
function dedupe(candidates: NormalizedCandidate[]): NormalizedCandidate[] {
  const seen = new Set<string>();
  const result: NormalizedCandidate[] = [];
  for (const candidate of candidates) {
    const key = candidate.url.replace(/\/+$/, '');
    if (!seen.has(key)) {
      seen.add(key);
      result.push(candidate);
    }
  }
  return result.slice(0, MAX_CANDIDATES);
}

/**
 * 根据用户粘贴的任意形态 URL 与渠道 API 格式，生成有序去重的规范化候选列表。
 *
 * 算法是通用的启发式规则（补协议、剥端点尾巴、按模式识别版本段、按格式家族调整），
 * 不依赖任何具体服务商域名。返回空数组表示输入无法解析。
 */
export function normalizeBaseURLCandidates(raw: string, apiFormat: ApiFormat): NormalizedCandidate[] {
  const url = cleanRawURL(raw);
  if (!url) {
    return [];
  }

  stripEndpointTails(url);

  const origin = `${url.protocol}//${url.host}`;
  const segments = pathSegments(url);

  if (apiFormat === ANTHROPIC_FORMAT) {
    return buildAnthropicCandidates(origin, segments);
  }
  return buildVersionedCandidates(origin, segments);
}
