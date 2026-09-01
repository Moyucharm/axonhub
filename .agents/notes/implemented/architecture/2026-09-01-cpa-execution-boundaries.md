# Agent Note: CPA 执行边界与自动巡检编排

Status: implemented

## Problem

CPA 的额度刷新、批次进度和自动巡检共享同一个 `CPAService`，但远端 client 生命周期、provider quota 请求、singleflight/并发门控、Ent 投影写入和 remote patch 后同步都混在流程函数中。刷新成功与失败分别拼接 Ent update，新增字段时容易漏掉其中一条路径；patrol 还会在同一实例任务中重复创建管理 client，并在 refresh 后额外查询 credential 才能做自动化决策。

## Decision

CPA 在 biz 包内建立四个内部边界，同时保留 `CPAService` 作为 GraphQL 与 FX 的兼容门面：

- `cpaConnectionProvider` 负责 system secret 解密和 `ManagementClient` 创建；client 的作用域关闭由统一 helper 或持有该 client 的 workflow 明确管理，已有 HTTP client 与安全限制不变。
- `cpaQuotaExecutor` 负责 provider fetch、Codex estimate hook、同 credential singleflight、单实例与全局 semaphore，并返回 typed execution outcome；它不执行数据库 I/O，但消费同步开始时的 Ent credential snapshot。
- `cpaRepository` 负责 credential/instance 查询、patrol candidate 查询、quota outcome 持久化和 snapshot transaction。它只持有 Ent accessor、实例写执行器、quota capability predicate 与 cache invalidator 等窄依赖，不反向持有完整 `CPAService`。quota 落库在实例写锁内重新读取当前 credential，再维护 quota 字段与 projection。
- automation policy 返回 `none / disable / enable` 决策；patrol 复用实例级 client，把 fresh persisted credential 直接交给策略，并在 remote patch 成功后立即使用同一 client 同步 snapshot。

Typed outcome 将 batch 执行状态 `success / failure / skipped` 与持久化的 provider quota state 分开，并在执行、聚合和持久化入口校验状态不变量：failure 必须携带 error，skipped 必须携带原因，success 只能携带可持久化 provider state。公开 `CPARefreshResult` 和 `CPARefreshProgress` 仍由内部 outcome 聚合，GraphQL schema 与前端 operation 不变。

同步 snapshot 的 Ent mutation 入口迁入 repository；`CPAService.syncCredentialSnapshot` 只保留兼容包装。只读 keyset/overview 查询、usage collector、价格与估算缓存不纳入本次 repository，避免形成新的 God object。

## Concurrency and failure semantics

- 同 credential 的并发 refresh 继续由 singleflight 合并；单实例 quota 并发最多 4，全局最多 8。
- runtime 继续一次 claim 同一实例的全部 due operation，并按 refresh、enabled patrol、disabled patrol 串行执行；跨实例执行最多 4。
- quota fetch failure 写入 error/attempt/failure metadata，但保留旧成功 snapshot。
- unsupported 与 insufficient-data 清空 snapshot；成功更新 success metadata。
- 所有 quota outcome 在写锁内重读当前 credential，避免覆盖请求期间发生的 sync、toggle、display/status/disabled/unavailable 更新。
- batch worker panic 转为且只发布一个 failure outcome；progress callback panic 被隔离。
- remote patch 失败不修改本地 disabled；patch 成功后立即同步，若同步失败则记录 convergence failure 并由后续任务重试。

## Alternatives considered

- **建立全仓通用 Repository 框架：** 当前仓库没有统一 repository 模式，引入框架会扩大重构范围；本次采用 CPA 内部具体 Ent 实现和消费侧窄边界。
- **让 quota executor 直接写 Ent：** 可以减少一个组件，但会重新把远端执行、并发和持久化投影绑定，难以独立验证 failure/snapshot 语义。
- **把 provider quota state 直接当 batch outcome：** `unsupported` 是一次成功执行后的业务状态，而网络错误是执行失败，两者不能用于同一计数语义。
- **patrol 收集所有 remote patch 后只同步一次：** 请求更少，但会延迟当前每次成功 patch 的本地收敛，并扩大 cancellation 后的短暂不一致；因此仍立即同步，只复用 client。
- **将 query/overview 和 usage collector 一并迁入 repository：** 会把本轮变成全域重写；这些边界已有独立性能或生命周期决定，后续按证据继续拆分。

## Consequences

refresh、patrol 和 manual toggle 不再各自实现 quota projection 写入或重复创建 patrol client。typed outcome 成为 batch result、progress 和 automation 的共同输入；repository 成为 quota projection 一致性的单一写入口。业务级 `httptest` 覆盖真实 management HTTP → sync → Ent → quota refresh 链路，并发测试覆盖 singleflight、实例 quota 上限和 runtime 跨实例上限。

代价是 `CPAService` 必须由 production/test constructor 一次性完整组装；singleflight 仍是进程内去重，跨进程执行资格继续依赖 runtime 的数据库 optimistic claim。Repository 当前只覆盖执行写路径，并不是完整 CPA persistence abstraction。
