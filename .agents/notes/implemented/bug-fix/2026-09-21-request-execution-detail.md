# Agent Note: Request 执行详情选择与 payload 空状态

Status: implemented

## Problem

Request detail 的 execution 列表原本按最新记录倒序获取，但新的步骤条改成 `first + ASC` 后，在执行次数超过展示上限时会拿到最早记录，最新执行无法查看。页面同时用数组 index 保存选择，切换到执行数量相同的另一个 request 时可能沿用旧选择。执行数量标题也显示当前 edges 数量而不是后端总数。

execution payload 还使用 truthy/object 判断。后端 `JSONRawMessage` 可以保存任意 JSON，`false`、`0`、字符串和数组会被误认为没有响应；外部存储 GC 使用空对象 `{}` 作为已清理 payload 标记。请求体和请求头的空状态翻译已存在但未接入渲染。

## Decision

前端执行查询使用现有 Relay connection 的 `last: 20` 与 `CREATED_AT ASC`，让后端返回最新 20 条且 edges 保持时间正序。详情页继续限制最近 20 条，但使用 `totalCount` 显示真实总数，并在截断时提示当前可见范围。

当前执行使用全局唯一的 execution ID 保存，而不是数组 index。列表刷新时保留仍存在的 ID；ID 不在当前窗口或 request/project 上下文变化时回退到最新可见执行，展示编号由当前列表位置派生。

`execution-display` 工具集中处理三项纯逻辑：按 ID 选择执行、判断 JSON payload 是否已记录、序列化包括 `false` 与 `0` 在内的 falsy JSON。空对象仍按无记录处理，以兼容现有 GC 标记；数组和 primitive JSON 视为有效记录。请求体、请求头和响应体均渲染对应空状态，response chunks 仍单独判断。

## Alternatives considered

- **继续使用 `first: 20 + DESC` 并在组件中反转：** 可以避免扩展查询变量，但把分页窗口和排序补偿放到了客户端；后端已经支持 `last/before`，因此选择服务端表达“最新 20 条”更直接。
- **继续保存数组 index：** 实现更少，但在 request 切换、记录删除或窗口刷新后无法保证选中同一条 execution；使用 ID 能保持选择语义稳定。
- **引入完整 cursor 分页控件：** 可以浏览全部历史，但会扩大详情页交互和状态范围。本次保留最近 20 条上限，用总数和截断提示避免误导，完整历史分页另行处理。
- **把所有空对象都视为有效 JSON：** 会让 GC 后的 `{}` 看起来像真实响应；当前数据清理约定明确使用空对象标记，因此保留空对象无记录语义。

## Consequences

详情页默认展示最新执行，步骤顺序仍从早到晚；用户手动选择在数据刷新时更稳定。总数和截断提示让 20 条展示上限可见。primitive/array response 可以继续查看、复制和下载，缺失的 request payload 会显示中英文空状态。

代价是执行查询 hook 增加了 `last/before` 变量，详情页仍不能浏览超过最近 20 条的更早历史；如果业务需要完整审计历史，需要后续增加 cursor pagination。空对象的语义继续依赖后端 GC 标记约定，未来若需要区分真实空对象，应增加明确的 payload-presence 字段。
