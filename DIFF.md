# DIFF.md — 自用分支与官方仓库差异记录

> 本文件记录 `自用` 分支相对官方仓库 [looplj/axonhub](https://github.com/looplj/axonhub) 的全部差异，供升级合并、功能回溯与版本管理参考。大功能与小修改均需记录。
>
> **版本基准约定（2026-08-18 定）：自用版本的基准 = 官方最新发行版（release tag），以发行版代码为主；unstable 上未发行的代码忽略，除非用户明确要求。** 详见第 5 节。

## 1. 分支与基线信息

| 项目 | 值 |
|---|---|
| 本分支 | `自用`（HEAD: `fa04299f`） |
| 官方仓库 | `https://github.com/looplj/axonhub.git` |
| 对比基准 | 官方最新发行版 `v1.0.0-beta7`（`b4d1fd04`，2026-08-11） |
| 当前基线提交（分叉点） | `b9af5ae2`（2026-08-04，官方 unstable，约定生效前遗留）feat(channels): add Groq channel (#2144) |
| 官方 unstable 最新（仅参考，按约定忽略） | `af423003`（2026-08-18）feat: GC 支持剥离已存储的请求/响应载荷 (#2246) |
| 本分支版本号 | `v1.0.0-beta8+azusa.v0.1`（约定生效前遗留，见第 5 节） |

### 更新本文件的方法

```bash
# 获取官方发行版 tag 与 unstable 参考分支（临时 refs，不修改 remote 配置）
git fetch https://github.com/looplj/axonhub.git \
  'refs/tags/v1.0.0-beta*:refs/tags/upstream/v1.0.0-beta*' \
  'refs/heads/unstable:refs/remotes/upstream-tmp/unstable'

# 查看本地独有提交（自用增量，相对官方最新发行版）
git log --oneline upstream/v1.0.0-beta7..自用

# 查看官方发行版独有提交（未合并内容）
git log --oneline 自用..upstream/v1.0.0-beta7

# 查看官方 unstable 未发行代码（仅参考，按约定忽略）
git log --oneline upstream/v1.0.0-beta7..upstream-tmp/unstable

# 查看文件级差异
git diff upstream/v1.0.0-beta7 自用 --stat
```

## 2. 自用功能增量 — 大功能

### 2.1 Key Pool 自动化与 Codex 渠道模拟

**提交**：`22d22a56`（后端）、`15986258`（前端 UI + 文档）、`2986e9e9`（前端改进）

在官方单一 API Key 模式之外，为渠道新增 **API Key Pool 模式**：

- **Key Pool 配置**：渠道级切换 `single` / `pool` 模式，池内多 Key 轮换使用，支持 `RetryCount`（同渠道总请求数）、`AutoCheck` 自动检查开关、`AutoCheckTimeoutSeconds` 探测超时等设置。
- **Key 自动禁用规则（渠道级）**：按 `statusCodes`（状态码）+ `keywordPatterns`（错误关键词）匹配，配置连续失败次数 `times`、动作 `action`（临时禁用 + `disableDurationMinutes`），独立于全局策略。
- **Key 自动检查与恢复**：`CheckChannelAPIKeys` 定期对池内 disabled keys 做并发探测（探测流量标记 `SourceTest`，不进入自动处置统计），成功后自动恢复 key 并清除失败连续计数。
- **Key 状态持久化**：`credentials.apiKeyStates` 记录每个 key 的失败计数、禁用到期时间、最近错误，跨实例通过 watcher/cache 同步。
- **Codex 渠道模拟**：`channel_codex_simulation.go` + `orchestrator/codex_simulation.go`，对 Codex 协议渠道提供本地模拟能力（模拟并发限制、随机失败等），用于无真实上游时测试渠道行为。
- **批量测试**：渠道 API key 批量并发测试能力。

**涉及模块**：`internal/objects/channel.go`、`internal/server/biz/channel_apikey*.go`、`internal/server/biz/channel_codex_simulation.go`、`internal/server/orchestrator/codex_simulation.go`、`internal/server/gql/*`、`frontend/src/features/channels/components/channel-api-key-pool-panel.tsx`、`channels-codex-simulation-dialog.tsx`、`api-key-auto-disable-rules-dialog.tsx`、`docs/{en,zh}/guides/channel-management.md`。

### 2.2 API Key 自动禁用策略配置（系统设置）

**提交**：`5220cea5`

- 系统设置「重试策略」页扩展：全局 API Key 自动禁用策略（`AutoDisableAPIKey`）配置 UI —— 启用开关、`any`/`codes` 模式、连续失败次数、状态码列表。
- 前端 `system.ts` 数据层 + `retry-settings.tsx` 组件 + 中英文 i18n。

**涉及模块**：`frontend/src/features/system/components/retry-settings.tsx`、`frontend/src/features/system/data/system.ts`、`frontend/src/locales/{en,zh-CN}/system.json`。

### 2.3 渠道自动冷却（Cooldown）

**提交**：`30da18a0`（后端核心）、`8253fa48`（GraphQL 数据链路）、`747ffca6`（orchestrator 路由）、`531ff9d2`（前端 UI）

在渠道自动禁用基础上新增**自动冷却恢复**能力，与 API Key 临时禁用设计对齐：

- **持久化状态**：渠道实体新增 `cooldown_until`（冷却到期时间）与 `auto_disable_state`（JSON：失败计数、策略 key、最近错误码/错误文本），均为服务端管理字段，跳过 GraphQL mutation input。
- **策略两级回退**：渠道级策略（`channelAutoDisable`，支持 `any`/`codes`、次数、`disable`/`cooldown` 动作、冷却分钟数）命中优先，未命中回退全局策略（`AutoDisableChannel` 新增 `action` + `cooldownDurationMinutes`）。
- **双维度独立累计**：渠道维度与 API Key 维度独立计数、独立执行；Key 规则仅对 Key Pool 渠道生效；单 Key 渠道不适用。
- **冷却语义**：冷却保持 `status=enabled`，生产路由排除有效冷却渠道（候选选择 + resolved 阶段双重过滤），手动测试绕过冷却；到期懒恢复（无定时 DB 清理），计数从 0 重新累计。
- **并发安全**：单渠道分片锁 + `UpdatedAtEQ` 乐观并发重试，跨实例正确性依赖乐观更新。
- **恢复入口**：`recoverChannelCooldown` mutation + 前端行菜单「恢复冷却」，只清冷却与运行计数，不改动人工 status/errorMessage；手动状态更新、批量状态更新自动清冷却。
- **Webhook 新事件**：`channel.auto_cooled`（payload 含 `trigger.action=cooldown`、`cooldown_until`、`channel.status=enabled`）；错误摘要脱敏（限长、不含响应正文）；异步派发带 panic 恢复。
- **前端**：冷却徽标（系统时区恢复时间）、tooltip（状态码 + 错误文本 + 恢复时间）、冷却筛选（`cooldownUntilGT`）、渠道错误处理规则对话框、系统设置 action/冷却分钟选择。

**涉及模块**：`internal/ent/schema/channel.go`（+生成代码）、`internal/objects/channel.go`、`internal/server/biz/channel_auto_disable.go`、`channel_metrics.go`、`channel.go`、`webhook_notifier.go`、`system.go`、`internal/server/orchestrator/{candidates,candidates_condition,select_candidates,performance,tester}.go`、`internal/server/gql/*`、`frontend/src/features/channels/**`、`frontend/src/features/system/**`。

### 2.4 CPA 管理（CLIProxyAPI，只读首期）

**提交**：待后续提交

新增独立一级「CPA 管理」功能，用于注册多个 CLIProxyAPI 实例并集中查看其 auth-files 凭证与额度快照：

- **多实例连接**：每个实例独立配置名称、项目地址、管理密钥、启用状态、自动刷新开关、5–1440 分钟刷新间隔及跳过 TLS 校验选项；地址/密钥变更与重新启用前必须成功读取 CPA `/v0/management/auth-files`。
- **密钥边界**：CPA 管理密钥以 JWT secret 经 HKDF-SHA256 派生的 AES-256-GCM 密钥可逆加密保存；GraphQL 仅返回 `hasSecret`，编辑留空保留原密钥，永不回显明文或密文。该方案不抵御同时取得完整数据库（含 JWT secret）的攻击。**注意：轮换 JWT secret 会使所有已保存的 CPA 管理密钥无法解密**（加密密钥由 JWT secret 派生，无多密钥版本化）；轮换后需对每个实例重新填写管理密钥（`updateCPAInstance` 传入新密钥即原地更新）。
- **只读凭证同步**：同步 CPA auth-files 的安全元数据；成功同步后硬删除已消失凭证，失败同步保留旧快照；支持 runtime-only 凭证，禁止下载 auth JSON 或持久化 access/refresh token、原始额度响应。
- **额度请求边界**：CPA 管理模块的 Codex、Claude、Antigravity、Kimi、xAI free 额度请求全部通过所选 CPA 的 `/v0/management/api-call` 发出，AxonHub 不直连供应商额度 API；xAI paid 不执行付费健康探测并标记为不支持。该限制仅适用于 CPA 管理，不影响现有渠道额度功能。
- **刷新与并发**：单实例独立周期调度，启动/重新启用后 15–60 秒随机抖动；自动刷新跳过 disabled/unavailable，手动刷新仅跳过 disabled；单实例最多 4、全局最多 8 个并发，同一凭证请求合并，单项失败不终止批次并保留上次成功快照。
- **独立界面**：新增 `/cpa` 路由与侧栏入口，复用系统级 `read_settings` / `write_settings` 权限；页面按单实例查看，提供固定供应商分组、名称/邮箱搜索、状态/套餐筛选、统计卡片、紧凑额度摘要与可展开多窗口详情，不复刻 CPA 自身面板。
- **网络安全**：仅允许 HTTP(S)，拒绝 URL userinfo、query/fragment、非同主机重定向及 HTTPS→HTTP 降级；请求有超时和响应大小限制；实例级跳过证书校验默认关闭并在表单、状态区持续警告。

**涉及模块**：`internal/ent/schema/cpa_*.go`（及生成代码）、`internal/objects/cpa.go`、`internal/server/biz/cpa*.go`、`internal/server/biz/cpa/**`、`internal/server/gql/cpa.graphql`、`internal/server/gql/cpa.resolvers.go`、`frontend/src/features/cpa/**`、`frontend/src/routes/_authenticated/cpa/index.tsx`、`frontend/src/locales/{en,zh-CN}/cpa.json`。

### 2.5 OpenAI 弱网关流终止兼容（工作树改动）

**提交**：随本批工作树改动一同提交；后续独立整理分支提交官方 AxonHub PR，届时移除本临时章节。

针对部分 OpenAI-compatible 上游（已实测 CCRNB 的 `muse-spark-1.2`）在已经返回文本/工具调用和 usage 后直接关闭 SSE、缺少 `finish_reason` 与 `[DONE]` 的情况，在 OpenAI Chat Completions 对外输出边界增加保守收尾：

- 仅在源流无错误、已产生实际输出且收到 usage 作为完整性证据时补发终止分片。
- 普通文本/推理使用 `finish_reason: "stop"`，工具调用使用 `finish_reason: "tool_calls"`。
- 终止分片携带空 `delta`，随后补发唯一 `[DONE]`。
- 没有 usage、没有实际输出或源流报错时不伪造成功，保留不完整流处理。

涉及文件：`llm/transformer/openai/inbound.go`、`llm/transformer/openai/inbound_stream.go` 及对应回归测试。该兼容补丁计划独立整理后提交官方 AxonHub PR。

> 本次仅记录功能差异，不单独修改 `internal/build/VERSION` 或创建 git tag；版本号与 tag 必须在明确发布时同步更新。

## 3. 自用修改 — 小修改

### 3.1 Key Pool RetryCount 语义调整（`91de02a2`）

`RetryCount` 重新定义为「同一渠道一次尝试的**总请求数**（含初始请求）」，而非纯重试次数：

- `SameChannelRetryLimit` 返回 `max(RetryCount-1, 0)`，即除去初始请求后的重试次数。
- `NormalizeAPIKeyPoolSettings` 拒绝 `RetryCount < 1`。
- 补充规范化与重试上限测试。

### 3.2 本地开发配置（`2d7237ea`）

- `.air.toml`：构建命令改为 `go build -o ./tmp/axonhub ./cmd/axonhub`。
- `frontend/pnpm-workspace.yaml`：允许 `@swc/core`、`esbuild` 构建脚本。
- `frontend/vite.config.ts`：开发端口改为 **5174** 并开启 `strictPort`（避免与默认 5173 冲突）。
- `frontend/pnpm-lock.yaml` 同步更新。

### 3.3 自用版本号（`a80d2b89`）

- `internal/build/VERSION` 设为 `v1.0.0-beta8+azusa.v0.1`（详见第 5 节版本号约定）。
- 创建 git tag `v1.0.0-beta8+azusa.v0.1`。

## 4. 官方差异 — 相对官方最新发行版 v1.0.0-beta7 的未合并内容（升级参考）

本地 `自用` 分支基线 `b9af5ae2`（2026-08-04）比官方最新发行版 `v1.0.0-beta7`（`b4d1fd04`，2026-08-11）**旧 26 个提交**（发行版内代码，升级时必须面对）；官方 unstable 相对 beta7 另有 **37 个未发行提交**（截至 `af423003`，按版本基准约定**忽略**）。beta7 相对自用基线的 26 个提交主要功能：

- **渠道**：qiniu/fenno 渠道类型（#2188）、unified API key management dialog（#2156）、per-credential auto disable with scheduled recovery（#2180）、渠道级 `downgradeMidConversationSystem` 开关（#2124）。
- **请求链路**：SSE keep alive（#2157）、请求日志表重设计（#2162）、请求日志记录 reasoning_effort（#2158）、请求体「对话阅览」模式（#2182）、request cache rate 展示（#2193）、SQLite TEXT 时间戳兼容与 backup 时区修复（#2189）、失败流/stream 系列修复（#2171/#2178/#2185/#2187/#2192/#2057）。
- **前端体验**：模型价格对话框虚拟化（#2163）、analytics 筛选 UX 对齐（#2154）。
- 其余为 fix/chore（#2155/#2172/#2176/#2177/#2190/#2191/#2194；完整清单见附录 B）。

### ⚠️ 合并冲突预警

官方 **#2180「per-credential auto disable with scheduled recovery」** 与本分支自用功能（Key Pool 自动禁用 + 渠道冷却）在 `channel_apikey.go`、`channel_auto_disable.go`、渠道 UI 及 webhook 事件上**功能重叠**；官方 **#2156「unified API key management dialog」** 与本分支 `channel-api-key-pool-panel.tsx` 等 UI 重叠；官方 **#2188** 新增的 qiniu/fenno 渠道类型与自用渠道类型 enum 并存。升级合并时需逐功能评审取舍，避免双份实现互相干扰。另注意 beta7 引入的 schema 字段（`channels.auto_disabled_at`、`request_executions.reasoning_effort`）与自用字段（`cooldown_until`、`auto_disable_state`）的共存——ent AutoMigrate 的 `WithDropColumn(true)` 会删除对方版本独有的列，迁移前必须备份。

## 5. 版本号约定与基准规则

本仓库为 axonhub 自用版，版本号基于官方版本号追加增强后缀：

```
<官方最新发行版版本号>+azusa.v<增强版本号>
```

### 基准规则（2026-08-18 定）

- **基准版本**：始终为上游仓库**最新发行版（release tag）**的版本号，以发行版代码为主。
- **未发行代码**：官方 `unstable` 上超出最新发行版的提交**忽略**（不合并、不作为基准、不计入差异清单重点），除非用户明确要求跟进。
- **增强部分**：`azusa.v0.x` 为 build metadata（SemVer 规范中不参与版本比较），保证官方发布新版本时更新检查始终正确。
- **递增规则**：每次自用功能更新增强号 `+0.1`（v0.1 → v0.2 → …），并同步更新 `internal/build/VERSION` 与 git tag。
- **当前状态（约定生效前遗留）**：`v1.0.0-beta8+azusa.v0.1` 的 beta8 继承自 unstable 开发线超前标记，基线为 2026-08-04 的 unstable 未发行代码（比官方 beta7 发行版旧 26 个提交）。**官方下一个发行版发布时按本约定对齐基准并更新版本号**，届时更新检查自然恢复正确。

### 官方新发行版发布时的升级流程

```
1. 以官方新发行版（如 v1.0.0-beta8）代码为基准
2. 重放/合并自用功能（Key Pool 自动化、Codex 模拟、渠道冷却、本地配置等；官方 #2180/#2156 与本分支功能重叠，需逐项评审取舍）
3. 更新版本号为 <新发行版>+azusa.v<x>：internal/build/VERSION + git tag + 重新触发镜像构建
4. 更新本文件（DIFF.md）：基线信息、差异清单、附录 A/B
```

### 生效路径与约束

- **生效路径**：本地/CI 构建经 `//go:embed VERSION` 与 Dockerfile 注入；正式发布经 goreleaser `{{ .Tag }}` 注入。
- **注意**：`VERSION` 文件只能包含纯版本号（代码用 `strings.TrimSpace` 后直接经 semver 解析），不可加注释。

## 附录 A：本地独有提交清单（`upstream-tmp/unstable..自用`）

| 提交 | 类型 | 说明 |
|---|---|---|
| `2d7237ea` | chore(dev) | 本地开发配置（air/端口/workspace） |
| `22d22a56` | feat(channels) | Key Pool 自动化与 Codex 模拟（后端） |
| `15986258` | feat(channels) | Key Pool / Codex 管理 UI 与文档 |
| `5220cea5` | feat(system) | API Key 自动禁用策略配置 UI |
| `2986e9e9` | feat(frontend) | Key Pool 管理 UI 改进 |
| `30da18a0` | feat(channels) | 渠道冷却持久化与自动处置核心 |
| `8253fa48` | feat(api) | 冷却 GraphQL 数据链路 |
| `747ffca6` | feat(orchestrator) | 冷却路由过滤与测试流量隔离 |
| `531ff9d2` | feat(frontend) | 冷却 UI 与 i18n |
| `91de02a2` | fix(keypool) | RetryCount 总请求数语义 |
| `a80d2b89` | build | 自用版本号 v1.0.0-beta8+azusa.v0.1 |
| `7a7ae1d1` | docs | 新增 DIFF.md 差异记录 |
| `3558b214` | chore | 自用 Docker 构建工作流 + .agent 忽略 |
| `fa04299f` | fix(frontend) | pnpm 10 重新生成 lockfile（Docker 构建修复） |

## 附录 B：官方未合并提交清单（`自用..upstream-tmp/unstable`，63 个）

> 其中 `2bdfcb61`…`b4d1fd04` 前 **26 个属于 v1.0.0-beta7 发行版内容**（升级基准）；`b4d1fd04` 之后为 unstable **未发行代码**（按基准约定忽略，除非用户要求）。

```
2bdfcb61 feat(transform): add channel switch to downgrade mid-conversation system messages (#2124)
446a0937 feat(requests): show final reasoning_effort in request logs (#2158)
1823ec34 feat(channels): add unified API key management dialog (#2156)
7ed44005 fix(cline): normalize empty message content (#2155)
ce8d6e7d fix(frontend): align analytics filter UX (#2154)
2c6efdb1 fix(responses): preserve function calls completed in done events (#2057)
359cf840 feat(requests): redesign request log table (#2162)
d6ed9c62 perf(frontend): virtualize model price dialog to fix lag with many models (#2163)
2d7d7c86 feat: add sse keep alive, close #2146 (#2157)
dba642a0 chore: add 2 sponsors
bbd854bf chore: sync model developers data (#2176)
783611df feat(channels): per-credential auto disable with scheduled recovery (#2180)
a6bfffa8 fix(responses): preserve polymorphic reasoning and Codex metadata (#2178)
e33bacdf feat(apikeys): sync and transfer profile templates (#2177)
4495aa3c fix(responses): propagate terminal status in streaming conversion (#2171)
1ce8b54d fix(responses): 兼容 Responses API created_at 的浮点整数形式 (#2187)
889bc8ee fix(stream): report incomplete streams to the client, close #2184 (#2185)
2b78817e feat: request 请求体支持 对话阅览 模式,便于请求体数据查看 (#2182)
3c12ccbd feat: add qiniu/fenno channel (#2188)
9dfd6ac0 fix(claude-code): automate cache compatibility and OpenAI effort mapping (#2172)
56dcc72f feat(models): add thinkingmachines as model developer (#2190)
732466d0 fix(sqlite-time-format-compat): 兼容 SQLite TEXT 时间戳格式并修复 backup 注册时区退化 (#2189)
c88c2c8d chore: add log for chat heartbeat (#2191)
d1140628 feat: show request cache rate, close #2170 (#2193)
35133b6e fix(responses): retry incomplete streams before done (#2192)
b4d1fd04 fix: model associate condition caused model not found, close #2183 (#2194)
b117c4bc opt: anthropic signature recognization (#2197)
800bb72f feat: unify auto-refresh controls and stabilize list animations (#2198)
4782a14f fix(i18n): change currency code example from RMB to CNY (#2202)
56747471 feat(quota): add Charm Hyper credit balance checker (#2199)
c8de8cf8 fix: align codex quota bar colors (#2166)
6027d959 feat(quota): track OpenCode Go quota via official usage API (#2204)
8597f5ef feat: hide unroutable configured models from public lists (#2215)
d81b4baf fix(trace): only persist explicitly identified traces (#2208)
3b7e8618 fix(responses): harden stream terminal, retry, and resource boundaries (#2196)
fae797d3 feat: add xai_responses channel type (#2212)
d232d343 fix(prompts): Parse project GUIDs in prompt schema (#2200)
31213e36 chore(actions): upgrade workflows to node24 actions (#2219)
d1cde099 fix(responses): preserve image tool_choice without pass-through (#2218)
fc1d27da fix(codex): skip pass-through for unsupported response limits (#2217)
24145b38 feat(requests): add reusable column reordering with drag-and-drop (#2222)
86f9829e feat: persit chunks for failed stream request (#2224)
f58ee08e feat(quota): allow exempting channels from quota filtering (#2214)
7a5a2927 feat: add xAI subscription SSO channel and Responses support for xai (#2225)
e06196a1 feat: provider quota estimate (#2228)
c0233704 feat: add codex missing headers (#2230)
852b8c6f fix(actions): derive unstable version from latest tag (#2231)
baacba6b fix: preserve top-level reasoning tokens from sglang (#2237)
2b4f0f5a fix: escalate nanogpt quota status to exhausted (#2236)
6f9bfc5f fix: fill reasoning context for codex responses lite (#2235)
4476e778 feat(frontend): add sans/serif/mono font options to theme menu (#2234)
8911bf04 chore: remove krill ai sponsor
9fb6f1af chore: minor ui/error optimization (#2238)
f2f80a9f fix: image outbound & override (#2241)
d8be5649 chore: update default docker compose config
f18f66a6 fix: restore meta muse lineup in model catalog (#2258)
86eac3a7 fix(channel): write UTC updated_at when saving model prices (#2257)
7f1e4082 fix(frontend): grant system-level route access to owner users (#2254)
fe74b95f fix(copilot): send Grok models through Responses API (#2250)
e8d1037c opt: gemini channel compatible (#2259)
877ff78b chore: sync model developers data (#2242)
38e36bad feat: introduce unified opencode transformer, close #2240 (#2260)
af423003 feat: GC 支持剥离已存储的请求/响应载荷 (#2246)
```
