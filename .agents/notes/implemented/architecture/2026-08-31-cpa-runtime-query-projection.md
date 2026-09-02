# Agent Note: CPA runtime 与查询投影边界

Status: implemented

## Problem

CPA 的定时 snapshot refresh、enabled patrol 和 disabled patrol 各自注册 dispatcher、查询 due instance 并维护下一次执行时间。三个 dispatcher 的并发上限彼此独立，多实例部署也只能依赖进程内 singleflight，因而同一实例可能被重复或并行处理。

凭证列表虽然暴露 cursor，却会读取实例全部凭证后在内存中计算健康状态、过滤、自然排序和截页。stats、provider counts 与 plan types 又通过独立 GraphQL root fields 重复读取或聚合同一实例，凭证规模增大后分页不能限制 Go 层读取量。

## Decision

CPA 只注册一个 `cpa-runtime` fixed-rate dispatcher。dispatcher 查询所有 due operation，以 `CPAInstance.updated_at` 乐观条件一次 claim 同一实例当前到期的 refresh、enabled patrol 和 disabled patrol，并在 claim 时推进对应 `next_*_at`。同一实例的已 claim operation 按 snapshot refresh、enabled patrol、disabled patrol 串行执行；不同实例共享统一并发上限。usage-event cleanup 由同一个注册入口注册，HTTP usage queue collector 仍保留独立生命周期。

`CPACredential` 持久化以下查询投影：

- `display_name_sort_key` 保留原有大小写不敏感自然排序；`display_name_sort_length` 只在名称以数字段结束时保存 lowercase 字节总长度，精确保留旧比较器对末尾前导零的特殊 tie-break，其他自然键相等名称仍由 ID 决胜；
- `health_state` 保存不依赖当前时间的 `healthy / abnormal / pending / disabled`；
- `quota_cooling` 与 `quota_cooldown_until` 保存 quota snapshot 的耗尽状态；
- `projection_version` 支持启动时按 ID keyset 幂等回填。

cooldown 与 health 正交。有效 cooldown 定义为 `quota_cooling && (quota_cooldown_until IS NULL || quota_cooldown_until > now)`，因此有 reset time 的 cooldown 可在读取时懒恢复，没有 reset time 的耗尽状态则保持有效直到下一次 quota 写入；当同一 snapshot 同时包含有限 reset 与无 reset 的耗尽窗口时，无 reset 窗口优先，`quota_cooldown_until` 必须保持 nil。

quota refresh 的网络请求仍使用开始时的 credential 输入，但 typed outcome 进入 repository 写锁后会重新读取当前 credential，再用当前 display/status/disabled/unavailable 与本次 quota outcome 计算 projection，避免并发 sync/toggle 被旧实体覆盖。provider fetch、singleflight 和实例/全局 semaphore 由独立 quota executor 负责，详见 [CPA 执行边界与自动巡检编排](./2026-09-01-cpa-execution-boundaries.md)。runtime job 与 batch refresh worker 都安装顶层 panic guard；batch worker 无论正常返回还是 panic 都只发布一个结果，progress callback 的 panic 也被隔离。

凭证查询把 search、provider、plan、enabled/disabled、abnormal、cooldown 和 abnormal-only 全部下推 SQL。稳定顺序为 `priority DESC, display_name_sort_key, display_name_sort_length, id`，cursor 保存同一组排序值，数据查询只读取 `first + 1` 行。旧 `{priority, display_name, id}` cursor 仍可转换为新排序投影。

GraphQL 使用 `cpaOverview(instanceID)` 返回 stats 和每个 provider 的 count/plan types。`cpaCredentialStats`、`cpaProviderCounts`、`cpaPlanTypes` 三个旧 root fields 已在同一重构系列的契约 checkpoint 删除；前端只请求新 overview，不再在 provider 切换时单独请求 plan types。

## Alternatives considered

- **保留三个 scheduler task，只共享 semaphore：** 可以限制部分 quota 请求，但无法阻止同实例操作并行，也不能解决多进程重复执行。
- **只使用进程内 mutex/singleflight：** 单进程有效，多实例部署没有执行资格边界；数据库 optimistic claim 才能覆盖所有进程。
- **查询时继续从 quota JSON 派生状态：** 能保持单一 snapshot，但 abnormal/cooldown 无法进入 SQL predicate，keyset 分页仍会退化为全量内存过滤。
- **把 cooldown 合并进单一 health enum：** quota error、disabled 与 cooldown 可以重叠，单 enum 会丢失现有状态筛选的 OR 语义；正交字段能保持兼容。
- **将自然排序改为普通字典序：** 实现更简单，但会改变 Account 2/Account 10 等现有可观察顺序，因此保留物化自然排序键。
- **立即删除旧 GraphQL root fields：** 主人明确选择不跨 release 保留兼容入口；当前前端已无旧字段调用，删除让 schema 与唯一 overview 契约保持一致。外部客户端必须迁移到 `cpaOverview`。

## Consequences

分页实体读取量现在受 page size 限制，状态过滤和总数由数据库完成；overview 不再加载 credential entity 全表，provider 切换也不再产生额外 GraphQL 请求。跨进程 dispatcher 通过 optimistic claim 避免同一实例 due work 被拆分执行。

代价是 credential 写路径必须同步维护 projection，启动时也必须完成旧行 backfill。自然排序额外占用两个列，并需要显式编码旧比较器只在末尾数字段使用长度 tie-break 的行为；overview stats 当前使用多个小型 COUNT aggregate，而不是一条方言相关的 conditional aggregate SQL。旧 GraphQL fields 在兼容期内仍增加少量 schema 表面。refresh persistence 的重读增加一次按 ID 查询，panic guard 则把单 worker 崩溃降级为可观测失败，避免 dispatcher 或批次死锁。
