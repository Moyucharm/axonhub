# Agent Note: beta12 正式发行对齐与自用增量保护

Status: implemented

## Problem

官方 `v1.0.0-beta12@2b1a0f93` 相对 beta11 包含 141 个提交，涉及凭证策略、重试、Responses 传输与流生命周期、额度、依赖和生成代码。自用分支同时保有 CPA、持久化 Key Pool、渠道冷却、Codex 指纹模拟、OpenCode Zen 和广告削减。机械采纳上游文件会丢掉自用状态，或重新带回已删功能。

## Decision

只整合正式 tag `upstream/v1.0.0-beta12`，不跟进其后的 `unstable`。用户确认批量更新，指定版本 `v1.0.0-beta12+azusa.v0.6.10`，并明确要求保护自用功能和削减项。基于干净的 `自用@a6f04afb` 使用 `git merge --no-commit --no-ff`，先整合与验证，不改写历史或创建分支。用户随后明确要求发版、push、编译，并确认创建合并提交、同名 tag、普通推送与自用 linux/amd64 镜像发布（含更新 `latest`）；不授权部署或生产数据操作。

### 凭证、设置与重试

- `channel_apikey_state.go` 继续持有自用凭证状态 CAS、服务端 failure state 与提交后缓存刷新；上游同名文件的多 Key 选择状态独立为 `channel_apikey_selection_state.go`。不能用任意一侧覆盖另一侧。
- 上游 sticky/random/round-robin/success/priority/fixed 策略与自用单次失败单一归属共存。测试流量不推进成功轮换，也不触发自动处置。上游 #2453 的整套自动禁用实现仍不恢复。
- 重试优先级为：全局关闭门禁 → 自用 Pool 总请求数（减去初次请求）→ 渠道级重试数 → 全局重试数；Pool 同时受可用 Key 数量约束，不循环使用已尝试 Key。兼容两种可选 pipeline 重试接口，保留原自用调用约定。
- 新的 `updateChannelSettings` 补丁接口同时支持 `apiKeyPool`、`codexSimulation`、渠道重试数和响应超时。共享前端 mutation fragment 保留凭证运行态、冷却、限流、覆盖操作与模拟设置；服务端版本锁和原有凭证状态防回写一起生效。
- Vertex Key 始终保留 project/region 绑定，不平铺写回 `apiKeys`；删除与最后一 Key 保留规则复用自用 CAS 路径。普通文本导入拒绝 Vertex，前端使用绑定编辑/管理入口，不能把无项目 Key 混入 Vertex。

### 协议与自用边界

采用上游 HTTP/WS 独立端点、Responses 连续对话约束、终止状态/交付确认/记账分离及原生 Images 转发。保留兼容网关 `[DONE]` 成功终止、终止后继续读取迟到传输错误、DONE 最多一次、OpenAI 最后一个 Gemini thought signature chunk 和强制上游流式请求对象同步。

OpenCode Zen 的三协议、匿名身份与官方工具兼容不变；任意兼容渠道仍可配置 Jev/System One，而不恢复上游只允许部分渠道的限制。渠道列表保持自用固定列宽和按断点隐藏次要列，不用上游横向滚动布局替换。

CPA 源码、实体 schema、前端和路由文件保持升级前内容；模型创建精确规则、借价匹配、执行详情选择和跨项目 RequestExecution 权限继续保留。Ent/GraphQL 以合并后的源 schema 运行 `make generate`，不手工拼接生成输出。

### 削减、依赖与发布

`atlascloud`、`qiniu`、`qiniu_anthropic`、`fenno` 继续不进入可选渠道枚举、配置、图标或文案；旧值只留在迁移与备份兼容路径。README 的赞助与推广块、官方 `docker-publish.yml` / `docker-unstable.yml` 均不恢复。保留官方 Release job 的 fork 守卫及自用 Docker workflow。

采用发行版依赖和 Docker 基础镜像；安全 overrides 汇入已有 `pnpm-workspace.yaml`，保留 `@swc/core` / `esbuild` 构建脚本许可和 Docker 对 workspace 文件的复制。Wafer 额度退出与启动前清理按正式版采用；不操作实际数据库、不重启开发服务。

## Verification

- `make generate` 成功。
- 根模块 `go test ./...` 通过（含 internal 全套）；`cd llm && go test ./...` 通过。
- 前端 `pnpm test:unit`：449 通过、1 跳过、0 失败（新增专项回归前的全量结果）。
- 新增业务回归验证设置补丁不清除 Pool、模拟、冷却、策略与 Key 运行态；Vertex 删除保留真实完整绑定且不接受无绑定导入。
- 新增 GraphQL 回归用当前生成 schema 校验真实前端渠道操作及共享 fragment，防止只更新前端类型而丢失服务端字段。
- Pool 重试回归覆盖总请求数优先于渠道级计数、缺省时继承渠道级计数、渠道级零值关闭继承重试。
- 完整前端 `tsc -p tsconfig.app.json --noEmit` 仍不通过。将升级前 HEAD 源码导出到临时目录并使用相同安装依赖比较，诊断从 215 减至 192，按“文件 + TS 错误码”计数没有新增。不能把根 `tsconfig.json` 的空文件检查成功冒充完整类型检查成功。
- 升级验证阶段不执行构建、lint、浏览器 E2E、生产迁移、镜像构建或部署；发版经单独确认后使用自用 Docker workflow 编译，结果以 Actions 为准。

## Alternatives considered

- **整块取 upstream/ours 消除冲突**：分别会删除自用持久化与削减规则，或漏掉 beta12 安全和流处理修复。按职责整合，并验证自动合并文件，而非只修冲突标记。
- **重放全部历史自用提交**：已被上游替代的旧 Cline 和模型审计实现可能复活，也会重引已回退内容。沿现有融合基线进行正式 tag 合并，保留历史决定。
- **引入更晚 unstable 修复或未经确认顺便发布镜像**：超出正式版基线或升级授权，不执行。发版阶段在用户单独批准后才提交、创建匹配 VERSION 的 tag 并编译镜像。
- **为通过上游测试而恢复广告枚举或收窄 System One**：违背用户明确要求。调整冲突测试来验证既定自用契约，保留上游其他测试。

## Consequences

收益是发行安全修复与自用能力同时保留，凭证绑定和补丁字段有可执行回归保护。代价是后续发布仍要人工评审凭证状态、重试、流终止与列表布局；不能将本分支看作原样上游。

生产升级前仍须备份数据库：Ent 保留既有 drop-column 行为，正式版移除 Wafer 额度行。代码集成与镜像发布不等于生产数据库迁移或部署完成；本次授权仅覆盖提交、tag、普通推送和镜像编译发布。完整前端类型错误属于已知验证限制，不掩盖为通过。
