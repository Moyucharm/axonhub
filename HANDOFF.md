# CPA 重构交接文档

更新时间：2026-09-01
基线提交：`8e503fc0`（`refactor: overhaul CPA runtime and credential management`）
当前状态：工作树包含下一阶段“CPA 后端执行边界重构”未提交改动

## 1. 当前结论

CPA 已完成两轮核心重构：

1. `8e503fc0` 完成 unified runtime、credential projection、SQL keyset pagination、GraphQL overview 与第一阶段前端职责拆分；
2. 当前工作树继续完成 connection、typed quota outcome、repository write boundary 与 automation workflow。

公开 GraphQL schema 和前端 operation 在第二轮未变化。`CPARefreshResult`、`CPARefreshProgress`、deprecated overview root fields 均保持兼容。

## 2. 当前执行边界

### Connection

`internal/server/biz/cpa_connection.go`：

- system secret 解密与 management client 创建集中在 `cpaConnectionProvider`；
- client 的作用域关闭由统一 helper 或持有该 client 的 workflow 明确管理；
- concrete HTTP client 与原有 redirect、timeout、body-size、安全限制不变；
- patrol 的同一 instance job 复用一个 client；成功 remote patch 后仍立即使用该 client 同步。

### Quota executor

`internal/server/biz/cpa_quota_execution.go`：

- provider fetch、Codex estimate hook、同 credential singleflight、单实例/全局 semaphore 从 `refreshOneCredential` 迁出；
- typed outcome 区分 execution `success / failure / skipped` 与 provider quota state；
- executor 不执行数据库 I/O，但消费同步开始时的 Ent credential snapshot；

### Repository/write boundary

`internal/server/biz/cpa_repository.go` 与 `cpa_sync.go`：

- quota outcome 的 success/failure/unsupported persistence 统一到一个入口；
- repository 只持有 Ent accessor、实例写执行器、quota capability predicate 与 cache invalidator 等窄依赖，不反向持有完整 service；
- 写锁内重新读取当前 credential，再计算 health/cooldown/sort projection；
- failure 保留旧成功 snapshot，unsupported/insufficient-data 清空 snapshot；
- auth-file snapshot transaction 已由 repository 承载，service 方法仅保留兼容包装；
- keyset/overview 只读查询暂不迁移。

### Refresh/progress

`internal/server/biz/cpa_refresh.go`、`cpa_refresh_progress.go`：

- batch worker 传递 typed outcome，不再只传 `error`/`failed bool`；
- aggregate 是 requested/succeeded/failed/skipped 的唯一计算入口；
- skipped 不增加 completed；旧 batch callback 不能污染后发 manual refresh；
- panic 仍只产生一个 failure outcome，callback panic 被隔离。

### Automation/runtime

`internal/server/biz/cpa_automation_policy.go`、`cpa_patrol.go`、`cpa_internal.go`：

- automation 统一产出 `none / disable / enable` 决策；
- enabled patrol 使用 quota persistence 返回的 fresh credential，不再额外 reload；
- disabled patrol 复用现有 client 执行 batch；
- runtime claim 与 refresh → enabled patrol → disabled patrol 顺序不变；
- runtime 跨实例并发上限有独立 helper/test。

## 3. 必须保持的行为

- CPA remote disabled 是唯一事实来源，本地不能自行推断并直接写 disabled；
- remote patch 失败不得修改本地 disabled；
- patch 成功后立即同步；同步失败记录诊断并等待后续收敛；
- quota error 保留旧 snapshot，但写 error/attempt/failure metadata；
- quota persistence 必须锁内重读当前 credential，避免覆盖并发 sync/toggle；
- 5 小时 quota window 只影响 UI cooldown，不触发自动禁用；
- 无 reset time 的耗尽窗口是无限期 cooldown；
- 同 credential singleflight、单实例 quota 4、全局 quota 8、runtime 跨实例 4；
- 不记录 secret、auth JSON、token 或原始 quota response。

## 4. 新增验证覆盖

- connection config forwarding 与成功/error/panic 关闭一次；
- typed outcome unsupported/failure/singleflight/非法状态校验；
- 单实例与全局 quota concurrency 上限；
- enabled/disabled patrol remote patch、失败不改本地状态、patch 后同 client 同步；
- repository failure 保留 snapshot 与当前并发字段；
- unsupported 清空 snapshot；
- runtime 跨实例 concurrency 上限；
- 真实 `httptest` management HTTP → auth-files sync → Ent → quota refresh/projection 链路；
- 既有 refresh projection、panic、progress、runtime order/CAS、sync remap 测试继续保留。

## 5. 已执行验证

以下命令已通过：

```text
go test ./internal/server/biz -run 'TestCPA(Connection|Repository|QuotaExecution|ExecutionIntegration|Refresh|Patrol|Credential|Automation|Runtime|Sync)' -count=1
go test -race ./internal/server/biz -run 'TestCPAQuotaExecution(SingleflightSharesProviderFetch|RespectsInstanceConcurrencyLimit|RespectsGlobalConcurrencyLimit)|TestCPA(EnabledPatrol|DisabledPatrol)|TestCPARuntimeJobsRespectCrossInstanceConcurrencyLimit|TestCPARefreshProjectionUsesCurrentCredential' -count=1
go test ./internal/server/biz ./internal/server/gql -count=1
```

未运行 lint、build、`make generate` 或前端测试；本轮没有 schema/前端变更。

## 6. 本轮不包含

- Ent/GraphQL schema 变化；
- GraphQL status enum；
- deprecated overview fields 删除；
- 前端进一步拆分或 CPA E2E；
- 1k/10k/100k benchmark 与新增复合索引；
- usage collector、price/estimate cache 的 repository 化；
- 跨进程 credential refresh 去重。

## 7. 后续剩余优先级

1. 评审并提交当前执行边界 checkpoint；
2. 建立 1k/10k/100k query/backfill benchmark 和 EXPLAIN 回归门禁；
3. 增加 CPA frontend critical-path E2E；
4. 继续拆分 `index.tsx`、`use-cpa-controller.ts`、`instance-dialog.tsx`；
5. 独立规划 GraphQL status enum；
6. 明确 deprecated overview fields 的兼容期与删除版本；
7. 根据真实 query plan 决定是否增加复合索引；
8. 后续再评估 usage/estimate persistence 边界。

## 8. 文档与决策记录

- `.agents/notes/implemented/architecture/2026-09-01-cpa-execution-boundaries.md`
- `.agents/notes/implemented/architecture/2026-08-31-cpa-management-client.md`
- `.agents/notes/implemented/architecture/2026-08-31-cpa-runtime-query-projection.md`
- `DIFF.md` CPA 章节

## 9. 操作约束

- 不要 reset、checkout、clean 或删除未跟踪文件；
- 不要 commit/tag/push，除非主人明确要求；
- 不运行 lint/build，不重启开发服务器；
- 若改变 Ent/GraphQL schema，必须停止并重新评审范围，再按规则执行 `make generate`；
- 不能把“执行写边界完成”描述为“完整 CPA 全域重构完成”。
