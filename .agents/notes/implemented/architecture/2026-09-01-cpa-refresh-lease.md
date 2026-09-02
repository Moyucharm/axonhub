# Agent Note: CPA 跨进程凭证刷新租约

Status: implemented

## Problem

CPA 的进程内 `singleflight` 只能合并同一进程内的 refresh。多个 AxonHub 进程、手动刷新与 scheduler/patrol 同时处理同一 credential 时，仍可能重复调用远端 provider；进程在网络请求中崩溃时，单纯的内存锁也无法恢复执行资格。

## Decision

CPA credential 使用数据库短租约和单调 `refresh_revision` 作为跨进程 refresh 协调边界：

- `refresh_lease_token` 保存随机 owner token，`refresh_lease_until` 保存过期时间，`refresh_revision` 标记最近一次已发布的终态；三者是内部字段，不暴露 GraphQL。
- owner 通过 credential revision 加上“租约为空或已过期”的条件更新执行 CAS claim。claim 只进行短数据库写入，绝不把远端网络请求放进事务或行锁。
- owner 使用同一 quota executor 完成 provider fetch 和 projection persistence；成功、failure、unsupported/insufficient-data 终态都在带 token/revision 条件的 repository 写入中递增 revision 并清理租约。
- 竞争者不发 provider 请求，而是轮询 revision。owner 成功或失败发布终态后，竞争者读取 fresh credential 并返回；调用者自己的 context 取消只结束等待，不取消 owner。
- owner 取消、panic 或 persistence 失败时使用 `context.WithoutCancel` 的短清理 context 释放自己的 token；若租约因进程崩溃遗留，过期后其他进程可以 takeover。旧 owner 的迟到写入必须匹配旧 token/revision，因此不能覆盖新 owner。
- 进程内 singleflight 保留，但只减少同进程 claim/poll 开销；数据库 CAS 才是最终去重边界。`RefreshCredential`、batch refresh 和 patrol 共享同一 refresh outcome 路径。

## Verification

使用共享 SQLite 文件和两个独立 Ent client/service 验证：只有一个 owner；竞争者等待同一 revision；同一 credential 的真实 quota refresh 只调用一次 fake provider；过期租约可 takeover；取消 owner 会释放租约；旧 token 无法清除新 owner；等待者取消返回自身 context 错误。

## Alternatives considered

- **只依赖进程内 singleflight/mutex：** 多进程无法共享状态，不能阻止重复 provider 请求。
- **持有数据库事务或行锁覆盖网络请求：** 网络 timeout 和进程崩溃会长期占用写资源，SQLite 尤其容易扩大锁竞争；短租约把网络执行与数据库写入分开。
- **使用数据库 advisory lock：** PostgreSQL 可用但 SQLite/MySQL 语义不统一，且不适合本项目默认的 SQLite 部署；credential 行上的 CAS 字段跨方言一致。
- **竞争者租约存在时直接返回 pending：** 会让 GraphQL/manual caller 得不到本次 refresh 的终态，也无法确认失败是否已落库；revision polling 保持单次调用的终态语义。
- **owner 失败后不递增 revision：** joiner 只能等到 expiry 或永久等待；失败终态也递增 revision，失败传播和崩溃接管都有明确边界。

## Consequences

同 credential 的跨进程远端调用去重并可从 owner 崩溃中恢复，手动刷新、批次和 patrol 不再各自维护并发规则。代价是每次 refresh 增加一次 lease claim 和 joiner polling 的数据库访问，租约时长必须覆盖远端 timeout 并留出 persistence 余量；若 owner 在 persistence 之前崩溃，joiner 要等待 lease expiry 后重试，期间不会误覆盖旧 snapshot。租约字段属于内部 schema，未来修改必须继续保持 token 条件清理和 revision 单调递增。
