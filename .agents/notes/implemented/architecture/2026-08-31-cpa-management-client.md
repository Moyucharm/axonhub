# Agent Note: CPA 远端管理接口抽象

Status: implemented

## Problem

同步、额度刷新和自动巡检都直接依赖具体的 `*cpa.Client`，因此业务流程无法在不启动真实 HTTP transport 的情况下替换远端行为，provider quota registry 也被具体 client 类型限制。

## Decision

在 `internal/server/biz/cpa/client.go` 定义 `ManagementClient`，覆盖 auth-files、provider call、usage queue、远端状态 patch 和连接回收。quota adapter、`QuotaRegistry`、`callJSON` 以及业务层 `clientForInstance` 统一依赖该接口。`CPAService` 通过内部 `clientFactory` 创建 client，默认实现仍是现有 HTTP client。

该接口只表达现有远端协议操作，不把加密、数据库事务、同步 diff 或调度职责放入 client；远端协议层和 CPA 业务层保持单向依赖。

## Alternatives considered

- **继续传递 `*cpa.Client`：** 不需要改签名，但所有业务测试都必须依赖具体 HTTP 实现，后续流程拆分没有可替换边界。
- **把整个 CPA client 重写成多种实现：** 当前不需要改变 HTTP 行为，新增实现会扩大范围。
- **把 Ent repository 也放进 client：** 会混淆远端 I/O 与本地一致性事务，违背单一职责。

## Consequences

provider quota 和业务 orchestration 现在可以注入 fake management client，后续可以为同步/刷新/巡检补充协议级测试。代价是 quota adapter 和 registry 的方法签名发生了内部调整；现有 concrete client 仍满足接口，公开 GraphQL 契约不变。
