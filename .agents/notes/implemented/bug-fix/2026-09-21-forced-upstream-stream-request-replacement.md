# Agent Note: 上游强制流式在请求对象被替换后丢失

Status: implemented

## Problem

OpenCode Zen 渠道（`opencode_zen`）的 chat 与 responses outbound transformer 为通过上游 FreeTier 准入，无条件把上游请求升级为流式（body `stream: true`、`Accept: text/event-stream`），并依赖 pipeline 的「非流式下游 → 自动聚合上游 SSE」能力返回普通 JSON。

pipeline 用自己持有的 `*llm.Request` 判定流式模式（`llm/pipeline/pipeline.go` `processRequest` 中 `effectiveWantStream := request.Stream != nil && *request.Stream`），而 `PersistentOutboundTransformer.TransformRequest` 的中间步骤会用副本替换该请求（`applyTransformOptions`、`applyReasoningEffortMapping`、Claude Code 中间件均返回 `&newReq`）。zen 的升级只写进副本，pipeline 仍按非流式处理，上游返回的 SSE 文本被 `json.Unmarshal`，于是渠道模型测试（发送非流式请求）报错：

```
failed to transform response: failed to unmarshal chat completion response: invalid character 'd' looking for beginning of value
```

responses 端点同型（`event: ...` → `invalid character 'e'`）。流式调用走 stream 分支，因此「模型可正常调用」。

复现前提是变换链中发生过请求对象替换（渠道 transform options、命中的 `reasoningEffortMapping`、或 CC/计费中间件）；渠道未开启这些选项时不会复现。

## Decision

在 `PersistentOutboundTransformer.TransformRequest` 中保留 pipeline 持有对象的引用（形参命名回 `request`，函数体用 `llmRequest := request`），并在 `p.wrapped.TransformRequest(ctx, llmRequest)` 成功后把生效的流式契约镜像回 pipeline 持有的对象：

```go
request.Stream = llmRequest.Stream
request.StreamOptions = llmRequest.StreamOptions
```

pipeline 的流式判定因此始终看到最终生效的模式，不再依赖「outbound 变换中途没有替换请求对象」这一隐含前提。无副本替换时（`llmRequest == request`）是自赋值，无副作用；只镜像 `Stream`/`StreamOptions` 两个契约字段，副本内的模型映射、消息改写保持局部。

这与仓库既有做法同构：`shouldForceStreamingForCandidate` 在策略强制流式时同时写 `llmRequest` 与 `p.state.LlmRequest`，原因正是请求可能已被替换成副本；zen 的升级发生在被包裹的 transformer 内部，缺少同步。

## Alternatives considered

- **在 `llm/pipeline/` 按请求体 `stream` 字段反推流式：** 会波及 codex 等其它渠道（`codex/outbound.go` 对副本设 `stream: true` 并靠自定义 executor 走流式），属于未验证的行为变更。
- **改 `applyTransformOptions` / `applyReasoningEffortMapping` 的副本语义让它们原地修改：** 这两个函数刻意不修改调用方对象，改成原地会扩大影响面到所有渠道的变换行为。
- **给 zen 的 messages 端点也补流式升级：** 该端点（`anthropic.NewOutboundTransformerWithConfig`）无强制流式要求，属于本次问题之外的契约变更。
- **为 zen 强制流式补 `stream_options.include_usage`：** 上游准入不要求，且与「下游非流式」的用量统计路径无关。

## Consequences

非流式下游请求在 zen 渠道（含开启渠道 transform options 的配置）能稳定拿到聚合后的 `chat.completion` / responses 结果，渠道模型测试不再出现 `failed to transform response`。

回归测试 `TestChatCompletionOrchestrator_Process_NonStreamingForcedStreamUpgradeSurvivesRequestReplacement` 覆盖该场景（zen 渠道 + `forceArrayInstructions` 副本替换触发器 + 只回 SSE 的 mock 上游），修复前以用户报的原文失败。

代价是 `TransformRequest` 末尾新增对 pipeline 持有对象的写入；任何后续在此函数内新增副本替换步骤时，都必须在 `TransformRequest` 返回前把流式契约镜像回 `request`，否则会重新引入同类问题。zen 上游对部分模型（如 Responses-only 的 `muse-spark-*`）仍可能返回 HTTP 500，这属于上游侧限制，与本修复无关。
