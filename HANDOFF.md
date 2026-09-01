# CPA 重构交接文档

更新时间：2026-09-01
当前分支：`自用`
当前 HEAD：`d1f91e6a`（工作树存在大量未提交改动）

## 1. 交接结论

本轮已完成并验证**已批准 CPA 四项重构方案**：

1. 统一 refresh、patrol 与 scheduler runtime；
2. 凭证列表改为 SQL 过滤 + 稳定排序 + keyset 分页；
3. 持久化查询投影：健康状态、cooldown、自然排序键；
4. 合并 GraphQL overview，并迁移前端到单一 overview 查询。

但“完整 CPA 领域分层重构”仍未全部完成。当前应把本轮视为一个可审查、尚未提交的 checkpoint，不要直接宣称所有 CPA 管理重构都结束。

## 2. 当前工作树状态

工作树包含本轮以及进入任务前已有的未提交变更。严禁使用 `git reset --hard`、批量 checkout、删除未知文件或覆盖其他改动。

当前 `git status --short` 显示：

- 39 个已修改 tracked 文件；
- 12 个未跟踪文件/目录，主要是 `.agents/notes/`、CPA frontend 拆分文件、CPA 配置/自动化/投影/回归测试文件；
- `git diff --check` 已通过。

重要：本轮没有执行 commit、tag、reset、lint、build，也没有重启开发服务器。

## 3. 本轮已实现内容

### 3.1 持久化凭证投影

涉及：

- `internal/objects/cpa.go`
- `internal/ent/schema/cpa_credential.go`
- `internal/server/biz/cpa_projection.go`
- Ent 生成文件和迁移 schema
- `cpa_sync.go`、`cpa_refresh.go`

新增字段：

- `display_name_sort_key`
- `display_name_sort_length`
- `health_state`
- `quota_cooling`
- `quota_cooldown_until`
- `projection_version`

当前 projection version 为 `1`。启动注册 CPA runtime 前会执行按 ID keyset 分批、幂等的 backfill。

健康状态取值：`healthy`、`abnormal`、`pending`、`disabled`。`disabled` 优先；异常 status/unavailable/quota error 为 abnormal；quota success/unsupported 且远端正常时为 healthy。

cooldown 与 health 正交：

- `available = health_state == healthy && effectiveCooling == false`；
- `effectiveCooling = quota_cooling && (quota_cooldown_until IS NULL || quota_cooldown_until > now)`；
- 5 小时窗口仅用于 UI cooldown，不触发自动禁用；
- 无 reset time 的已耗尽窗口表示无限期 cooldown；
- 已经过期 reset 的窗口不再 cooling，等待下一次成功刷新。

### 3.2 CPA runtime

主要文件：

- `internal/server/biz/cpa_internal.go`
- `internal/server/biz/cpa_patrol.go`
- `internal/server/biz/fx_module.go`

已将三个 5 秒 fixed-rate dispatcher 合并为单个 `cpa-runtime`：

- 统一处理 refresh、enabled patrol、disabled patrol；
- 通过 `CPAInstance.UpdatedAt` + `Enabled` 条件更新进行 CAS claim；
- claim 时推进对应的 `next_*_at`，避免多实例重复执行；
- 同一实例在 `executeCPARuntimeJob` 中按 refresh → enabled patrol → disabled patrol 顺序执行；
- 不同实例由 `maxCPAInstanceConcurrency` 限制；
- `RegisterScheduledTasks` 是 CPA scheduler 的唯一注册入口；
- usage cleanup 仍由该入口注册，usage stream 保持独立 Start/Stop lifecycle；
- 启动顺序为 projection backfill → schedule initialization → scheduler registration。

### 3.3 SQL keyset 分页

主要文件：`internal/server/biz/cpa_query.go`、`cpa_query_test.go`。

凭证查询现在：

- search/provider/planTypes/statuses/abnormalOnly 下推 SQL；
- status 多选保持 OR 语义；
- cooldown 使用持久化字段与 SQL predicate；
- `Count` 单独获得 totalCount；
- 数据查询使用 `Limit(first + 1)` 判断 hasNextPage；
- 排序为 `priority DESC, display_name_sort_key ASC, display_name_sort_length ASC, id ASC`；
- cursor v2 包含 priority/sortKey/sortLength/id；
- decoder 兼容旧 cursor，并从旧 display name 重新生成 sort key；
- 不再全量 `.All()` 后在 Go 中过滤、排序、分页。

SQLite 10k synthetic dataset 的 `EXPLAIN QUERY PLAN` 已验证：

- 无筛选/provider/plan 查询直接使用 order index；
- disabled 使用 disabled index，但排序可能使用临时 B-tree；
- health filter 当前倾向 order index；
- cooldown 使用 multi-index OR，排序可能使用临时 B-tree。

这不是失败，而是后续是否新增复合索引的依据；目前不要无证据继续堆索引。

### 3.4 GraphQL overview 与前端迁移

GraphQL：

- 新增 `CPAOverview`；
- 新增 `CPAProviderOverview`，包含 provider/count/planTypes；
- 新增 `cpaOverview(instanceID)`；
- 旧 `cpaCredentialStats`、`cpaProviderCounts`、`cpaPlanTypes` 保留并标记 deprecated；
- gqlgen 已重新生成；
- resolver 已加入 `ScopeReadSettings` 校验并调用 biz `Overview`。

Frontend：

- `OVERVIEW_QUERY` 只请求 `cpaOverview`；
- plan types 从 provider overview 派生；
- 删除独立 `PLAN_TYPES_QUERY` 和 `useCPAPlanTypes`；
- 删除 `plans` 缓存失效键；
- 保留 granular invalidation：instances、overview、credentials、all；
- CPA 页面和凭证表已拆出多个组件/类型/API/controller 文件。

## 4. 已执行验证

已通过：

```text
make generate

go test ./internal/server/biz ./internal/server/gql

cd frontend && pnpm test:unit
```

最近一次完整验证结果：

- `make generate` 通过；
- backend CPA biz/gql 测试通过；
- frontend unit tests：87 个通过；
- 新增/补强的自然排序 keyset、cooldown、refresh 并发投影、panic guard、runtime 顺序与 overview 权限测试通过；
- frontend overview contract tests 2 个通过；
- `git diff --check` 通过；
- Agent Note tree/format 校验通过。

最近的定向验证包括：

```text
go test ./internal/server/biz -run 'TestCPA(DisplayNameSortKeyMatchesNaturalOrder|QueryUsesStableKeysetAndOverviewAggregates|ProjectionBackfillIsIdempotent)'
go test ./internal/server/biz -run 'TestCPAQuotaCooldown|TestCPAAutoManageQuotaCooldown'
go test ./internal/server/biz -run 'TestCPARefresh(Worker|Projection)'
go test ./internal/server/biz -run 'TestCPARuntime(OperationsExecuteInOrder|ClaimIsAtomicAcrossDueOperations)'
go test ./internal/server/gql -run 'TestCPAOverviewResolverRequiresReadSettings'
```

## 5. 最近修复与当前审查重点

### 已修复：无 reset time 的混合 cooldown

`cpaQuotaCooldownDetailFor` 现在把“无 reset time 的耗尽窗口”当作无限期 cooldown；它优先于其他有限 reset 时间。测试已加入混合窗口场景。

### 已修复：刷新投影的并发旧实体问题

`refreshOneCredential` 的 quota error/success 持久化路径现在在写入前重新读取当前 credential，再基于当前 remote/status/disabled/unavailable 与新的 quota outcome 计算 projection，避免使用 refresh 开始时的 stale entity 覆盖 sync/toggle 的最新字段。

### 已补齐：worker panic 防护

`errgroup.Go` worker 增加 `defer recover`。runtime job 与 credential refresh worker 均会记录 panic；refresh worker 会尽力发送失败结果，避免 batch progress 永远等待。

### 已修复：自然排序 projection 的前导零边界

`display_name_sort_length` 现在只在名称以数字段结束时保存 lowercase 字节总长度，精确保留旧 `naturalStringCompare` 的特殊 tie-break。`a2x` 与 `a02x` 这类中间数字段等值名称继续由 ID 决胜，而 `Account 2` 与 `Account 002` 仍按旧长度语义排序；旧 cursor decoder 与 projection 共用同一 helper。

### 已补齐：并发与权限回归测试

新增测试证明 refresh 成功/失败写入都会保留网络请求期间发生的 disabled/unavailable/display name 更新；batch worker panic 只发布一个失败结果，progress callback panic 被隔离；同实例 runtime 操作按 refresh → enabled patrol → disabled patrol 执行；`cpaOverview` 无 `read_settings` 时拒绝访问，具备权限时返回聚合结果。

注意：这部分改动已经完成 gofmt、生成、完整 backend/frontend 测试、diff check 和 Agent Note 校验。

## 6. 仍未完成的更大范围工作

这些不属于已批准四项方案的完成证明，但如果目标是“完整 CPA 领域重构”，仍需继续：

- 尚无独立 CPA repository/transaction-write layer；
- 尚无独立 connection/client factory（目前仅有 `ManagementClient` 接口与可注入 `clientFactory`）；
- 尚无正式 typed quota execution/outcome 组件；
- patrol 仍有直接创建 client、刷新后重新查询 credential 等职责；
- refresh progress、batch execution result 尚未完全收敛到统一 outcome 模型；
- `cpa.go` 仍承担较多 facade/config/client/删除等职责；
- 前端 `index.tsx`、`use-cpa-controller.ts`、`instance-dialog.tsx` 仍可进一步拆分；
- 缺少 fake CPA HTTP server integration tests；
- 缺少同实例三类操作串行、跨实例并发上限、singleflight 单请求等完整测试；
- 缺少 1k/10k/100k scale benchmark/回归门禁；
- 缺少 GraphQL overview permission 专项测试；
- 缺少 CPA critical-path frontend E2E；
- GraphQL status fields 尚未全部转换为 enum；
- `DIFF.md` 尚未补充本轮 runtime/projection/keyset/overview 架构变化；
- deprecated overview root fields 尚未删除，这是有意保留的兼容期行为。

## 7. Agent Notes

本轮已存在以下决策记录：

- `.agents/notes/implemented/architecture/2026-08-31-cpa-runtime-query-projection.md`
- `.agents/notes/implemented/architecture/2026-08-31-cpa-instance-config.md`
- `.agents/notes/implemented/architecture/2026-08-31-cpa-management-client.md`
- `.agents/notes/implemented/simplification/2026-08-31-cpa-frontend-boundaries.md`

后续重要架构/行为修改请继续更新或新增 Agent Note，尤其是：

- projection consistency 与 refresh/sync 并发边界；
- runtime claim/调度语义；
- GraphQL 兼容期与 deprecated 字段删除时机；
- 是否新增复合索引及其 EXPLAIN 证据。

## 8. 建议下一步顺序

1. 检查最终 `git diff` 与工作树分类，确认当前 checkpoint 包含本轮前已有的 CPA 配置/client/frontend 边界改动以及本轮 runtime/projection/keyset/overview 与回归测试。
2. 如需继续“完整 CPA 领域重构”，从第 6 节未完成项另开计划，不要把它们混入当前已通过门禁的 checkpoint。
3. 仅在主人明确确认后执行 commit；当前仍不要 commit、tag 或 reset。

## 9. 禁止事项

- 不要 reset、checkout、clean、删除未跟踪文件；
- 不要 commit/tag，除非用户明确确认；
- 不要运行 lint/build；
- 不要重启开发服务器；
- 不要把“本轮四项方案完成”误写成“完整 CPA 重构完成”；
- 不要移除 deprecated GraphQL 字段；
- 不要恢复全量 credential 查询；
- 不要把 5 小时 quota window 纳入自动禁用决策；
- 不要把远端 disabled 以本地推断替代；
- 不要覆盖工作树中进入任务前的 API Key Pool 或 outbound 相关改动。

## 10. 交接给下一对话的一句话

请从当前未提交工作树继续：本轮 CPA 四项批准重构及其最终边界修复已经通过完整生成、backend/frontend 测试、diff check 与 Agent Note 校验；当前是可审查 checkpoint，下一步只需决定是否继续更大范围重构或在主人明确确认后提交，禁止擅自 commit/reset/clean。
