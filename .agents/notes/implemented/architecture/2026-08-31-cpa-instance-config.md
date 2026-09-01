# Agent Note: CPA 实例配置归一化边界

Status: implemented

## Problem

`CreateInstance` 和 `UpdateInstance` 同时处理输入 trim、默认值、范围校验、URL 归一化、密钥变化判断和连接变化判断。配置语义散落在远端验证和数据库事务之间，新增字段时容易让 create/update 行为不一致。

## Decision

新增 `internal/server/biz/cpa_config.go`，用内部 `cpaInstanceConfig` 表达已经归一化的实例设置：

- `normalizeCreateCPAInstanceConfig` 负责创建输入的默认值、范围校验和 URL/密钥归一化。
- `mergeUpdateCPAInstanceConfig` 以当前 Ent 实例为基准合并 patch，并返回 `secretChanged` 与 `connectionChanged` 两个显式决策。
- 密钥加解密、远端连接验证和事务写入仍由 `CPAService` 编排；配置组件不访问数据库，也不发网络请求。

空的更新密钥仍代表保留旧密钥；URL 变化仍必须伴随新密钥；从 disabled 变为 enabled 仍会触发连接验证。公开 input 类型和错误文本保持不变。

## Alternatives considered

- **继续在 `CreateInstance`/`UpdateInstance` 中维护条件分支：** 短期改动更小，但 create/update 的默认值和变化判定会继续重复。
- **让配置组件直接负责加密、网络验证和 Ent 写入：** 会把纯配置决策重新和副作用绑定，测试和事务边界更差。
- **立即改成全新的 public domain model：** 当前只需要内部拆分，公开 GraphQL 契约没有必要同步变化。

## Consequences

实例服务的主流程更短，配置规则可单独用表格测试，后续增加字段可以在单一归一化边界接入。代价是暂时新增一层内部字段映射，且调度时间重置规则仍留在服务编排器中，后续自动化阶段再继续拆分。CPA 行为测试和配置测试均通过。
