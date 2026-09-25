# Agent Note: Codex CPA 周/月额度估算的窗口身份与区间保留

Status: implemented

## Problem

上一轮修复（本地决策记录 `.agent/notes/implemented/bug-fix/2026-09-02-cpa-estimate-survives-restart.md`、`.agent/notes/implemented/bug-fix/2026-09-13-cpa-estimate-window-isolation.md`，见 DIFF.md §3.9/§3.11）稳定了 collector identity 与 7d high-water 基线，但 `estimated_limit_usd` 在周期未刷新时仍会反复消失。逐条定位到 6 个叠加机制：

1. **窗口身份按秒级完全相等判定**：`sameQuotaReset` 是 `left.Unix() == right.Unix()`，被 6 处调用。上游对未锚定窗口报「浮动 reset」（自用 CPA 插件 `quota_value.go` 正是为此把 reset 归一到 5 分钟桶），且同一 7d 窗口的两个来源（usage 响应头 vs WHAM 快照）天然可能差几秒。任何漂移都会让 `advanceCodexWeeklyObservation` 走 reanchor 分支，把 7d baseline 重置为当前百分比——区间重新从 0 累计，增量长期只有几个百分点；同时 `applyQuotaEstimate` 清空 `estimate_*` 且 `carryForwardCodexQuotaEstimate` 拒绝继承，已显示金额被抹掉。
2. **无 duration 的旧记录把 5h 窗口当周窗口**：`parseCodexWeeklyObservation` 的 legacy fallback 直接把 `x-codex-secondary-*` 当 7d，不校验周期；7d 位于 primary 而 secondary 是 5h 时，每次 5h 重置都会改写 7d 观测。
3. **没有可用观测时清空而非保留**：`collectorSessionID == ""`（usage stream 关闭、worker 未运行、identity 为空）或月度 `used_percent` 缺失时，`prepareCodex*Interval` 执行 `clearCodexEstimateInterval` + `reanchored=true`，即便同周期无任何新事件也会抹掉金额。
4. **月度区间对百分比回退零容忍**：任意 1 点抖动（上游取整、CPA/WHAM 缓存）就重锚 30 天周期；该函数还缺周期一致性判断，槽位换窗口时会把 5h 百分比当月度端点。
5. **区间端点越过 high-water 采样**：同 reset 内的百分比回退样本只保留 high-water 百分比，却仍把 `SecondaryLatestEventID` 前移到回退事件；估算分子继续累加成本、分母冻结，金额持续变小。
6. **槽位迁移丢窗口**：snapshot item id 是 `<prefix>-<slot>`，而 `previousQuotaItem` 只按 id 匹配，上游把窗口在 primary/secondary 间迁移时找不到前值，`carryForward` 放弃旧金额。

## Decision

改动集中在 `internal/server/biz/cpa_estimate_codex.go`，另加 `internal/objects/cpa.go` 的一个可选 JSON 字段：

- **`cpaQuotaResetTolerance = 5 * time.Minute`** 作为唯一开关，`sameQuotaReset` 改为容忍比较；窗口锚点仍只在 reanchor 时写入 `SecondaryResetAt`，保持稳定。取值与自用 CPA 插件的 5 分钟归一桶一致，且远小于最短可估算窗口（7 天）。
- **legacy 样本需快照确认**：`codexWeeklyObservation` 增加非持久化字段 `durationVerified`；无 duration 的样本只有在凭证快照里存在同 reset 的 7d 窗口时才被采纳（`codexWeeklyResetConfirmed`），否则既不建基线也不重锚。显式 duration 的样本不受该门槛限制。
- **无观测时继承区间**：新增 `sameQuotaPeriod` / `sameCodexEstimateWindow`（同槽位 id 或同 group，叠加 period + reset）与 `adoptCodexQuotaInterval`；`prepareCodexWeeklyInterval` 在无 live collector checkpoint 时改为继承，`prepareCodexMonthlyInterval` 在无样本（无百分比、无 checkpoint、collector 尚无事件即 `latestEventID <= 0`）时改为继承。以 0 为锚点的历史行由月度重锚条件（`previous.EstimateBaselineEventID <= 0`）在下次刷新自愈。
  窗口身份取「同槽位 id 或同 group」的并集：槽位迁移（id 变、group 不变）与上游改名（`additional_rate_limits` 的 `limit_name` 变、位置派生的 id 不变）都能沿用前值，而跨组互串要求两个槽位 id 相等，实际不可达。
- **月度容忍抖动**：只有当前百分比比基线低满 3 点（`cpaEstimateMinPercentDelta`，即真实重置或 grant）才重锚，1–3 点抖动只推进端点；同时用 `sameCodexEstimateWindow` 补齐 period/group/reset 一致性。
- **`previous` 按窗口身份查找**：`previousQuotaItem` 以「同窗口」为主判据、id 相同者优先，使窗口在槽位间迁移时仍能继承同窗口的区间元数据。
- **`carryForwardCodexQuotaEstimate` 去掉 collectorSessionID 参数**：改为按窗口 + 已记录区间身份（collector identity、baseline 百分比/事件、latest 事件不回退）判断，identity 轮换由 `prepare*Interval` 的 live-session 校验与 `intervalReanchored` 承担；`isEstimableCodexQuotaPeriod` 失去唯一调用点后删除。
- **区间端点只跟随 high-water**：`CPAQuotaObserved` 增加 `secondary_checkpoint_event_id`（JSON 字段，无 Ent schema/生成代码变更）。`SecondaryCheckpointEventID` 是最新已持久化事件，仅用于拒绝重放/乱序；`SecondaryLatestEventID` 是区间末端，只在百分比不低于 high-water 时前进（整数四舍五入下相等是常态，仍前进）。旧行回退读取 `SecondaryLatestEventID` 作 checkpoint。

## Alternatives considered

- **只改观测侧或只放宽 3% 阈值：** 同一窗口的两个来源都要参与身份判定（观测 reanchor、区间 gate、`prepare*`、`carryForward`），只改一侧仍会在另一侧漂移时清空；放宽 3% 阈值会丢掉估算可靠性的核心契约。
- **按「已用百分比跌破 baseline」判定周窗口真实换代：** 对周窗口，reset 变化本身已是换代信号，容忍值只吸收秒级漂移；百分比回退（含 5h 联动样本）不是换代证据。
- **把 `estimated_limit_usd` 直接持久化到独立字段并只在换代时清空：** 需要 Ent schema 变更与迁移，且与「区间元数据在 `quota_data`」的既有约定分裂成两套真相来源。
- **无 duration 的 legacy 样本一律忽略：** 会让仍未带 duration 的旧版 CPA 在首次额度刷新前完全不产出估算；改为「快照确认」在保留兼容的同时消除 5h 误判。

## Consequences

- 同一（collector identity + 资源组 + 周期 + reset）窗口内，区间只前进；一旦出现过一次 ≥3% 的有效区间，`estimated_limit_usd` 在后续每次刷新都保持显示（可用更优区间覆盖），不再因百分比抖动、无新事件、collector 缺席或槽位迁移而清空。
- 真实换代（reset 偏移超出容忍、identity 轮换、月额度跌幅 ≥3 点、窗口迁移后仍无同窗口前值）仍清空 `estimated_*`，从新周期的 ≥3% 区间重新出现；`reset-mismatch` 判定同步获得容忍，快照与响应头相差数十秒不再让金额消失。
- 月度窗口不再以 event id 0 为锚点：该锚点会让分子统计整个周期的事件而分母只从本次刷新起算，新代码改为沿用上一区间，历史脏锚点自愈。
- 区间成本不再包含「分母未计入」的回退样本，估算金额在同一窗口内单调不减（`EstimatedCostUSD`）。
- 需要迁移/生成代码：无。`quota_observed` 与 `quota_data` 均为 JSON 字段，新增字段对旧数据可选、对旧版本可忽略。
- 若实测 reset 漂移大于 5 分钟（例如上游把 `reset_after_seconds` 取整到小时级），只需调整 `cpaQuotaResetTolerance`，不影响其它逻辑。
- 回归覆盖（含只读审查后的补强）：`TestCodexEstimateSurvivesResetJitter`（含容差边界两侧）、`TestCodexEstimateClearsOnRealWindowReset`、`TestCodexEstimatePersistsUntilWindowReset`（含 `quota_observed` 落库往返与 live 路径换代）、`TestCodexEstimateKeepsIntervalWhenCollectorIsUnavailable`（含窗口迁移时的清空）、`TestCodexMonthlyIntervalSurvivesPercentRegression`、`TestCodexMonthlyIntervalAdoptsWhenCheckpointHasNoEvents`、`TestPrepareCodexWeeklyIntervalReanchorsOnForeignObservation`、`TestLegacyCodexWeeklySignalRequiresSnapshotConfirmation`、`TestPreviousQuotaItemFollowsWindowAcrossSlots`（含改名与跨组）、`TestAdvanceCodexWeeklyObservationMaintainsWindowBoundaries`、`TestAdvanceCodexWeeklyObservationUpgradesLegacyCheckpoint`、`TestCarryForwardCodexQuotaEstimateKeepsCompatibleLastValue`、`TestPersistUsageEventsTracksCodexCollectorInterval`（checkpoint 字段落库与回退样本滞后语义）。
