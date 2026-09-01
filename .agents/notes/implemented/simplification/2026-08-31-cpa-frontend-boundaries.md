# Agent Note: CPA 前端页面职责边界

Status: implemented

## Problem

CPA 管理页面把实例选择、筛选分页、刷新进度、确认操作和凭证表格渲染集中在 `index.tsx`，导致状态变化和展示变化相互耦合。类型定义、GraphQL 操作和 React Query hooks 也集中在 `data.ts`，修改一个请求容易波及整个页面。

## Decision

CPA 前端保留现有 GraphQL 和用户可观察行为，只先建立内部边界：

- `types.ts` 单独承载 CPA 领域类型、provider 顺序和支持集合。
- `api/operations.ts` 单独承载 GraphQL operation 文本，`api/queries.ts` 与 `api/mutations.ts` 分离请求职责。
- `use-cpa-controller.ts` 承载页面状态、查询编排、分页重置、刷新和确认操作。
- `components/credential-table.tsx` 承载凭证表格、展开详情和服务端分页展示。
- `index.tsx` 保留页面布局和组件组装，不在这一批次改变 API 或视觉交互。

分页游标的实际推进仍由页面 controller 提供回调，表格组件只负责触发回调；这样表格不会知道查询缓存和筛选状态的实现细节。

## Alternatives considered

- **只移动文件而保留所有 state 在页面中：** 文件数量会增加，但页面仍然承担同样的协调职责，无法降低修改风险。
- **一次性重写整个 CPA 页面和 GraphQL 层：** 会同时改变查询契约、缓存行为和交互，难以区分结构性回归与功能变化，因此暂不采用。
- **先引入全局状态库：** CPA 页面状态是局部且具有明确生命周期，引入全局 store 会扩大作用域，违背当前简化目标。

## Consequences

页面中的业务编排、请求定义、领域类型和展示代码已经可以分别演进。表单也统一使用仓库既有的 `react-hook-form + zodResolver`，保留空密钥和 TLS 确认语义。代价是 controller 仍暂时暴露较多回调，后续可在补充交互测试后继续收敛；当前不改变 GraphQL schema。
