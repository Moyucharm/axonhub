# Agent Note: OpenCode Zen 渠道

Status: implemented

## Problem

AxonHub 的 `opencode_zen` 在同一渠道提供 Chat Completions、Responses 和 Anthropic Messages 三种原生协议。免费层校验不止 User-Agent 和工具定义，还涉及 2.x provider-family 路径、父子会话身份、JSON 编码及 Bun TLS/HTTP 请求特征。`Free usage limit reached` 不能单独证明 IP 真实额度耗尽；完整客户端兼容也不保证上游开放匿名访问。

第三方 Agent 的工具定义又不等于官方 OpenCode：例如 Pi 的 `read` 使用自己的参数结构。直接把同名客户端工具替换成官方 schema，虽然可能通过准入，却会让模型产生客户端无法执行的参数。Responses 的 `custom` 工具和 `namespace` 容器也使用 `name` 字段；仅凭名称识别官方函数会删除客户端的自由格式工具或整个命名空间。另外 pipeline 在 raw middleware 前调用 `FinalizeAuthHeaders` 并清空 `request.Auth`；finalizer 若仅从 Auth 读取 Key，会把真实用户 Key 错误回退为 `public`。

## Decision

AxonHub 保留独立渠道类型 `opencode_zen`，在 `llm/transformer/opencode/zen` 内实现协议兼容，不依赖外部代理，不复制 GPL 参考项目的实现。

### 地址与认证

- 默认配置地址仍为 `https://opencode.ai/zen/v1`，兼容已保存的渠道；官方主机的标准 `/zen/v1` 和 `/inference/v1` 推理路径统一迁移到 `/inference/openai/v1/chat/completions`、`/inference/openai/v1/responses` 或 `/inference/anthropic/v1/messages`，不改变选中的凭据。
- 自定义网关主机和非标准 endpoint path 不做路径迁移；无需修改数据库中的渠道地址，不新增 v1 回退。Messages 保留已有查询参数并固定唯一的 `beta=true`，防止通用 Query 拼接重复或覆盖。
- 用户 Key 优先，没有可用 Key 才使用 `public`。Chat/Responses 使用 Bearer，Messages 使用 `X-API-Key`。
- 请求初始化时把已选 Key 保存为不可变的私有 `requestIdentity` 值，存于 HTTP request 的 transformer metadata。该值字段不导出，JSON 序列化不包含凭据；finalizer 不重复选择 Key，也不信任后续 header override。
- finalizer 克隆 headers/body/metadata，再恢复认证和客户端身份；`Body` 与 `JSONBody` 保持同步。移除非规范大小写的受保护 header，避免同一身份字段出现多个冲突值。

### 客户端身份

三种原生协议共用 `identity.go`：

- User-Agent 为 `opencode/latest/2.1.1/cli`；client 为 `cli`。合法客户端项目哈希保留，派生项目标识按调用者 scope 隔离，仍为 40 位小写十六进制值。
- `x-opencode-session-id` 保留子会话；`x-opencode-session`、`x-session-id` 和 `x-session-affinity` 使用 root/parent 会话。有 parent 时同时发送两个 parent headers，不再把父子 ID 强制为同一值。
- 不发送 `x-opencode-request`；追踪保留在网关本地。Messages 固定 `anthropic-version: 2023-06-01` 和 `anthropic-beta: interleaved-thinking-2025-05-14`。
- 优先读取客户端 session headers，其次使用共享上下文 session、请求 metadata.session_id，最后哈希 system/developer 前缀和首个用户消息。服务端 session scope 在存在时隔离派生身份。
- 合法官方 session 保留；其他客户端 session 确定性编码成 `ses_` + descending timestamp 布局 + Base62 后缀。同一会话追加历史消息不会重置 session；finalizer 从私有身份值恢复 root/child/parent，普通 overrides 不能替换身份。
- 继续使用项目原有 `httpclient` / `net/http`，不另建请求客户端、TLS、代理、解压或连接池实现，也不增加 Bun/uTLS/Brotli 依赖。Accept、Cache-Control、TLS 与 HTTP 头顺序仍由原有执行器和标准库处理；本次只同步 transformer 协议字段，不宣称网络指纹与 Bun 完全相同。

### 请求体与工具兼容

- 三种协议都强制上游 SSE。pipeline 保留调用方原始模式，并为非流式客户端聚合响应。Chat 强制 `stream_options.include_usage: true`，使自动聚合仍有上游 usage。
- Chat 未显式设置 `max_tokens` 或 `max_completion_tokens` 时补 `max_completion_tokens: 32000`；显式字段包括 `0/null` 均保留，由上游验证。
- Chat 顶层按 `model, messages, tools, stream, stream_options, max_completion_tokens` 排列，其余字段排序；三种协议均关闭 JSON HTML escaping。数字使用 `json.Number` 保持精度，工具参数字符串和字面反斜杠-u 内容不被解释或改写。
- 按各原生协议补齐官方 `shell/edit/glob/grep/read` 工具：Chat 为嵌套 function，Responses 为扁平 function，Messages 为 `input_schema`。
- `opencode_tools.json` 的说明和输入 schema 来自 MIT 许可的官方 OpenCode 源码，固定到 commit `37df62f4a1b6d89187a1169187a6cef10557c0ca`；与本次参考上游的五个核心工具内容一致，无需替换来源。shell 描述按宿主系统选择 Linux/bash 或 Windows/powershell；仅注入的官方 Chat function 带 `strict: false`，客户端 strict/schema 保留。来源和完整许可见同目录 `OPENCODE_LICENSE.md`。
- `toolAliasOutbound` 对客户端与官方保留名称冲突的 function 工具使用不碰撞的 `axonhub_*` 内部别名，保留客户端 description/schema，并同步历史 tool calls 和指定工具选择。转换路径的流式事件、普通响应和聚合结果中的工具名称还原为客户端名称；结构化工具输入、参数字符串和用户文本不被改写。
- Responses 在通用 transformer 完成原生序列化后对顶层 `function/custom` 工具分配别名，保留 custom 的 grammar/format、历史调用及 named/allowed_tools 选择。候选别名避开所有顶层工具、namespace 容器和成员名称，防止省略 namespace 的流式 done 事件误还原。namespace 的名称、描述、成员定义和 namespaced 调用保持原样。
- Responses 工具协调仅重建与官方名称匹配的顶层 `type: function`，不按同名关系过滤 custom、namespace 或其他原生工具类型。
- finalizer 在通用 header/body override 后再次固定工具和身份字段，raw body pass-through 仍被拒绝。协调操作幂等，不重复追加官方工具。
- Chat 和 Messages 的无工具请求禁用工具选择。Responses 保留既有的 `tool_choice: none` 清理行为。Muse Spark Contributor Free 实测仅支持 `auto`，不支持 `required` 或 named function choice；网关不把所有模型的强制工具选择静默降级成 auto。

### 渠道能力

后端保持 `openai/chat_completions`、`openai/responses`、`anthropic/messages` 三种 endpoint，不开放 Responses WebSocket。凭据优先级为渠道测试 override、可用渠道 Key、public fallback，统一使用 `getOpenCodeZenAPIKeyProvider`。Zen 不开放 Key Pool，只保存首个非空用户 Key，public 不作为伪凭据落库。

模型通过 `GET /models` 动态获取，不维护前端静态目录，也不对未知模型静默回退。本次更新 transformer 的默认测试模型为实际可用的 `mimo-v2.6-flash-free`，不修改用户已保存的 supported models 或默认测试模型。

## Verification

### 2026-10-24 客户端协议同步

协议参考固定到 opencode2api commit `57b906b23fbecf3eed12297883a9e3bbf44e1493`，仅同步 2.1.1 的请求协议特征；未部署或运行 opencode2api 作对照测试。按用户确认的范围删除了试验性的 Bun 传输、专用测试、接入标记及生产依赖，恢复原有 HTTP 客户端。

- 清理后独立、不落盘的本地 smoke 使用原生 `*http.Transport` 和 TLS 1.3，实际收到 `Accept: text/event-stream`、`opencode/latest/2.1.1/cli`。JSON 字节布局、缺省 token 上限及官方工具 strict 检查通过，SSE 成功聚合正文 `OK`、总 usage 11。
- 清理后的 HTTP、Zen 三协议、pipeline、Responses 和 Anthropic 定向竞态测试通过；根模块的渠道与 orchestrator 定向测试通过，均使用只读模块模式，不需要新增依赖。
- 清理前，经用户单独批准的一次匿名请求使用试验实现发出：`public` + `deepseek-v4.1-flash-free` + 固定文本，收到 HTTP 429 `FreeUsageLimitError`。该批准仅针对网络验证，不等于批准新增传输架构。该结果不是当前保留实现的线上验证；本轮未重复线上请求，不能宣称免费层已恢复，也不能仅凭该错误断言 IP 额度真实耗尽。

```sh
# llm 模块
GOTMPDIR="/home/azusa/.cache/go-build" GOPROXY=off go test -p 2 -mod=readonly -vet=off -race ./httpclient ./transformer/opencode/... ./pipeline/... ./transformer/openai/responses/... ./transformer/anthropic/... -skip '^TestZenLive' -count=1
# 仓库根模块
GOTMPDIR="/home/azusa/.cache/go-build" GOPROXY=off go test -p 2 -mod=readonly -vet=off ./internal/server/biz ./internal/server/orchestrator -run 'OpenCodeZen|ModelFetcherFetchesOpenCodeZen|BulkImport.*OpenCodeZen|FinalizeTransportRequest' -count=1
```

### 2026-09-18 原实现验证（历史记录）

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

2026-09-18 的匿名 Chat 与 Responses 文本和客户端工具调用有历史上游成功证据，但不代表当前线上可用性。本次只更新 transformer 的客户端协议字段，复用现有请求链路，不复制 Bun 网络栈。匿名准入和额度仍由上游决定。渠道保留现有代理、重试、记录、模型管理和用户 Key 能力，不需要数据库迁移或新增部署服务。

代价是官方工具定义会占用 prompt tokens，且需要随上游协议变动维护；保留名称别名增加少量请求/响应处理。工具名称还原属于转换路径；原始响应或事件透传仍沿用通用透传语义，不在此层改写原始数据。客户端不应依赖内部别名；模型仍可能返回兼容占位工具，网关不执行这些工具。强制工具选择等模型能力限制仍由真实上游决定。已有渠道如果仍保存已下线模型，需要用户通过模型获取更新，不会自动替换用户模型映射。最终 User-Agent、身份 header、认证和上游 stream 属于准入契约，不受普通 override 配置控制。

## Sources

- [opencode2api 本次协议参考](https://github.com/jasonxu114514/opencode2api/tree/57b906b23fbecf3eed12297883a9e3bbf44e1493)
- [2.x 请求体特征更新 #142](https://github.com/jasonxu114514/opencode2api/pull/142)
- [Bun 请求头特征更新 #143](https://github.com/jasonxu114514/opencode2api/pull/143)
- [相同免费额度报错 #145（社区讨论，不是官方计额说明）](https://github.com/jasonxu114514/opencode2api/issues/145)
- [OpenCode 官方模型与免费层客户端限制](https://opencode.ai/v2/docs/console/models/)
- [OpenCode 官方工具定义](https://github.com/anomalyco/opencode/tree/37df62f4a1b6d89187a1169187a6cef10557c0ca/packages/core/src/tool/plugin)
- [OpenCode MIT License](https://github.com/anomalyco/opencode/blob/37df62f4a1b6d89187a1169187a6cef10557c0ca/LICENSE)
