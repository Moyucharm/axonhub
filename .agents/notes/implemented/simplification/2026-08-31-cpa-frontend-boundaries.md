# Agent Note: CPA 前端页面职责边界

Status: implemented

## Problem

CPA 管理页面把实例选择、筛选分页、刷新进度、确认操作和凭证表格渲染集中在 `index.tsx`，导致状态变化和展示变化相互耦合。类型定义、GraphQL 操作和 React Query hooks 也集中在 `data.ts`，修改一个请求容易波及整个页面。

## Decision

CPA 前端保留现有 GraphQL 和用户可观察行为，只先建立内部边界：

- `types.ts` 单独承载 CPA 领域类型、provider 顺序和支持集合。
- `api/operations.ts` 单独承载 GraphQL operation 文本，`api/queries.ts` 与 `api/mutations.ts` 分离请求职责。
- `use-cpa-controller.ts` 承载页面查询编排和 mutation 协调；`use-cpa-pagination.ts` 单独管理 page-size 持久化、cursor history 与 reset，避免 controller 继续增长。
- `components/credential-table.tsx` 承载凭证表格、展开详情和服务端分页展示。
- `components/instance-toolbar.tsx`、`instance-alerts.tsx`、`refresh-progress.tsx` 与 `confirmation-dialogs.tsx` 承载页面级展示区块，`index.tsx` 只保留 shell 与组装。
- `instance-form.ts` 承载实例表单 schema、默认值和 API input normalization；dialog 保留 form/mutation ownership 与安全确认交互。

分页游标的实际推进仍由页面 controller 提供回调，表格组件只负责触发回调；这样表格不会知道查询缓存和筛选状态的实现细节。

## Alternatives considered

- **只移动文件而保留所有 state 在页面中：** 文件数量会增加，但页面仍然承担同样的协调职责，无法降低修改风险。
- **一次性重写整个 CPA 页面和 GraphQL 层：** 会同时改变查询契约、缓存行为和交互，难以区分结构性回归与功能变化，因此暂不采用。
- **先引入全局状态库：** CPA 页面状态是局部且具有明确生命周期，引入全局 store 会扩大作用域，违背当前简化目标。

## Consequences

页面中的业务编排、请求定义、领域类型和展示代码可以分别演进。后续拆分把 `index.tsx` 从 358 行降至约 226 行，把 controller 从 237 行降至约 193 行；实例表单 schema/default/input mapping 也不再与 289 行 dialog JSX 混在一起。页面关键交互新增稳定 `data-testid`，`frontend/tests/cpa.spec.ts` 使用真实 AxonHub E2E backend 与本机 fake CPA management server 覆盖 create/sync、search、refresh、remote toggle、secret-preserving edit 和 delete。

代价是局部组件与文件数量增加，controller 仍作为兼容 facade 暴露部分平铺状态，dialog 的 automation settings JSX 仍较长。E2E 在当前环境已通过 Playwright test discovery，但浏览器可执行文件缺失，完整运行被基础设施阻塞而不是被写成通过。
