# Agent Note: 移除 OpenCode Zen 并迁移为 OpenAI Chat

Status: implemented

## Problem

用户明确要求移除 OpenCode Zen 专用渠道，并让已有 Zen 渠道在升级后强制变为 OpenAI Chat 格式。仅删除前端入口会留下旧运行时；仅修改渠道类型会留下模型的 Responses/Messages 强制协议。MySQL/TiDB 收缩 enum 前也必须先消除旧值，否则 schema 迁移可能失败。

本决定取代 [OpenCode Zen 专用渠道](../feature/2026-09-18-opencode-zen-channel.md) 的运行时支持；旧笔记保留其历史 rationale，不再描述当前功能。

## Decision

移除 Ent/GraphQL `opencode_zen` 枚举、前端供应商/渠道入口、凭据可选特例、Zen 三协议 transformer、身份模拟、工具定义与注入。`opencode_go` 和 `opencode_go_anthropic` 不变；普通 OpenAI 渠道仍可使用用户配置的 Zen URL，但不再获得特殊兼容处理。

### 启动迁移

`internal/server/db/legacy_channel_type_migration.go` 在 Ent schema 迁移前检测旧 Zen 记录，并调用 `legacy_opencode_zen_migration.go`。迁移基于旧类型是否仍存在，不使用 SemVer gate，避免自用版本构建元数据导致迁移漏跑。启用自动数据库迁移是前提；关闭该选项的部署需自行安排迁移。

在一个 SQL 事务内转换所有旧 Zen 记录，包括软删除、禁用和归档记录：

- `type` 变为 `openai`。
- `endpoints` 只保留 Chat；已有 Chat 端点保留 path/base_url 覆盖，移除旧 Responses/Messages 端点及 transport。
- 支持模型和旧 modelProtocols 涉及的模型去重后统一显式固定 `openai/chat_completions`，覆盖旧格式和旧 disabled 标记，保证客户端协议不会绕回旧出站格式。普通 OpenAI 的默认端点可能包含其他格式，因此不能只删旧覆盖而不重新固定 Chat。
- 已配置密钥和禁用状态不主动恢复。原来的单 `apiKey` 同时写入普通编辑器读取的 `apiKeys`；完全空密钥才显式写入 `public`，继承旧 transformer 的隐式凭据。
- 保留渠道 ID、名称、Base URL、模型列表、状态、策略、关联，以及无关 JSON 设置/凭据字段；不改 updated_at，不改数据库结构。使用 RawMessage 仅重写相关 JSON 字段，避免类型化往返丢弃未来或扩展字段。
- 解码或更新失败回滚整批转换；类型和配置不会分开提交。转换后再次启动不再匹配旧类型，因此不覆盖迁移后的用户编辑。

SQL 使用 SQLite/MySQL `?` 与 PostgreSQL `$n` 参数占位符，不使用方言专属 JSON 修改表达式。此次自动化验证使用 SQLite，没有真实 PostgreSQL/MySQL 实例验证。

### 备份和通用能力

`internal/ent/channel/type.go` 仅保留迁移用 `LegacyTypeOpencodeZen` 常量，不作为合法渠道类型。`internal/server/backup/restore.go` 对旧 Zen 备份的新增/覆盖恢复同步转换设置、端点与空凭据，避免匿名旧记录被跳过或恢复后仍受 Messages/Responses 限制；其他渠道恢复行为不变。

通用的“transformer 强制上游流式后遇到统一 request 替换”回归测试保留，改用测试专用 `forcedStreamTestOutbound`，不再依赖已删除的 Zen 包。补充普通 OpenAI 回归，验证迁移后的 URL、public 凭据、Chat 模型覆盖、不注入 OpenCode header/工具、不强制流式。

## Alternatives considered

- 只隐藏前端入口：旧渠道仍依赖被移除的特殊运行时，不能满足移除要求。
- 仅将类型改为 openai：旧模型协议和自定义端点会继续选择 Responses/Messages，也会让空凭据渠道无法加载。
- 在 schema 迁移后再改类型：MySQL enum 收缩可能先失败，故在现有 pre-schema legacy migration 入口处理。
- 无条件每次启动强制 Chat：会破坏用户迁移后的正常编辑，因此只匹配仍为旧类型的行。
- 保留 Zen transformer、工具或旧身份作为兼容层：与用户明确移除的要求冲突，直接删除。
- 自动改写 Base URL 或修复匿名免费推理：用户要求普通 OpenAI Chat，而不是继续维护 Zen 特殊推理协议，故保留原配置地址并明确不保证匿名免费访问。

## Verification

- `make generate` 成功；Ent 源 schema、生成代码和 GraphQL enum 同步移除类型，没有手改生成文件。
- `go test ./internal/server/db ./internal/ent/channel ./internal/server/backup -count=1` 通过；覆盖启动迁移顺序、旧备份新增/覆盖、空/单/已有密钥、未知 JSON 字段保留、软删除记录、非 Zen 不变、重启后编辑保留、坏 JSON 与实际 UPDATE 失败回滚。
- `go test ./internal/server/biz ./internal/server/orchestrator -count=1` 通过。
- `go test ./internal/server/biz -run TestMigratedZenUsesOrdinaryOpenAIChat -count=1` 通过。
- 在 `llm/` 运行 `go test ./transformer/opencode/... -count=1` 通过，OpenCode Go 保留。
- `frontend/` 的 `pnpm test:unit`：446 项，445 通过、1 跳过、0 失败。
- 全量 TypeScript `tsc -p tsconfig.app.json --noEmit` 未通过。使用只读 CompilerHost 将本次修改的 TypeScript 源替换为 HEAD 内容作基线对照：HEAD 与当前各 190 条诊断，按文件与错误码比较无新增，未在本任务扩展修复既有类型问题。
- Agent Note tree/format 校验均通过（14 篇笔记）。
- `git diff --check` 通过；没有执行 build/lint、手动重启服务、修改生产数据库、提交、推送或变更版本号。

## Consequences

渠道配置与运行时统一回到普通 OpenAI Chat，删除约三千多行 Zen 专用实现/测试和工具数据，保留通用管线验证。用户无需逐个手动切换旧 Zen 渠道。迁移会丢弃旧多协议端点和协议选择，降级不能自动还原，升级前应备份数据库；不保证曾依赖特殊身份和工具指纹的匿名免费请求继续成功。实际 Docker 部署及线上迁移未在本任务执行。
