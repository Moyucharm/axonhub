# Agent Note: OpenCode Zen 渠道

Status: implemented

## Problem

AxonHub 的 `opencode_zen` 在同一渠道提供 Chat Completions、Responses 和 Anthropic Messages 三种原生协议。Zen 的匿名推理入口与带 Key 的入口不同，免费层还校验 OpenCode 客户端身份和完整工具定义。继续向 `/zen/v1` 发送 `public` 请求、使用旧 User-Agent、遗漏第二个 session header、发送旧 request header 或仅补简化版 `bash/read`，都不能代表当前 OpenCode 客户端。

第三方 Agent 的工具定义又不等于官方 OpenCode：例如 Pi 的 `read` 使用自己的参数结构。直接把同名客户端工具替换成官方 schema，虽然可能通过准入，却会让模型产生客户端无法执行的参数。Responses 的 `custom` 工具和 `namespace` 容器也使用 `name` 字段；仅凭名称识别官方函数会删除客户端的自由格式工具或整个命名空间。另外 pipeline 在 raw middleware 前调用 `FinalizeAuthHeaders` 并清空 `request.Auth`；finalizer 若仅从 Auth 读取 Key，会把真实用户 Key 错误回退为 `public`。

## Decision

AxonHub 保留独立渠道类型 `opencode_zen`，在 `llm/transformer/opencode/zen` 内实现协议兼容，不依赖外部代理，不复制 GPL 参考项目的实现。

### 地址与认证

- 默认配置地址仍为 `https://opencode.ai/zen/v1`，兼容已保存的渠道；使用 `public` 时，仅把官方主机的标准 Chat、Responses、Messages 推理路径改写到 `https://opencode.ai/inference/v1`。
- 用户 Key 保留原地址。自定义网关主机和自定义 endpoint path 不做匿名地址迁移；无需修改数据库中的渠道地址。
- 用户 Key 优先，没有可用 Key 才使用 `public`。Chat/Responses 使用 Bearer，Messages 使用 `X-API-Key`。
- 请求初始化时把已选 Key 保存为不可变的私有 `requestIdentity` 值，存于 HTTP request 的 transformer metadata。该值字段不导出，JSON 序列化不包含凭据；finalizer 不重复选择 Key，也不信任后续 header override。
- finalizer 克隆 headers/body/metadata，再恢复认证和客户端身份；`Body` 与 `JSONBody` 保持同步。移除非规范大小写的受保护 header，避免同一身份字段出现多个冲突值。

### 客户端身份

三种原生协议共用 `identity.go`：

- User-Agent 为 `opencode/latest/2.0.29/cli`；client 为 `cli`，project 使用稳定的 40 位小写十六进制值。
- `x-opencode-session`、`x-opencode-session-id`、`x-session-id` 和 `x-session-affinity` 使用同一个 session。
- 不发送 `x-opencode-request`；追踪保留在网关本地。
- 优先读取客户端 session headers，其次使用共享上下文 session、请求 metadata.session_id，最后取首个用户消息作为稳定信号。服务端 session scope 在存在时参与隔离。
- 合法官方 session 保留；其他客户端 session 确定性编码成 `ses_` + descending timestamp 布局 + Base62 后缀。同一会话追加历史消息不会重置 session。

### 请求体与工具兼容

- 三种协议都强制上游 SSE。pipeline 保留调用方原始模式，并为非流式客户端聚合响应。Chat 强制 `stream_options.include_usage: true`，使自动聚合仍有上游 usage。
- 按各原生协议补齐官方 `shell/edit/glob/grep/read` 工具：Chat 为嵌套 function，Responses 为扁平 function，Messages 为 `input_schema`。
- `opencode_tools.json` 的说明和输入 schema 来自 MIT 许可的官方 OpenCode 源码，固定到 commit `37df62f4a1b6d89187a1169187a6cef10557c0ca`；shell 使用 Linux/bash 变体。来源和完整许可见同目录 `OPENCODE_LICENSE.md`。
- `toolAliasOutbound` 对客户端与官方保留名称冲突的 function 工具使用不碰撞的 `axonhub_*` 内部别名，保留客户端 description/schema，并同步历史 tool calls 和指定工具选择。转换路径的流式事件、普通响应和聚合结果中的工具名称还原为客户端名称；结构化工具输入、参数字符串和用户文本不被改写。
- Responses 在通用 transformer 完成原生序列化后对顶层 `function/custom` 工具分配别名，保留 custom 的 grammar/format、历史调用及 named/allowed_tools 选择。候选别名避开所有顶层工具、namespace 容器和成员名称，防止省略 namespace 的流式 done 事件误还原。namespace 的名称、描述、成员定义和 namespaced 调用保持原样。
- Responses 工具协调仅重建与官方名称匹配的顶层 `type: function`，不按同名关系过滤 custom、namespace 或其他原生工具类型。
- finalizer 在通用 header/body override 后再次固定工具和身份字段，raw body pass-through 仍被拒绝。协调操作幂等，不重复追加官方工具。
- Chat 和 Messages 的无工具请求禁用工具选择。Responses 保留既有的 `tool_choice: none` 清理行为。Muse Spark Contributor Free 实测仅支持 `auto`，不支持 `required` 或 named function choice；网关不把所有模型的强制工具选择静默降级成 auto。

### 渠道能力

后端保持 `openai/chat_completions`、`openai/responses`、`anthropic/messages` 三种 endpoint，不开放 Responses WebSocket。凭据优先级为渠道测试 override、可用渠道 Key、public fallback，统一使用 `getOpenCodeZenAPIKeyProvider`。Zen 不开放 Key Pool，只保存首个非空用户 Key，public 不作为伪凭据落库。

模型通过 `GET /models` 动态获取，不维护前端静态目录，也不对未知模型静默回退。本次更新 transformer 的默认测试模型为实际可用的 `mimo-v2.6-flash-free`，不修改用户已保存的 supported models 或默认测试模型。

## Verification

本地验证：

```sh
cd llm
GOPROXY=off go test -mod=readonly -vet=off -race ./transformer/opencode/... ./pipeline/... ./transformer/openai/responses/... ./transformer/anthropic/... -skip '^TestZenLive' -count=1
cd ..
go test ./internal/server/biz -run 'OpenCodeZen|ModelFetcherFetchesOpenCodeZen|BulkImport.*OpenCodeZen' -count=1
go test ./internal/server/orchestrator -run 'OpenCodeZen|FinalizeTransportRequest' -count=1
node --test frontend/src/features/channels/data/channel-config.test.mjs frontend/src/features/channels/data/protocol-options.test.mjs
git diff --check
```

`protocol_test.go` 使用本地 HTTP server 走完整 pipeline，覆盖三种上游协议、流式/非流式调用、同名 read 工具还原及 token usage；验证 Auth 已被清空后仍恢复用户 Key。另覆盖 session 稳定性、scope 隔离、官方工具完整 schema、自定义地址、别名冲突、参数与大整数不被改写、协调幂等和 metadata 克隆。

`responses_tool_alias_test.go` 覆盖原生 Responses custom/namespace 与官方工具同名、grammar 和原始 namespace 描述/成员保留、跨类型及 namespace 成员别名避碰、历史调用、named/allowed_tools 选择、请求与扩展元数据不被修改、finalizer 幂等，以及普通响应、流式事件和聚合结果的 custom 名称还原。测试不访问真实上游。

真实匿名验证使用固定测试文本、`public`、官方主机，不读取用户渠道 Key、不发送项目内容、不执行任何模型工具：

```sh
cd llm
AXONHUB_ZEN_LIVE_TEST=1 go test ./transformer/opencode/zen -run 'TestZenLive(Anonymous|ClientTools)' -v -count=1
```

- 相同兼容请求：旧 `/zen/v1/chat/completions` 返回 `403 FreeTierError`，新 `/inference/v1/chat/completions` 返回 HTTP 200 和完整 SSE。
- `mimo-v2.6-flash-free`：Chat 请求返回 `OK`；客户端 read/write 声明及 `tool_choice: auto` 返回 `read({"filePath":"example.txt"})`。
- `muse-spark-1.3-contributor-free`：Responses 请求经 pipeline 聚合返回 `OK`；同名客户端工具测试返回相同 read 参数结构。
- 一次 MiMo 请求返回 HTTP 200、正常 stop 和 usage，但只包含 reasoning 中的 `OK`、没有 content。线上测试校验有效输出与 usage，不依赖随机生成的正文措辞，也不把 reasoning 改成 content；这是模型输出边界，不是免费层准入失败。
- Messages 完整链路通过本地模拟验证；本次没有使用真实账户 Key 或付费模型，不能把本地测试当作 Messages 付费实测。
- 真实网络测试默认跳过，只有显式设置 `AXONHUB_ZEN_LIVE_TEST=1` 才消耗匿名上游额度。

## Alternatives considered

- **继续部署 `oc2api.py` 或 opencode2api：** 需要独立服务，AxonHub 无法直接管理完整请求链路；本次仅参考 wire 协议差异，继续使用现有 transformer/pipeline。
- **复用 `opencode_go` 类型：** 平台的 endpoint、认证和指纹不同，会耦合不相关契约。
- **为三种协议分别增加渠道类型：** 重复地址、凭据、同步和健康状态；现有多 endpoint 抽象已经足够。
- **只使用渠道 header/body override：** 无法可靠处理匿名地址迁移、流式升级、条件工具补齐和最终身份恢复。
- **保留简化工具存根：** 名称相同不等于官方定义，无法满足当前免费层准入。
- **覆盖客户端同名工具 schema：** 会破坏 Pi 等 Agent 的调用参数；内部别名虽然增加了映射成本，但保留客户端契约。
- **仅让 Responses 非 function 工具跳过名称过滤：** custom 仍会与补齐的官方 function 共享顶层名称，无法确保调用命中客户端定义；顶层 custom 也需要分配别名。
- **在 Responses 原生序列化前修改工具名：** 会使通用 transformer 的工具签名与原始 namespace/工具选择扩展不匹配，导致 namespace 描述和 custom 子工具等原始定义无法回放；别名作用于已完成通用转换的原生请求体。
- **从 finalizer 现有 header 读取用户 Key：** header 可能已被 override；直接恢复最初选中的凭据才能保证单次请求的认证一致。
- **直接复制 GPL 项目工具与逻辑：** 与本仓库 Apache 分发约束不匹配；协议逻辑独立实现，工具定义从 MIT 官方源码取得并保留许可。
- **所有请求固定 public、或把 public 存成渠道 Key：** 前者忽略用户账户能力，后者把运行时 fallback 伪装成真实凭据，并干扰编辑、导出和禁用行为。
- **未知模型自动回退：** 违反网关显式模型路由语义，掩盖配置错误。

## Consequences

匿名 Chat 与 Responses 的文本和客户端工具调用有真实上游成功证据；Messages 使用同一身份和工具契约，并有本地完整链路覆盖。渠道保留现有代理、重试、记录、模型管理和用户 Key 能力，不需要数据库迁移或新增部署服务。

代价是官方工具定义会占用 prompt tokens，且需要随上游协议变动维护；保留名称别名增加少量请求/响应处理。工具名称还原属于转换路径；原始响应或事件透传仍沿用通用透传语义，不在此层改写原始数据。客户端不应依赖内部别名；模型仍可能返回兼容占位工具，网关不执行这些工具。强制工具选择等模型能力限制仍由真实上游决定。已有渠道如果仍保存已下线模型，需要用户通过模型获取更新，不会自动替换用户模型映射。最终 User-Agent、身份 header、认证和上游 stream 属于准入契约，不受普通 override 配置控制。

## Sources

- [opencode2api 参考实现](https://github.com/jasonxu114514/opencode2api/tree/25afadd30345faaa9c6f18c45c0c14b176862b89)
- [OpenCode 官方工具定义](https://github.com/anomalyco/opencode/tree/37df62f4a1b6d89187a1169187a6cef10557c0ca/packages/core/src/tool/plugin)
- [OpenCode MIT License](https://github.com/anomalyco/opencode/blob/37df62f4a1b6d89187a1169187a6cef10557c0ca/LICENSE)
