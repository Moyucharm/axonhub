# Agent Note: CPA 查询规模门禁与 provider-plan 索引

Status: implemented

## Problem

CPA credential 查询已把过滤、keyset 排序和 overview 聚合下推 SQL。一次受控的 SQLite 规模实验用于确认 provider-plan page 与 provider overview 的真实 query plan，但把大 fixture 和 backfill benchmark 放进常规 Go 测试包会增加普通测试的内存、临时空间和运行时风险。

## Decision

保留已验证的 `cpa_credentials_by_provider_plan_sort` 索引：

```text
(cpa_instance_id, provider, plan_type, priority DESC,
 display_name_sort_key, display_name_sort_length, id)
```

一次性实验覆盖 1k/10k 数据规模，观察到 provider-plan page 与 provider overview 使用该 covering index，overview 不需要 temporary B-tree；projection backfill 每 200 行短事务相较于每行提交减少约 10% 时间。

规模 fixture、backfill benchmark 和 10k query-plan 门禁不再放在 `internal/server/biz` 的常规测试包中。普通 Go 测试只验证小型功能行为；未来压力测试必须以独立、明确授权的手工任务运行，不得进入常规测试脚本或并行验证矩阵。

详细历史结果保存在 `.agent/summary/cpa-query-plan.md`。

## Alternatives considered

- **保留 1k/10k benchmark 在 package test 中：** 即使不运行 benchmark，测试源码和 fixture 仍容易被常规验证误用；大 backfill 还会消耗大量内存型临时空间，因此不保留。
- **继续保留 100k fixture：** 没有必要满足当前决策，资源风险更高，明确不采用。
- **为 provider、health、cooldown 各增加带完整 sort tuple 的索引：** 一次性实验中 provider-only 与 abnormal 已处于较低耗时，收益证据不足；多个宽索引会增加同步写放大。
- **用普通 B-tree 优化 contains-fold search：** `%term%` 和多列 `LOWER` 无法利用普通前缀索引；真正优化需要 FTS/trigram 与新的搜索契约。
- **把 backfill 改成一条 CASE SQL 或临时表：** 需要 SQLite/MySQL/PostgreSQL 分支并扩大迁移风险；当前短事务收益不足以证明复杂方案必要。

## Consequences

索引决定仍有一次真实 query-plan 证据，但普通测试不再隐式创建大数据集或运行 backfill 压测，降低开发机内存和临时空间风险。代价是未来不会自动回归大规模 query plan；需要压力测试时必须单独安排、串行执行并记录资源边界。精确 totalCount、contains search 和 Ent 单行 backfill 的规模增长仍需独立的计数、搜索或 bulk-update 设计。
