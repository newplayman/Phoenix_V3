# Phase 5.0 — Phoenix_V3 Risk Control Module（设计与接口定义）

## 1）风险控制模块的定位
Risk Control Module（RCM）是 Phoenix_V3 决策链中的“裁决层”，位于 `strategy` 与 `intent executor` 之间：

- 输入：`candidate_intent` + `RiskContext`（当前时间、`control.json` 状态、系统健康指标、价格源摘要等）
- 输出：`RiskDecision`（`APPROVE / MODIFY / REJECT`）以及可解释的 `reason` / 稳定 `rule_id`

RCM 只对“意图”做审核与裁决，不直接修改市场数据，不生成策略，不替代策略输出语义。

## 2）风险控制必须解决的问题（分类）

### A. 执行层硬限制（Hard Limits）
目标：阻断确定性风险、确保任何情况下不发生真实下单或链上写操作。

- 频率限制：intent 触发过密时拒绝或进入冷却
- 暴露/强约束：禁止扩大风险的修改（`MODIFY` 只能降级）
- 连续失败保护：失败阈值触发 cooldown
- dry-run 强制：不得绕过 `control.json.force_dry_run`，不得在本阶段产生真实广播

### B. 市场与信号软限制（Soft Limits）
目标：在信号质量不足时拒绝执行，避免“错误但看似可执行”的交易意图。

- 价格源偏差：多源分歧过大时拒绝
- 流动性不足：不足以承载意图时拒绝（本阶段仅定义接口与所需字段）
- 波动异常：异常波动时拒绝或降级（本阶段仅定义接口与所需字段）

### C. 系统健康保护（System Guard）
目标：当系统自身不健康时主动拒绝，避免放大故障。

- RPC 异常、链网关异常
- tick/backlog 异常（延迟/堆积）
- `control.json.risk_mode=HALT`：无条件拒绝（最高优先级）

## 3）明确不做的事情
- 不负责生成交易策略（RCM 不产生 candidate intent）
- 不负责资金管理细节（余额、仓位、资金分配策略属于其他层）
- 不绕过 Control Plane 的任何安全护栏（`control.json.force_dry_run`、`control.json.risk_mode` 等）

## 4）风险裁决模型（接口级）
核心概念：

- `RiskVerdict`：`APPROVE / MODIFY / REJECT`
- `RiskDecision`：包含 `verdict`、`reason`、`rule_id`、可选 `degrade`（降级建议）
- `RiskRule`：单条规则接口（输入 `candidate_intent` + `RiskContext`，输出 `RiskDecision`）
- `RiskContext`：封装当前时间、控制面状态、系统健康指标、价格源摘要等

约束：
- 每一次裁决必须有可解释 `reason`
- `rule_id` 稳定（用于日志与审计）
- `MODIFY` 只能用于“降级”，不能扩大风险

## 5）决策链插入点（顺序）
Phoenix_V3 新决策链顺序（接口与注释层面确定）：

`market data`
-> `strategy`
-> `candidate_intent`
-> `risk_control.evaluate()`
-> `final_intent`（或拒绝）
-> `executor`（本阶段仍保持 `force_dry_run=true`）

裁决聚合规则：
- 若任一规则返回 `REJECT`，则整体 `REJECT`
- 若存在 `MODIFY` 且无 `REJECT`，则按“最保守降级”生效（只能降级）
- 若全部 `APPROVE`，则放行

## 6）MVP 风控规则（仅定义与接口，不启用真实交易）
本阶段至少定义三条规则（实现可为 stub，默认不扩大影响面）：

1) `ForceDryRunRule`
- 当 `control.json.force_dry_run=true` 或本阶段要求 dry-run 时，任何可能导致真实广播的意图必须 `REJECT`
- 不得绕过控制面与系统安全策略

2) `CooldownAndFrequencyRule`
- 限制 intent 触发频率
- 连续失败达到阈值进入 cooldown
- cooldown 期间直接 `REJECT` 并给出 `reason`

3) `PriceSourceDivergenceRule`
- 多价格源偏差超过阈值时 `REJECT`
- 仅使用已有数据（`PriceAggregator` 的 snapshot），不新增外部依赖

### PriceSourceDivergenceRule（Phase 5.3 落地细节）
- 数据来源（同一时刻的多源快照，规则不发起任何网络请求）：
  - `exchange`：来自 `PriceAggregator.Snapshot().Aggregate.AggPrice` / `AggUpdatedAt`
  - `onchain`：来自本进程监听到的池子 tick 推导价格（`tickToDexPrice(...)`）的最近一次值与时间戳
- 阈值含义：
  - `max_deviation_bps`（默认 100 bps = 1.00%）：两来源相对偏差超过阈值则拒绝
  - `max_staleness_sec`（默认 30s）：任一来源快照超过该时间视为 stale
- stale 策略选择：
  - 选择 **SKIP（不拒绝）**：stale 更像“不可比较”的状态，短期可能由源重连/短暂抖动导致；同时系统仍受 `HALT/force_dry_run/cooldown` 保护，避免误伤造成长期拒绝。

## 7）与控制面的关系
- Risk Control 服从 `control.json.risk_mode`
- 当 `risk_mode=HALT` 时，风险模块必须无条件 `REJECT`
- 风险模块只读取 `control.json`，不写入、不修改
