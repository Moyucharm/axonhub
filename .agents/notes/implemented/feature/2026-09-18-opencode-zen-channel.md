# Agent Note: OpenCode Zen 渠道

Status: implemented

## Problem

AxonHub 已支持 OpenCode Go，但 OpenCode Zen 同时按模型提供 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 三种原生协议。免费层模型在 Chat Completions 和 Responses 两套端点上均执行准入检查：请求必须具备 OpenCode 客户端身份、上游强制使用流式传输，且工具列表必须包含 `bash` 与 `read`。此外 Responses 协议有其特定约束（扁平工具结构定义，且拒绝 `tool_choice: "none"`）。此前 Responses outbound 仅注入 Header 而未做请求体协调，导致除完整官方客户端之外的请求（如非流式或无工具请求）被上游判定为 `FreeTierError`。

## Decision

AxonHub 提供独立渠道类型 `opencode_zen`，直接连接 `https://opencode.ai/zen/v1`。`llm/transformer/opencode/zen` 为 Chat Completions 与 Responses 复用同一套 OpenCode CLI 请求头和 30 字符 session/request ID；Chat transformer 额外负责强制上游流式以及 `bash`/`read` 工具协调。渠道可以保存一个用户 API Key；存在可用用户 Key 时发送该 Key，没有可用 Key 时才回退到 `Bearer public`。`public` 仅是运行时 fallback，不作为伪凭据写入渠道。

Chat Completions 使用专用 Zen transformer。该 transformer 实现 `TransportRequestFinalizer`，在通用 User-Agent 透传、body/header override 之后重新固定免费 Chat 模型所需的准入字段，并保留 transformer 已选择的用户 Key；同时实现 `PassThroughBodyPolicy` 并拒绝 raw body pass-through。非流式下游请求通过 pipeline 现有的流式自动聚合路径返回标准非流式响应。`applyIdentityFingerprint` 显式同步 `request.Auth` 与 Header，并在 `FinalizeTransportRequest` 中深拷贝 `TransformerMetadata`（`maps.Clone`），避免并发或回退时的元数据污染。所有存根工具在包初始化阶段预编译为 `json.RawMessage` 切片，避免请求热路径上的静态 JSON 反序列化开销。

Responses 使用 Zen 专用包装器复用现有 OpenAI Responses transformer，直接请求 `/responses`，并在 transport finalizer 阶段注入并重新固定 OpenCode 客户端身份。同时，Responses outbound 实现 `reconcileResponsesRequestBody` 与 `PassThroughBodyPolicy`（拒绝 raw body pass-through）：强制上游流式传输（下游非流式请求由 pipeline 自动聚合）、清理不兼容的 `tool_choice: "none"`、在缺少 `bash`/`read` 时复用预编译存根工具补齐 Responses 扁平结构，并完整保留下游客户端定义的自定义工具。Messages 继续复用 Anthropic Messages transformer 并请求 `/messages`。Responses 使用 Bearer 认证，Messages 使用 `X-API-Key`；未配置渠道 Key 时继续使用运行时 `public` fallback。Muse Spark Contributor Free 无论是官方客户端还是第三方普通客户端/Agent 均可透明接入。Zen Responses 仅开放 HTTP/SSE，不开放 AxonHub 的 Responses WebSocket transport。

模型列表通过 OpenAI 兼容的 `GET /models` 接口动态获取。请求优先使用用户配置的 API Key；未配置时使用运行时 `public` fallback。前端不再内置 Zen 快速添加模型，避免模型目录随服务端变化而过期。未知模型不做静默回退，继续由 supported models 和模型映射显式控制。

后端为该类型内置 `openai/chat_completions`、`openai/responses`、`anthropic/messages` 三个 endpoint，并允许无用户 API Key 创建和批量导入。凭据优先级为渠道测试 `apiKeyOverride`、可用渠道 Key、`public` fallback；三种原生 endpoint、自定义 endpoint 和动态模型获取使用相同凭据选择规则（统一通过 `getOpenCodeZenAPIKeyProvider` 解析），其他协议在写入验证和运行时构建两层被拒绝。前端使用独立 OpenCode Zen provider 分组，展示一个可选单 Key 输入、隐藏 Key Pool 控件，允许为模型选择三种 Zen 原生协议，并在编辑渠道追加新模型或编辑高级设置时正确维护模型协议映射。

## Verification

- `cd llm && go test ./transformer/opencode/zen`
  - Responses 回归测试只通过 `/responses` 构造 `muse-spark-1.3-contributor-free` 请求，不测试该模型的 Chat Completions 调用。
- `go test ./internal/server/biz -run 'OpenCodeZen|ModelFetcherFetchesOpenCodeZen|BulkImport.*OpenCodeZen'`
- `node --test frontend/src/features/channels/data/channel-config.test.mjs frontend/src/features/channels/data/protocol-options.test.mjs`
- `git diff --check`

自动测试通过本地 HTTP server 验证 `/models` 路径、public fallback、用户 Key 和动态响应解析。单元测试覆盖 Chat 与 Responses 双端点的流式强制升格、存根工具补齐、自定义工具保留与透传策略。线上实验确认：Muse Spark Contributor Free 使用 `Bearer public` 时，准入条件与 Chat 免费模型一致（OpenCode Headers + `stream: true` + `tools` 含 `bash` 与 `read`）；在补齐上述协调逻辑后，任意第三方客户端的纯文本请求与 Agent 自定义工具调用均稳定返回 HTTP 200。

## Alternatives considered

- **继续部署 `oc2api.py`：** 改动最少，但需要额外运行 Python 服务，AxonHub 无法直接管理渠道健康、代理和请求记录。
- **复用 `opencode_go` 渠道类型：** 两个平台的 endpoint、认证、模型和协议指纹不同，复用会把不相关契约耦合在同一类型中。
- **为 Responses 和 Messages 新增独立渠道类型：** 会重复 base URL、凭据、模型同步和渠道状态；Zen 本身就是同时暴露三种协议的单一 provider，使用同一渠道的多 endpoint 更符合现有抽象。
- **仅用渠道 body/header override 配置：** 无法可靠实现非流式请求的上游流式升级、条件化工具补齐和不可被后续中间件覆盖的最终指纹。
- **保留未知模型自动回退：** 与网关的显式模型路由语义冲突，会让错误配置表现为调用了另一个模型。
- **始终强制 `Bearer public`：** 最简单，但会忽略用户已提供的 Zen API Key，也使付费或账户级能力无法通过原生渠道使用。
- **把 `public` 持久化为渠道 API Key：** 可以复用普通渠道逻辑，但会把运行时默认值伪装成用户凭据，并干扰凭据编辑、导出与禁用状态。

## Consequences

OpenCode Zen 现在可在同一渠道中按模型使用 Chat Completions、Responses 或 Messages；支持匿名访问的免费模型继续使用 `public`，付费模型使用用户提供的单个 Zen API Key。Muse Spark Contributor Free 以及其它免费模型能够无缝支持任意客户端（包括纯文本调用与 Agent 工具调用）。实现不再依赖 Python 代理，并保留 AxonHub 的请求记录、代理、重试和模型管理能力。

代价是该渠道仍有意覆盖用户对官方 User-Agent、OpenCode 标识头、认证 header、上游 `stream` 以及必需存根工具的最终修改；认证 header override 不能替换渠道已选择的 Key。这些字段属于上游准入契约，不是普通可配置项。Zen 不开放 Key Pool，所有配置只保留第一个非空 Key；禁用或清空该 Key 后推理与模型获取都回退到 public。模型选择依赖 `/models` endpoint 的实时可用性，不再提供可能过期的前端静态目录。
