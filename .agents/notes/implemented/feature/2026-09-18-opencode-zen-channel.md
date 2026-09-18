# Agent Note: OpenCode Zen 免费渠道

Status: implemented

## Problem

AxonHub 已支持 OpenCode Go，但 OpenCode Zen 免费层使用另一套准入契约：请求必须伪装为 OpenCode CLI、上游必须使用流式传输，并且工具列表必须同时包含 `bash` 与 `read`。此前只能通过仓库外的 `oc2api.py` 代理满足这些条件，增加了独立进程、端口和部署维护成本。

## Decision

AxonHub 提供独立渠道类型 `opencode_zen`，直接连接 `https://opencode.ai/zen/v1`。`llm/transformer/opencode/zen` 复用 OpenAI Chat Completions transformer，并负责 OpenCode CLI 请求头、30 字符 session/request ID、强制上游流式以及 `bash`/`read` 工具协调。渠道可以保存一个用户 API Key；存在可用用户 Key 时发送该 Key，没有可用 Key 时才回退到 `Bearer public`。`public` 仅是运行时 fallback，不作为伪凭据写入渠道。

该 transformer 实现 `TransportRequestFinalizer`，在通用 User-Agent 透传、body/header override 之后重新固定 Zen 准入字段，并保留 transformer 已选择的用户 Key；同时实现 `PassThroughBodyPolicy` 并拒绝 raw body pass-through。非流式下游请求通过 pipeline 现有的流式自动聚合路径返回标准非流式响应。

渠道仅暴露 `mimo-v2.5-free`、`nemotron-3.5-lightning-free`、`nemotron-3-ultra-free` 和 `ling-3.0-flash-fin-free`，模型获取直接返回该静态目录。未知模型不做静默回退，继续由 supported models 和模型映射显式控制。

后端将该类型限制为唯一的 `openai/chat_completions` endpoint，并允许无用户 API Key 创建和批量导入。凭据优先级为渠道测试 `apiKeyOverride`、可用渠道 Key、`public` fallback；自定义 Chat endpoint 使用相同规则，非 Chat endpoint 在写入验证和运行时构建两层被拒绝。前端使用独立 OpenCode Zen provider 分组，展示一个可选单 Key 输入、隐藏 Key Pool 控件，并允许无 Key 获取静态模型。

## Verification

- `cd llm && go test ./transformer/opencode/zen`
- `go test ./internal/server/biz -run 'OpenCodeZen|ModelFetcherReturnsOpenCodeZen|BulkImport.*OpenCodeZen'`
- `node --test frontend/src/features/channels/data/channel-config.test.mjs frontend/src/features/channels/data/protocol-options.test.mjs`
- `git diff --check`

真实 OpenCode Zen 端点未在自动验证中调用；live smoke test 需要单独确认网络请求。

## Alternatives considered

- **继续部署 `oc2api.py`：** 改动最少，但需要额外运行 Python 服务，AxonHub 无法直接管理渠道健康、代理和请求记录。
- **复用 `opencode_go` 渠道类型：** 两个平台的 endpoint、认证、模型和协议指纹不同，复用会把不相关契约耦合在同一类型中。
- **仅用渠道 body/header override 配置：** 无法可靠实现非流式请求的上游流式升级、条件化工具补齐和不可被后续中间件覆盖的最终指纹。
- **保留未知模型自动回退：** 与网关的显式模型路由语义冲突，会让错误配置表现为调用了另一个模型。
- **始终强制 `Bearer public`：** 最简单，但会忽略用户已提供的 Zen API Key，也使付费或账户级能力无法通过原生渠道使用。
- **把 `public` 持久化为渠道 API Key：** 可以复用普通渠道逻辑，但会把运行时默认值伪装成用户凭据，并干扰凭据编辑、导出与禁用状态。

## Consequences

OpenCode Zen 免费层现在既可作为原生无凭据渠道使用，也可使用用户提供的单个 API Key，不再依赖 Python 代理，并保留 AxonHub 的请求记录、代理、重试和模型管理能力。认证选择与准入逻辑集中在专用 transformer 和渠道组装层中，服务端指纹变化时可通过常量和单元测试更新。

代价是该渠道仍有意覆盖用户对官方 User-Agent、OpenCode 标识头、`stream` 和必需工具指纹的最终修改；认证 header override 也不能替换渠道已选择的 Key。这些字段属于上游准入契约，不是普通可配置项。Zen 不开放 Key Pool，所有配置只保留第一个非空 Key；禁用或清空该 Key 后请求回退到 public。静态模型目录也需要在 OpenCode Zen 免费模型变化时随代码更新。
